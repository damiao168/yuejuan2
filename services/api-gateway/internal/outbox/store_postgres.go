package outbox

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) Claim(ctx context.Context, owner string, limit int, leaseTTL time.Duration) ([]Event, error) {
	// 领取与租约写入合并为一个 SQL，SKIP LOCKED 让并行分发器跳过其他实例占用的事件。
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if leaseTTL <= 0 {
		leaseTTL = 30 * time.Second
	}
	rows, err := s.db.QueryContext(ctx, `
WITH candidates AS (
  SELECT id
  FROM event_outbox
  WHERE published_at IS NULL
    AND dead_lettered_at IS NULL
    AND next_attempt_at <= now()
    AND (lock_expires_at IS NULL OR lock_expires_at < now())
    AND attempt_count < max_attempts
  ORDER BY next_attempt_at, occurred_at, id
  LIMIT $1
  FOR UPDATE SKIP LOCKED
)
UPDATE event_outbox e
SET locked_by=$2,
    lock_expires_at=now()+($3 * interval '1 millisecond'),
    last_attempt_at=now(),
    attempt_count=e.attempt_count+1
FROM candidates c
WHERE e.id=c.id
RETURNING e.id::text,e.tenant_id::text,e.aggregate_type,e.aggregate_id::text,
          e.event_type,e.payload,e.occurred_at,e.attempt_count,e.max_attempts`, limit, owner, leaseTTL.Milliseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]Event, 0, limit)
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.TenantID, &event.AggregateType, &event.AggregateID, &event.EventType, &event.Payload, &event.OccurredAt, &event.AttemptCount, &event.MaxAttempts); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *PostgresStore) MarkPublished(ctx context.Context, eventID string, owner string) error {
	// 只有当前未过期租约的持有者能确认发布，迟到实例不能覆盖接管者的状态。
	result, err := s.db.ExecContext(ctx, `
UPDATE event_outbox
SET published_at=now(),locked_by=NULL,lock_expires_at=NULL,last_error=NULL
WHERE id=$1::uuid AND published_at IS NULL AND dead_lettered_at IS NULL
  AND locked_by=$2 AND lock_expires_at>=now()`, eventID, owner)
	if err != nil {
		return err
	}
	return requireOneRow(result)
}

func (s *PostgresStore) MarkFailed(ctx context.Context, eventID string, owner string, failure string, retryDelay time.Duration) error {
	if retryDelay < 0 {
		retryDelay = 0
	}
	failure = strings.TrimSpace(failure)
	if len(failure) > 1000 {
		failure = failure[:1000]
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE event_outbox
SET last_error=$3,
    dead_lettered_at=CASE WHEN attempt_count>=max_attempts THEN now() ELSE NULL END,
    next_attempt_at=CASE WHEN attempt_count>=max_attempts THEN next_attempt_at ELSE now()+($4 * interval '1 millisecond') END,
    locked_by=NULL,
    lock_expires_at=NULL
WHERE id=$1::uuid AND published_at IS NULL AND dead_lettered_at IS NULL
  AND locked_by=$2 AND lock_expires_at>=now()`, eventID, owner, failure, retryDelay.Milliseconds())
	if err != nil {
		return err
	}
	return requireOneRow(result)
}

func requireOneRow(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrLeaseLost
	}
	return nil
}
