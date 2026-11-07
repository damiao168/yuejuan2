package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) FailPaperImport(ctx context.Context, tenantID, id, code string, issues []string) (PaperImportJob, error) {
	i, _ := json.Marshal(issues)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	var generation int64
	result, err := tx.ExecContext(ctx, `UPDATE paper_import_job SET status='failed',issues=$3,error_code=$4,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND status='processing' AND deleted_at IS NULL`, tenantID, id, i, code)
	if err != nil {
		return PaperImportJob{}, err
	}
	rows, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return PaperImportJob{}, rowsErr
	}
	if rows != 1 {
		return PaperImportJob{}, ErrConflict
	}
	if err = tx.QueryRowContext(ctx, `SELECT current_generation FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id).Scan(&generation); err != nil {
		return PaperImportJob{}, err
	}
	runResult, err := tx.ExecContext(ctx, `UPDATE paper_import_run SET status='failed',dispatch_status=CASE WHEN dispatch_status='pending' THEN 'failed' ELSE dispatch_status END,error_code=$4,completed_at=now(),updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND generation=$3 AND status='processing'`, tenantID, id, generation, code)
	if err != nil {
		return PaperImportJob{}, err
	}
	runRows, rowsErr := runResult.RowsAffected()
	if rowsErr != nil {
		return PaperImportJob{}, rowsErr
	}
	if runRows != 1 {
		return PaperImportJob{}, ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return s.GetPaperImport(ctx, tenantID, id)
}

func (s *PostgresStore) CancelPaperImport(ctx context.Context, tenantID, id string) (PaperImportJob, error) {
	return s.cancelPaperImportGeneration(ctx, tenantID, id, 0)
}

func (s *PostgresStore) CancelPaperImportGeneration(ctx context.Context, tenantID, id string, expectedGeneration int64) (PaperImportJob, error) {
	if expectedGeneration <= 0 {
		return PaperImportJob{}, ErrInvalidInput
	}
	return s.cancelPaperImportGeneration(ctx, tenantID, id, expectedGeneration)
}

func (s *PostgresStore) cancelPaperImportGeneration(ctx context.Context, tenantID, id string, expectedGeneration int64) (PaperImportJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	var status string
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT status,current_generation FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id).Scan(&status, &generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PaperImportJob{}, ErrNotFound
		}
		return PaperImportJob{}, err
	}
	if status != "processing" {
		return PaperImportJob{}, ErrConflict
	}
	if expectedGeneration > 0 && expectedGeneration != generation {
		return PaperImportJob{}, ErrConflict
	}
	issues, _ := json.Marshal([]string{"识别任务已手动停止"})
	if err = lockPaperImportTasksInTx(ctx, tx, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET status='cancelled',issues=$3,error_code='paper_import_cancelled',updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id, issues); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt AS attempt
SET status='cancelled',completed_at=now(),error_code='cancelled'
FROM agent_worker_task AS task
WHERE attempt.tenant_id=$1 AND attempt.task_id=task.id AND attempt.completed_at IS NULL
  AND task.tenant_id=$1 AND (
    (task.source_type='paper_import_job' AND task.source_id=$2::uuid)
    OR (task.source_type='paper_import_parse' AND EXISTS (
      SELECT 1 FROM paper_import_parse_input parse_input
      WHERE parse_input.tenant_id=task.tenant_id AND parse_input.id=task.source_id
        AND parse_input.paper_import_id=$2::uuid
    ))
  )
  AND task.status IN ('queued','leased','running')`, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task
SET status='cancelled',cancelled_at=now(),completed_at=now(),updated_at=now(),revision=revision+1
WHERE tenant_id=$1 AND (
    (source_type='paper_import_job' AND source_id=$2::uuid)
    OR (source_type='paper_import_parse' AND EXISTS (
      SELECT 1 FROM paper_import_parse_input parse_input
      WHERE parse_input.tenant_id=agent_worker_task.tenant_id AND parse_input.id=agent_worker_task.source_id
        AND parse_input.paper_import_id=$2::uuid
    ))
  )
  AND status IN ('queued','leased','running')`, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_source SET processing_status='failed',updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND processing_status IN ('pending','processing') AND deleted_at IS NULL`, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_run SET status='cancelled',dispatch_status=CASE WHEN dispatch_status='pending' THEN 'not_required' ELSE dispatch_status END,error_code='paper_import_cancelled',completed_at=now(),updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND generation=$3 AND status='processing'`, tenantID, id, generation); err != nil {
		return PaperImportJob{}, err
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return s.GetPaperImport(ctx, tenantID, id)
}
