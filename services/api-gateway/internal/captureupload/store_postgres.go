package captureupload

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

const sessionColumns = `
id::text, tenant_id::text, exam_id::text, capture_batch_id::text,
idempotency_key, original_name, content_type, expected_sha256, total_size,
chunk_size, confirmed_offset, status, COALESCE(file_asset_id::text, ''),
COALESCE(capture_file_id::text, ''), COALESCE(error_code, ''), created_at, completed_at, COALESCE(completion_token::text, ''), COALESCE(completion_lease_until, 'epoch'::timestamptz)`

func (s *PostgresStore) Init(ctx context.Context, tenantID, _ string, input InitInput, chunkSize int64) (Session, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	session, found, err := getSessionForUpdate(ctx, tx, tenantID, "capture_batch_id::text = $2 AND idempotency_key = $3", input.BatchID, input.IdempotencyKey)
	if err != nil {
		return Session{}, false, err
	}
	if found {
		if session.ExamID != input.ExamID || session.SHA256 != input.SHA256 || session.Size != input.Size || session.ContentType != input.MIME {
			return Session{}, false, ErrConflict
		}
		if err := tx.Commit(); err != nil {
			return Session{}, false, err
		}
		return session, true, nil
	}

	row := tx.QueryRowContext(ctx, `
INSERT INTO capture_upload_session (
  id, tenant_id, exam_id, capture_batch_id, idempotency_key, original_name,
  content_type, expected_sha256, total_size, chunk_size, confirmed_offset, status
)
VALUES ($1, $2, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10, 0, 'uploading')
RETURNING `+sessionColumns, uuid.NewString(), tenantID, input.ExamID, input.BatchID, input.IdempotencyKey, input.Filename, input.MIME, input.SHA256, input.Size, chunkSize)
	created, err := scanSession(row)
	if err != nil {
		// A concurrent idempotent init may have won between the select and
		// insert. Re-read its immutable contract after releasing this
		// transaction; do not recursively retain the first transaction.
		if isUniqueViolation(err) {
			_ = tx.Rollback()
			current, readErr := s.findByIdempotency(ctx, tenantID, input.BatchID, input.IdempotencyKey)
			if readErr != nil {
				return Session{}, false, readErr
			}
			if current.ExamID != input.ExamID || current.SHA256 != input.SHA256 || current.Size != input.Size || current.ContentType != input.MIME {
				return Session{}, false, ErrConflict
			}
			return current, true, nil
		}
		return Session{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, false, err
	}
	return created, false, nil
}

func (s *PostgresStore) findByIdempotency(ctx context.Context, tenantID, batchID, idempotencyKey string) (Session, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT `+sessionColumns+` FROM capture_upload_session
WHERE tenant_id=$1 AND capture_batch_id::text=$2 AND idempotency_key=$3
`, tenantID, batchID, idempotencyKey)
	return scanSession(row)
}

func (s *PostgresStore) Get(ctx context.Context, tenantID, uploadID string) (Session, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM capture_upload_session WHERE tenant_id=$1 AND id::text=$2`, tenantID, uploadID)
	return scanSession(row)
}

func (s *PostgresStore) AppendChunk(ctx context.Context, tenantID, uploadID string, input ChunkInput) (Session, error) {
	// 锁住会话并把分块与确认偏移一起提交，避免客户端看到尚未持久化的进度。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback() }()
	session, found, err := getSessionForUpdate(ctx, tx, tenantID, "id::text = $2", uploadID)
	if err != nil {
		return Session{}, err
	}
	if !found {
		return Session{}, ErrNotFound
	}
	if session.Status != "uploading" {
		return Session{}, ErrConflict
	}
	if input.Offset < session.ConfirmedOffset {
		var existingHash string
		var existingSize int
		err := tx.QueryRowContext(ctx, `
SELECT chunk_sha256, size_bytes FROM capture_upload_chunk
WHERE tenant_id=$1 AND capture_upload_session_id::text=$2 AND offset_bytes=$3
`, tenantID, uploadID, input.Offset).Scan(&existingHash, &existingSize)
		if err == nil && existingHash == input.SHA256 && existingSize == len(input.Data) {
			if err := tx.Commit(); err != nil {
				return Session{}, err
			}
			return session, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Session{}, err
		}
		return Session{}, ErrConflict
	}
	if input.Offset != session.ConfirmedOffset || input.Offset+int64(len(input.Data)) > session.Size {
		return Session{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO capture_upload_chunk (tenant_id, capture_upload_session_id, offset_bytes, size_bytes, chunk_sha256, payload)
VALUES ($1, $2::uuid, $3, $4, $5, $6)
`, tenantID, uploadID, input.Offset, len(input.Data), input.SHA256, input.Data); err != nil {
		if isUniqueViolation(err) {
			return Session{}, ErrConflict
		}
		return Session{}, err
	}
	row := tx.QueryRowContext(ctx, `
UPDATE capture_upload_session
SET confirmed_offset=confirmed_offset+$3, updated_at=now(), error_code=NULL
WHERE tenant_id=$1 AND id::text=$2 AND confirmed_offset=$4 AND status='uploading'
RETURNING `+sessionColumns, tenantID, uploadID, len(input.Data), input.Offset)
	updated, err := scanSession(row)
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return updated, nil
}

func (s *PostgresStore) BeginComplete(ctx context.Context, tenantID, uploadID string) (Session, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	session, found, err := getSessionForUpdate(ctx, tx, tenantID, "id::text = $2", uploadID)
	if err != nil {
		return Session{}, false, err
	}
	if !found {
		return Session{}, false, ErrNotFound
	}
	if session.Status == "completed" {
		if err := tx.Commit(); err != nil {
			return Session{}, false, err
		}
		return session, false, nil
	}
	// 所有实例以数据库时钟判断租约，避免服务器时钟偏差导致提前接管。
	var databaseNow time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		return Session{}, false, err
	}
	if (session.Status == "finalizing" && session.CompletionLeaseUntil.After(databaseNow)) || session.Status == "failed" {
		return Session{}, false, ErrConflict
	}
	if (session.Status != "uploading" && session.Status != "finalizing") || session.ConfirmedOffset != session.Size {
		return Session{}, false, ErrIncomplete
	}
	row := tx.QueryRowContext(ctx, `
UPDATE capture_upload_session SET status='finalizing', updated_at=now(), completion_token=$3::uuid, completion_lease_until=clock_timestamp()+($4 * interval '1 millisecond')
WHERE tenant_id=$1 AND id::text=$2 AND status IN ('uploading','finalizing')
RETURNING `+sessionColumns, tenantID, uploadID, uuid.NewString(), completionLeaseDuration.Milliseconds())
	updated, err := scanSession(row)
	if err != nil {
		return Session{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, false, err
	}
	return updated, true, nil
}

func (s *PostgresStore) ReadChunks(ctx context.Context, tenantID, uploadID string, consume func([]byte) error) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT offset_bytes, size_bytes, payload FROM capture_upload_chunk
WHERE tenant_id=$1 AND capture_upload_session_id::text=$2
ORDER BY offset_bytes ASC
`, tenantID, uploadID)
	if err != nil {
		return err
	}
	defer rows.Close()
	expectedOffset := int64(0)
	for rows.Next() {
		var offset int64
		var size int
		var payload []byte
		if err := rows.Scan(&offset, &size, &payload); err != nil {
			return err
		}
		if offset != expectedOffset || size != len(payload) {
			return ErrIncomplete
		}
		if err := consume(payload); err != nil {
			return err
		}
		expectedOffset += int64(size)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	session, err := s.Get(ctx, tenantID, uploadID)
	if err != nil {
		return err
	}
	if expectedOffset != session.Size {
		return ErrIncomplete
	}
	return nil
}

func (s *PostgresStore) Complete(ctx context.Context, tenantID, uploadID, fileAssetID, captureFileID, completionToken string) (Session, error) {
	// 完成回执和分块清理同事务提交；失败回滚后仍可读取原分块重试。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback() }()
	row := tx.QueryRowContext(ctx, `
UPDATE capture_upload_session
SET status='completed', file_asset_id=$3::uuid, capture_file_id=$4::uuid,
    error_code=NULL, completed_at=now(), updated_at=now()
WHERE tenant_id=$1 AND id::text=$2 AND status='finalizing' AND completion_token=$5::uuid
RETURNING `+sessionColumns, tenantID, uploadID, fileAssetID, captureFileID, completionToken)
	session, err := scanSession(row)
	if errors.Is(err, ErrNotFound) {
		return Session{}, ErrConflict
	}
	if err != nil {
		return Session{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM capture_upload_chunk WHERE tenant_id=$1 AND capture_upload_session_id::text=$2`, tenantID, uploadID); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *PostgresStore) Resume(ctx context.Context, tenantID, uploadID, errorCode, completionToken string) error {
	return s.setFinalizingStatus(ctx, tenantID, uploadID, "uploading", errorCode, completionToken)
}

func (s *PostgresStore) Fail(ctx context.Context, tenantID, uploadID, errorCode, completionToken string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
UPDATE capture_upload_session SET status='failed', error_code=NULLIF($3, ''), updated_at=now()
WHERE tenant_id=$1 AND id::text=$2 AND status='finalizing' AND completion_token=$4::uuid
`, tenantID, uploadID, errorCode, completionToken)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrConflict
	}
	// The workstation retains the source asset. Keeping failed server chunks
	// would only retain an untrusted duplicate without making it resumable.
	if _, err := tx.ExecContext(ctx, `DELETE FROM capture_upload_chunk WHERE tenant_id=$1 AND capture_upload_session_id::text=$2`, tenantID, uploadID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) setFinalizingStatus(ctx context.Context, tenantID, uploadID, status, errorCode, completionToken string) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE capture_upload_session SET status=$3, error_code=NULLIF($4, ''), updated_at=now()
WHERE tenant_id=$1 AND id::text=$2 AND status='finalizing' AND completion_token=$5::uuid
`, tenantID, uploadID, status, errorCode, completionToken)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrConflict
	}
	return nil
}

func getSessionForUpdate(ctx context.Context, tx *sql.Tx, tenantID, predicate string, args ...any) (Session, bool, error) {
	params := append([]any{tenantID}, args...)
	row := tx.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM capture_upload_session WHERE tenant_id=$1 AND `+predicate+` FOR UPDATE`, params...)
	session, err := scanSession(row)
	if errors.Is(err, ErrNotFound) {
		return Session{}, false, nil
	}
	return session, err == nil, err
}

func scanSession(scanner interface{ Scan(...any) error }) (Session, error) {
	var session Session
	if err := scanner.Scan(
		&session.ID, &session.TenantID, &session.ExamID, &session.BatchID,
		&session.IdempotencyKey, &session.OriginalName, &session.ContentType, &session.SHA256, &session.Size,
		&session.ChunkSize, &session.ConfirmedOffset, &session.Status, &session.FileAssetID, &session.CaptureFileID,
		&session.ErrorCode, &session.CreatedAt, &session.CompletedAt, &session.CompletionToken, &session.CompletionLeaseUntil,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	return session, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint"))
}
