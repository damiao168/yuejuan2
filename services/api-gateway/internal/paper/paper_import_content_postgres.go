package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// FillPaperImportContent 在同一事务中检查识别代次和考试状态，再补空白字段。
// 导入行锁防止资料被替换；考试和题目行锁防止就绪确认或人工保存与补齐交错。
// 整批失败回滚，重试时已有内容会被跳过，不增加重复答案和解析版本。
func (s *PostgresStore) FillPaperImportContent(ctx context.Context, tenantID, id, userID string, input FillPaperImportContentInput) (result PaperImportContentResult, err error) {
	stage := "validate import"
	defer func() {
		if err != nil {
			err = fmt.Errorf("fill import content (%s): %w", stage, err)
		}
	}()
	if input.ExpectedGeneration <= 0 {
		return PaperImportContentResult{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportContentResult{}, err
	}
	defer tx.Rollback()
	job, err := loadPaperImportContentJobTx(ctx, tx, tenantID, id, input)
	if err != nil {
		return PaperImportContentResult{}, err
	}
	var issues []byte
	stage = "load extraction issues"
	if err = tx.QueryRowContext(ctx, `SELECT structured_issues FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id).Scan(&issues); err != nil {
		return PaperImportContentResult{}, err
	}
	if err = json.Unmarshal(issues, &job.StructuredIssues); err != nil {
		return PaperImportContentResult{}, err
	}
	if err = ensureExamPaperMutableTx(ctx, tx, tenantID, job.ExamID); err != nil {
		return PaperImportContentResult{}, err
	}
	stage = "lock and load questions"
	questions, err := lockPaperImportContentQuestionsTx(ctx, tx, tenantID, job.ExamID)
	if err != nil {
		return PaperImportContentResult{}, err
	}
	patches, result := planPaperImportContent(job, questions)
	for _, patch := range patches {
		stage = "fill question " + patch.question.QuestionNo
		if err = s.applyPaperImportContentPatchTx(ctx, tx, tenantID, id, job.ExamID, userID, patch); err != nil {
			return PaperImportContentResult{}, err
		}
	}
	stage = "commit content"
	if err = tx.Commit(); err != nil {
		return PaperImportContentResult{}, err
	}
	return result, nil
}

// 调用方拥有事务；先锁 job 再锁当前 run，代次与结果证据校验不能拆到事务之外。
func loadPaperImportContentJobTx(ctx context.Context, tx *sql.Tx, tenantID, id string, input FillPaperImportContentInput) (PaperImportJob, error) {
	job, err := scanPaperImport(tx.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return PaperImportJob{}, ErrNotFound
	}
	if err != nil {
		return PaperImportJob{}, err
	}
	if job.Status != "review_required" {
		return PaperImportJob{}, ErrConflict
	}
	var resultGeneration sql.NullInt64
	var runStatus, revision, jobTask, runTask, jobHash, runHash string
	err = tx.QueryRowContext(ctx, `SELECT job.current_generation,job.result_generation,COALESCE(job.result_task_id::text,''),COALESCE(job.result_payload_hash,''),run.status,run.source_revision,COALESCE(run.result_task_id::text,''),COALESCE(run.result_payload_hash,'')
FROM paper_import_job job JOIN paper_import_run run ON run.tenant_id=job.tenant_id AND run.paper_import_id=job.id AND run.generation=job.current_generation
WHERE job.tenant_id=$1 AND job.id=$2::uuid FOR UPDATE OF run`, tenantID, id).Scan(&job.Generation, &resultGeneration, &jobTask, &jobHash, &runStatus, &revision, &runTask, &runHash)
	if err != nil {
		return PaperImportJob{}, err
	}
	verified := resultGeneration.Valid && resultGeneration.Int64 == job.Generation && jobTask != "" && jobTask == runTask && jobHash != "" && jobHash == runHash
	// 与完整导入使用相同的历史兼容边界，普通未验证结果不能通过这个入口写入题目。
	legacy := revision == "legacy-unverified" && jobTask == "" && runTask == ""
	if job.Generation != input.ExpectedGeneration || runStatus != "review_required" || (!verified && !legacy) {
		return PaperImportJob{}, ErrConflict
	}
	return job, nil
}

// exam 已由调用方在同一事务内锁定；按固定 ID 顺序锁题目，读完并关闭 rows 后再加载内容。
func lockPaperImportContentQuestionsTx(ctx context.Context, tx *sql.Tx, tenantID, examID string) ([]Question, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM question WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL AND status<>'deleted' ORDER BY id FOR UPDATE`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var questionID string
		if err = rows.Scan(&questionID); err != nil {
			rows.Close()
			return nil, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	questions, err := listQuestions(ctx, tx, tenantID, examID)
	if err != nil {
		return nil, err
	}
	// 发布过规则的题目已有生效评分上下文，不再自动补齐它的任何字段。
	for i := range questions {
		var published bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scoring_rule WHERE tenant_id=$1 AND question_id=$2::uuid AND status='published' AND deleted_at IS NULL)`, tenantID, questions[i].ID).Scan(&published); err != nil {
			return nil, err
		}
		if published {
			questions[i].Rubric = &Rubric{Status: "locked"}
		}
	}
	return questions, nil
}

// 题目、答案/解析和默认规则沿用调用方事务，任一失败交给外层整批回滚。
func (s *PostgresStore) applyPaperImportContentPatchTx(ctx context.Context, tx *sql.Tx, tenantID, importID, examID, userID string, patch paperImportContentPatch) error {
	q, draft := patch.question, patch.draft
	stem, options := q.Stem, q.Options
	if patch.stem {
		stem = draft.Stem
	}
	if patch.options {
		options = draft.Options
	}
	optionsJSON, _ := json.Marshal(wireList(options))
	refs, _ := json.Marshal(wireList(draft.SourceRefs))
	_, err := tx.ExecContext(ctx, `UPDATE question SET stem=$3,options=$4,paper_import_id=$5::uuid,paper_import_candidate_id=NULLIF($6,''),paper_import_source_refs=$7,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, q.ID, stem, optionsJSON, importID, draft.CandidateID, refs)
	if err != nil {
		return err
	}
	if err = s.applyImportedAnswerSolutionAndRubric(ctx, tx, tenantID, importID, q.ID, userID, draft); err != nil {
		return err
	}
	if draft.AnswerKey != nil && draft.Score > 0 {
		// 已有规则草稿属于教师编辑；自动补齐只在没有草稿时生成可核对的默认规则。
		var existing bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scoring_rule WHERE tenant_id=$1 AND question_id=$2::uuid AND status='draft' AND deleted_at IS NULL)`, tenantID, q.ID).Scan(&existing); err != nil {
			return err
		}
		if !existing {
			if err = insertImportedObjectiveRuleDraft(ctx, tx, tenantID, examID, q.ID, userID, draft); err != nil {
				return err
			}
		}
	}
	return nil
}
