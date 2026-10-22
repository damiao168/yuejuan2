package modelgovernance

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// 只在新建的唯一 schema 中建夹具；public 仅用于复制已部署的父表结构，不读取或写入业务记录。
// 本测试验证新迁移和选择仓储，不替代全部历史迁移、登录鉴权或供应商图片能力验证。
func newAnswerSheetSelectionPostgresFixture(t *testing.T) (*PostgresStore, string, string, string) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("YUEJUAN_ANSWER_SHEET_SELECTION_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("YUEJUAN_ANSWER_SHEET_SELECTION_TEST_DATABASE_URL is required")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatal("PostgreSQL URL is required")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "test_answer_sheet_selection_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(t.Context(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("fixture cleanup: %v", err)
		}
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	query.Set("application_name", schema)
	parsed.RawQuery = query.Encode()
	database, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(6)
	t.Cleanup(func() { _ = database.Close() })
	for _, ddl := range []string{
		`CREATE TABLE tenant(id uuid PRIMARY KEY)`,
		`CREATE TABLE managed_model_api_config (LIKE public.managed_model_api_config INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES)`,
		`CREATE TABLE model_role_binding(tenant_id uuid,managed_model_api_config_id uuid,status text)`,
		`CREATE TABLE model_approval(tenant_id uuid,managed_model_api_config_id uuid,revoked_at timestamptz,expires_at timestamptz)`,
		`CREATE TABLE model_sandbox_approval(tenant_id uuid,managed_model_api_config_id uuid,revoked_at timestamptz,expires_at timestamptz)`,
		`CREATE TABLE tenant_model_policy_model(tenant_id uuid,managed_model_api_config_id uuid,enabled bool)`,
		`CREATE TABLE tenant_document_model_selection(tenant_id uuid,managed_model_api_config_id uuid,mode text)`,
		`CREATE FUNCTION edugrade_tenant_matches(candidate uuid) RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT current_setting('edugrade.tenant_id',true)=candidate::text $$`,
		`GRANT USAGE ON SCHEMA ` + schema + ` TO edugrade_tenant_runtime`,
		`GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA ` + schema + ` TO edugrade_tenant_runtime`,
	} {
		if _, err = database.ExecContext(t.Context(), ddl); err != nil {
			t.Fatal(err)
		}
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000191_answer_sheet_model_selection.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.ExecContext(t.Context(), string(migration)); err != nil {
		t.Fatal(err)
	}
	school, foreign, actor := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = database.ExecContext(t.Context(), `INSERT INTO tenant(id) VALUES($1),($2)`, school, foreign); err != nil {
		t.Fatal(err)
	}
	cipher, err := NewCredentialCipher("synthetic-answer-sheet-selection-test-master-32")
	if err != nil {
		t.Fatal(err)
	}
	return NewPostgresStore(database, cipher), school, foreign, actor
}

func createSelectionPostgresConfig(t *testing.T, store *PostgresStore, tenant, actor string) ManagedAPIConfig {
	t.Helper()
	config, err := store.CreateManagedAPIConfig(t.Context(), tenant, actor, ManagedAPIConfigInput{ProviderKey: "custom", DisplayName: "Synthetic answer sheet model", AdapterType: "openai_compatible", BaseURL: "https://example.invalid/v1", ModelName: "synthetic-answer-sheet-model", ModelVersion: "v1", Region: "global", Status: "active", APIKey: "synthetic-answer-sheet-key"})
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func TestPostgresAnswerSheetModelSelectionCASAndReferenceDeletion(t *testing.T) {
	store, school, _, actor := newAnswerSheetSelectionPostgresFixture(t)
	ctx := t.Context()
	config := createSelectionPostgresConfig(t, store, school, actor)
	initial, err := store.GetAnswerSheetModelSelection(ctx, school)
	if err != nil || initial.Mode != "unconfigured" || initial.Revision != "absent" {
		t.Fatalf("initial %#v %v", initial, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.SaveAnswerSheetModelSelection(ctx, school, actor, DocumentModelSelectionInput{ExpectedRevision: "absent", Mode: "model", ModelConfigID: config.ID})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrAnswerSheetSelectionChanged) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS success=%d conflict=%d", success, conflict)
	}
	selected, err := store.GetAnswerSheetModelSelection(ctx, school)
	if err != nil || selected.ModelConfigID != config.ID {
		t.Fatalf("selected %#v %v", selected, err)
	}
	resolved, err := ResolveAnswerSheetManagedAPI(ctx, store, school)
	if err != nil || resolved == nil || resolved.Config.ID != config.ID {
		t.Fatalf("resolve: %v", err)
	}
	if err = store.DeleteManagedAPIConfig(ctx, school, actor, config.ID); !errors.Is(err, ErrManagedConfigInUse) {
		t.Fatalf("referenced deletion: %v", err)
	}
	if _, err = store.SaveAnswerSheetModelSelection(ctx, school, actor, DocumentModelSelectionInput{ExpectedRevision: selected.Revision, Mode: "unconfigured"}); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteManagedAPIConfig(ctx, school, actor, config.ID); err != nil {
		t.Fatalf("unreferenced deletion: %v", err)
	}
	if _, err = ResolveAnswerSheetManagedAPI(ctx, store, school); !errors.Is(err, ErrAnswerSheetModelRequired) {
		t.Fatalf("cleared selection fell back: %v", err)
	}
}

func TestPostgresAnswerSheetModelSelectionTenantRLSAndAvailability(t *testing.T) {
	store, school, foreign, actor := newAnswerSheetSelectionPostgresFixture(t)
	ctx := t.Context()
	config := createSelectionPostgresConfig(t, store, school, actor)
	if _, err := store.SaveAnswerSheetModelSelection(ctx, foreign, actor, DocumentModelSelectionInput{ExpectedRevision: "absent", Mode: "model", ModelConfigID: config.ID}); !errors.Is(err, ErrAnswerSheetModelRequired) {
		t.Fatalf("foreign model accepted: %v", err)
	}
	selected, err := store.SaveAnswerSheetModelSelection(ctx, school, actor, DocumentModelSelectionInput{ExpectedRevision: "absent", Mode: "model", ModelConfigID: config.ID})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL ROLE edugrade_tenant_runtime`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `SELECT set_config('edugrade.tenant_id',$1,true)`, foreign); err != nil {
		t.Fatal(err)
	}
	var visible int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM tenant_answer_sheet_model_selection`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("RLS leaked selection: count=%d err=%v", visible, err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO tenant_answer_sheet_model_selection(tenant_id,mode,managed_model_api_config_id,updated_by) VALUES($1,'model',$2,$3)`, school, config.ID, actor); err == nil {
		t.Fatal("RLS accepted foreign write")
	}
	_ = tx.Rollback()
	var forced bool
	if err = store.db.QueryRowContext(ctx, `SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid='tenant_answer_sheet_model_selection'::regclass`).Scan(&forced); err != nil || !forced {
		t.Fatalf("forced RLS=%v %v", forced, err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE managed_model_api_config SET status='disabled' WHERE id=$1`, config.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = ResolveAnswerSheetManagedAPI(ctx, store, school); !errors.Is(err, ErrAnswerSheetModelRequired) {
		t.Fatalf("disabled model resolved: %v", err)
	}
	if _, err = store.SaveAnswerSheetModelSelection(ctx, school, actor, DocumentModelSelectionInput{ExpectedRevision: selected.Revision, Mode: "model", ModelConfigID: config.ID}); !errors.Is(err, ErrAnswerSheetModelRequired) {
		t.Fatalf("disabled reassignment accepted: %v", err)
	}
}

func TestPostgresAnswerSheetModelSelectionRollbackOnForeignKey(t *testing.T) {
	store, school, foreign, actor := newAnswerSheetSelectionPostgresFixture(t)
	config := createSelectionPostgresConfig(t, store, school, actor)
	_, err := store.db.ExecContext(t.Context(), `INSERT INTO tenant_answer_sheet_model_selection(tenant_id,mode,managed_model_api_config_id,updated_by) VALUES($1,'model',$2,$3)`, foreign, config.ID, actor)
	if err == nil {
		t.Fatal("cross tenant composite reference accepted")
	}
	selection, err := store.GetAnswerSheetModelSelection(t.Context(), foreign)
	if err != nil || selection.Revision != "absent" {
		t.Fatalf("failed insert left selection %#v %v", selection, err)
	}
}
