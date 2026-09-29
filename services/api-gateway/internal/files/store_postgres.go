package files

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

const assetColumns = `
id::text, tenant_id::text, COALESCE(school_id::text, ''), COALESCE(exam_id::text, ''),
COALESCE(submission_id::text, ''), owner_type, COALESCE(owner_id::text, ''),
original_name, content_type, size_bytes, hash_sha256, storage_bucket, storage_key,
visibility, COALESCE(uploaded_by::text, ''), lifecycle_status, revision,
COALESCE(last_storage_error, ''), delete_attempts, last_storage_error_at,
retention_until, legal_hold, verified_at, created_at`

const assetSelectColumns = assetColumns + `,
COALESCE((SELECT s.student_id::text FROM submission s
          WHERE s.tenant_id=file_asset.tenant_id AND s.id=file_asset.submission_id
            AND s.deleted_at IS NULL), '')`

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) Create(ctx context.Context, input CreateAssetInput) (FileAsset, error) {
	return s.create(ctx, input, LifecycleActive)
}

func (s *PostgresStore) CreatePending(ctx context.Context, input CreateAssetInput) (FileAsset, error) {
	return s.create(ctx, input, LifecyclePendingUpload)
}

func (s *PostgresStore) create(ctx context.Context, input CreateAssetInput, lifecycle string) (FileAsset, error) {
	if existing, ok, err := s.FindDuplicate(ctx, input.TenantID, input.OwnerType, input.OwnerID, input.HashSHA256); err != nil {
		return FileAsset{}, err
	} else if ok {
		return existing, ErrDuplicateFile
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO file_asset (
  tenant_id, school_id, exam_id, submission_id, owner_type, owner_id,
  original_name, content_type, size_bytes, hash_sha256,
  storage_bucket, storage_key, visibility, uploaded_by, lifecycle_status
)
VALUES (
  $1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, $5, NULLIF($6, '')::uuid,
  $7, $8, $9, $10,
  $11, $12, $13, NULLIF($14, '')::uuid, $15
)
RETURNING `+assetColumns+`
`, input.TenantID, input.SchoolID, input.ExamID, input.SubmissionID, input.OwnerType, input.OwnerID,
		input.OriginalName, input.ContentType, input.SizeBytes, input.HashSHA256,
		input.StorageBucket, input.StorageKey, input.Visibility, input.UploadedBy, lifecycle)
	var asset FileAsset
	if err := scanAsset(row, &asset); err != nil {
		return FileAsset{}, err
	}
	return asset, nil
}

func (s *PostgresStore) FindDuplicate(ctx context.Context, tenantID string, ownerType string, ownerID string, hashSHA256 string) (FileAsset, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT `+assetSelectColumns+`
FROM file_asset
WHERE tenant_id = $1
  AND owner_type = $2
  AND (($3 = '' AND owner_id IS NULL) OR owner_id::text = $3)
  AND hash_sha256 = $4
  AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT 1
`, tenantID, ownerType, ownerID, hashSHA256)
	var asset FileAsset
	if err := scanAssetWithStudent(row, &asset); err != nil {
		if errors.Is(err, ErrNotFound) {
			return FileAsset{}, false, nil
		}
		return FileAsset{}, false, err
	}
	return asset, true, nil
}

func (s *PostgresStore) Get(ctx context.Context, tenantID string, id string) (FileAsset, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT `+assetSelectColumns+`
FROM file_asset
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL AND lifecycle_status='active'
`, tenantID, id)
	var asset FileAsset
	if err := scanAssetWithStudent(row, &asset); err != nil {
		return FileAsset{}, err
	}
	return asset, nil
}

func (s *PostgresStore) Delete(ctx context.Context, tenantID string, id string) (FileAsset, error) {
	row := s.db.QueryRowContext(ctx, `
UPDATE file_asset
SET deleted_at = now(), lifecycle_status='deleted', revision=revision+1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
RETURNING `+assetColumns+`
`, tenantID, id)
	var asset FileAsset
	if err := scanAsset(row, &asset); err != nil {
		return FileAsset{}, err
	}
	return asset, nil
}

func (s *PostgresStore) ValidateCreateScope(_ context.Context, scope auth.AccessScope, input CreateAssetInput) error {
	if !AllowsCreate(scope, input) {
		return ErrForbidden
	}
	return nil
}

func (s *PostgresStore) GetScoped(ctx context.Context, scope auth.AccessScope, id string) (FileAsset, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT `+assetSelectColumns+`
FROM file_asset
WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL
`, scope.TenantID, id)
	var asset FileAsset
	if err := scanAssetWithStudent(row, &asset); err != nil {
		return FileAsset{}, err
	}
	if !AllowsAsset(scope, asset) {
		return FileAsset{}, ErrForbidden
	}
	// A file becomes question-bank content once a version pins it. School scope
	// alone must never turn that durable reference into a download capability.
	var bankAuthorized bool
	if err := s.db.QueryRowContext(ctx, `SELECT NOT EXISTS(
		SELECT 1 FROM question_bank_item_asset qa WHERE qa.tenant_id=$1 AND qa.file_asset_id=$2
	) OR EXISTS(
		SELECT 1 FROM question_bank_item_asset qa
		JOIN question_bank_item_version v ON v.tenant_id=qa.tenant_id AND v.id=qa.version_id
		JOIN question_bank_item i ON i.tenant_id=v.tenant_id AND i.id=v.item_id
		WHERE qa.tenant_id=$1 AND qa.file_asset_id=$2
		AND question_bank_actor_has_action(i.tenant_id,i.bank_id,$3::uuid,'read')
	)`, scope.TenantID, id, scope.ActorID).Scan(&bankAuthorized); err != nil {
		return FileAsset{}, err
	}
	if !bankAuthorized {
		return FileAsset{}, ErrForbidden
	}
	return asset, nil
}

func (s *PostgresStore) Activate(ctx context.Context, tenantID, id string, expectedRevision int64) (FileAsset, error) {
	return s.transition(ctx, tenantID, id, expectedRevision, []string{LifecyclePendingUpload, LifecycleUploadFailed}, LifecycleActive, "", false)
}

func (s *PostgresStore) RecoverMissing(ctx context.Context, tenantID, id string, expectedRevision int64) (FileAsset, error) {
	return s.transition(ctx, tenantID, id, expectedRevision, []string{LifecycleMissingObject}, LifecycleActive, "", false)
}

func (s *PostgresStore) MarkMissing(ctx context.Context, tenantID, id string, expectedRevision int64) (FileAsset, error) {
	return s.transition(ctx, tenantID, id, expectedRevision, []string{LifecycleActive}, LifecycleMissingObject, "object_missing", false)
}

func (s *PostgresStore) MarkUploadFailed(ctx context.Context, tenantID, id string, expectedRevision int64, detail string) (FileAsset, error) {
	return s.transition(ctx, tenantID, id, expectedRevision, []string{LifecyclePendingUpload}, LifecycleUploadFailed, detail, false)
}

func (s *PostgresStore) BeginDelete(ctx context.Context, scope auth.AccessScope, id string, expectedRevision int64) (FileAsset, error) {
	asset, err := s.GetScoped(ctx, scope, id)
	if err != nil {
		return FileAsset{}, err
	}
	if asset.Revision != expectedRevision {
		return FileAsset{}, ErrConflict
	}
	// 题库版本引用的是持久内容；即使调用方能读取资产，也不能删除被版本固定的文件。
	var pinned bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM question_bank_item_asset WHERE tenant_id=$1 AND file_asset_id=$2)`, scope.TenantID, id).Scan(&pinned); err != nil {
		return FileAsset{}, err
	}
	if pinned {
		return FileAsset{}, ErrForbidden
	}
	if asset.LegalHold || (asset.RetentionUntil != nil && asset.RetentionUntil.After(time.Now().UTC())) {
		return FileAsset{}, ErrForbidden
	}
	return s.transition(ctx, scope.TenantID, id, expectedRevision, []string{LifecycleActive, LifecycleDeleteFailed}, LifecyclePendingDelete, "", false)
}

func (s *PostgresStore) CompleteDelete(ctx context.Context, tenantID, id string, expectedRevision int64) (FileAsset, error) {
	return s.transition(ctx, tenantID, id, expectedRevision, []string{LifecyclePendingDelete}, LifecycleDeleted, "", true)
}

func (s *PostgresStore) MarkDeleteFailed(ctx context.Context, tenantID, id string, expectedRevision int64, detail string) (FileAsset, error) {
	return s.transition(ctx, tenantID, id, expectedRevision, []string{LifecyclePendingDelete}, LifecycleDeleteFailed, detail, false)
}

func (s *PostgresStore) transition(ctx context.Context, tenantID, id string, expectedRevision int64, from []string, to, detail string, deleted bool) (FileAsset, error) {
	// 前置读取不代表状态仍有效；实际更新同时比较修订号和允许的来源状态。
	placeholders := make([]string, len(from))
	args := []any{tenantID, id, expectedRevision, to, detail, deleted}
	for index, state := range from {
		placeholders[index] = "$" + strconv.Itoa(7+index)
		args = append(args, state)
	}
	query := `
UPDATE file_asset
SET lifecycle_status=$4, revision=revision+1, last_storage_error=NULLIF($5,''),
    last_storage_error_at=CASE WHEN NULLIF($5,'') IS NULL THEN last_storage_error_at ELSE now() END,
    delete_attempts=CASE WHEN $4='delete_failed' THEN delete_attempts+1 ELSE delete_attempts END,
    deleted_at=CASE WHEN $6 THEN now() ELSE deleted_at END, updated_at=now()
WHERE tenant_id=$1 AND id::text=$2 AND revision=$3 AND deleted_at IS NULL
  AND lifecycle_status IN (` + strings.Join(placeholders, ",") + `)
RETURNING ` + assetColumns
	row := s.db.QueryRowContext(ctx, query, args...)
	var asset FileAsset
	if err := scanAsset(row, &asset); err != nil {
		if errors.Is(err, ErrNotFound) {
			var exists bool
			if checkErr := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM file_asset WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL)`, tenantID, id).Scan(&exists); checkErr == nil && exists {
				return FileAsset{}, ErrConflict
			}
		}
		return FileAsset{}, err
	}
	return asset, nil
}

type assetScanner interface {
	Scan(dest ...any) error
}

func scanAsset(row assetScanner, asset *FileAsset) error {
	if err := row.Scan(
		&asset.ID,
		&asset.TenantID,
		&asset.SchoolID,
		&asset.ExamID,
		&asset.SubmissionID,
		&asset.OwnerType,
		&asset.OwnerID,
		&asset.OriginalName,
		&asset.ContentType,
		&asset.SizeBytes,
		&asset.HashSHA256,
		&asset.StorageBucket,
		&asset.StorageKey,
		&asset.Visibility,
		&asset.UploadedBy,
		&asset.Lifecycle,
		&asset.Revision,
		&asset.LastError,
		&asset.DeleteAttempts,
		&asset.LastErrorAt,
		&asset.RetentionUntil,
		&asset.LegalHold,
		&asset.VerifiedAt,
		&asset.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func scanAssetWithStudent(row assetScanner, asset *FileAsset) error {
	if err := row.Scan(
		&asset.ID, &asset.TenantID, &asset.SchoolID, &asset.ExamID, &asset.SubmissionID,
		&asset.OwnerType, &asset.OwnerID, &asset.OriginalName, &asset.ContentType, &asset.SizeBytes,
		&asset.HashSHA256, &asset.StorageBucket, &asset.StorageKey, &asset.Visibility, &asset.UploadedBy,
		&asset.Lifecycle, &asset.Revision, &asset.LastError, &asset.DeleteAttempts, &asset.LastErrorAt,
		&asset.RetentionUntil, &asset.LegalHold, &asset.VerifiedAt, &asset.CreatedAt, &asset.StudentID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}
