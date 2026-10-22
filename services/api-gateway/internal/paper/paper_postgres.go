package paper

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) CreatePaper(ctx context.Context, tenantID string, examID string, userID string, input CreatePaperInput) (Paper, error) {
	purpose, err := normalizePaperPurpose(input.Purpose)
	if err != nil {
		return Paper{}, err
	}
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
	// 引用已上传资产时使用库内元数据，并拒绝关联其他考试的文件；文件归属与试卷版本同事务写入。
	fileID := input.FileAssetID
	fileInput := input.File
	if fileID != "" {
		var linkedExamID string
		err := tx.QueryRowContext(ctx, `
SELECT id::text, COALESCE(exam_id::text, ''), original_name, content_type, size_bytes, hash_sha256, storage_bucket, storage_key
FROM file_asset
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
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
		// 上传成功后登记响应可能丢失；同一资产的重试返回原版本，不能重复登记或改作另一种用途。
		var existing Paper
		err = tx.QueryRowContext(ctx, `SELECT id::text, tenant_id::text, exam_id::text, file_asset_id::text, version_no, status, purpose FROM exam_paper WHERE tenant_id = $1 AND file_asset_id::text = $2 AND deleted_at IS NULL LIMIT 1`, tenantID, fileID).Scan(&existing.ID, &existing.TenantID, &existing.ExamID, &existing.FileAssetID, &existing.VersionNo, &existing.Status, &existing.Purpose)
		if err == nil {
			if existing.ExamID != examID || existing.Purpose != purpose {
				return Paper{}, ErrInvalidInput
			}
			existing.File = fileInput
			existing.Duplicate = true
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Paper{}, err
		}
		if existing, found, err := duplicateAnswerSheetPaperTx(ctx, tx, tenantID, examID, purpose, fileInput.HashSHA256); err != nil {
			return Paper{}, err
		} else if found {
			return existing, nil
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE file_asset
SET exam_id = $3, owner_type = 'exam_paper', owner_id = $4::uuid
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, fileID, examID, paperID); err != nil {
			return Paper{}, err
		}
	} else {
		if existing, found, err := duplicateAnswerSheetPaperTx(ctx, tx, tenantID, examID, purpose, fileInput.HashSHA256); err != nil {
			return Paper{}, err
		} else if found {
			return existing, nil
		}
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
INSERT INTO exam_paper (id, tenant_id, exam_id, file_asset_id, version_no, status, uploaded_by, purpose)
VALUES ($1, $2, $3, $4, $5, 'uploaded', $6, $7)
RETURNING id::text, tenant_id::text, exam_id::text, file_asset_id::text, version_no, status, purpose
`, paperID, tenantID, examID, fileID, version, userID, purpose)
	var out Paper
	if err := row.Scan(&out.ID, &out.TenantID, &out.ExamID, &out.FileAssetID, &out.VersionNo, &out.Status, &out.Purpose); err != nil {
		return Paper{}, err
	}
	out.File = fileInput
	if err := tx.Commit(); err != nil {
		return Paper{}, err
	}
	return out, nil
}

func duplicateAnswerSheetPaperTx(ctx context.Context, tx *sql.Tx, tenantID, examID, purpose, hash string) (Paper, bool, error) {
	if purpose != PaperPurposeAnswerSheet || hash == "" {
		return Paper{}, false, nil
	}
	// 考试行锁已由调用方持有；不同上传资产但字节内容相同也只能登记一个答题卡来源。
	var existing Paper
	err := tx.QueryRowContext(ctx, `SELECT p.id::text,p.tenant_id::text,p.exam_id::text,p.file_asset_id::text,p.version_no,p.status,p.purpose,
 f.original_name,f.content_type,f.size_bytes,f.hash_sha256,f.storage_bucket,f.storage_key
FROM exam_paper p JOIN file_asset f ON f.tenant_id=p.tenant_id AND f.id=p.file_asset_id
WHERE p.tenant_id=$1 AND p.exam_id=$2::uuid AND p.purpose='answer_sheet' AND p.deleted_at IS NULL
 AND f.deleted_at IS NULL AND f.hash_sha256=$3 ORDER BY p.version_no LIMIT 1`, tenantID, examID, hash).Scan(
		&existing.ID, &existing.TenantID, &existing.ExamID, &existing.FileAssetID, &existing.VersionNo, &existing.Status, &existing.Purpose,
		&existing.File.OriginalName, &existing.File.ContentType, &existing.File.SizeBytes, &existing.File.HashSHA256, &existing.File.StorageBucket, &existing.File.StorageKey)
	if errors.Is(err, sql.ErrNoRows) {
		return Paper{}, false, nil
	}
	if err != nil {
		return Paper{}, false, err
	}
	existing.Duplicate = true
	return existing, true, nil
}

func (s *PostgresStore) ListPapers(ctx context.Context, tenantID string, examID string) ([]Paper, error) {
	return listPapers(ctx, s.db, tenantID, examID)
}

func listPapers(ctx context.Context, queryer postgresQueryer, tenantID string, examID string) ([]Paper, error) {
	rows, err := queryer.QueryContext(ctx, `
SELECT p.id::text, p.tenant_id::text, p.exam_id::text, p.file_asset_id::text, p.version_no, p.status, p.purpose,
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
		if err := rows.Scan(&item.ID, &item.TenantID, &item.ExamID, &item.FileAssetID, &item.VersionNo, &item.Status, &item.Purpose, &item.File.OriginalName, &item.File.ContentType, &item.File.SizeBytes, &item.File.HashSHA256, &item.File.StorageBucket, &item.File.StorageKey); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
