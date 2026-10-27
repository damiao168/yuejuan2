package paper

// 解析入队、运行回读及失败恢复的事务入口；每次阶段写入继续使用既有运行绑定和锁序。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func (s *PostgresStore) QueuePaperImportParse(ctx context.Context, tenantID string, job PaperImportJob, actorID string, input PaperImportParseRequest) error {
	if len(input.Documents) == 0 || actorID == "" {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = queuePaperImportParseInTx(ctx, tx, tenantID, job, actorID, input); err != nil {
		return err
	}
	return tx.Commit()
}

// 解析输入、任务和派发状态使用调用方同一事务；任一步失败时不能留下没有任务的已完成上游。
func queuePaperImportParseInTx(ctx context.Context, tx *sql.Tx, tenantID string, job PaperImportJob, actorID string, input PaperImportParseRequest) error {
	if tx == nil || tenantID == "" || job.ID == "" || actorID == "" || len(input.Documents) == 0 {
		return ErrInvalidInput
	}
	current, err := getPaperImportInTx(ctx, tx, tenantID, job.ID)
	if err != nil {
		return err
	}
	runID, generation, persistedRevision, err := currentPaperImportRunInTx(ctx, tx, tenantID, job.ID)
	if err != nil {
		return err
	}
	sourceRevision := paperImportSourceConfigurationHash(current.Sources)
	if current.Status != "processing" || sourceRevision != paperImportSourceConfigurationHash(job.Sources) || persistedRevision != sourceRevision || (job.Generation > 0 && job.Generation != generation) {
		return ErrConflict
	}
	if err = validatePaperImportDispatchLease(ctx, tx, tenantID, job); err != nil {
		return err
	}
	if err = lockPaperImportTasksInTx(ctx, tx, tenantID, job.ID); err != nil {
		return err
	}
	if err = validatePaperImportParseSources(current.Sources, input); err != nil {
		return err
	}
	documents, err := json.Marshal(input.Documents)
	if err != nil {
		return ErrInvalidInput
	}
	persistedPages := input.Pages
	if persistedPages == nil {
		persistedPages = []PaperImportDecodedPage{}
	}
	pages, err := json.Marshal(persistedPages)
	if err != nil {
		return ErrInvalidInput
	}
	issues, err := json.Marshal(input.ExtraIssues)
	if err != nil {
		return ErrInvalidInput
	}
	serializedInput, _ := json.Marshal(input)
	inputHash := contentHash(serializedInput)
	var inputID string
	err = tx.QueryRowContext(ctx, `
INSERT INTO paper_import_parse_input
  (tenant_id,paper_import_id,run_id,generation,source_revision,input_hash,documents,pages,extra_issues,created_by,protocol_version)
VALUES ($1,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10::uuid,2)
ON CONFLICT (tenant_id,run_id,input_hash)
DO UPDATE SET input_hash=EXCLUDED.input_hash
RETURNING id::text`, tenantID, job.ID, runID, generation, sourceRevision, inputHash, documents, pages, issues, actorID).Scan(&inputID)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("paper-import:%s:g:%d:parse:%s", job.ID, generation, inputHash)
	task, err := workerruntime.CreateTaskInTx(ctx, tx, tenantID, actorID, workerruntime.CreateTaskInput{
		TaskType: "paper_parse", QueueName: "paper-parse", SourceType: "paper_import_parse", SourceID: inputID,
		Priority: 55, PayloadSchemaVersion: "paper-import-parse-v1", IdempotencyKey: key, DedupeKey: key,
		Payload:     map[string]any{"paper_import_id": job.ID, "parse_input_id": inputID, "source_revision": sourceRevision},
		MaxAttempts: 3, RetryBackoffSeconds: 15,
	})
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE agent_worker_task SET paper_import_run_id=$3::uuid,paper_import_generation=$4::bigint,task_protocol_version=2,payload_schema_version='paper-import-parse-v2',payload=payload || jsonb_build_object('run_id',$3::text,'generation',$4::bigint) WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, task.ID, runID, generation); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_run SET dispatch_status='queued',dispatch_lease_owner=NULL,dispatch_lease_expires_at=NULL,initial_task_id=COALESCE(initial_task_id,$4::uuid),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND generation=$3 AND status='processing'`, tenantID, runID, generation, task.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt AS attempt
SET status='cancelled',completed_at=now(),error_code='cancelled'
FROM agent_worker_task AS prior
JOIN paper_import_parse_input AS parse_input
  ON parse_input.tenant_id=prior.tenant_id AND parse_input.id=prior.source_id
WHERE attempt.tenant_id=$1 AND attempt.task_id=prior.id AND attempt.completed_at IS NULL
  AND prior.tenant_id=$1 AND prior.source_type='paper_import_parse'
  AND parse_input.paper_import_id=$2::uuid AND prior.id<>$3::uuid
  AND prior.status IN ('queued','leased','running')`, tenantID, job.ID, task.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task AS prior
SET status='cancelled',cancelled_at=now(),completed_at=now(),updated_at=now(),revision=revision+1
FROM paper_import_parse_input AS parse_input
WHERE prior.tenant_id=$1 AND prior.source_type='paper_import_parse'
  AND parse_input.tenant_id=prior.tenant_id AND parse_input.id=prior.source_id
  AND parse_input.paper_import_id=$2::uuid AND prior.id<>$3::uuid
  AND prior.status IN ('queued','leased','running')`, tenantID, job.ID, task.ID)
	return err
}

func getPaperImportInTx(ctx context.Context, tx *sql.Tx, tenantID, importID string) (PaperImportJob, error) {
	job, err := scanPaperImport(tx.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, importID))
	if err != nil {
		return PaperImportJob{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text,file_asset_id::text,document_index,role_hint,detected_role,role_confidence::float8,processing_status,created_at FROM paper_import_source WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL ORDER BY document_index,id`, tenantID, importID)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var source PaperImportSource
		if err = rows.Scan(&source.ID, &source.FileAssetID, &source.DocumentIndex, &source.RoleHint, &source.DetectedRole, &source.RoleConfidence, &source.ProcessingStatus, &source.CreatedAt); err != nil {
			return PaperImportJob{}, err
		}
		job.Sources = append(job.Sources, source)
	}
	return job, rows.Err()
}

// 审核保存需要完整回执；解析入队仍使用最小读取，先拒绝错误来源绑定，再进入运行详情查询。
func getPaperImportDetailsInTx(ctx context.Context, tx *sql.Tx, tenantID, importID string) (PaperImportJob, error) {
	job, err := scanPaperImport(tx.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, importID))
	if err != nil {
		return PaperImportJob{}, err
	}
	return loadPaperImportDetails(ctx, tx, tenantID, job)
}

func (s *PostgresStore) LoadPaperImportParseInput(ctx context.Context, tenantID, inputID string) (string, PaperImportParseRequest, error) {
	var importID string
	var documents, pages, issues []byte
	err := s.db.QueryRowContext(ctx, `SELECT paper_import_id::text,documents,pages,extra_issues FROM paper_import_parse_input WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, inputID).Scan(&importID, &documents, &pages, &issues)
	if errors.Is(err, sql.ErrNoRows) {
		return "", PaperImportParseRequest{}, ErrNotFound
	}
	if err != nil {
		return "", PaperImportParseRequest{}, err
	}
	var input PaperImportParseRequest
	if json.Unmarshal(documents, &input.Documents) != nil || json.Unmarshal(pages, &input.Pages) != nil || json.Unmarshal(issues, &input.ExtraIssues) != nil || len(input.Documents) == 0 {
		return "", PaperImportParseRequest{}, ErrInvalidInput
	}
	return importID, input, nil
}

func (s *PostgresStore) LoadPaperImportParseRunInput(ctx context.Context, tenantID, inputID string) (PaperImportRunBinding, error) {
	var binding PaperImportRunBinding
	var documents, pages, issues, previousSnapshot []byte
	err := s.db.QueryRowContext(ctx, `SELECT input.paper_import_id::text,input.run_id::text,input.generation,input.source_revision,input.input_hash,
       input.documents,input.pages,input.extra_issues,run.command_type,previous.source_snapshot
FROM paper_import_parse_input input
JOIN paper_import_run run ON run.tenant_id=input.tenant_id AND run.id=input.run_id AND run.generation=input.generation
LEFT JOIN paper_import_run previous ON previous.tenant_id=input.tenant_id AND previous.paper_import_id=input.paper_import_id AND previous.generation=input.generation-1
WHERE input.tenant_id=$1 AND input.id=$2::uuid AND input.protocol_version=2`, tenantID, inputID).Scan(
		&binding.ImportID, &binding.RunID, &binding.Generation, &binding.SourceRevision, &binding.InputHash, &documents, &pages, &issues, &binding.CommandType, &previousSnapshot,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PaperImportRunBinding{}, ErrNotFound
	}
	if err != nil {
		return PaperImportRunBinding{}, err
	}
	binding.InputID = inputID
	if json.Unmarshal(documents, &binding.Input.Documents) != nil || json.Unmarshal(pages, &binding.Input.Pages) != nil || json.Unmarshal(issues, &binding.Input.ExtraIssues) != nil || len(binding.Input.Documents) == 0 {
		return PaperImportRunBinding{}, ErrInvalidInput
	}
	if binding.CommandType == "add_sources" && len(previousSnapshot) > 0 {
		binding.NewSourceIDs, err = paperImportNewSourceIDs(binding.Input.Documents, previousSnapshot)
		if err != nil {
			return PaperImportRunBinding{}, err
		}
	}
	serialized, err := json.Marshal(binding.Input)
	if err != nil || contentHash(serialized) != binding.InputHash {
		return PaperImportRunBinding{}, ErrConflict
	}
	return binding, nil
}

func (s *PostgresStore) FailPaperImportRuntime(ctx context.Context, tenantID, importID string, input PaperImportRuntimeFailure) error {
	if input.TaskID == "" || input.LeaseToken == "" || input.ErrorCode == "" {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	runID, generation, _, err := currentPaperImportRunInTx(ctx, tx, tenantID, importID)
	if err != nil {
		return err
	}
	if err = validatePaperImportTaskBinding(ctx, tx, tenantID, input.TaskID, runID, generation); err != nil {
		return err
	}
	task, err := workerruntime.FailTaskInTx(ctx, tx, tenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: input.Retryable, ErrorCode: input.ErrorCode, ErrorDetail: input.ErrorDetail, DurationMS: input.DurationMS})
	if err != nil {
		return err
	}
	// 可重试失败只把任务重新入队，导入仍保持处理中；耗尽重试后才同步终止导入和来源。
	if task.Status == workerruntime.StatusQueued {
		return tx.Commit()
	}
	if task.TaskType == "paper_formula" {
		detail, _ := json.Marshal(map[string]any{"error_code": input.ErrorCode, "error_detail": input.ErrorDetail})
		if _, err = tx.ExecContext(ctx, `UPDATE paper_import_formula_input SET status='failed',result=$4::jsonb,completed_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND run_id=$3::uuid`, tenantID, importID, runID, detail); err != nil {
			return err
		}
	}
	jobErrorCode := "paper_ocr_failed"
	issuePrefix := "扫描文档文字识别失败："
	if input.ErrorCode == "paper_parse_failed" {
		jobErrorCode = "ai_parse_failed"
		issuePrefix = "AI 解析服务暂不可用："
	}
	issues, _ := json.Marshal([]string{issuePrefix + paperImportRuntimeErrorMessage(input.ErrorCode)})
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET status='failed',error_code=$3,issues=$4,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND status='processing' AND deleted_at IS NULL`, tenantID, importID, jobErrorCode, issues); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_source SET processing_status='failed',updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL`, tenantID, importID); err != nil {
		return err
	}
	detail, _ := json.Marshal(input.ErrorDetail)
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_run SET status='failed',error_code=$4,error_detail=$5,completed_at=now(),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND generation=$3 AND status='processing'`, tenantID, runID, generation, input.ErrorCode, detail); err != nil {
		return err
	}
	return tx.Commit()
}

func paperImportRuntimeErrorMessage(code string) string {
	switch strings.TrimSpace(code) {
	case "page_processing_failed":
		return "页面处理失败"
	case "source_ocr_failed":
		return "图片文字识别失败"
	case "paper_ocr_unavailable":
		return "文字识别服务暂不可用"
	case "paper_parse_failed":
		return "结构化解析失败，请稍后重试"
	case "paper_formula_failed":
		return "数学公式识别失败，请稍后重试"
	case "source_download_forbidden":
		return "源文件访问被拒绝，请重新提交资料或联系管理员"
	case "source_not_found":
		return "源文件不存在或已删除"
	case "source_download_failed":
		return "源文件读取失败，请稍后重试"
	default:
		return "处理失败，请检查资料后重试"
	}
}
