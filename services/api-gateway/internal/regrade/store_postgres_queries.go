package regrade

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) Get(ctx context.Context, tenantID, jobID string) (Summary, error) {
	job, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM regrade_job WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return Summary{}, ErrNotFound
	}
	if err != nil {
		return Summary{}, err
	}
	items, err := s.items(ctx, tenantID, jobID)
	if err != nil {
		return Summary{}, err
	}
	events, err := s.events(ctx, tenantID, jobID)
	if err != nil {
		return Summary{}, err
	}
	return Summary{Job: job, Items: items, Events: events}, nil
}

func (s *PostgresStore) List(ctx context.Context, tenantID, examID, questionID string, accesses ...auth.ExamCollectionAccess) ([]Job, error) {
	accessArgs, err := jobCollectionAccess(accesses).SQLArgs(tenantID)
	if err != nil {
		return nil, err
	}
	// 客户端题目/考试条件只能缩小服务端范围，集合不借助单项路由守卫授权。
	args := append([]any{tenantID, examID, questionID}, accessArgs...)
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM regrade_job WHERE tenant_id=$1::uuid AND ($2='' OR exam_id::text=$2) AND ($3='' OR question_id::text=$3) AND `+auth.ExamCollectionPredicate("regrade_job.exam_id", 4)+` ORDER BY created_at DESC,id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []Job{}
	for rows.Next() {
		job, scanErr := scanJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *PostgresStore) ListAssigned(ctx context.Context, tenantID, reviewerID string) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM (
SELECT item.* FROM regrade_item item
JOIN regrade_job job ON job.tenant_id=item.tenant_id AND job.id=item.job_id
WHERE item.tenant_id=$1::uuid AND item.assigned_to=$2::uuid
  AND job.status IN ('running','diff_review')
  AND (item.status='pending' OR (item.status='claimed' AND item.claimed_by=$2::uuid))
) AS regrade_item ORDER BY created_at,id`, tenantID, reviewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		item, scanErr := scanItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) items(ctx context.Context, tenantID, jobID string) ([]Item, error) {
	return s.itemsQuery(ctx, s.db, tenantID, jobID)
}

func (s *PostgresStore) itemsTx(ctx context.Context, tx *sql.Tx, tenantID, jobID string) ([]Item, error) {
	return s.itemsQuery(ctx, tx, tenantID, jobID)
}

func (s *PostgresStore) itemsQuery(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, tenantID, jobID string) ([]Item, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+itemColumns+` FROM regrade_item WHERE tenant_id=$1::uuid AND job_id=$2::uuid ORDER BY created_at,id`, tenantID, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		item, scanErr := scanItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) events(ctx context.Context, tenantID, jobID string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,job_id::text,COALESCE(item_id::text,''),event_type,actor_id::text,payload,created_at FROM regrade_event WHERE tenant_id=$1::uuid AND job_id=$2::uuid ORDER BY created_at,id`, tenantID, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var event Event
		var payload []byte
		if err := rows.Scan(&event.ID, &event.JobID, &event.ItemID, &event.Type, &event.ActorID, &payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &event.Payload); err != nil {
			return nil, err
		}
		event.CreatedAt = event.CreatedAt.UTC()
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *PostgresStore) idempotentTx(ctx context.Context, tx *sql.Tx, tenantID, examID, key string) (Job, bool, error) {
	job, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM regrade_job WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND idempotency_key=$3`, tenantID, examID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	return job, err == nil, err
}

func jobForUpdate(ctx context.Context, tx *sql.Tx, tenantID, jobID string) (Job, error) {
	job, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM regrade_job WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return job, err
}

func itemAndJobForUpdate(ctx context.Context, tx *sql.Tx, tenantID, itemID string) (Item, Job, error) {
	// 先锁明细，再锁所属任务；领题、候选提交和复核都在拿到两把锁后检查状态。
	item, err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM regrade_item WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, Job{}, ErrNotFound
	}
	if err != nil {
		return Item{}, Job{}, err
	}
	job, err := jobForUpdate(ctx, tx, tenantID, item.JobID)
	return item, job, err
}

// addEventTx 使用调用方事务，事件写入失败时不能单独保留任务或条目的变更。
func addEventTx(ctx context.Context, tx *sql.Tx, tenantID string, event Event) error {
	raw, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO regrade_event (tenant_id,job_id,item_id,event_type,actor_id,payload) VALUES ($1::uuid,$2::uuid,NULLIF($3,'')::uuid,$4,$5::uuid,$6::jsonb)`, tenantID, event.JobID, event.ItemID, event.Type, event.ActorID, string(raw))
	return err
}
