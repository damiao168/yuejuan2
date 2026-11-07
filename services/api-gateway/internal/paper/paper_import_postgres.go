package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"strings"
)

func (s *PostgresStore) CreatePaperImport(ctx context.Context, tenantID, examID, userID string, input CreatePaperImportInput) (PaperImportJob, error) {
	if input.CommandID == "" {
		input.CommandID = uuid.NewString()
	}
	sources := normalizePaperImportSourceInputs(input)
	if strings.TrimSpace(input.Subject) == "" || validateCreatePaperImportSources(sources) != nil {
		return PaperImportJob{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	var authoritativeSubject string
	if err = tx.QueryRowContext(ctx, `SELECT subject FROM exam WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, examID).Scan(&authoritativeSubject); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PaperImportJob{}, ErrNotFound
		}
		return PaperImportJob{}, err
	}
	canonicalSubject, subjectErr := normalizePaperImportSubject(authoritativeSubject, input.Subject)
	if subjectErr != nil {
		return PaperImportJob{}, ErrInvalidInput
	}
	// 浏览器值只用于一致性断言。持久化考试记录中的标准学科，避免伪造或过时请求控制 OCR 路由。
	input.Subject = canonicalSubject
	requestHash := paperImportCommandHash(input)
	if existingID, existingHash, replayErr := findPaperImportCommandInTx(ctx, tx, tenantID, userID, input.CommandID); replayErr == nil {
		if existingHash != requestHash {
			return PaperImportJob{}, ErrConflict
		}
		var sameTarget bool
		if err = tx.QueryRowContext(ctx, `SELECT exam_id=$3::uuid FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, existingID, examID).Scan(&sameTarget); err != nil {
			return PaperImportJob{}, err
		}
		if !sameTarget {
			return PaperImportJob{}, ErrConflict
		}
		if err = tx.Commit(); err != nil {
			return PaperImportJob{}, err
		}
		return s.GetPaperImport(ctx, tenantID, existingID)
	} else if !errors.Is(replayErr, ErrNotFound) {
		return PaperImportJob{}, replayErr
	}
	if err = ensureExamPaperMutableTx(ctx, tx, tenantID, examID); err != nil {
		return PaperImportJob{}, err
	}
	if err = validatePaperImportAssetsInTx(ctx, tx, tenantID, examID, input.ExamPaperID, sources); err != nil {
		return PaperImportJob{}, err
	}
	row := tx.QueryRowContext(ctx, `INSERT INTO paper_import_job
(tenant_id, exam_id, exam_paper_id, paper_file_asset_id, answer_file_asset_id, status, subject, created_by)
VALUES ($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,'processing',$6,$7)
RETURNING id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at`, tenantID, examID, input.ExamPaperID, input.PaperFileAssetID, input.AnswerFileAssetID, input.Subject, userID)
	job, err := scanPaperImport(row)
	if err != nil {
		return PaperImportJob{}, err
	}
	for _, source := range sources {
		var item PaperImportSource
		err = tx.QueryRowContext(ctx, `INSERT INTO paper_import_source(tenant_id,paper_import_id,file_asset_id,document_index,role_hint) VALUES($1,$2::uuid,$3::uuid,$4,$5) RETURNING id::text,file_asset_id::text,document_index,role_hint,detected_role,role_confidence::float8,processing_status,created_at`, tenantID, job.ID, source.FileAssetID, source.DocumentIndex, source.RoleHint).Scan(&item.ID, &item.FileAssetID, &item.DocumentIndex, &item.RoleHint, &item.DetectedRole, &item.RoleConfidence, &item.ProcessingStatus, &item.CreatedAt)
		if err != nil {
			return PaperImportJob{}, err
		}
		job.Sources = append(job.Sources, item)
	}
	// 导入记录与待派发意图一并提交，HTTP 返回后进程退出也能由执行器接续准备资料。
	job.Generation = 1
	job.SourceRevision = paperImportSourceConfigurationHash(job.Sources)
	if err = tx.QueryRowContext(ctx, `INSERT INTO paper_import_run
(tenant_id,paper_import_id,generation,command_id,command_type,command_request_hash,actor_id,source_revision,status,dispatch_status)
VALUES($1,$2::uuid,1,$3,'create',$4,$5::uuid,$6,'processing','pending') RETURNING id::text`,
		tenantID, job.ID, input.CommandID, requestHash, userID, job.SourceRevision).Scan(&job.RunID); err != nil {
		return PaperImportJob{}, err
	}
	if err = savePaperImportSourceSnapshot(ctx, tx, tenantID, job.RunID, job.Sources); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET current_generation=1,source_revision=$3 WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, job.ID, job.SourceRevision); err != nil {
		return PaperImportJob{}, err
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return job, nil
}

func (s *PostgresStore) CompletePaperImportCandidates(ctx context.Context, tenantID, id string, parsed PaperImportParseResult) (PaperImportJob, error) {
	// PostgreSQL 导入必须通过 CompletePaperImportParseTask 发布，该方法在一个事务中核验
	// 协议 v2、运行/代次/输入绑定与有效租约。保留此接口方法供内存存储使用，
	// 避免大范围 API 破坏，同时拒绝全部旧版无版本数据库写入。
	return PaperImportJob{}, ErrConflict
}

func (s *PostgresStore) GetPaperImport(ctx context.Context, tenantID, id string) (PaperImportJob, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, id)
	job, err := scanPaperImport(row)
	if err != nil {
		return job, err
	}
	return s.loadPaperImportDetails(ctx, tenantID, job)
}

func (s *PostgresStore) ListPaperImports(ctx context.Context, tenantID, examID string) ([]PaperImportJob, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at FROM paper_import_job WHERE tenant_id=$1 AND exam_id=$2 AND deleted_at IS NULL ORDER BY created_at DESC`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PaperImportJob{}
	for rows.Next() {
		item, err := scanPaperImport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i], err = s.loadPaperImportDetails(ctx, tenantID, out[i])
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *PostgresStore) loadPaperImportDetails(ctx context.Context, tenantID string, job PaperImportJob) (PaperImportJob, error) {
	return loadPaperImportDetails(ctx, s.db, tenantID, job)
}

// 事务内回执与普通详情共享字段装载；传入事务时所有查询均在提交前执行，不能改用连接池重读下一次保存。
func loadPaperImportDetails(ctx context.Context, queryer postgresQueryer, tenantID string, job PaperImportJob) (PaperImportJob, error) {
	if err := hydratePaperImportRun(ctx, queryer, tenantID, &job); err != nil {
		return PaperImportJob{}, err
	}
	var q, a, so, r, si, usage []byte
	if err := queryer.QueryRowContext(ctx, `SELECT question_candidates,answer_candidates,solution_candidates,rubric_candidates,structured_issues,model_usage,COALESCE(structure_confirmed_generation,0) FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, job.ID).Scan(&q, &a, &so, &r, &si, &usage, &job.StructureConfirmedGeneration); err != nil {
		return PaperImportJob{}, err
	}
	_ = json.Unmarshal(q, &job.QuestionCandidates)
	_ = json.Unmarshal(a, &job.AnswerCandidates)
	_ = json.Unmarshal(so, &job.SolutionCandidates)
	_ = json.Unmarshal(r, &job.RubricCandidates)
	_ = json.Unmarshal(si, &job.StructuredIssues)
	_ = json.Unmarshal(usage, &job.ModelUsage)
	rows, err := queryer.QueryContext(ctx, `SELECT s.id::text,s.file_asset_id::text,s.document_index,s.role_hint,s.detected_role,s.role_confidence::float8,s.processing_status,f.original_name,f.content_type,s.created_at FROM paper_import_source s JOIN file_asset f ON f.tenant_id=s.tenant_id AND f.id=s.file_asset_id WHERE s.tenant_id=$1 AND s.paper_import_id=$2::uuid AND s.deleted_at IS NULL ORDER BY s.document_index`, tenantID, job.ID)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer rows.Close()
	job.Sources = []PaperImportSource{}
	for rows.Next() {
		var item PaperImportSource
		if err := rows.Scan(&item.ID, &item.FileAssetID, &item.DocumentIndex, &item.RoleHint, &item.DetectedRole, &item.RoleConfidence, &item.ProcessingStatus, &item.OriginalName, &item.ContentType, &item.CreatedAt); err != nil {
			return PaperImportJob{}, err
		}
		job.Sources = append(job.Sources, item)
	}
	return job, rows.Err()
}

type paperImportScanner interface{ Scan(...any) error }

func scanPaperImport(row paperImportScanner) (PaperImportJob, error) {
	var out PaperImportJob
	var questions, issues []byte
	var applied sql.NullTime
	err := row.Scan(&out.ID, &out.TenantID, &out.ExamID, &out.ExamPaperID, &out.PaperFileAssetID, &out.AnswerFileAssetID, &out.Status, &out.Subject, &questions, &issues, &out.ErrorCode, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt, &applied)
	if errors.Is(err, sql.ErrNoRows) {
		return PaperImportJob{}, ErrNotFound
	}
	if err != nil {
		return PaperImportJob{}, err
	}
	if len(questions) > 0 {
		_ = json.Unmarshal(questions, &out.Questions)
	}
	if out.Questions == nil {
		out.Questions = []PaperImportDraftQuestion{}
	}
	if len(issues) > 0 {
		_ = json.Unmarshal(issues, &out.Issues)
	}
	if out.Issues == nil {
		out.Issues = []string{}
	}
	if applied.Valid {
		out.AppliedAt = &applied.Time
	}
	return out, nil
}
