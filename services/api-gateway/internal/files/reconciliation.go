package files

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type ReconciliationOptions struct {
	Bucket          string
	BatchSize       int
	ObjectScanLimit int
	StaleAfter      time.Duration
	Repair          bool
}

type ReconciliationRun struct {
	ID             string     `json:"id"`
	Mode           string     `json:"mode"`
	Status         string     `json:"status"`
	ScannedAssets  int        `json:"scanned_assets"`
	ScannedObjects int        `json:"scanned_objects"`
	FindingCount   int        `json:"finding_count"`
	RepairedCount  int        `json:"repaired_count"`
	StartedAt      time.Time  `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	ErrorDetail    string     `json:"error_detail,omitempty"`
}

type ReconciliationFinding struct {
	ID          string         `json:"id"`
	RunID       string         `json:"run_id"`
	TenantID    string         `json:"tenant_id,omitempty"`
	FileAssetID string         `json:"file_asset_id,omitempty"`
	Bucket      string         `json:"storage_bucket"`
	Key         string         `json:"storage_key"`
	Code        string         `json:"code"`
	Detail      map[string]any `json:"detail"`
	Repaired    bool           `json:"repaired"`
	CreatedAt   time.Time      `json:"created_at"`
}

type ReconciliationReader interface {
	LatestReconciliation(context.Context) (ReconciliationRun, []ReconciliationFinding, error)
}

type Reconciler struct {
	db      *sql.DB
	objects ReconciliationObjectStorage
}

func NewReconciler(db *sql.DB, objects ReconciliationObjectStorage) *Reconciler {
	return &Reconciler{db: db, objects: objects}
}

func (r *Reconciler) Run(ctx context.Context, options ReconciliationOptions) (run ReconciliationRun, runErr error) {
	if options.BatchSize <= 0 || options.BatchSize > 1000 {
		options.BatchSize = 200
	}
	if options.ObjectScanLimit <= 0 || options.ObjectScanLimit > 10000 {
		options.ObjectScanLimit = 1000
	}
	if options.StaleAfter <= 0 {
		options.StaleAfter = time.Hour
	}
	if options.Bucket == "" {
		return run, errors.New("reconciliation bucket is required")
	}
	// report 模式仍记录本次巡检与发现，但不修复资产或删除对象。
	mode := "report"
	if options.Repair {
		mode = "repair"
	}
	if err := r.db.QueryRowContext(ctx, `
INSERT INTO file_reconciliation_run(mode) VALUES($1)
RETURNING id::text,mode,status,started_at`, mode).Scan(&run.ID, &run.Mode, &run.Status, &run.StartedAt); err != nil {
		return run, err
	}
	defer func() {
		status := "completed"
		detail := ""
		if runErr != nil {
			status = "failed"
			detail = truncateStorageError(runErr.Error())
		}
		var completed time.Time
		// 完结写入不受巡检取消影响，但仍沿用调用方的租户或维护作用域。
		finishErr := r.db.QueryRowContext(context.WithoutCancel(ctx), `
UPDATE file_reconciliation_run
SET status=$2,scanned_assets=$3,scanned_objects=$4,finding_count=$5,
    repaired_count=$6,completed_at=now(),error_detail=NULLIF($7,'')
WHERE id=$1::uuid
RETURNING completed_at`, run.ID, status, run.ScannedAssets, run.ScannedObjects,
			run.FindingCount, run.RepairedCount, detail).Scan(&completed)
		if finishErr != nil && runErr == nil {
			runErr = finishErr
		}
		run.Status = status
		run.ErrorDetail = detail
		run.CompletedAt = &completed
	}()

	if err := r.scanAssets(ctx, options, &run); err != nil {
		return run, err
	}

	// 孤立对象只在本次限定数量内检查并记录，不能据此认定整个桶已检查完毕。
	objects, err := r.objects.List(ctx, options.Bucket, "tenant/", options.ObjectScanLimit)
	if err != nil {
		return run, fmt.Errorf("list object storage for reconciliation: %w", err)
	}
	for _, object := range objects {
		run.ScannedObjects++
		var exists bool
		if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM file_asset WHERE storage_bucket=$1 AND storage_key=$2 AND deleted_at IS NULL
)`, object.Bucket, object.Key).Scan(&exists); err != nil {
			return run, err
		}
		if !exists {
			if _, err := r.addFinding(ctx, run.ID, FileAsset{}, reconciliationFindingInput{
				bucket: object.Bucket, key: object.Key, code: "orphan_object",
				detail: map[string]any{"size_bytes": object.SizeBytes},
			}); err != nil {
				return run, err
			}
			run.FindingCount++
		}
	}
	return run, nil
}

func (r *Reconciler) scanAssets(ctx context.Context, options ReconciliationOptions, run *ReconciliationRun) error {
	cursor := ""
	for {
		assets, err := r.listAssets(ctx, cursor, options.BatchSize)
		if err != nil {
			return err
		}
		if len(assets) == 0 {
			break
		}
		for _, asset := range assets {
			run.ScannedAssets++
			findings, err := r.inspectAsset(ctx, run.ID, asset, options)
			if err != nil {
				return err
			}
			run.FindingCount += len(findings)
			for _, finding := range findings {
				if finding.Repaired {
					run.RepairedCount++
				}
			}
			cursor = asset.ID
		}
		if len(assets) < options.BatchSize {
			break
		}
	}
	return nil
}

func (r *Reconciler) LatestReconciliation(ctx context.Context) (ReconciliationRun, []ReconciliationFinding, error) {
	var run ReconciliationRun
	err := r.db.QueryRowContext(ctx, `
SELECT id::text,mode,status,scanned_assets,scanned_objects,finding_count,repaired_count,
       started_at,completed_at,COALESCE(error_detail,'')
FROM file_reconciliation_run ORDER BY started_at DESC,id DESC LIMIT 1`).Scan(
		&run.ID, &run.Mode, &run.Status, &run.ScannedAssets, &run.ScannedObjects,
		&run.FindingCount, &run.RepairedCount, &run.StartedAt, &run.CompletedAt, &run.ErrorDetail,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ReconciliationRun{}, []ReconciliationFinding{}, nil
	}
	if err != nil {
		return run, nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT id::text,run_id::text,COALESCE(tenant_id::text,''),COALESCE(file_asset_id::text,''),
       storage_bucket,storage_key,code,detail,repaired,created_at
FROM file_reconciliation_finding WHERE run_id=$1::uuid ORDER BY created_at,id LIMIT 200`, run.ID)
	if err != nil {
		return run, nil, err
	}
	defer rows.Close()
	findings := make([]ReconciliationFinding, 0)
	for rows.Next() {
		var finding ReconciliationFinding
		var detail []byte
		if err := rows.Scan(&finding.ID, &finding.RunID, &finding.TenantID, &finding.FileAssetID,
			&finding.Bucket, &finding.Key, &finding.Code, &detail, &finding.Repaired, &finding.CreatedAt); err != nil {
			return run, nil, err
		}
		_ = json.Unmarshal(detail, &finding.Detail)
		findings = append(findings, finding)
	}
	return run, findings, rows.Err()
}

func (r *Reconciler) listAssets(ctx context.Context, cursor string, limit int) ([]FileAsset, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT `+assetColumns+`
FROM file_asset
WHERE deleted_at IS NULL AND lifecycle_status<>'deleted'
  AND ($1='' OR id>$1::uuid)
ORDER BY id LIMIT $2`, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets := make([]FileAsset, 0, limit)
	for rows.Next() {
		var asset FileAsset
		if err := scanAsset(rows, &asset); err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

func (r *Reconciler) inspectAsset(ctx context.Context, runID string, asset FileAsset, options ReconciliationOptions) ([]ReconciliationFinding, error) {
	findings := make([]ReconciliationFinding, 0, 2)
	add := func(code string, detail map[string]any, repaired bool) error {
		finding, err := r.addFinding(ctx, runID, asset, reconciliationFindingInput{
			bucket: asset.StorageBucket, key: asset.StorageKey, code: code, detail: detail, repaired: repaired,
		})
		if err == nil {
			findings = append(findings, finding)
		}
		return err
	}
	expectedPrefix := "tenant/" + asset.TenantID + "/"
	if !strings.HasPrefix(asset.StorageKey, expectedPrefix) {
		if err := add("tenant_prefix_mismatch", map[string]any{"expected_prefix": expectedPrefix}, false); err != nil {
			return findings, err
		}
	}

	info, err := r.objects.Stat(ctx, asset.StorageBucket, asset.StorageKey)
	if errors.Is(err, ErrObjectNotFound) {
		// 回调可能追加发现；先执行分支，再取返回切片，避免返回旧的长度。
		err = r.inspectMissingAsset(ctx, asset, options, add)
		return findings, err
	}
	if err != nil {
		return findings, fmt.Errorf("stat object %s: %w", asset.StorageKey, err)
	}
	if info.SizeBytes != asset.SizeBytes {
		repaired := false
		if options.Repair {
			repaired, err = r.setLifecycle(ctx, asset, LifecycleQuarantined, "object_size_mismatch")
			if err != nil {
				return findings, err
			}
		}
		if err := add("size_mismatch", map[string]any{"expected_size": asset.SizeBytes, "actual_size": info.SizeBytes}, repaired); err != nil {
			return findings, err
		}
		return findings, nil
	}
	actualHash, err := r.objectHash(ctx, asset.StorageBucket, asset.StorageKey)
	if err != nil {
		return findings, err
	}
	if !strings.EqualFold(actualHash, asset.HashSHA256) {
		repaired := false
		if options.Repair {
			repaired, err = r.setLifecycle(ctx, asset, LifecycleQuarantined, "object_hash_mismatch")
			if err != nil {
				return findings, err
			}
		}
		if err := add("hash_mismatch", map[string]any{}, repaired); err != nil {
			return findings, err
		}
		return findings, nil
	}

	// 只有大小和完整内容哈希都匹配，才允许把已有对象恢复为可继续处理的状态。
	err = r.inspectVerifiedAsset(ctx, asset, options, add)
	return findings, err
}

func (r *Reconciler) inspectMissingAsset(ctx context.Context, asset FileAsset, options ReconciliationOptions, add func(string, map[string]any, bool) error) error {
	code := "object_missing"
	if asset.Lifecycle == LifecyclePendingUpload || asset.Lifecycle == LifecycleUploadFailed {
		// 上传元数据可能先于对象落盘；等待宽限期，避免把进行中的上传判为遗失。
		if time.Since(asset.CreatedAt) < options.StaleAfter {
			return nil
		}
		code = "stale_pending_upload"
	}
	repaired := false
	if options.Repair {
		var err error
		repaired, err = r.repairMissing(ctx, asset)
		if err != nil {
			return err
		}
	}
	return add(code, map[string]any{"lifecycle_status": asset.Lifecycle}, repaired)
}

func (r *Reconciler) inspectVerifiedAsset(ctx context.Context, asset FileAsset, options ReconciliationOptions, add func(string, map[string]any, bool) error) error {
	var err error
	switch asset.Lifecycle {
	case LifecyclePendingUpload, LifecycleUploadFailed:
		repaired := false
		if options.Repair {
			repaired, err = r.setLifecycle(ctx, asset, LifecycleOrphanRecovered, "")
			if err != nil {
				return err
			}
		}
		if err := add("recoverable_pending_upload", map[string]any{}, repaired); err != nil {
			return err
		}
	case LifecycleMissingObject, LifecycleQuarantined:
		repaired := false
		if options.Repair {
			repaired, err = r.setLifecycle(ctx, asset, LifecycleActive, "")
			if err != nil {
				return err
			}
		}
		if err := add("recoverable_metadata_state", map[string]any{"lifecycle_status": asset.Lifecycle}, repaired); err != nil {
			return err
		}
	case LifecyclePendingDelete, LifecycleDeleteFailed:
		repaired := false
		var referencePinned bool
		if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM question_reference_manifest_item i JOIN file_asset f ON f.tenant_id=i.tenant_id AND f.id IN(i.source_file_asset_id,i.page_file_asset_id,i.derived_file_asset_id) WHERE f.storage_bucket=$1 AND f.storage_key=$2)`, asset.StorageBucket, asset.StorageKey).Scan(&referencePinned); err != nil {
			return err
		}
		if referencePinned {
			return add("reference_material_pinned", map[string]any{}, false)
		}
		if options.Repair && !asset.LegalHold && (asset.RetentionUntil == nil || !asset.RetentionUntil.After(time.Now().UTC())) {
			// 远端删除成功后才标记 deleted；SQL 版本检查失败不会恢复已删除的对象。
			if removeErr := r.objects.Remove(ctx, asset.StorageBucket, asset.StorageKey); removeErr == nil {
				repaired, err = r.markDeleted(ctx, asset)
			} else {
				// 删除失败只写入待重试状态；这里返回补偿更新的错误，不直接返回远端错误。
				_, err = r.setLifecycle(ctx, asset, LifecycleDeleteFailed, "object_remove_failed")
			}
			if err != nil {
				return err
			}
		}
		if err := add("delete_cleanup_pending", map[string]any{"legal_hold": asset.LegalHold}, repaired); err != nil {
			return err
		}
	default:
		if options.Repair {
			_, err = r.db.ExecContext(ctx, `UPDATE file_asset SET verified_at=now(),updated_at=now() WHERE id=$1::uuid AND revision=$2`, asset.ID, asset.Revision)
		}
	}
	return err
}

func (r *Reconciler) repairMissing(ctx context.Context, asset FileAsset) (bool, error) {
	if asset.Lifecycle == LifecyclePendingDelete || asset.Lifecycle == LifecycleDeleteFailed {
		if asset.LegalHold || (asset.RetentionUntil != nil && asset.RetentionUntil.After(time.Now().UTC())) {
			return false, nil
		}
		return r.markDeleted(ctx, asset)
	}
	if asset.Lifecycle == LifecycleActive || asset.Lifecycle == LifecycleOrphanRecovered {
		return r.setLifecycle(ctx, asset, LifecycleMissingObject, "object_missing")
	}
	return false, nil
}

func (r *Reconciler) setLifecycle(ctx context.Context, asset FileAsset, status string, errorCode string) (bool, error) {
	// 巡检可能耗时较长；版本已变化时放弃这次修复，避免覆盖期间的业务写入。
	result, err := r.db.ExecContext(ctx, `
UPDATE file_asset
SET lifecycle_status=$3,revision=revision+1,last_storage_error=NULLIF($4,''),
    last_storage_error_at=CASE WHEN $4='' THEN last_storage_error_at ELSE now() END,
    verified_at=CASE WHEN $3 IN ('active','orphan_recovered') THEN now() ELSE verified_at END,
    delete_attempts=CASE WHEN $3='delete_failed' THEN delete_attempts+1 ELSE delete_attempts END,
    updated_at=now()
WHERE id=$1::uuid AND revision=$2 AND deleted_at IS NULL`, asset.ID, asset.Revision, status, errorCode)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (r *Reconciler) markDeleted(ctx context.Context, asset FileAsset) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
UPDATE file_asset SET lifecycle_status='deleted',revision=revision+1,deleted_at=now(),updated_at=now()
WHERE id=$1::uuid AND revision=$2 AND deleted_at IS NULL AND legal_hold=false
  AND (retention_until IS NULL OR retention_until<=now())`, asset.ID, asset.Revision)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (r *Reconciler) objectHash(ctx context.Context, bucket string, key string) (string, error) {
	body, err := r.objects.Get(ctx, bucket, key)
	if err != nil {
		return "", err
	}
	defer body.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, body); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type reconciliationFindingInput struct {
	bucket, key, code string
	detail            map[string]any
	repaired          bool
}

func (r *Reconciler) addFinding(ctx context.Context, runID string, asset FileAsset, input reconciliationFindingInput) (ReconciliationFinding, error) {
	encoded, _ := json.Marshal(input.detail)
	var finding ReconciliationFinding
	var detailRaw []byte
	err := r.db.QueryRowContext(ctx, `
INSERT INTO file_reconciliation_finding(run_id,tenant_id,file_asset_id,storage_bucket,storage_key,code,detail,repaired)
VALUES($1::uuid,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8)
RETURNING id::text,run_id::text,COALESCE(tenant_id::text,''),COALESCE(file_asset_id::text,''),
          storage_bucket,storage_key,code,detail,repaired,created_at`, runID, asset.TenantID, asset.ID,
		input.bucket, input.key, input.code, encoded, input.repaired).Scan(&finding.ID, &finding.RunID, &finding.TenantID,
		&finding.FileAssetID, &finding.Bucket, &finding.Key, &finding.Code, &detailRaw, &finding.Repaired, &finding.CreatedAt)
	if err == nil {
		_ = json.Unmarshal(detailRaw, &finding.Detail)
	}
	return finding, err
}

func truncateStorageError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 1000 {
		return value[:1000]
	}
	return value
}
