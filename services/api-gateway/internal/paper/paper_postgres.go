package paper

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) CreatePaper(ctx context.Context, tenantID string, examID string, userID string, input CreatePaperInput) (Paper, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Paper{}, err
	}
	defer tx.Rollback()
	if err := ensureExamPaperMutableTx(ctx, tx, tenantID, examID); err != nil {
		return Paper{}, err
	}
	version := 1
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_no), 0) + 1 FROM exam_paper WHERE tenant_id = $1 AND exam_id = $2`, tenantID, examID).Scan(&version)
	var paperID string
	if err := tx.QueryRowContext(ctx, `SELECT gen_random_uuid()::text`).Scan(&paperID); err != nil {
		return Paper{}, err
	}
	fileID := input.FileAssetID
	fileInput := input.File
	if fileID != "" {
		var linkedExamID string
		err := tx.QueryRowContext(ctx, `
SELECT id::text, COALESCE(exam_id::text, ''), original_name, content_type, size_bytes, hash_sha256, storage_bucket, storage_key
FROM file_asset
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, fileID).Scan(&fileID, &linkedExamID, &fileInput.OriginalName, &fileInput.ContentType, &fileInput.SizeBytes, &fileInput.HashSHA256, &fileInput.StorageBucket, &fileInput.StorageKey)
		if errors.Is(err, sql.ErrNoRows) {
			return Paper{}, ErrNotFound
		}
		if err != nil {
			return Paper{}, err
		}
		if linkedExamID != "" && linkedExamID != examID {
			return Paper{}, ErrInvalidInput
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE file_asset
SET exam_id = $3, owner_type = 'exam_paper', owner_id = $4::uuid
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, fileID, examID, paperID); err != nil {
			return Paper{}, err
		}
	} else {
		if err := tx.QueryRowContext(ctx, `SELECT gen_random_uuid()::text`).Scan(&fileID); err != nil {
			return Paper{}, err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO file_asset (id, tenant_id, exam_id, owner_type, owner_id, original_name, content_type, size_bytes, hash_sha256, storage_bucket, storage_key, visibility, uploaded_by)
VALUES ($1, $2, $3, 'exam_paper', $4, $5, $6, $7, $8, $9, $10, 'private', $11)
`, fileID, tenantID, examID, paperID, fileInput.OriginalName, fileInput.ContentType, fileInput.SizeBytes, fileInput.HashSHA256, fileInput.StorageBucket, fileInput.StorageKey, userID); err != nil {
			return Paper{}, err
		}
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO exam_paper (id, tenant_id, exam_id, file_asset_id, version_no, status, uploaded_by)
VALUES ($1, $2, $3, $4, $5, 'uploaded', $6)
RETURNING id::text, tenant_id::text, exam_id::text, file_asset_id::text, version_no, status
`, paperID, tenantID, examID, fileID, version, userID)
	var out Paper
	if err := row.Scan(&out.ID, &out.TenantID, &out.ExamID, &out.FileAssetID, &out.VersionNo, &out.Status); err != nil {
		return Paper{}, err
	}
	out.File = fileInput
	if err := tx.Commit(); err != nil {
		return Paper{}, err
	}
	return out, nil
}

func (s *PostgresStore) ListPapers(ctx context.Context, tenantID string, examID string) ([]Paper, error) {
	return listPapers(ctx, s.db, tenantID, examID)
}

func listPapers(ctx context.Context, queryer postgresQueryer, tenantID string, examID string) ([]Paper, error) {
	rows, err := queryer.QueryContext(ctx, `
SELECT p.id::text, p.tenant_id::text, p.exam_id::text, p.file_asset_id::text, p.version_no, p.status,
       f.original_name, f.content_type, f.size_bytes, f.hash_sha256, f.storage_bucket, f.storage_key
FROM exam_paper p
JOIN file_asset f ON f.tenant_id = p.tenant_id AND f.id = p.file_asset_id
WHERE p.tenant_id = $1 AND p.exam_id = $2 AND p.deleted_at IS NULL
ORDER BY p.version_no DESC
`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Paper{}
	for rows.Next() {
		var item Paper
		if err := rows.Scan(&item.ID, &item.TenantID, &item.ExamID, &item.FileAssetID, &item.VersionNo, &item.Status, &item.File.OriginalName, &item.File.ContentType, &item.File.SizeBytes, &item.File.HashSHA256, &item.File.StorageBucket, &item.File.StorageKey); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
