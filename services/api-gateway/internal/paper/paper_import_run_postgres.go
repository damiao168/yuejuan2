package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const paperImportDispatchMaxAttempts = 3

// 同一轮识别的来源快照只写一次；追加资料通过比较前后快照确定新增来源。
func savePaperImportSourceSnapshot(ctx context.Context, tx *sql.Tx, tenantID, runID string, sources []PaperImportSource) error {
	raw, err := json.Marshal(append([]PaperImportSource{}, sources...))
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE paper_import_run SET source_snapshot=$3::jsonb WHERE tenant_id=$1 AND id=$2::uuid AND source_snapshot IS NULL`, tenantID, runID, raw)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PostgresStore) ClaimPendingPaperImportDispatch(ctx context.Context, owner string, lease time.Duration) (PaperImportJob, bool, error) {
	if strings.TrimSpace(owner) == "" || lease <= 0 {
		return PaperImportJob{}, false, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, false, err
	}
	defer tx.Rollback()
	var tenantID, importID, runID string
	// 多实例跳过已被锁定的工作，并只领取当前代次；领取次数与派发租约一起提交。
	err = tx.QueryRowContext(ctx, `SELECT run.tenant_id::text,run.paper_import_id::text,run.id::text
FROM paper_import_run run JOIN paper_import_job job ON job.tenant_id=run.tenant_id AND job.id=run.paper_import_id AND job.current_generation=run.generation
WHERE run.status='processing' AND run.dispatch_status IN ('pending','failed') AND run.dispatch_available_at<=now()
  AND run.dispatch_attempt_count<$1
  AND (run.dispatch_lease_expires_at IS NULL OR run.dispatch_lease_expires_at<now()) AND job.status='processing' AND job.deleted_at IS NULL
ORDER BY run.dispatch_available_at,run.created_at FOR UPDATE OF job,run SKIP LOCKED LIMIT 1`, paperImportDispatchMaxAttempts).Scan(&tenantID, &importID, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return PaperImportJob{}, false, nil
	}
	if err != nil {
		return PaperImportJob{}, false, err
	}
	claimed, err := tx.ExecContext(ctx, `UPDATE paper_import_run SET dispatch_lease_owner=$4,dispatch_lease_expires_at=now()+($5*interval '1 millisecond'),dispatch_attempt_count=dispatch_attempt_count+1,updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND id=$3::uuid AND status='processing'`, tenantID, importID, runID, owner, lease.Milliseconds())
	if err != nil {
		return PaperImportJob{}, false, err
	}
	if rows, rowsErr := claimed.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return PaperImportJob{}, false, rowsErr
		}
		return PaperImportJob{}, false, ErrConflict
	}
	job, err := getPaperImportInTx(ctx, tx, tenantID, importID)
	if err != nil {
		return PaperImportJob{}, false, err
	}
	job.RunID = runID
	job.dispatchLeaseOwner = owner
	if err = tx.QueryRowContext(ctx, `SELECT generation,source_revision FROM paper_import_run WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, runID).Scan(&job.Generation, &job.SourceRevision); err != nil {
		return PaperImportJob{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, false, err
	}
	return job, true, nil
}

func validatePaperImportDispatchLease(ctx context.Context, tx *sql.Tx, tenantID string, job PaperImportJob) error {
	if job.dispatchLeaseOwner == "" {
		return nil
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT dispatch_lease_owner=$3 AND dispatch_lease_expires_at>clock_timestamp() AND dispatch_status IN ('pending','failed')
FROM paper_import_run WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, job.RunID, job.dispatchLeaseOwner).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrConflict
	}
	return nil
}

func (s *PostgresStore) FailPendingPaperImportDispatch(ctx context.Context, job PaperImportJob, owner, message string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	runID, generation, revision, err := currentPaperImportRunInTx(ctx, tx, job.TenantID, job.ID)
	if err != nil {
		return err
	}
	if runID != job.RunID || generation != job.Generation || revision != job.SourceRevision {
		return ErrConflict
	}
	var attempts int
	err = tx.QueryRowContext(ctx, `UPDATE paper_import_run SET dispatch_status='failed',dispatch_lease_owner=NULL,dispatch_lease_expires_at=NULL,dispatch_available_at=now()+LEAST(dispatch_attempt_count,6)*interval '5 seconds',error_detail=jsonb_build_object('dispatch_error',$4::text),updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND dispatch_lease_owner=$3 AND dispatch_lease_expires_at>clock_timestamp() AND dispatch_status IN ('pending','failed') AND status='processing'
RETURNING dispatch_attempt_count`, job.TenantID, runID, owner, strings.TrimSpace(message)).Scan(&attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if attempts < paperImportDispatchMaxAttempts {
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_run SET status='failed',error_code='paper_import_dispatch_exhausted',completed_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, job.TenantID, runID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE paper_import_job SET status='failed',error_code='paper_import_dispatch_exhausted',issues='["资料准备多次失败，请检查源文件后重新运行"]'::jsonb,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND current_generation=$3 AND status='processing'`, job.TenantID, job.ID, generation)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_source SET processing_status='failed',updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL`, job.TenantID, job.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func findPaperImportCommandInTx(ctx context.Context, tx *sql.Tx, tenantID, actorID, commandID string) (string, string, error) {
	if commandID == "" || actorID == "" {
		return "", "", ErrNotFound
	}
	// Serialize the same command before checking its receipt, including the
	// first insert where there is no business row to lock yet.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "paper-import-command:"+tenantID+":"+actorID+":"+commandID); err != nil {
		return "", "", err
	}
	var importID, requestHash string
	err := tx.QueryRowContext(ctx, `SELECT paper_import_id::text,command_request_hash
FROM paper_import_run WHERE tenant_id=$1 AND actor_id=$2::uuid AND command_id=$3`, tenantID, actorID, commandID).Scan(&importID, &requestHash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return importID, requestHash, err
}

func ensureImportExamMutableInTx(ctx context.Context, tx *sql.Tx, tenantID, importID string) error {
	var examID string
	err := tx.QueryRowContext(ctx, `SELECT exam_id::text FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, importID).Scan(&examID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return ensureExamPaperMutableTx(ctx, tx, tenantID, examID)
}

func supersedePaperImportRunsInTx(ctx context.Context, tx *sql.Tx, tenantID, importID string, generation int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE paper_import_run
SET status='superseded',completed_at=COALESCE(completed_at,now()),updated_at=now()
WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND generation<$3 AND status='processing'`, tenantID, importID, generation); err != nil {
		return err
	}
	if err := lockPaperImportTasksInTx(ctx, tx, tenantID, importID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_worker_task_attempt attempt
SET status='cancelled',completed_at=now(),error_code='run_superseded'
FROM agent_worker_task task
WHERE attempt.tenant_id=task.tenant_id AND attempt.task_id=task.id AND attempt.completed_at IS NULL
  AND task.tenant_id=$1 AND task.paper_import_run_id IN (
    SELECT id FROM paper_import_run WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND generation<$3
  ) AND task.status IN ('queued','leased','running')`, tenantID, importID, generation); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE agent_worker_task
SET status='cancelled',cancelled_at=now(),completed_at=now(),updated_at=now(),revision=revision+1,error_code='run_superseded'
WHERE tenant_id=$1 AND paper_import_run_id IN (
  SELECT id FROM paper_import_run WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND generation<$3
) AND status IN ('queued','leased','running')`, tenantID, importID, generation)
	return err
}

// 按任务 ID 固定加锁顺序，供替换、取消和重新排队共用，减少并发事务的锁顺序冲突。
func lockPaperImportTasksInTx(ctx context.Context, tx *sql.Tx, tenantID, importID string) error {
	_, err := tx.ExecContext(ctx, `SELECT t.id FROM agent_worker_task t JOIN paper_import_run r ON r.tenant_id=t.tenant_id AND r.id=t.paper_import_run_id WHERE t.tenant_id=$1 AND r.paper_import_id=$2::uuid AND t.status IN ('queued','leased','running') ORDER BY t.id FOR UPDATE OF t`, tenantID, importID)
	return err
}

func currentPaperImportRunInTx(ctx context.Context, tx *sql.Tx, tenantID, importID string) (string, int64, string, error) {
	runID, generation, revision, status, err := paperImportRunForUpdate(ctx, tx, tenantID, importID)
	if err == nil && status != "processing" {
		return "", 0, "", ErrConflict
	}
	return runID, generation, revision, err
}

func paperImportRunForUpdate(ctx context.Context, tx *sql.Tx, tenantID, importID string) (string, int64, string, string, error) {
	var runID, revision string
	var status string
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT run.id::text,job.current_generation,job.source_revision,job.status
FROM paper_import_job job
JOIN paper_import_run run ON run.tenant_id=job.tenant_id AND run.paper_import_id=job.id AND run.generation=job.current_generation
WHERE job.tenant_id=$1 AND job.id=$2::uuid AND job.deleted_at IS NULL
FOR UPDATE OF job,run`, tenantID, importID).Scan(&runID, &generation, &revision, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", "", ErrConflict
	}
	return runID, generation, revision, status, err
}

func validatePaperImportTaskBinding(ctx context.Context, tx *sql.Tx, tenantID, taskID, runID string, generation int64) error {
	var taskRunID string
	var taskGeneration int64
	var protocol int
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(paper_import_run_id::text,''),COALESCE(paper_import_generation,0),task_protocol_version
FROM agent_worker_task WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, taskID).Scan(&taskRunID, &taskGeneration, &protocol)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if protocol != 2 || taskRunID != runID || taskGeneration != generation {
		return ErrConflict
	}
	return nil
}

// The run identity alone cannot authorize a stage result: a valid decode lease
// must never complete a parse task, nor may a parse lease publish another input.
func validatePaperImportStageBinding(ctx context.Context, tx *sql.Tx, tenantID, taskID, runID string, generation int64, taskType, sourceType, sourceID string) error {
	if err := validatePaperImportTaskBinding(ctx, tx, tenantID, taskID, runID, generation); err != nil {
		return err
	}
	var matches bool
	err := tx.QueryRowContext(ctx, `SELECT task_type=$3 AND source_type=$4 AND source_id=$5::uuid
FROM agent_worker_task WHERE tenant_id=$1 AND id=$2::uuid FOR UPDATE`, tenantID, taskID, taskType, sourceType, sourceID).Scan(&matches)
	if err != nil {
		return err
	}
	if !matches {
		return ErrConflict
	}
	return nil
}

func (s *PostgresStore) hydratePaperImportRun(ctx context.Context, tenantID string, job *PaperImportJob) error {
	var resultGeneration sql.NullInt64
	var policyJSON, formulaResult []byte
	err := s.db.QueryRowContext(ctx, `SELECT j.current_generation,j.source_revision,COALESCE(r.id::text,''),j.result_generation,
COALESCE(r.authoritative_subject_code,''),COALESCE(r.recognition_policy_hash,''),COALESCE(r.recognition_policy_snapshot,'{}'::jsonb),
COALESCE(f.status,''),COALESCE(f.result,'{}'::jsonb)
FROM paper_import_job j
LEFT JOIN paper_import_run r ON r.tenant_id=j.tenant_id AND r.paper_import_id=j.id AND r.generation=j.current_generation
LEFT JOIN paper_import_formula_input f ON f.tenant_id=r.tenant_id AND f.run_id=r.id
WHERE j.tenant_id=$1 AND j.id=$2::uuid`, tenantID, job.ID).Scan(&job.Generation, &job.SourceRevision, &job.RunID, &resultGeneration, &job.AuthoritativeSubjectCode, &job.RecognitionPolicyHash, &policyJSON, &job.FormulaStatus, &formulaResult)
	if err != nil {
		return err
	}
	if resultGeneration.Valid {
		job.ResultGeneration = resultGeneration.Int64
	}
	var policy PaperRecognitionPolicy
	if json.Unmarshal(policyJSON, &policy) == nil {
		job.RecognitionPolicyVersion = policy.Version
	}
	var formulaResultValue PaperImportFormulaResult
	if json.Unmarshal(formulaResult, &formulaResultValue) == nil {
		job.FormulaRegionCount = len(formulaResultValue.Regions)
		for _, region := range formulaResultValue.Regions {
			if region.Status != "accepted" {
				job.FormulaReviewCount++
			}
		}
	}
	if job.RunID == "" {
		return nil
	}
	var runtime PaperImportRuntimeProgress
	var runtimeJSON []byte
	var startedAt sql.NullTime
	err = s.db.QueryRowContext(ctx, `SELECT task_type,status,progress,started_at,updated_at
FROM agent_worker_task
WHERE tenant_id=$1 AND paper_import_run_id=$2::uuid AND paper_import_generation=$3
ORDER BY CASE status WHEN 'running' THEN 0 WHEN 'leased' THEN 1 WHEN 'queued' THEN 2 ELSE 3 END,
         created_at DESC
LIMIT 1`, tenantID, job.RunID, job.Generation).Scan(
		&runtime.TaskType, &runtime.TaskStatus, &runtimeJSON, &startedAt, &runtime.UpdatedAt,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		_ = json.Unmarshal(runtimeJSON, &runtime)
		if startedAt.Valid {
			value := startedAt.Time.UTC()
			runtime.StartedAt = &value
		}
		job.RuntimeProgress = &runtime
	}
	return nil
}
