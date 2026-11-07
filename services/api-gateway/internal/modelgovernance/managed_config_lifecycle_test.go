package modelgovernance

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestManagedAPIConfigDeleteRequiresSwitchingAwayFromCurrent(t *testing.T) {
	store := NewMemoryStore()
	input := ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "secret-value-at-least-16", ModelName: "deepseek-v4-pro",
		ModelVersion: "deepseek-v4-pro", Region: "global", Status: "active", IsDefault: true,
	}
	item, err := store.CreateManagedAPIConfig(context.Background(), "school-a", "actor", input)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteManagedAPIConfig(context.Background(), "school-a", "actor", item.ID); !errors.Is(err, ErrManagedDefaultMutation) {
		t.Fatalf("deleted current model: %v", err)
	}
	_, err = store.UpdateManagedAPIConfig(context.Background(), "school-a", item.ID, ManagedAPIConfigUpdateInput{
		DisplayName: item.DisplayName, AdapterType: item.AdapterType, BaseURL: item.BaseURL,
		ModelName: item.ModelName, ModelVersion: item.ModelVersion, Region: item.Region,
		Status: "disabled", IsDefault: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteManagedAPIConfig(context.Background(), "school-a", "actor", item.ID); err != nil {
		t.Fatal(err)
	}
}

func TestManagedAPIConfigSoftDeleteRejectsActivePanelBinding(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	const tenantID = "school-a"
	item, err := store.CreateManagedAPIConfig(ctx, tenantID, "actor", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "secret-value-at-least-16", ModelName: "deepseek-grader",
		ModelVersion: "deepseek-grader", Region: "global", Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	item.LastCapabilityStatus = "success"
	item.LastCapabilityVersion = ManagedCapabilityProbeVersion
	store.managedConfigs[item.ID] = item
	binding := SaveModelRoleBindingInput{
		ExpectedRevision: ModelRoleBindingAbsent,
		EducationStage:   "senior", SubjectCode: "physics", ArchetypeCode: "*", AgentRole: ModelRolePrimaryA,
		ManagedModelAPIConfigID: item.ID, PromptVersion: "grader-v1", StrengthRank: 1, Status: "active",
	}
	if _, err = store.SaveModelRoleBinding(ctx, tenantID, "actor", binding); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteManagedAPIConfig(ctx, tenantID, "actor", item.ID); !errors.Is(err, ErrManagedConfigInUse) {
		t.Fatalf("active panel binding did not block deletion: %v", err)
	}
	itemsBefore, _ := store.ListConfiguredModelRoleBindings(ctx, tenantID, "senior", "physics", "*")
	binding.ExpectedRevision = itemsBefore[0].Revision
	binding.Status = "disabled"
	if _, err = store.SaveModelRoleBinding(ctx, tenantID, "actor", binding); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteManagedAPIConfig(ctx, tenantID, "actor", item.ID); err != nil {
		t.Fatal(err)
	}
	if store.managedConfigs[item.ID].DeletedAt == nil || store.managedConfigs[item.ID].DeletedBy != "actor" {
		t.Fatalf("deletion audit was not retained: %#v", store.managedConfigs[item.ID])
	}
	items, err := store.ListManagedAPIConfigs(ctx, tenantID)
	if err != nil || len(items) != 0 {
		t.Fatalf("deleted model remains visible: %#v %v", items, err)
	}
	if _, err = store.GetManagedAPIConnection(ctx, tenantID, item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted model credential remains accessible: %v", err)
	}
	if _, err = store.CreateManagedAPIConfig(ctx, tenantID, "actor", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "Replacement", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "secret-value-at-least-16", ModelName: "deepseek-grader",
		ModelVersion: "deepseek-grader", Region: "global", Status: "active",
	}); err != nil {
		t.Fatalf("soft-deleted provider/model key cannot be reused: %v", err)
	}
}

func TestManagedAPIModelDiscoveryUsesSelectedProviderWithoutModelName(t *testing.T) {
	providerSeen := ""
	prober := listingManagedProber{models: []string{"deepseek-v4-pro", "deepseek-v4-flash"}, providerSeen: &providerSeen}
	service := NewAutoManagedAPIConfigService(NewMemoryStore(), prober)
	resolved, list, result, err := service.ListModels(context.Background(), "school-a", AutoManagedAPIConfigInput{
		APIKey: "deepseek-secret-at-least-16", Provider: "deepseek",
	})
	if err != nil || !result.OK || resolved.Provider.Key != "deepseek" || resolved.ModelName != "" || list.Provider != "deepseek" || len(list.Models) != 2 {
		t.Fatalf("unexpected model discovery: %#v %#v %#v %v", resolved, list, result, err)
	}
	if providerSeen != "deepseek" {
		t.Fatalf("model discovery used the wrong provider: %q", providerSeen)
	}
}

func TestManagedAPIModelDiscoveryHandlerReusesSavedKeyWithoutEchoingIt(t *testing.T) {
	store := NewMemoryStore()
	const tenantID = "00000000-0000-0000-0000-000000000020"
	const secret = "deepseek-secret-at-least-16"
	source, err := store.CreateManagedAPIConfig(context.Background(), tenantID, "actor", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: secret, ModelName: "deepseek-chat",
		ModelVersion: "deepseek-chat", Region: "global", Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	seenKey := ""
	handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).
		WithManagedAPIProber(listingManagedProber{models: []string{"deepseek-reasoner"}, keySeen: &seenKey})
	user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
	response := performHandlerRequest(t, user, http.MethodPost, "/api/v1/platform/model-api-configs/models", `{
		"tenant_id":"`+tenantID+`","credential_source_id":"`+source.ID+`","provider":"deepseek"
	}`, handler.ListAvailableManagedAPIModels)
	if response.Code != http.StatusOK || seenKey != secret ||
		!strings.Contains(response.Body.String(), "deepseek-reasoner") || strings.Contains(response.Body.String(), secret) {
		t.Fatalf("saved-key discovery response=%d used_source=%t body=%s", response.Code, seenKey == secret, response.Body.String())
	}
}

func TestManagedAPIModelDiscoveryFailureAuditContainsOnlySafeDiagnostics(t *testing.T) {
	store := NewMemoryStore()
	audits := auth.NewMemoryStore()
	handler := NewHandler(store, audits, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).
		WithManagedAPIProber(failingListingManagedProber{})
	user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
	const tenantID = "00000000-0000-0000-0000-000000000020"
	const requestID = "request-safe-probe-audit"
	const credential = "deepseek-secret-at-least-16"
	request := httptest.NewRequest(http.MethodPost, "/api/v1/platform/model-api-configs/models", strings.NewReader(`{
		"tenant_id":"`+tenantID+`","api_key":"`+credential+`","provider":"deepseek"
	}`))
	request = request.WithContext(logger.WithRequestID(auth.WithUser(request.Context(), user), requestID))
	response := httptest.NewRecorder()
	handler.ListAvailableManagedAPIModels(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("model discovery returned %d: %s", response.Code, response.Body.String())
	}
	records, err := audits.ListAudits(context.Background(), auth.PlatformTenantID, auth.AuditFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Action != "model.managed_api_model_discovery_failed" {
			continue
		}
		raw, _ := json.Marshal(record.AfterValue)
		text := string(raw)
		for _, forbidden := range []string{credential, "api.deepseek.com", "203.0.113.9", "upstream response body"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("probe audit leaked %q: %s", forbidden, text)
			}
		}
		if record.RequestID != requestID || record.AfterValue["provider_key"] != "deepseek" ||
			record.AfterValue["error_code"] != "provider_timeout" || record.AfterValue["failure_stage"] != "connect" ||
			record.AfterValue["attempts"] != 2 {
			t.Fatalf("probe audit omitted safe diagnostics: %#v", record)
		}
		return
	}
	t.Fatal("model discovery failure audit was not recorded")
}

func TestManagedAPIModelDiscoveryRequiresProviderWhenModelNameIsEmpty(t *testing.T) {
	providerSeen := ""
	service := NewAutoManagedAPIConfigService(NewMemoryStore(), listingManagedProber{providerSeen: &providerSeen})
	_, _, _, err := service.ListModels(context.Background(), "school-a", AutoManagedAPIConfigInput{
		APIKey: "must-not-be-sent-anywhere-123",
	})
	if !errors.Is(err, ErrManagedProviderUnknown) || providerSeen != "" {
		t.Fatalf("empty discovery target must fail before request: provider=%q err=%v", providerSeen, err)
	}
}

func TestManagedAPIConfigHandlerPromotesPreviouslyVerifiedBackupWithoutNewGeneration(t *testing.T) {
	store := NewMemoryStore()
	tenantID := "00000000-0000-0000-0000-000000000020"
	base := ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "secret-value-at-least-16", ModelName: "deepseek-v4-pro",
		ModelVersion: "deepseek-v4-pro", Region: "global", Status: "active", IsDefault: true,
	}
	if _, err := store.CreateManagedAPIConfig(context.Background(), tenantID, "actor", base); err != nil {
		t.Fatal(err)
	}
	base.ProviderKey = "aliyun"
	base.DisplayName = "阿里云百炼"
	base.BaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	base.ModelName = "qwen-plus"
	base.ModelVersion = "qwen-plus"
	base.Region = "cn"
	base.IsDefault = false
	verified := successfulManagedProbe()
	verified.ProbeMode = "capability"
	verified.GeneratedRequest = true
	base.InitialProbe = &verified
	backup, err := store.CreateManagedAPIConfig(context.Background(), tenantID, "actor", base)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).
		WithManagedAPIProber(countingManagedProber{calls: &calls, result: successfulManagedProbe()})
	user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
	body := `{"expected_updated_at":"` + backup.UpdatedAt.Format(time.RFC3339Nano) + `","display_name":"阿里云百炼","adapter_type":"openai_compatible",` +
		`"base_url":"https://dashscope.aliyuncs.com/compatible-mode/v1","api_key":"",` +
		`"model_name":"qwen-plus","model_version":"qwen-plus","region":"cn","status":"active","is_default":true}`
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/platform/model-api-configs/"+backup.ID+"?tenant_id="+tenantID, strings.NewReader(body))
	request.SetPathValue("id", backup.ID)
	request = request.WithContext(auth.WithUser(request.Context(), user))
	response := httptest.NewRecorder()
	handler.UpdateManagedAPIConfig(response, request)
	if response.Code != http.StatusOK || calls != 0 {
		t.Fatalf("set current returned %d, probe calls=%d: %s", response.Code, calls, response.Body.String())
	}
	items, err := store.ListManagedAPIConfigs(context.Background(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == backup.ID && (!item.IsDefault || item.LastTestStatus != "success") {
			t.Fatalf("backup was not safely promoted: %#v", item)
		}
	}
}

func TestManagedAPIConfigHandlerRejectsUnverifiedBackupAsCurrent(t *testing.T) {
	store := NewMemoryStore()
	tenantID := "00000000-0000-0000-0000-000000000020"
	backup, err := store.CreateManagedAPIConfig(context.Background(), tenantID, "actor", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "secret-value-at-least-16", ModelName: "deepseek-v4-pro",
		ModelVersion: "deepseek-v4-pro", Region: "global", Status: "active", IsDefault: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).
		WithManagedAPIProber(countingManagedProber{calls: &calls, result: successfulManagedProbe()})
	user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
	body := `{"expected_updated_at":"` + backup.UpdatedAt.Format(time.RFC3339Nano) + `","display_name":"DeepSeek","adapter_type":"openai_compatible","base_url":"https://api.deepseek.com",` +
		`"api_key":"","model_name":"deepseek-v4-pro","model_version":"deepseek-v4-pro","region":"global","status":"active","is_default":true}`
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/platform/model-api-configs/"+backup.ID+"?tenant_id="+tenantID, strings.NewReader(body))
	request.SetPathValue("id", backup.ID)
	request = request.WithContext(auth.WithUser(request.Context(), user))
	response := httptest.NewRecorder()
	handler.UpdateManagedAPIConfig(response, request)
	if response.Code != http.StatusConflict || calls != 0 || !strings.Contains(response.Body.String(), "managed_model_text_required") {
		t.Fatalf("unverified promotion returned %d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
}

func TestAutoManagedAPIConfigHandlerReturnsValidationAndNeverEchoesKey(t *testing.T) {
	store := NewMemoryStore()
	handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).
		WithManagedAPIProber(staticManagedProber{result: successfulManagedProbe()})
	user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
	tenantID := "00000000-0000-0000-0000-000000000020"
	secret := "deepseek-secret-at-least-16"
	response := performHandlerRequest(t, user, http.MethodPost, "/api/v1/platform/model-api-configs/auto", `{
		"tenant_id":"`+tenantID+`","api_key":"`+secret+`","model_name":"deepseek-v4-pro"
	}`, handler.AutoCreateManagedAPIConfig)
	if response.Code != http.StatusCreated || strings.Contains(response.Body.String(), secret) {
		t.Fatalf("auto create returned %d or exposed key: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Config     ManagedAPIConfig      `json:"config"`
		Validation ManagedAPIProbeResult `json:"validation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.Config.IsDefault || !payload.Validation.OK || payload.Validation.ProbeMode != "quick" {
		t.Fatalf("unexpected auto response: %#v %v", payload, err)
	}
}

func TestAutoManagedAPIConfigHandlerReusesKeyWithoutEchoingIt(t *testing.T) {
	store := NewMemoryStore()
	handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).
		WithManagedAPIProber(staticManagedProber{result: successfulManagedProbe()})
	user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
	tenantID := "00000000-0000-0000-0000-000000000020"
	secret := "deepseek-secret-at-least-16"
	source, err := store.CreateManagedAPIConfig(context.Background(), tenantID, "actor", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: secret, ModelName: "deepseek-v4-pro",
		ModelVersion: "deepseek-v4-pro", Region: "global", Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := performHandlerRequest(t, user, http.MethodPost, "/api/v1/platform/model-api-configs/auto", `{
		"tenant_id":"`+tenantID+`","credential_source_id":"`+source.ID+`",
		"provider":"deepseek","model_name":"deepseek-v4-flash"
	}`, handler.AutoCreateManagedAPIConfig)
	if response.Code != http.StatusCreated || strings.Contains(response.Body.String(), secret) || !strings.Contains(response.Body.String(), "deepseek-v4-flash") {
		t.Fatalf("credential reuse response=%d body=%s", response.Code, response.Body.String())
	}
	items, err := store.ListManagedAPIConfigs(context.Background(), tenantID)
	if err != nil || len(items) != 2 {
		t.Fatalf("expected two independently managed models: %#v %v", items, err)
	}
}

func TestManagedAPIConfigLegacyCreateAlsoValidatesBeforeSaving(t *testing.T) {
	store := NewMemoryStore()
	failed := successfulManagedProbe()
	failed.OK = false
	failed.ErrorCode = "credential_invalid"
	failed.Message = "API Key 无效或已被禁用"
	handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).
		WithManagedAPIProber(staticManagedProber{result: failed})
	user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
	tenantID := "00000000-0000-0000-0000-000000000020"
	response := performHandlerRequest(t, user, http.MethodPost, "/api/v1/platform/model-api-configs", `{
		"tenant_id":"`+tenantID+`","provider_key":"deepseek","display_name":"DeepSeek",
		"adapter_type":"openai_compatible","base_url":"https://api.deepseek.com",
		"api_key":"invalid-secret-at-least-16","model_name":"deepseek-v4-pro","model_version":"deepseek-v4-pro",
		"region":"global","status":"active","is_default":true
	}`, handler.CreateManagedAPIConfig)
	items, err := store.ListManagedAPIConfigs(context.Background(), tenantID)
	if response.Code != http.StatusUnprocessableEntity || err != nil || len(items) != 0 {
		t.Fatalf("failed legacy create returned %d and saved %#v: %s", response.Code, items, response.Body.String())
	}
}

func TestAutoManagedAPIConfigHandlerReturnsProviderChoicesBeforeProbingUnknownModel(t *testing.T) {
	store := NewMemoryStore()
	calls := 0
	handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).
		WithManagedAPIProber(countingManagedProber{calls: &calls, result: successfulManagedProbe()})
	user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
	response := performHandlerRequest(t, user, http.MethodPost, "/api/v1/platform/model-api-configs/auto", `{
		"tenant_id":"00000000-0000-0000-0000-000000000020",
		"api_key":"must-not-be-sent-anywhere-123","model_name":"school-model-v2"
	}`, handler.AutoCreateManagedAPIConfig)
	if response.Code != http.StatusUnprocessableEntity || calls != 0 ||
		!strings.Contains(response.Body.String(), `"code":"provider_unknown"`) ||
		!strings.Contains(response.Body.String(), `"key":"custom"`) {
		t.Fatalf("unknown model response=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
}
