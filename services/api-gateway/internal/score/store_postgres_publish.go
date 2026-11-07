package score

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/score/autoprep"
	"time"
)

func (s *PostgresStore) ConfirmGrades(ctx context.Context, tenantID string, examID string, actorID string, input ConfirmInput) ([]SubmissionGrade, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var replay []SubmissionGrade
	identity := commandreceipt.Identity{TenantID: tenantID, ActorID: actorID, Operation: "score.confirm", TargetID: examID}
	if found, err := commandreceipt.Load(ctx, tx, identity, input, &replay); err != nil || found {
		return replay, err
	}
	if err := lockExamTx(ctx, tx, tenantID, examID); err != nil {
		return nil, err
	}
	quality, err := s.qualityReportTx(ctx, tx, tenantID, examID, false)
	if err != nil {
		return nil, err
	}
	if !quality.Passed {
		return nil, ErrQualityGateFailed
	}
	count, err := s.countSubmissionGradesTx(ctx, tx, tenantID, examID)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, ErrNotFound
	}
	if invalid, err := s.countUnexpectedSubmissionStatusesTx(ctx, tx, tenantID, examID, "pending_confirmation"); err != nil {
		return nil, err
	} else if invalid > 0 {
		return nil, ErrInvalidTransition
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE submission_grade
SET status = 'confirmed', confirmed_by = $3::uuid, confirmed_at = now(), revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND locked = false AND status = 'pending_confirmation'
`, tenantID, examID, actorID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE final_grade
SET status = 'confirmed', updated_at = now()
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND locked = false AND status = 'pending_confirmation'
`, tenantID, examID); err != nil {
		return nil, err
	}
	grades, err := s.listGradesTx(ctx, tx, tenantID, examID, true)
	if err != nil {
		return nil, err
	}
	if err := commandreceipt.Save(ctx, tx, identity, input, grades); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return grades, nil
}

func (s *PostgresStore) PublishGrades(ctx context.Context, tenantID string, examID string, actorID string, input PublishInput) (PublishResult, error) {
	// 总分公开、题分锁定、考试状态和命令回执在同一事务提交，失败不能只留下部分发布事实。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var replay PublishResult
	identity := commandreceipt.Identity{TenantID: tenantID, ActorID: actorID, Operation: "score.publish", TargetID: examID}
	if found, err := commandreceipt.Load(ctx, tx, identity, input, &replay); err != nil || found {
		return replay, err
	}
	if err := lockExamTx(ctx, tx, tenantID, examID); err != nil {
		return PublishResult{}, err
	}
	if err := autoprep.LockCollection(ctx, tx, tenantID, examID); err != nil {
		return PublishResult{}, err
	}
	if locked, err := s.hasLockedGradesTx(ctx, tx, tenantID, examID); err != nil {
		return PublishResult{}, err
	} else if locked {
		return PublishResult{}, ErrInvalidTransition
	}
	quality, err := s.qualityReportTx(ctx, tx, tenantID, examID, true)
	if err != nil {
		return PublishResult{}, err
	}
	if !quality.Passed {
		return PublishResult{Status: "blocked", Quality: quality}, ErrQualityGateFailed
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
UPDATE submission_grade
SET status = 'published', locked = true, published_by = $3::uuid, published_at = now(), revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
`, tenantID, examID, actorID); err != nil {
		return PublishResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE final_grade
SET status = 'locked', locked = true, updated_at = now()
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
`, tenantID, examID); err != nil {
		return PublishResult{}, err
	}
	if err := publishExamTx(ctx, tx, tenantID, examID); err != nil {
		return PublishResult{}, err
	}
	grades, err := s.listGradesTx(ctx, tx, tenantID, examID, true)
	if err != nil {
		return PublishResult{}, err
	}
	result := PublishResult{
		Status:           "published",
		SubmissionGrades: grades,
		Quality:          QualityReport{Passed: true},
		PublishedAt:      now,
	}

	if err := commandreceipt.Save(ctx, tx, identity, input, result); err != nil {
		return PublishResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishResult{}, err
	}
	return result, nil
}

func (s *PostgresStore) hasLockedGradesTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string) (bool, error) {
	count, err := scalarCountTx(ctx, tx, `SELECT COUNT(*) FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND (locked = true OR status IN ('published', 'locked'))`, tenantID, examID)
	return count > 0, err
}

func (s *PostgresStore) countSubmissionGradesTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string) (int, error) {
	return scalarCountTx(ctx, tx, `SELECT COUNT(*) FROM submission_grade WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL`, tenantID, examID)
}

func (s *PostgresStore) countUnexpectedSubmissionStatusesTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, allowedStatus string) (int, error) {
	return scalarCountTx(ctx, tx, `SELECT COUNT(*) FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
  AND (locked = true OR status <> $3)`, tenantID, examID, allowedStatus)
}

// 同一考试的汇总、确认、发布及出勤修改共用事务锁；新增同类写操作也应遵守此约定。
func lockExamTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, tenantID, examID)
	return err
}

func publishExamTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string) error {
	result, err := tx.ExecContext(ctx, `
UPDATE exam
SET status = 'published', updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
  AND status IN ('draft', 'configured', 'ready', 'collecting', 'grading', 'reviewing', 'finalized')
`, tenantID, examID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 1 {
		return nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM exam WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
)
`, tenantID, examID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return ErrInvalidTransition
}
