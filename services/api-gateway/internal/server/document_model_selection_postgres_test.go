package server

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

// 隔离数据库覆盖首次选择、修订冲突和软删除引用；不访问供应商或修改本地业务数据库。
func TestPostgresDocumentModelSelection(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	ctx := context.Background()
	var tenant, actor string
	if err := db.QueryRowContext(ctx, `SELECT t.id::text,u.id::text FROM tenant t JOIN app_user u ON u.tenant_id=t.id WHERE t.code='demo' AND u.username='tenant_admin' AND u.deleted_at IS NULL`).Scan(&tenant, &actor); err != nil {
		t.Fatal(err)
	}
	cipher, err := modelgovernance.NewCredentialCipher("synthetic-document-selection-master-at-least-32")
	if err != nil {
		t.Fatal(err)
	}
	store := modelgovernance.NewPostgresStore(db, cipher)
	probe := modelgovernance.ManagedAPIProbeResult{OK: true, ProbeMode: "structured", GeneratedRequest: true, TextCheck: modelgovernance.ManagedAPICheckResult{OK: true}, CapabilityCheck: modelgovernance.ManagedAPICheckResult{OK: true}}
	config, err := store.CreateManagedAPIConfig(ctx, tenant, actor, modelgovernance.ManagedAPIConfigInput{ProviderKey: "custom", DisplayName: "Synthetic parser", AdapterType: "openai_compatible", BaseURL: "https://example.invalid/v1", ModelName: "synthetic-parser", ModelVersion: "v1", Region: "global", APIKey: "synthetic-selection-key", Status: "active", InitialProbe: &probe})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.SaveDocumentModelSelection(ctx, tenant, actor, modelgovernance.DocumentModelSelectionInput{ExpectedRevision: "absent", Mode: "model", ModelConfigID: config.ID})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, modelgovernance.ErrDocumentSelectionChanged) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	selection, err := store.GetDocumentModelSelection(ctx, tenant)
	if err != nil || selection.ModelConfigID != config.ID {
		t.Fatalf("read: %#v %v", selection, err)
	}
	uses, err := store.ListGradingModelUses(ctx, tenant)
	if err != nil || len(uses) != 0 {
		t.Fatalf("empty grading uses: %#v %v", uses, err)
	}
	resolve := newDocumentModelResolver(store)
	document, err := resolve(ctx, tenant)
	if err != nil || document == nil || document.ModelName != config.ModelName {
		t.Fatalf("resolve: %#v %v", document, err)
	}
	if err = store.DeleteManagedAPIConfig(ctx, tenant, actor, config.ID); !errors.Is(err, modelgovernance.ErrManagedConfigInUse) {
		t.Fatalf("delete referenced: %v", err)
	}
	_, err = store.SaveDocumentModelSelection(ctx, tenant, actor, modelgovernance.DocumentModelSelectionInput{ExpectedRevision: selection.Revision, Mode: "follow_chat"})
	if err != nil {
		t.Fatal(err)
	}
	if document, err = resolve(ctx, tenant); err != nil || document != nil {
		t.Fatalf("follow local: %#v %v", document, err)
	}
	if err = store.DeleteManagedAPIConfig(ctx, tenant, actor, config.ID); err != nil {
		t.Fatal(err)
	}
	var forced bool
	if err = db.QueryRowContext(ctx, `SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE relname='tenant_document_model_selection'`).Scan(&forced); err != nil || !forced {
		t.Fatalf("RLS: %t %v", forced, err)
	}
}
