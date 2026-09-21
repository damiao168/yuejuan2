package paper

import (
	"context"
	"database/sql"
	"errors"
)

// RetryPaperImportParseGeneration requeues only the immutable parse input for
// the current generation. OCR and formula results are deliberately preserved.
// The active/succeeded branches make client retries safe after a lost response.
func (s *PostgresStore) RetryPaperImportParseGeneration(ctx context.Context, tenantID, importID string, expectedGeneration int64) (PaperImportJob, error) {
	if tenantID == "" || importID == "" || expectedGeneration <= 0 {
		return PaperImportJob{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()

	var jobStatus, jobError, runID, runStatus, runError string
	var generation int64
	err = tx.QueryRowContext(ctx, `
SELECT j.status,j.error_code,j.current_generation,r.id::text,r.status,COALESCE(r.error_code,'')
FROM paper_import_job j
JOIN paper_import_run r
  ON r.tenant_id=j.tenant_id AND r.paper_import_id=j.id AND r.generation=j.current_generation
WHERE j.tenant_id=$1 AND j.id=$2::uuid AND j.deleted_at IS NULL
FOR UPDATE OF j,r`, tenantID, importID).Scan(&jobStatus, &jobError, &generation, &runID, &runStatus, &runError)
	if errors.Is(err, sql.ErrNoRows) {
		return PaperImportJob{}, ErrNotFound
	}
	if err != nil {
		return PaperImportJob{}, err
	}
	if generation != expectedGeneration {
		return PaperImportJob{}, ErrConflict
	}

	var taskID, taskStatus, taskError string
	err = tx.QueryRowContext(ctx, `
SELECT task.id::text,task.status,COALESCE(task.error_code,'')
FROM agent_worker_task task
JOIN paper_import_parse_input input
  ON input.tenant_id=task.tenant_id AND input.id=task.source_id
WHERE task.tenant_id=$1
  AND task.paper_import_run_id=$2::uuid
  AND task.paper_import_generation=$3
  AND task.task_type='paper_parse'
  AND task.source_type='paper_import_parse'
  AND input.paper_import_id=$4::uuid
  AND input.run_id=$2::uuid
  AND input.generation=$3
ORDER BY task.created_at DESC
LIMIT 1
FOR UPDATE OF task`, tenantID, runID, generation, importID).Scan(&taskID, &taskStatus, &taskError)
	if errors.Is(err, sql.ErrNoRows) {
		return PaperImportJob{}, ErrConflict
	}
	if err != nil {
		return PaperImportJob{}, err
	}

	// A repeated HTTP request may arrive after the first request was accepted or
	// even after the very fast deterministic parser completed.
	if (jobStatus == "processing" && (taskStatus == "queued" || taskStatus == "leased" || taskStatus == "running")) ||
		((jobStatus == "review_required" || jobStatus == "applied") && taskStatus == "succeeded") {
		if err = tx.Commit(); err != nil {
			return PaperImportJob{}, err
		}
		return s.GetPaperImport(ctx, tenantID, importID)
	}
	failedParse := jobStatus == "failed" && jobError == "ai_parse_failed" && runStatus == "failed" && runError == "paper_parse_failed" &&
		(taskStatus == "failed" || taskStatus == "dead_letter") && taskError == "paper_parse_failed"
	cancelledParse := jobStatus == "cancelled" && jobError == "paper_import_cancelled" && runStatus == "cancelled" && runError == "paper_import_cancelled" && taskStatus == "cancelled"
	if !failedParse && !cancelledParse {
		return PaperImportJob{}, ErrConflict
	}

	if _, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task
SET status='queued',max_attempts=GREATEST(max_attempts,attempt_count+1),not_before=NULL,
    lease_token=NULL,lease_expires_at=NULL,leased_by=NULL,worker_service=NULL,worker_instance_id=NULL,
    started_at=NULL,completed_at=NULL,cancelled_at=NULL,duration_ms=NULL,error_code=NULL,error_detail='{}'::jsonb,
    result='{}'::jsonb,result_schema_version=NULL,result_payload_hash=NULL,
    progress=jsonb_build_object('stage','paper_parse','completed',0,'total',1,'unit','parse','message','资料结构解析已重新排队'),
    updated_at=now(),revision=revision+1
WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, taskID); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE paper_import_job
SET status='processing',error_code='',issues='[]'::jsonb,updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, importID); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE paper_import_run
SET status='processing',error_code=NULL,error_detail='{}'::jsonb,result_task_id=NULL,
    result_payload_hash=NULL,completed_at=NULL,updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, runID); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE paper_import_source
SET processing_status='processed',updated_at=now()
WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL`, tenantID, importID); err != nil {
		return PaperImportJob{}, err
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return s.GetPaperImport(ctx, tenantID, importID)
}
