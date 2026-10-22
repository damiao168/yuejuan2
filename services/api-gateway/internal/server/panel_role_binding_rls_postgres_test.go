package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	database "edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 平台身份与学校数据作用域不同；使用真正的受限登录，才能发现内存存储和超级用户会掩盖的 RLS 拒绝。
func TestPostgresPlatformPanelRoleBindingUsesAuthorizedSchoolScope(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	adminDB := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, adminDB)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var tenantID, actorID string
	if err := adminDB.QueryRowContext(ctx, `SELECT t.id::text,u.id::text FROM tenant t
JOIN tenant platform ON platform.code='platform'
JOIN app_user u ON u.tenant_id=platform.id AND u.username='platform_admin'
WHERE t.code='demo' AND t.deleted_at IS NULL AND u.deleted_at IS NULL`).Scan(&tenantID, &actorID); err != nil {
		t.Fatal(err)
	}
	cipher, err := modelgovernance.NewCredentialCipher("panel-rls-test-key-at-least-32-characters")
	if err != nil {
		t.Fatal(err)
	}
	model, err := modelgovernance.NewPostgresStore(adminDB, cipher).CreateManagedAPIConfig(ctx, tenantID, actorID, modelgovernance.ManagedAPIConfigInput{
		ProviderKey: "custom", DisplayName: "Panel RLS regression", AdapterType: "openai_compatible",
		BaseURL: "https://example.invalid/v1", ModelName: "panel-rls", ModelVersion: "v1", Region: "global",
		Status: "active", APIKey: "synthetic-panel-rls-secret",
		InitialProbe: &modelgovernance.ManagedAPIProbeResult{
			OK: true, ProbeMode: "structured", GeneratedRequest: true,
			TextCheck: modelgovernance.ManagedAPICheckResult{OK: true}, CapabilityCheck: modelgovernance.ManagedAPICheckResult{OK: true},
		},
	})
	if err != nil {
		t.Fatalf("seed capable school model: %v", err)
	}
	scopedDB := panelRoleRLSPostgresDB(t, adminDB, dsn)
	store := modelgovernance.NewPostgresStore(scopedDB, cipher)
	user := auth.User{ID: actorID, TenantID: auth.PlatformTenantID, Roles: []string{"platform_admin"}, Permissions: []string{"model:read", "model:panel:manage", "model:managed_api:manage"}}
	platformCtx := auth.WithUser(ctx, user)
	input := modelgovernance.SaveModelRoleBindingInput{
		ExpectedRevision: modelgovernance.ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "mathematics", ArchetypeCode: "*",
		AgentRole: modelgovernance.ModelRolePrimaryA, ManagedModelAPIConfigID: model.ID, PromptVersion: "panel-rls-v1", StrengthRank: 1, Status: "active",
	}
	// 原始平台上下文不能直接写学校绑定，避免测试因运行角色绕过 RLS 而假通过。
	_, err = store.SaveModelRoleBinding(platformCtx, tenantID, actorID, input)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("platform context must be rejected by school binding RLS: %v", err)
	}
	handler := modelgovernance.NewHandler(store, nil, nil, modelgovernance.LocalBaseline{})
	saved := savePanelRoleRLSRequest(t, handler, platformCtx, tenantID, input)
	if saved.TenantID != tenantID || saved.CreatedBy != actorID || saved.Revision == "" {
		t.Fatalf("school binding lost platform attribution: %+v", saved)
	}
	listURL := "/api/v1/platform/panel-model-bindings?tenant_id=" + tenantID + "&education_stage=senior&subject_code=mathematics&archetype_code=*"
	recorder := httptest.NewRecorder()
	handler.ListPlatformPanelRoleBindings(recorder, httptest.NewRequest(http.MethodGet, listURL, nil).WithContext(platformCtx))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list school role bindings: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var listed struct {
		Bindings []modelgovernance.ModelRoleBinding `json:"bindings"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil || len(listed.Bindings) != 1 || listed.Bindings[0].ID != saved.ID {
		t.Fatalf("platform must read the saved school binding: err=%v body=%s", err, recorder.Body.String())
	}
	input.ExpectedRevision, input.PromptVersion = saved.Revision, "panel-rls-v2"
	updated := savePanelRoleRLSRequest(t, handler, platformCtx, tenantID, input)
	if updated.ID != saved.ID || updated.Revision == saved.Revision || updated.PromptVersion != input.PromptVersion {
		t.Fatalf("school binding update did not advance revision: %+v", updated)
	}
	assertPanelRLSApplications(t, handler, platformCtx, tenantID, model.ID, "follow_chat")
	documentBody, err := json.Marshal(map[string]string{"tenant_id": tenantID, "expected_revision": "absent", "mode": "model", "model_config_id": model.ID})
	if err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	handler.SavePlatformDocumentModelSelection(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/platform/document-model-selection", bytes.NewReader(documentBody)).WithContext(platformCtx))
	if recorder.Code != http.StatusOK {
		t.Fatalf("save school document model: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertPanelRLSApplications(t, handler, platformCtx, tenantID, model.ID, "model")
	if tenant, ok := database.TenantFromContext(platformCtx); !ok || tenant != auth.PlatformTenantID {
		t.Fatalf("handler changed the caller database identity: %q", tenant)
	}
}

func assertPanelRLSApplications(t *testing.T, handler *modelgovernance.Handler, ctx context.Context, tenantID, modelID, documentMode string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.GetPlatformModelApplications(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/platform/model-applications?tenant_id="+tenantID, nil).WithContext(ctx))
	if recorder.Code != http.StatusOK {
		t.Fatalf("read school model applications: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var result struct {
		Document modelgovernance.DocumentModelSelection `json:"document_selection"`
		ModelIDs []string                               `json:"grading_model_ids"`
		Uses     []modelgovernance.GradingModelUse      `json:"grading_uses"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.ModelIDs) != 1 || result.ModelIDs[0] != modelID || len(result.Uses) != 1 || result.Uses[0].AgentRole != modelgovernance.ModelRolePrimaryA || result.Uses[0].ModelConfigID != modelID {
		t.Fatalf("applications omitted the school role binding: %+v", result)
	}
	if result.Document.Mode != documentMode || (documentMode == "model" && (result.Document.ModelConfigID != modelID || result.Document.Revision == "absent")) {
		t.Fatalf("applications did not read the saved document selection: %+v", result.Document)
	}
}

func savePanelRoleRLSRequest(t *testing.T, handler *modelgovernance.Handler, ctx context.Context, tenantID string, input modelgovernance.SaveModelRoleBindingInput) modelgovernance.ModelRoleBinding {
	t.Helper()
	body, err := json.Marshal(struct {
		TenantID string `json:"tenant_id"`
		modelgovernance.SaveModelRoleBindingInput
	}{tenantID, input})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.SavePlatformPanelRoleBinding(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/platform/panel-model-bindings", bytes.NewReader(body)).WithContext(ctx))
	if recorder.Code != http.StatusOK {
		t.Fatalf("save school role binding: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var result struct {
		Binding modelgovernance.ModelRoleBinding `json:"binding"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Binding
}

// 登录只持有运行角色，测试结束先关连接再删除角色；数据库和业务记录由隔离数据库夹具统一回收。
func panelRoleRLSPostgresDB(t *testing.T, adminDB *sql.DB, dsn string) *sql.DB {
	t.Helper()
	roleName := "e2e_panel_rls_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedRole := pgx.Identifier{roleName}.Sanitize()
	password := "Rls" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDB.Exec(`CREATE ROLE ` + quotedRole + ` LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD '` + password + `'`); err != nil {
		t.Fatalf("create panel RLS test login: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.Exec(`DROP ROLE IF EXISTS ` + quotedRole); err != nil {
			t.Errorf("drop panel RLS test login: %v", err)
		}
	})
	if _, err := adminDB.Exec(`GRANT edugrade_tenant_runtime TO ` + quotedRole); err != nil {
		t.Fatalf("grant panel RLS test runtime role: %v", err)
	}
	var databaseName string
	if err := adminDB.QueryRow(`SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("parse panel RLS test database URL")
	}
	parsed.Path, parsed.User = "/"+databaseName, url.UserPassword(roleName, password)
	db, closeDB, err := database.OpenPostgres(config.PostgresConfig{
		DSN: parsed.String(), TenantRLSEnabled: true, MaxOpenConns: 1, MaxIdleConns: 1,
		StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal("open panel RLS test database")
	}
	t.Cleanup(func() {
		if err := closeDB(); err != nil {
			t.Errorf("close panel RLS test database: %v", err)
		}
	})
	return db
}
