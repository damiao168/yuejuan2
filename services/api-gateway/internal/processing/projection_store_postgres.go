package processing

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"strings"
	"time"
)

var ErrProjectionLeaseLost = errors.New("processing projection lease lost")

func (s *PostgresStore) ClaimProjection(ctx context.Context, owner string, leaseTTL time.Duration) (ProjectionRefresh, bool, error) {
	// SKIP LOCKED 让多个投影进程各领一条待处理版本；租约过期后任务才会再次可领取。
	if strings.TrimSpace(owner) == "" || leaseTTL <= 0 {
		return ProjectionRefresh{}, false, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectionRefresh{}, false, err
	}
	defer tx.Rollback()

	var refresh ProjectionRefresh
	err = tx.QueryRowContext(ctx, `
SELECT tenant_id::text, exam_id::text, requested_version, attempt_count + 1
FROM processing_projection_cursor
WHERE requested_version > projected_version
  AND available_at <= now()
  AND (lease_expires_at IS NULL OR lease_expires_at < now())
ORDER BY available_at, requested_at, tenant_id, exam_id
LIMIT 1
FOR UPDATE SKIP LOCKED
`).Scan(&refresh.TenantID, &refresh.ExamID, &refresh.RequestedVersion, &refresh.AttemptCount)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectionRefresh{}, false, nil
	}
	if err != nil {
		return ProjectionRefresh{}, false, err
	}
	refresh.claimID = uuid.NewString()
	if _, err = tx.ExecContext(ctx, `
UPDATE processing_projection_cursor
SET lease_owner=$3, lease_expires_at=now()+($4 * interval '1 millisecond'),
    attempt_count=attempt_count+1
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid
`, refresh.TenantID, refresh.ExamID, owner+":"+refresh.claimID, leaseTTL.Milliseconds()); err != nil {
		return ProjectionRefresh{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return ProjectionRefresh{}, false, err
	}
	return refresh, true, nil
}

func (s *PostgresStore) CompleteProjection(ctx context.Context, owner string, refresh ProjectionRefresh) error {
	// 完成更新必须带上租约所有者和有效期，防止过期 worker 覆盖后来接手的任务。
	result, err := s.db.ExecContext(ctx, `
UPDATE processing_projection_cursor
SET projected_version=GREATEST(projected_version,$4), projected_at=now(),
    available_at=now(), lease_owner=NULL, lease_expires_at=NULL,
    attempt_count=0, last_error=NULL
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND lease_owner=$3 AND lease_expires_at>clock_timestamp()
`, refresh.TenantID, refresh.ExamID, owner+":"+refresh.claimID, refresh.RequestedVersion)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrProjectionLeaseLost
	}
	return nil
}

func (s *PostgresStore) FailProjection(ctx context.Context, owner string, refresh ProjectionRefresh, message string, backoff time.Duration) error {
	// 失败只释放当前租约并推迟 available_at；版本仍保持未投影，供后续重试。
	if backoff < 0 {
		backoff = 0
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE processing_projection_cursor
SET available_at=now()+($4 * interval '1 millisecond'),
    lease_owner=NULL, lease_expires_at=NULL, last_error=$5
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND lease_owner=$3 AND lease_expires_at>clock_timestamp()
`, refresh.TenantID, refresh.ExamID, owner+":"+refresh.claimID, backoff.Milliseconds(), strings.TrimSpace(message))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrProjectionLeaseLost
	}
	return nil
}

var _ ProjectionStore = (*PostgresStore)(nil)
