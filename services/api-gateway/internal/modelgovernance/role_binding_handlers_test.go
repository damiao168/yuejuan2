package modelgovernance

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestPlatformPanelBindingsAreSchoolScopedAndSeparateFromChatDefault(t *testing.T) {
	store := NewMemoryStore()
	const school = "00000000-0000-0000-0000-000000000020"
	const otherSchool = "00000000-0000-0000-0000-000000000030"
	const chatModel = "00000000-0000-0000-0000-000000000041"
	const gradingModel = "00000000-0000-0000-0000-000000000042"
	const foreignModel = "00000000-0000-0000-0000-000000000043"
	for _, item := range []ManagedAPIConfig{
		{ID: chatModel, TenantID: school, ModelName: "chat", Status: "active", IsDefault: true, LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion},
		{ID: gradingModel, TenantID: school, ModelName: "grader", Status: "active", LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion},
		{ID: foreignModel, TenantID: otherSchool, ModelName: "foreign", Status: "active", LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion},
	} {
		store.managedConfigs[item.ID] = item
	}
	handler := NewHandler(store, auth.NewMemoryStore(), NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{ID: "00000000-0000-0000-0000-000000000010", TenantID: auth.PlatformTenantID,
		Roles: []string{"platform_admin"}, Permissions: []string{"model:provider:manage"}}
	input := savePlatformPanelRoleInput{TenantID: school, SaveModelRoleBindingInput: SaveModelRoleBindingInput{
		EducationStage: "senior", SubjectCode: "math", ArchetypeCode: "structured_steps", AgentRole: ModelRolePrimaryA,
		ManagedModelAPIConfigID: gradingModel, PromptVersion: "grader-v2", StrengthRank: 1, Status: "active",
	}}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response := performHandlerRequest(t, user, http.MethodPut, "/api/v1/platform/panel-model-bindings", string(encoded), handler.SavePlatformPanelRoleBinding)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"subject_code":"mathematics"`) {
		t.Fatalf("platform role assignment failed: %d %s", response.Code, response.Body.String())
	}
	chat, err := store.ListManagedAPIConfigs(context.Background(), school)
	if err != nil || len(chat) != 2 || !store.managedConfigs[chatModel].IsDefault || store.managedConfigs[gradingModel].IsDefault {
		t.Fatalf("role assignment changed the chat default: %#v %v", chat, err)
	}
	response = performHandlerRequest(t, user, http.MethodGet,
		"/api/v1/platform/panel-model-bindings?tenant_id="+school+"&education_stage=senior&subject_code=mathematics&archetype_code=structured_steps",
		"", handler.ListPlatformPanelRoleBindings)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), gradingModel) {
		t.Fatalf("school binding was not listed: %d %s", response.Code, response.Body.String())
	}
	response = performHandlerRequest(t, user, http.MethodGet,
		"/api/v1/platform/panel-model-bindings?tenant_id="+otherSchool+"&education_stage=senior&subject_code=mathematics&archetype_code=structured_steps",
		"", handler.ListPlatformPanelRoleBindings)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), gradingModel) {
		t.Fatalf("cross-school binding leaked: %d %s", response.Code, response.Body.String())
	}
	input.ManagedModelAPIConfigID = foreignModel
	encoded, _ = json.Marshal(input)
	response = performHandlerRequest(t, user, http.MethodPut, "/api/v1/platform/panel-model-bindings", string(encoded), handler.SavePlatformPanelRoleBinding)
	if response.Code != http.StatusConflict {
		t.Fatalf("foreign school model was accepted: %d %s", response.Code, response.Body.String())
	}
}

func TestPanelBindingAdminCanDisableUnverifiedModelButCannotActivateIt(t *testing.T) {
	store := NewMemoryStore()
	const school = "00000000-0000-0000-0000-000000000020"
	const model = "00000000-0000-0000-0000-000000000042"
	store.managedConfigs[model] = ManagedAPIConfig{ID: model, TenantID: school, Status: "active",
		LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion}
	user := auth.User{ID: "00000000-0000-0000-0000-000000000010", TenantID: auth.PlatformTenantID,
		Roles: []string{"platform_admin"}, Permissions: []string{"model:provider:manage"}}
	handler := NewHandler(store, auth.NewMemoryStore(), NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	input := savePlatformPanelRoleInput{TenantID: school, SaveModelRoleBindingInput: SaveModelRoleBindingInput{
		EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "*", AgentRole: ModelRoleArbiter,
		ManagedModelAPIConfigID: model, PromptVersion: "arbiter-v1", StrengthRank: 2, Status: "active",
	}}
	encoded, _ := json.Marshal(input)
	if response := performHandlerRequest(t, user, http.MethodPut, "/api/v1/platform/panel-model-bindings", string(encoded), handler.SavePlatformPanelRoleBinding); response.Code != http.StatusOK {
		t.Fatalf("initial role save failed: %d %s", response.Code, response.Body.String())
	}
	config := store.managedConfigs[model]
	config.Status = "disabled"
	store.managedConfigs[model] = config
	input.Status = "disabled"
	encoded, _ = json.Marshal(input)
	if response := performHandlerRequest(t, user, http.MethodPut, "/api/v1/platform/panel-model-bindings", string(encoded), handler.SavePlatformPanelRoleBinding); response.Code != http.StatusOK {
		t.Fatalf("unable to disable role after model revocation: %d %s", response.Code, response.Body.String())
	}
	items, err := store.ListConfiguredModelRoleBindings(context.Background(), school, "senior", "physics", "*")
	if err != nil || len(items) != 1 || items[0].Status != "disabled" {
		t.Fatalf("disabled role disappeared from admin view: %#v %v", items, err)
	}
	input.Status = "active"
	encoded, _ = json.Marshal(input)
	if response := performHandlerRequest(t, user, http.MethodPut, "/api/v1/platform/panel-model-bindings", string(encoded), handler.SavePlatformPanelRoleBinding); response.Code != http.StatusConflict {
		t.Fatalf("revoked model was reactivated: %d %s", response.Code, response.Body.String())
	}
	delete(store.managedConfigs, model)
	input.Status = "disabled"
	encoded, _ = json.Marshal(input)
	if response := performHandlerRequest(t, user, http.MethodPut, "/api/v1/platform/panel-model-bindings", string(encoded), handler.SavePlatformPanelRoleBinding); response.Code != http.StatusOK {
		t.Fatalf("removed model binding could not be kept disabled: %d %s", response.Code, response.Body.String())
	}
}

func TestPlatformPanelBindingActorMigrationKeepsAuditIdentity(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000161_platform_panel_model_binding_actor.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{"DROP CONSTRAINT model_role_binding_tenant_id_created_by_fkey", "FOREIGN KEY (created_by) REFERENCES app_user(id)"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("platform actor migration missing %q", required)
		}
	}
}
