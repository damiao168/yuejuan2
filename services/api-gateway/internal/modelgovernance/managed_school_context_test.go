package modelgovernance

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	database "edugrade-enterprise/services/api-gateway/internal/db"
)

type managedSchoolContextCall struct {
	operation, tenantID, databaseTenantID, actorID string
	user                                           auth.User
}

type managedSchoolContextStore struct {
	*MemoryStore
	calls []managedSchoolContextCall
}

func (s *managedSchoolContextStore) record(ctx context.Context, operation, tenantID, actorID string) {
	databaseTenantID, _ := database.TenantFromContext(ctx)
	user, _ := auth.UserFromContext(ctx)
	s.calls = append(s.calls, managedSchoolContextCall{operation, tenantID, databaseTenantID, actorID, user})
}

func (s *managedSchoolContextStore) ListConfiguredModelRoleBindings(ctx context.Context, tenantID, _, _, _ string) ([]ModelRoleBinding, error) {
	s.record(ctx, "list_roles", tenantID, "")
	return []ModelRoleBinding{}, nil
}

func (s *managedSchoolContextStore) SaveModelRoleBinding(ctx context.Context, tenantID, actorID string, _ SaveModelRoleBindingInput) (ModelRoleBinding, error) {
	s.record(ctx, "save_role", tenantID, actorID)
	return ModelRoleBinding{ID: "binding", TenantID: tenantID}, nil
}

func (s *managedSchoolContextStore) GetDocumentModelSelection(ctx context.Context, tenantID string) (DocumentModelSelection, error) {
	s.record(ctx, "get_document", tenantID, "")
	return initialDocumentSelection(), nil
}

func (s *managedSchoolContextStore) ListGradingModelUses(ctx context.Context, tenantID string) ([]GradingModelUse, error) {
	s.record(ctx, "list_grading_uses", tenantID, "")
	return []GradingModelUse{}, nil
}

func (s *managedSchoolContextStore) SaveDocumentModelSelection(ctx context.Context, tenantID, actorID string, _ DocumentModelSelectionInput) (DocumentModelSelection, error) {
	s.record(ctx, "save_document", tenantID, actorID)
	return initialDocumentSelection(), nil
}

type managedSchoolContextAudit struct {
	events          []auth.AuditEvent
	databaseTenants []string
}

func (a *managedSchoolContextAudit) Audit(ctx context.Context, event auth.AuditEvent) error {
	tenantID, _ := database.TenantFromContext(ctx)
	a.events = append(a.events, event)
	a.databaseTenants = append(a.databaseTenants, tenantID)
	return nil
}

func TestManagedSchoolHandlersScopeDatabaseAfterAuthorizationAndKeepActorIdentity(t *testing.T) {
	const school = "00000000-0000-0000-0000-000000000020"
	const actor = "00000000-0000-0000-0000-000000000010"
	operations := []struct {
		name, method, path, permission string
		handler                        func(*Handler) http.HandlerFunc
		storeCalls                     []string
	}{
		{"list_roles", http.MethodGet, "/api/v1/platform/panel-model-bindings", "model:read", func(h *Handler) http.HandlerFunc { return h.ListPlatformPanelRoleBindings }, []string{"list_roles"}},
		{"save_role", http.MethodPut, "/api/v1/platform/panel-model-bindings", "model:panel:manage", func(h *Handler) http.HandlerFunc { return h.SavePlatformPanelRoleBinding }, []string{"save_role"}},
		{"list_applications", http.MethodGet, "/api/v1/platform/model-applications", "model:read", func(h *Handler) http.HandlerFunc { return h.GetPlatformModelApplications }, []string{"get_document", "list_grading_uses"}},
		{"save_document", http.MethodPut, "/api/v1/platform/document-model-selection", "model:managed_api:manage", func(h *Handler) http.HandlerFunc { return h.SavePlatformDocumentModelSelection }, []string{"save_document"}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			for _, scenario := range []struct {
				name, target string
				user         auth.User
				wantStatus   int
			}{
				{"authorized_school", school, auth.User{ID: actor, TenantID: auth.PlatformTenantID, Roles: []string{"platform_admin"}, Permissions: []string{operation.permission}}, http.StatusOK},
				{"missing_permission", school, auth.User{ID: actor, TenantID: auth.PlatformTenantID, Roles: []string{"platform_admin"}}, http.StatusForbidden},
				{"foreign_school_user", school, auth.User{ID: actor, TenantID: "00000000-0000-0000-0000-000000000030", Roles: []string{"school_admin"}, Permissions: []string{operation.permission}}, http.StatusForbidden},
				{"platform_is_not_school", auth.PlatformTenantID, auth.User{ID: actor, TenantID: auth.PlatformTenantID, Roles: []string{"platform_admin"}, Permissions: []string{operation.permission}}, http.StatusBadRequest},
				{"invalid_school_id", "invalid", auth.User{ID: actor, TenantID: auth.PlatformTenantID, Roles: []string{"platform_admin"}, Permissions: []string{operation.permission}}, http.StatusBadRequest},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					store := &managedSchoolContextStore{MemoryStore: NewMemoryStore()}
					audit := &managedSchoolContextAudit{}
					handler := NewHandler(store, audit, NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
					path, body := operation.path, ""
					if operation.method == http.MethodGet {
						path += "?tenant_id=" + scenario.target + "&education_stage=senior&subject_code=mathematics&archetype_code=*"
					} else {
						payload := map[string]any{"tenant_id": scenario.target, "expected_revision": "absent"}
						if operation.name == "save_role" {
							payload["education_stage"], payload["subject_code"], payload["archetype_code"] = "senior", "mathematics", "*"
							payload["agent_role"], payload["prompt_version"], payload["strength_rank"], payload["status"] = "primary_a", "grader-v1", 1, "active"
							payload["managed_model_api_config_id"] = "00000000-0000-0000-0000-000000000042"
						} else {
							payload["mode"] = "follow_chat"
						}
						encoded, err := json.Marshal(payload)
						if err != nil {
							t.Fatal(err)
						}
						body = string(encoded)
					}
					response := performHandlerRequest(t, scenario.user, operation.method, path, body, operation.handler(handler))
					if response.Code != scenario.wantStatus {
						t.Fatalf("status %d, want %d: %s", response.Code, scenario.wantStatus, response.Body.String())
					}
					if scenario.wantStatus != http.StatusOK {
						if len(store.calls) != 0 || len(audit.events) != 0 {
							t.Fatalf("rejected request reached store or audit: %#v %#v", store.calls, audit.events)
						}
						return
					}
					// 内存存储不执行 RLS；直接核对传入作用域，防止测试在平台上下文下误报成功。
					var gotOperations []string
					for _, call := range store.calls {
						gotOperations = append(gotOperations, call.operation)
						if call.tenantID != school || call.databaseTenantID != school || !reflect.DeepEqual(call.user, scenario.user) {
							t.Fatalf("school database scope or platform identity changed: %#v", call)
						}
						if operation.method == http.MethodPut && call.actorID != actor {
							t.Fatalf("platform actor lost: %#v", call)
						}
					}
					if !reflect.DeepEqual(gotOperations, operation.storeCalls) {
						t.Fatalf("store calls %v, want %v", gotOperations, operation.storeCalls)
					}
					if operation.method == http.MethodPut {
						if len(audit.events) != 1 || audit.events[0].TenantID != auth.PlatformTenantID || audit.events[0].ActorID != actor || audit.events[0].AfterValue["tenant_id"] != school || audit.databaseTenants[0] != auth.PlatformTenantID {
							t.Fatalf("audit must retain platform identity and target school: %#v, scopes %v", audit.events, audit.databaseTenants)
						}
					}
				})
			}
		})
	}
}
