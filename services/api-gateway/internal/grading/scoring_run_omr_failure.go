package grading

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
	"errors"
)

// ApplyOMRWorkerFailure 与完成回调一样先锁来源，再在同一事务校验并结束当前尝试。
// 任何来源或汇总写入失败都回滚 runtime；否则终止失败会漏出评分工作区的重试列表。
func (s *PostgresStore) ApplyOMRWorkerFailure(ctx context.Context, tenantID, id string, input OMRFailureInput) (OMRRun, string, error) {
	raw, err := json.Marshal(input.ErrorDetail)
	if err != nil {
		return OMRRun{}, "", ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OMRRun{}, "", err
	}
	defer tx.Rollback()
	var runtimeTaskID string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(runtime_task_id::text,'') FROM omr_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id).Scan(&runtimeTaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return OMRRun{}, "", ErrNotFound
	}
	if err != nil {
		return OMRRun{}, "", err
	}
	if runtimeTaskID != input.TaskID {
		return OMRRun{}, "", ErrInvalidTransition
	}
	task, err := workerruntime.FailTaskInTx(ctx, tx, tenantID, input.TaskID, workerruntime.FailInput{
		LeaseToken: input.LeaseToken, Retryable: input.Retryable, ErrorCode: input.ErrorCode, ErrorDetail: input.ErrorDetail, DurationMS: input.DurationMS,
	})
	if err != nil {
		return OMRRun{}, "", err
	}
	if task.SourceType != "omr_run" || task.SourceID != id {
		return OMRRun{}, "", ErrInvalidTransition
	}
	if err = s.applyOMRFailureTx(ctx, tx, tenantID, id, input.ErrorCode, raw, task.Status == workerruntime.StatusQueued); err != nil {
		return OMRRun{}, "", err
	}
	if err = tx.Commit(); err != nil {
		return OMRRun{}, "", err
	}
	out, err := s.GetOMRRun(ctx, tenantID, id)
	return out, task.Status, err
}

// ApplyOMRFailure 只保存领域失败，兼容已协调 runtime 的内部调用；Worker HTTP 回调使用上面的原子命令。
func (s *PostgresStore) ApplyOMRFailure(ctx context.Context, tenantID, id, errorCode string, detail map[string]any, retryable bool) (OMRRun, error) {
	raw, _ := json.Marshal(detail)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OMRRun{}, err
	}
	defer tx.Rollback()
	if err = s.applyOMRFailureTx(ctx, tx, tenantID, id, errorCode, raw, retryable); err != nil {
		return OMRRun{}, err
	}
	if err = tx.Commit(); err != nil {
		return OMRRun{}, err
	}
	return s.GetOMRRun(ctx, tenantID, id)
}

func (s *PostgresStore) applyOMRFailureTx(ctx context.Context, tx *sql.Tx, tenantID, id, errorCode string, raw []byte, retryable bool) error {
	// 可重试失败保留 retryable_error，终止失败写入完成时间；两种结果都刷新同一事务内的汇总。
	status := "terminal_error"
	if retryable {
		status = "retryable_error"
	}
	var runID string
	err := tx.QueryRowContext(ctx, `
UPDATE omr_run o
SET status=$3,error_code=$4,error_detail=$5::jsonb,updated_at=now(),
  completed_at=CASE WHEN $3='terminal_error' THEN now() ELSE NULL END
FROM scoring_run sr
WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid AND o.status IN ('queued','processing','retryable_error')
  AND sr.tenant_id=o.tenant_id AND sr.id=o.scoring_run_id AND sr.status='processing'
RETURNING o.scoring_run_id::text`, tenantID, id, status, errorCode, raw).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRevisionConflict
	}
	if err != nil {
		return err
	}
	_, err = s.refreshScoringRunTx(ctx, tx, tenantID, runID)
	return err
}
