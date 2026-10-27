package paper

// 文字识别完成与公式/解析分流共用调用方事务，沿用已冻结的识别策略。

import (
	"context"
	"encoding/json"
	"fmt"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func (s *PostgresStore) CompletePaperImportOCR(ctx context.Context, tenantID, importID string, input PaperImportOCRResult, parseInput PaperImportParseRequest) error {
	if input.TaskID == "" || input.LeaseToken == "" || len(input.Blocks) == 0 || len(parseInput.Documents) == 0 {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	runID, generation, sourceRevision, err := currentPaperImportRunInTx(ctx, tx, tenantID, importID)
	if err != nil {
		return err
	}
	if err = validatePaperImportStageBinding(ctx, tx, tenantID, input.TaskID, runID, generation, paperImportTaskStage{taskType: "ocr", sourceType: "paper_import_job", sourceID: importID}); err != nil {
		return err
	}
	var policyJSON []byte
	var policyHash string
	if err = tx.QueryRowContext(ctx, `SELECT recognition_policy_snapshot,recognition_policy_hash FROM paper_import_run WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, runID).Scan(&policyJSON, &policyHash); err != nil {
		return err
	}
	var policy PaperRecognitionPolicy
	if json.Unmarshal(policyJSON, &policy) != nil {
		return ErrInvalidInput
	}
	var ocrPayload []byte
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM agent_worker_task WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, input.TaskID).Scan(&ocrPayload); err != nil {
		return err
	}
	var taskPayload struct {
		Pages []PaperImportDecodedPage `json:"pages"`
	}
	if json.Unmarshal(ocrPayload, &taskPayload) != nil || len(taskPayload.Pages) == 0 {
		return ErrInvalidInput
	}
	parseInput.Pages = taskPayload.Pages
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, input.TaskID, workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "paper-import-ocr-result-v3", Result: mapFromJSON(input), DurationMS: input.DurationMS}); err != nil {
		return err
	}
	var status, actorID string
	if err = tx.QueryRowContext(ctx, `SELECT status,created_by::text FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, importID).Scan(&status, &actorID); err != nil {
		return err
	}
	if status != "processing" {
		return fmt.Errorf("%w: paper import is %s", ErrConflict, status)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_source SET processing_status='processed',updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL`, tenantID, importID); err != nil {
		return err
	}
	job, err := getPaperImportInTx(ctx, tx, tenantID, importID)
	if err != nil {
		return err
	}
	if policy.FormulaEnabled {
		documents, _ := json.Marshal(parseInput.Documents)
		issues, _ := json.Marshal(parseInput.ExtraIssues)
		pagesJSON, _ := json.Marshal(parseInput.Pages)
		inputHash := contentHash(pagesJSON, documents, issues, policyJSON)
		var formulaInputID string
		if err = tx.QueryRowContext(ctx, `INSERT INTO paper_import_formula_input(tenant_id,paper_import_id,run_id,generation,source_revision,policy_hash,pages,documents,extra_issues,created_by)
VALUES($1,$2::uuid,$3::uuid,$4,$5,$6,$7::jsonb,$8::jsonb,$9::jsonb,$10::uuid)
ON CONFLICT(tenant_id,run_id) DO UPDATE SET policy_hash=EXCLUDED.policy_hash
RETURNING id::text`, tenantID, importID, runID, generation, sourceRevision, policyHash, pagesJSON, documents, issues, actorID).Scan(&formulaInputID); err != nil {
			return err
		}
		formulaPayload, _ := json.Marshal(map[string]any{"paper_import_id": importID, "exam_id": job.ExamID, "formula_input_id": formulaInputID, "run_id": runID, "generation": generation, "source_revision": sourceRevision, "subject_code": policy.SubjectCode, "recognition_policy": policy, "pages": parseInput.Pages, "input_hash": inputHash})
		key := fmt.Sprintf("paper-import:%s:g:%d:formula:%s", importID, generation, inputHash)
		if _, err = tx.ExecContext(ctx, `INSERT INTO agent_worker_task(tenant_id,task_type,queue_name,source_type,source_id,priority,payload,payload_schema_version,idempotency_key,dedupe_key,max_attempts,retry_backoff_seconds,created_by,paper_import_run_id,paper_import_generation,task_protocol_version)
VALUES($1,'paper_formula','paper-formula','paper_import_job',$2::uuid,58,$3,'paper-import-formula-v2',$4,$4,2,30,$5::uuid,$6::uuid,$7,2)
ON CONFLICT(tenant_id,task_type,idempotency_key) DO NOTHING`, tenantID, importID, formulaPayload, key, actorID, runID, generation); err != nil {
			return err
		}
	} else if err = queuePaperImportParseInTx(ctx, tx, tenantID, job, actorID, parseInput); err != nil {
		return err
	}
	return tx.Commit()
}
