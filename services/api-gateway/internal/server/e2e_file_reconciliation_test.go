package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestFileReconciliationWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping PostgreSQL file reconciliation workflow")
	}
	database := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, database)

	var tenantID, userID string
	if err := database.QueryRow(`
SELECT t.id::text,u.id::text FROM tenant t JOIN app_user u ON u.tenant_id=t.id
WHERE t.code='demo' AND u.username='tenant_admin' LIMIT 1`).Scan(&tenantID, &userID); err != nil {
		t.Fatalf("lookup file reconciliation identity: %v", err)
	}
	store := files.NewPostgresStore(database)
	objects := files.NewMemoryObjectStorage()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	missingBody := []byte("missing-object-metadata")
	missing := createReconciliationAsset(t, ctx, store, reconciliationAssetSpec{TenantID: tenantID, UserID: userID, Name: "missing.pdf", Body: missingBody, Lifecycle: files.LifecycleActive})
	recoverableBody := []byte("recoverable-pending-object")
	recoverable := createReconciliationAsset(t, ctx, store, reconciliationAssetSpec{TenantID: tenantID, UserID: userID, Name: "recoverable.pdf", Body: recoverableBody, Lifecycle: files.LifecyclePendingUpload})
	if err := objects.Put(ctx, recoverable.StorageBucket, recoverable.StorageKey, bytes.NewReader(recoverableBody), int64(len(recoverableBody)), "application/pdf"); err != nil {
		t.Fatalf("put recoverable object: %v", err)
	}
	quarantinedBody := []byte("synthetic-quarantined-object")
	quarantined := createReconciliationAsset(t, ctx, store, reconciliationAssetSpec{TenantID: tenantID, UserID: userID, Name: "quarantined.pdf", Body: quarantinedBody, Lifecycle: files.LifecycleActive})
	tamperedBody := bytes.Repeat([]byte("x"), len(quarantinedBody))
	if err := objects.Put(ctx, quarantined.StorageBucket, quarantined.StorageKey, bytes.NewReader(tamperedBody), int64(len(tamperedBody)), "application/pdf"); err != nil {
		t.Fatalf("put synthetic mismatched object: %v", err)
	}
	orphanKey := "tenant/" + tenantID + "/files/orphan.pdf"
	if err := objects.Put(ctx, "reconcile-test", orphanKey, bytes.NewReader([]byte("orphan")), 6, "application/pdf"); err != nil {
		t.Fatalf("put orphan object: %v", err)
	}

	reconciler := files.NewReconciler(database, objects)
	report, err := reconciler.Run(ctx, files.ReconciliationOptions{Bucket: "reconcile-test", BatchSize: 10, ObjectScanLimit: 10})
	if err != nil {
		t.Fatalf("report reconciliation: %v", err)
	}
	if report.FindingCount < 4 || report.RepairedCount != 0 {
		t.Fatalf("report mode must detect without repairing: %#v", report)
	}
	var pendingFindingID string
	if err := database.QueryRowContext(ctx, `SELECT id::text FROM file_reconciliation_finding
WHERE run_id=$1::uuid AND file_asset_id=$2::uuid AND code='recoverable_pending_upload' AND repaired=false`, report.ID, recoverable.ID).Scan(&pendingFindingID); err != nil {
		t.Fatalf("pending uploads must retain reconciliation findings: %v", err)
	}
	scope := auth.AccessScope{TenantID: tenantID, ActorID: userID, TenantWide: true}
	storedMissing, err := store.GetScoped(ctx, scope, missing.ID)
	if err != nil || storedMissing.Lifecycle != files.LifecycleActive {
		t.Fatalf("report mode mutated missing asset: %#v err=%v", storedMissing, err)
	}

	repair, err := reconciler.Run(ctx, files.ReconciliationOptions{Bucket: "reconcile-test", BatchSize: 10, ObjectScanLimit: 10, Repair: true})
	if err != nil {
		t.Fatalf("repair reconciliation: %v", err)
	}
	if repair.RepairedCount < 2 {
		t.Fatalf("repair mode did not recover safe states: %#v", repair)
	}
	storedMissing, err = store.GetScoped(ctx, scope, missing.ID)
	if err != nil || storedMissing.Lifecycle != files.LifecycleMissingObject {
		t.Fatalf("missing object state not repaired: %#v err=%v", storedMissing, err)
	}
	storedRecoverable, err := store.GetScoped(ctx, scope, recoverable.ID)
	if err != nil || storedRecoverable.Lifecycle != files.LifecycleActive {
		t.Fatalf("pending object state not recovered: %#v err=%v", storedRecoverable, err)
	}
	if _, err := store.Get(ctx, tenantID, recoverable.ID); err != nil {
		t.Fatalf("recovered object is not readable: %v", err)
	}
	storedQuarantined, err := store.GetScoped(ctx, scope, quarantined.ID)
	if err != nil || storedQuarantined.Lifecycle != files.LifecycleQuarantined {
		t.Fatalf("mismatched object was not quarantined: %#v err=%v", storedQuarantined, err)
	}
	var quarantinedFindings int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM file_reconciliation_finding
WHERE run_id=$1::uuid AND file_asset_id=$2::uuid AND code='hash_mismatch' AND repaired=true`, repair.ID, quarantined.ID).Scan(&quarantinedFindings); err != nil || quarantinedFindings != 1 {
		t.Fatalf("quarantined assets must retain repair findings: count=%d err=%v", quarantinedFindings, err)
	}
	if _, err := objects.Stat(ctx, "reconcile-test", orphanKey); err != nil {
		t.Fatalf("repair mode must never delete an orphan object: %v", err)
	}
	// 维护台账只豁免资产生命周期，不豁免租户归属；改 tenant 而不改资产 ID 也必须重新核验。
	assertTenantGuard := func(operation string, err error) {
		t.Helper()
		var failure *pgconn.PgError
		if !errors.As(err, &failure) || failure.Code != "23514" {
			t.Fatalf("%s must reject a foreign-tenant asset: %v", operation, err)
		}
	}
	_, err = database.ExecContext(ctx, `INSERT INTO file_reconciliation_finding
(run_id,tenant_id,file_asset_id,storage_bucket,storage_key,code)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,'synthetic_forged_tenant')`, report.ID, auth.PlatformTenantID, recoverable.ID, recoverable.StorageBucket, recoverable.StorageKey)
	assertTenantGuard("finding insert", err)
	_, err = database.ExecContext(ctx, `UPDATE file_reconciliation_finding SET tenant_id=$1::uuid WHERE id=$2::uuid`, auth.PlatformTenantID, pendingFindingID)
	assertTenantGuard("finding tenant update", err)
	var findingTenant string
	if err := database.QueryRowContext(ctx, `SELECT tenant_id::text FROM file_reconciliation_finding WHERE id=$1::uuid`, pendingFindingID).Scan(&findingTenant); err != nil || findingTenant != tenantID {
		t.Fatalf("rejected tenant update changed the original finding: tenant=%s err=%v", findingTenant, err)
	}
	// 巡检台账描述异常和修复历史，不是正式业务引用；恢复后仍可正常删除没有业务绑定的 generic 文件。
	// 全程保留生产触发器，既验证异常状态能登记发现，也防止发现记录把对象永久钉住。
	deleting, err := store.BeginDelete(ctx, scope, recoverable.ID, storedRecoverable.Revision)
	if err != nil || deleting.Lifecycle != files.LifecyclePendingDelete {
		t.Fatalf("reconciliation history must not pin an unbound generic asset: %#v err=%v", deleting, err)
	}
}

// 资产夹具携带完整归属和生命周期，正文仍按原始字节计算内容哈希。
type reconciliationAssetSpec struct {
	TenantID, UserID, Name, Lifecycle string
	Body                              []byte
}

func createReconciliationAsset(t *testing.T, ctx context.Context, store *files.PostgresStore, spec reconciliationAssetSpec) files.FileAsset {
	t.Helper()
	tenantID, userID, name, body, lifecycle := spec.TenantID, spec.UserID, spec.Name, spec.Body, spec.Lifecycle
	digest := sha256.Sum256(body)
	input := files.CreateAssetInput{
		TenantID: tenantID, OwnerType: "generic", OriginalName: name, ContentType: "application/pdf",
		SizeBytes: int64(len(body)), HashSHA256: hex.EncodeToString(digest[:]), StorageBucket: "reconcile-test",
		StorageKey: "tenant/" + tenantID + "/files/" + name, Visibility: "private", UploadedBy: userID,
	}
	var (
		asset files.FileAsset
		err   error
	)
	if lifecycle == files.LifecycleActive {
		asset, err = store.Create(ctx, input)
	} else {
		asset, err = store.CreatePending(ctx, input)
	}
	if err != nil {
		t.Fatalf("create reconciliation asset %s: %v", name, err)
	}
	return asset
}
