package regrade

import (
	"context"
	"database/sql"
)

func (s *PostgresStore) Approve(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	return s.jobTransition(ctx, tenantID, jobID, actorID, jobTransitionRule{expected: StatusAwaitingApproval, target: StatusApproved, eventType: "approved"}, func(ctx context.Context, tx *sql.Tx) (Job, error) {
		return scanJob(tx.QueryRowContext(ctx, `UPDATE regrade_job SET status='approved',approved_by=$3::uuid,approved_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+jobReturning, tenantID, jobID, actorID))
	})
}

func (s *PostgresStore) Start(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	return s.jobTransition(ctx, tenantID, jobID, actorID, jobTransitionRule{expected: StatusApproved, target: StatusRunning, eventType: "started"}, func(ctx context.Context, tx *sql.Tx) (Job, error) {
		return scanJob(tx.QueryRowContext(ctx, `UPDATE regrade_job SET status='running',updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+jobReturning, tenantID, jobID))
	})
}

func (s *PostgresStore) Pause(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	job, err := jobForUpdate(ctx, tx, tenantID, jobID)
	if err != nil {
		return Job{}, err
	}
	if job.Status == StatusPaused {
		return job, nil
	}
	if job.Status != StatusRunning && job.Status != StatusDiffReview {
		return Job{}, ErrStateConflict
	}
	job, err = scanJob(tx.QueryRowContext(ctx, `UPDATE regrade_job SET status='paused',updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+jobReturning, tenantID, jobID))
	if err != nil {
		return Job{}, err
	}
	if err = addEventTx(ctx, tx, tenantID, Event{JobID: jobID, Type: "paused", ActorID: actorID, Payload: map[string]any{}}); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *PostgresStore) Resume(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	job, err := jobForUpdate(ctx, tx, tenantID, jobID)
	if err != nil {
		return Job{}, err
	}
	if job.Status == StatusRunning || job.Status == StatusDiffReview {
		return job, nil
	}
	if job.Status != StatusPaused {
		return Job{}, ErrStateConflict
	}
	// 条目写入也持有父作业锁；在锁内从候选/复核事实恢复阶段，不能把已录完候选的作业退回运行阶段。
	job, err = scanJob(tx.QueryRowContext(ctx, `UPDATE regrade_job SET status=CASE WHEN EXISTS(
SELECT 1 FROM regrade_item WHERE tenant_id=$1::uuid AND job_id=$2::uuid
AND (candidate_score IS NOT NULL OR status IN ('candidate_ready','awaiting_review','resolved','exception'))
) THEN 'diff_review' ELSE 'running' END,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+jobReturning, tenantID, jobID))
	if err != nil {
		return Job{}, err
	}
	if err = addEventTx(ctx, tx, tenantID, Event{JobID: jobID, Type: "resumed", ActorID: actorID, Payload: map[string]any{}}); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}

// 同一转换的允许状态、目标状态和事件类型一起声明，避免调整状态时漏改审计事件。
type jobTransitionRule struct {
	expected, target, eventType string
}

// jobTransition 在任务行锁内检查状态，并把状态变化与事件一起提交；已到目标状态的重试直接返回。
func (s *PostgresStore) jobTransition(ctx context.Context, tenantID, jobID, actorID string, rule jobTransitionRule, update func(context.Context, *sql.Tx) (Job, error)) (Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	current, err := jobForUpdate(ctx, tx, tenantID, jobID)
	if err != nil {
		return Job{}, err
	}
	if current.Status == rule.target {
		return current, nil
	}
	if current.Status != rule.expected {
		return Job{}, ErrStateConflict
	}
	job, err := update(ctx, tx)
	if err != nil {
		return Job{}, err
	}
	if err = addEventTx(ctx, tx, tenantID, Event{JobID: jobID, Type: rule.eventType, ActorID: actorID, Payload: map[string]any{}}); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *PostgresStore) Finalize(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	job, err := jobForUpdate(ctx, tx, tenantID, jobID)
	if err != nil {
		return Job{}, err
	}
	if job.Status == StatusReadyForRelease {
		return job, nil
	}
	if job.Status != StatusDiffReview {
		return Job{}, ErrStateConflict
	}
	var outstanding int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM regrade_item WHERE tenant_id=$1::uuid AND job_id=$2::uuid AND status<>'resolved'`, tenantID, jobID).Scan(&outstanding); err != nil {
		return Job{}, err
	}
	if outstanding > 0 {
		return Job{}, ErrStateConflict
	}
	job, err = scanJob(tx.QueryRowContext(ctx, `UPDATE regrade_job SET status='ready_for_release',finalized_by=$3::uuid,finalized_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+jobReturning, tenantID, jobID, actorID))
	if err != nil {
		return Job{}, err
	}
	if err = addEventTx(ctx, tx, tenantID, Event{JobID: jobID, Type: "finalized", ActorID: actorID, Payload: map[string]any{"next": "create_new_score_release"}}); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	return job, nil
}
