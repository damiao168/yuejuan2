package modelgovernance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestCreateProviderNeverEchoesOrAuditsSecretReference(t *testing.T) {
	t.Setenv("STORY061_VENDOR_KEY", "super-secret-value")
	store := NewMemoryStore()
	audits := auth.NewMemoryStore()
	handler := NewHandler(store, audits, NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{
		ID:          "00000000-0000-0000-0000-000000000010",
		TenantID:    auth.PlatformTenantID,
		Permissions: []string{"model:provider:manage"},
	}
	body := `{
	  "provider_key":"vendor-a",
	  "display_name":"Vendor A",
	  "provider_kind":"external",
	  "adapter_type":"vendor_a_native",
	  "credential_ref":"env://STORY061_VENDOR_KEY",
	  "region":"cn-east",
	  "data_policy":{"training_allowed":false,"retention_mode":"no_store"}
	}`
	response := performHandlerRequest(t, user, http.MethodPost, "/api/v1/model-providers", body, handler.CreateProvider)
	if response.Code != http.StatusCreated {
		t.Fatalf("create provider returned %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "STORY061_VENDOR_KEY") ||
		strings.Contains(response.Body.String(), "super-secret-value") ||
		strings.Contains(response.Body.String(), `"credential_ref":`) {
		t.Fatalf("provider response exposed secret material: %s", response.Body.String())
	}
	records, err := audits.ListAudits(context.Background(), user.TenantID, auth.AuditFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	raw, _ := json.Marshal(records)
	if bytes.Contains(raw, []byte("STORY061_VENDOR_KEY")) || bytes.Contains(raw, []byte("super-secret-value")) {
		t.Fatalf("audit exposed secret material: %s", raw)
	}
}

func TestCreateProviderRejectsUnknownFieldsAndExternalActivation(t *testing.T) {
	t.Setenv("STORY061_VENDOR_KEY", "configured")
	handler := NewHandler(NewMemoryStore(), nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
	unknown := `{
	  "provider_key":"vendor-a","display_name":"Vendor A","provider_kind":"external",
	  "adapter_type":"vendor_a_native","credential_ref":"env://STORY061_VENDOR_KEY",
	  "region":"cn-east","data_policy":{"training_allowed":false,"retention_mode":"no_store"},
	  "api_key":"must-not-be-accepted"
	}`
	response := performHandlerRequest(t, user, http.MethodPost, "/api/v1/model-providers", unknown, handler.CreateProvider)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field returned %d: %s", response.Code, response.Body.String())
	}
	active := `{
	  "provider_key":"vendor-a","display_name":"Vendor A","provider_kind":"external",
	  "adapter_type":"vendor_a_native","credential_ref":"env://STORY061_VENDOR_KEY",
	  "region":"cn-east","status":"active",
	  "data_policy":{"training_allowed":false,"retention_mode":"no_store"}
	}`
	response = performHandlerRequest(t, user, http.MethodPost, "/api/v1/model-providers", active, handler.CreateProvider)
	if response.Code != http.StatusConflict {
		t.Fatalf("unverified external activation returned %d: %s", response.Code, response.Body.String())
	}
}

func TestTenantAdminCannotReadAnotherTenantGovernance(t *testing.T) {
	handler := NewHandler(NewMemoryStore(), nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{
		ID:          "tenant-admin",
		TenantID:    "00000000-0000-0000-0000-000000000020",
		Permissions: []string{"model:read", "model:policy:manage"},
	}
	response := performHandlerRequest(t, user, http.MethodGet,
		"/api/v1/model-providers?tenant_id=00000000-0000-0000-0000-000000000030", "", handler.ListProviders)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant read returned %d: %s", response.Code, response.Body.String())
	}
}

func TestPolicyUpdateRequiresKnownDeploymentAndVersion(t *testing.T) {
	store := NewMemoryStore()
	tenantID := "00000000-0000-0000-0000-000000000020"
	if err := store.EnsureLocalBaseline(context.Background(), tenantID, testBaseline()); err != nil {
		t.Fatalf("ensure baseline: %v", err)
	}
	handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{ID: "tenant-admin", TenantID: tenantID, Permissions: []string{"model:policy:manage"}}
	unknown := `{
	  "display_name":"Default","mode":"shadow_compare","external_enabled":true,
	  "text_export_enabled":true,"image_export_enabled":false,
	  "allowed_deployments":["missing-deployment"],
	  "max_cost_micros_per_question":100,"max_cost_micros_per_exam":1000,
	  "fallback_mode":"manual_only","expected_version":1,"reason":"test explicit authorization"
	}`
	response := performHandlerRequest(t, user, http.MethodPut, "/api/v1/model-policy", unknown, handler.UpdatePolicy)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown deployment returned %d: %s", response.Code, response.Body.String())
	}
	localOnly := `{
	  "display_name":"Default","mode":"local_only","external_enabled":false,
	  "text_export_enabled":false,"image_export_enabled":false,
	  "allowed_deployments":[],
	  "max_cost_micros_per_question":0,"max_cost_micros_per_exam":0,
	  "fallback_mode":"manual_only","expected_version":1,"reason":"confirm local-only policy"
	}`
	response = performHandlerRequest(t, user, http.MethodPut, "/api/v1/model-policy", localOnly, handler.UpdatePolicy)
	if response.Code != http.StatusOK {
		t.Fatalf("valid policy update returned %d: %s", response.Code, response.Body.String())
	}
	response = performHandlerRequest(t, user, http.MethodPut, "/api/v1/model-policy", localOnly, handler.UpdatePolicy)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale policy update returned %d: %s", response.Code, response.Body.String())
	}
}

func TestUpdatePolicyRejectsMixedLegacyAndManagedModels(t *testing.T) {
	const tenantID = "00000000-0000-0000-0000-000000000020"
	handler := NewHandler(NewMemoryStore(), nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{ID: "tenant-admin", TenantID: tenantID, Permissions: []string{"model:policy:manage"}}
	body := `{"mode":"cloud_suggestion","external_enabled":true,"text_export_enabled":true,
		"allowed_deployments":["old-deployment"],
		"allowed_model_config_ids":["11111111-1111-4111-8111-111111111111"],
		"fallback_mode":"manual_only","expected_version":1,"reason":"test"}`
	response := performHandlerRequest(t, user, http.MethodPut, "/api/v1/model-policy", body, handler.UpdatePolicy)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"mixed_model_policy_sources"`) {
		t.Fatalf("mixed policy returned %d: %s", response.Code, response.Body.String())
	}
}

func TestUpdatePolicyRejectsUnavailableManagedModel(t *testing.T) {
	const tenantID = "00000000-0000-0000-0000-000000000020"
	const modelID = "11111111-1111-4111-8111-111111111111"
	user := auth.User{ID: "tenant-admin", TenantID: tenantID, Permissions: []string{"model:policy:manage"}}
	body := `{"mode":"cloud_suggestion","external_enabled":true,"text_export_enabled":true,
		"allowed_model_config_ids":["` + modelID + `"],
		"fallback_mode":"manual_only","expected_version":1,"reason":"test"}`
	valid := ManagedAPIConfig{ID: modelID, TenantID: tenantID, Status: "active",
		LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion}
	tests := []struct {
		name       string
		config     *ManagedAPIConfig
		wantStatus int
		wantCode   string
	}{
		{name: "missing", wantStatus: http.StatusBadRequest, wantCode: "unknown_model_config"},
		{name: "other school", config: func() *ManagedAPIConfig { c := valid; c.TenantID = "other-school"; return &c }(), wantStatus: http.StatusBadRequest, wantCode: "unknown_model_config"},
		{name: "disabled", config: func() *ManagedAPIConfig { c := valid; c.Status = "disabled"; return &c }(), wantStatus: http.StatusBadRequest, wantCode: "unknown_model_config"},
		{name: "capability failed", config: func() *ManagedAPIConfig { c := valid; c.LastCapabilityStatus = "failed"; return &c }(), wantStatus: http.StatusBadRequest, wantCode: "unknown_model_config"},
		{name: "stale probe", config: func() *ManagedAPIConfig { c := valid; c.LastCapabilityVersion = "structured-json-v2"; return &c }(), wantStatus: http.StatusBadRequest, wantCode: "unknown_model_config"},
		{name: "ready", config: &valid, wantStatus: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStore()
			if tt.config != nil {
				store.managedConfigs[modelID] = *tt.config
			}
			handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
			response := performHandlerRequest(t, user, http.MethodPut, "/api/v1/model-policy", body, handler.UpdatePolicy)
			if response.Code != tt.wantStatus || (tt.wantCode != "" && !strings.Contains(response.Body.String(), `"`+tt.wantCode+`"`)) {
				t.Fatalf("policy update returned %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestStoreBoundaryRejectsUnverifiedExternalActivation(t *testing.T) {
	store := NewMemoryStore()
	tenantID := "00000000-0000-0000-0000-000000000020"
	_, err := store.CreateProvider(context.Background(), tenantID, "", ProviderInput{
		Key:           "vendor-a",
		DisplayName:   "Vendor A",
		Kind:          ProviderExternal,
		AdapterType:   "vendor_a_native",
		CredentialRef: "vault://edugrade/vendor-a",
		Region:        "cn-east",
		DataPolicy:    DataPolicy{RetentionMode: "no_store"},
		Status:        "active",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("store boundary accepted active unverified external provider: %v", err)
	}
}

func performHandlerRequest(
	t *testing.T,
	user auth.User,
	method string,
	target string,
	body string,
	handler http.HandlerFunc,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request = request.WithContext(auth.WithUser(request.Context(), user))
	response := httptest.NewRecorder()
	handler(response, request)
	return response
}

func testBaseline() LocalBaseline {
	return LocalBaseline{
		ProviderKey:       "local",
		ProviderName:      "Local grading runtime",
		DeploymentKey:     "local-qwen3-4b-q4-k-m",
		ModelName:         "Qwen/Qwen3-4B-GGUF",
		ModelVersion:      "Qwen/Qwen3-4B-GGUF:Q4_K_M",
		AdapterType:       "local_llama_cpp",
		Region:            "on_premise",
		CapabilityProfile: "local-pilot-v1",
	}
}
