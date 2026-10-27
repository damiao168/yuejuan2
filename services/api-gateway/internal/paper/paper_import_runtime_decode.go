package paper

// 来源解码阶段沿用同一事务固定运行、完成上游并排队下游；不得分开提交。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func (s *PostgresStore) QueuePaperImportOCR(ctx context.Context, tenantID string, job PaperImportJob, actorID string, assets []PaperImportOCRAsset) error {
	return s.QueuePaperImportDecode(ctx, tenantID, job, actorID, assets, nil)
}

// QueuePaperImportDecode 将来源文件渲染为页面图像。
// 旧名称 QueuePaperImportOCR 保留为兼容包装，但新分发的导入在此阶段后不调度文本或公式 OCR。
func (s *PostgresStore) QueuePaperImportDecode(ctx context.Context, tenantID string, job PaperImportJob, actorID string, assets []PaperImportOCRAsset, baseDocuments []PaperImportParseDocument) error {
	if len(assets) == 0 {
		return ErrInvalidInput
	}
	documents := make([]map[string]any, 0, len(assets))
	for _, asset := range assets {
		if asset.SourceID == "" || asset.DocumentIndex < 0 || !validPaperImportRole(asset.RoleHint, true) {
			return ErrInvalidInput
		}
		documents = append(documents, map[string]any{
			"source_id": asset.SourceID, "document_index": asset.DocumentIndex, "role_hint": asset.RoleHint, "file_asset_id": asset.FileAssetID,
			"download_url": "/api/v1/files/" + asset.FileAssetID + "/download",
			"content_type": asset.ContentType, "render_dpi": 220, "max_pages": 100,
		})
	}
	sourceRevision := paperImportSourceConfigurationHash(job.Sources)
	payload, _ := json.Marshal(map[string]any{"paper_import_id": job.ID, "exam_id": job.ExamID, "run_id": job.RunID, "generation": job.Generation, "source_revision": sourceRevision, "documents": documents, "parse_documents": baseDocuments})
	idempotencyKey := fmt.Sprintf("paper-import:%s:g:%d:decode:%s", job.ID, job.Generation, contentHash(payload))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	runID, generation, currentRevision, runErr := currentPaperImportRunInTx(ctx, tx, tenantID, job.ID)
	if runErr != nil {
		return runErr
	}
	if generation != job.Generation || runID != job.RunID || currentRevision != sourceRevision {
		return ErrConflict
	}
	if err = validatePaperImportDispatchLease(ctx, tx, tenantID, job); err != nil {
		return err
	}
	if err = lockPaperImportTasksInTx(ctx, tx, tenantID, job.ID); err != nil {
		return err
	}
	var taskID string
	if err = tx.QueryRowContext(ctx, `INSERT INTO agent_worker_task(tenant_id,task_type,queue_name,source_type,source_id,priority,payload,payload_schema_version,idempotency_key,dedupe_key,max_attempts,retry_backoff_seconds,created_by,paper_import_run_id,paper_import_generation,task_protocol_version)
VALUES($1,'layout','page-processing','paper_import_job',$2::uuid,55,$3,'paper-import-decode-v3',$4,$4,3,10,$5::uuid,$6::uuid,$7,2)
ON CONFLICT(tenant_id,task_type,idempotency_key) DO UPDATE SET updated_at=agent_worker_task.updated_at
RETURNING id::text`, tenantID, job.ID, payload, idempotencyKey, actorID, runID, generation).Scan(&taskID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt AS attempt
SET status='cancelled',completed_at=now(),error_code='cancelled'
FROM agent_worker_task AS task
WHERE attempt.tenant_id=$1 AND attempt.task_id=task.id AND attempt.completed_at IS NULL
  AND task.tenant_id=$1 AND task.source_type='paper_import_job' AND task.source_id=$2::uuid
  AND task.id<>$3::uuid AND task.status IN ('queued','leased','running')`, tenantID, job.ID, taskID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task
SET status='cancelled',cancelled_at=now(),completed_at=now(),updated_at=now(),revision=revision+1
WHERE tenant_id=$1 AND source_type='paper_import_job' AND source_id=$2::uuid
  AND id<>$3::uuid AND status IN ('queued','leased','running')`, tenantID, job.ID, taskID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_run SET dispatch_status='queued',dispatch_lease_owner=NULL,dispatch_lease_expires_at=NULL,initial_task_id=COALESCE(initial_task_id,$4::uuid),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND generation=$3 AND status='processing'`, tenantID, runID, generation, taskID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) CompletePaperImportDecode(ctx context.Context, tenantID, importID string, input PaperImportDecodeResult) error {
	if input.TaskID == "" || input.LeaseToken == "" || len(input.Pages) == 0 {
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
	if err = validatePaperImportStageBinding(ctx, tx, tenantID, input.TaskID, runID, generation, paperImportTaskStage{taskType: "layout", sourceType: "paper_import_job", sourceID: importID}); err != nil {
		return err
	}
	var taskPayloadJSON []byte
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM agent_worker_task WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, input.TaskID).Scan(&taskPayloadJSON); err != nil {
		return err
	}
	var taskPayload struct {
		ParseDocuments []PaperImportParseDocument `json:"parse_documents"`
	}
	if json.Unmarshal(taskPayloadJSON, &taskPayload) != nil {
		return ErrInvalidInput
	}
	result := mapFromJSON(input)
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, input.TaskID, workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "paper-import-decode-result-v3", Result: result, DurationMS: input.DurationMS}); err != nil {
		return err
	}
	var actorID string
	if err = tx.QueryRowContext(ctx, `SELECT created_by::text FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND status='processing' AND deleted_at IS NULL FOR UPDATE`, tenantID, importID).Scan(&actorID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	job, err := getPaperImportInTx(ctx, tx, tenantID, importID)
	if err != nil {
		return err
	}
	parseInput, err := buildDecodedVisualParseInput(job, taskPayload.ParseDocuments, input.Pages)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_source SET processing_status='processed',updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL`, tenantID, importID); err != nil {
		return err
	}
	if err = queuePaperImportParseInTx(ctx, tx, tenantID, job, actorID, parseInput); err != nil {
		return err
	}
	return tx.Commit()
}
