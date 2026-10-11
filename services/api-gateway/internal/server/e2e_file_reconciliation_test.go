package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/files"
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
	orphanKey := "tenant/" + tenantID + "/files/orphan.pdf"
	if err := objects.Put(ctx, "reconcile-test", orphanKey, bytes.NewReader([]byte("orphan")), 6, "application/pdf"); err != nil {
		t.Fatalf("put orphan object: %v", err)
	}

	reconciler := files.NewReconciler(database, objects)
	report, err := reconciler.Run(ctx, files.ReconciliationOptions{Bucket: "reconcile-test", BatchSize: 10, ObjectScanLimit: 10})
	if err != nil {
		t.Fatalf("report reconciliation: %v", err)
	}
	if report.FindingCount < 3 || report.RepairedCount != 0 {
		t.Fatalf("report mode must detect without repairing: %#v", report)
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
	if err != nil || storedRecoverable.Lifecycle != files.LifecycleOrphanRecovered {
		t.Fatalf("pending object state not recovered: %#v err=%v", storedRecoverable, err)
	}
	if _, err := objects.Stat(ctx, "reconcile-test", orphanKey); err != nil {
		t.Fatalf("repair mode must never delete an orphan object: %v", err)
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
