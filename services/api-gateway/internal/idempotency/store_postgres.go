package idempotency

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) Begin(ctx context.Context, input BeginInput) (Record, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, false, err
	}
	defer tx.Rollback()
	// TTL 只使已完成的 HTTP 响应缓存过期。处理中预留保存恢复中断命令所需的持久请求指纹；删除它会允许其他载荷取而代之。
	_, _ = tx.ExecContext(ctx, `DELETE FROM idempotency_record WHERE expires_at < now() AND tenant_id=$1 AND state='completed'`, input.TenantID)
	if !input.AllowTakeover {
		// 没有持久业务恢复约定的旧受保护路由，保留原有 TTL 行为；不能只因闭环命令需要指纹，就将旧路由变成永久处理中记录。
		_, _ = tx.ExecContext(ctx, `DELETE FROM idempotency_record
WHERE tenant_id=$1 AND actor_id=$2 AND method=$3 AND route=$4 AND idempotency_key=$5
  AND state='processing' AND expires_at<now()`, input.TenantID, input.ActorID, input.Method, input.Route, input.Key)
	}
	var inserted bool
	err = tx.QueryRowContext(ctx, `
WITH inserted AS (
  INSERT INTO idempotency_record(tenant_id,actor_id,method,route,idempotency_key,request_hash,request_body,expires_at)
  VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''::bytea),$8)
  ON CONFLICT (tenant_id,actor_id,method,route,idempotency_key) DO NOTHING
  RETURNING true
)
SELECT COALESCE((SELECT true FROM inserted), false)`, input.TenantID, input.ActorID, input.Method, input.Route, input.Key, input.RequestHash, input.RequestBody, input.ExpiresAt).Scan(&inserted)
	if err != nil {
		return Record{}, false, err
	}
	if inserted {
		if err = tx.Commit(); err != nil {
			return Record{}, false, err
		}
		return Record{State: "processing", RequestHash: input.RequestHash}, true, nil
	}
	var record Record
	var headersRaw []byte
	err = tx.QueryRowContext(ctx, `
SELECT state,request_hash,COALESCE(request_body,''::bytea),COALESCE(response_status,0),response_headers,COALESCE(response_body,''::bytea),updated_at
FROM idempotency_record
WHERE tenant_id=$1 AND actor_id=$2 AND method=$3 AND route=$4 AND idempotency_key=$5
FOR UPDATE`, input.TenantID, input.ActorID, input.Method, input.Route, input.Key).Scan(
		&record.State, &record.RequestHash, &record.RequestBody, &record.ResponseStatus, &headersRaw, &record.ResponseBody, &record.UpdatedAt,
	)
	if err != nil {
		return Record{}, false, err
	}
	if record.RequestHash != input.RequestHash {
		return Record{}, false, ErrKeyConflict
	}
	if record.State == "processing" {
		if input.AllowTakeover && !input.StaleBefore.IsZero() && record.UpdatedAt.Before(input.StaleBefore) {
			result, updateErr := tx.ExecContext(ctx, `
UPDATE idempotency_record SET updated_at=now(),expires_at=$7
WHERE tenant_id=$1 AND actor_id=$2 AND method=$3 AND route=$4 AND idempotency_key=$5
  AND request_hash=$6 AND state='processing' AND updated_at<$8`,
				input.TenantID, input.ActorID, input.Method, input.Route, input.Key, input.RequestHash, input.ExpiresAt, input.StaleBefore)
			if updateErr != nil {
				return Record{}, false, updateErr
			}
			if rows, _ := result.RowsAffected(); rows == 1 {
				if updateErr = tx.Commit(); updateErr != nil {
					return Record{}, false, updateErr
				}
				return Record{State: "processing", RequestHash: input.RequestHash}, true, nil
			}
		}
		return Record{}, false, ErrInProgress
	}
	if err = json.Unmarshal(headersRaw, &record.ResponseHeaders); err != nil {
		return Record{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Record{}, false, err
	}
	return record, false, nil
}

func (s *PostgresStore) Complete(ctx context.Context, input BeginInput, status int, headers map[string]string, body []byte) error {
	headersRaw, err := json.Marshal(headers)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE idempotency_record
SET state='completed',response_status=$7,response_headers=$8,response_body=$9,updated_at=now()
WHERE tenant_id=$1 AND actor_id=$2 AND method=$3 AND route=$4 AND idempotency_key=$5
  AND request_hash=$6 AND state='processing'`, input.TenantID, input.ActorID, input.Method, input.Route, input.Key, input.RequestHash, status, headersRaw, body)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrKeyConflict
	}
	return nil
}

func (s *PostgresStore) Abort(ctx context.Context, input BeginInput) error {
	_, err := s.db.ExecContext(ctx, `
DELETE FROM idempotency_record
WHERE tenant_id=$1 AND actor_id=$2 AND method=$3 AND route=$4 AND idempotency_key=$5
  AND request_hash=$6 AND state='processing'`, input.TenantID, input.ActorID, input.Method, input.Route, input.Key, input.RequestHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
