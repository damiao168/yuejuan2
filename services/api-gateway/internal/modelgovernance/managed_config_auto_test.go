package modelgovernance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestProviderRegistryResolvesOfficialModelsWithoutUsingCredential(t *testing.T) {
	registry := NewProviderRegistry()
	tests := map[string]string{
		"deepseek-v4-pro":  "deepseek",
		"qwen-plus":        "aliyun",
		"gpt-5.6":          "openai",
		"o4-mini":          "openai",
		"glm-5":            "zhipu",
		"kimi-k2.5":        "moonshot",
		"claude-sonnet-5":  "anthropic",
		"gemini-3.8-flash": "gemini",
	}
	for model, expected := range tests {
		provider, err := registry.Resolve(model, "", "")
		if err != nil || provider.Key != expected {
			t.Fatalf("resolve %s: provider=%s err=%v", model, provider.Key, err)
		}
	}
	if _, err := registry.Resolve("school-model-v2", "", ""); !errors.Is(err, ErrManagedProviderUnknown) {
		t.Fatalf("unknown model returned %v", err)
	}
	custom, err := registry.Resolve("school-model-v2", "custom", "https://models.school.example/v1")
	if err != nil || custom.BaseURL != "https://models.school.example/v1" {
		t.Fatalf("custom provider not resolved: %#v %v", custom, err)
	}
}

func TestAutoManagedAPIConfigChecksConnectionBeforeSavingAsBackup(t *testing.T) {
	store := NewMemoryStore()
	calls := 0
	prober := countingManagedProber{calls: &calls, result: successfulManagedProbe()}
	service := NewAutoManagedAPIConfigService(store, prober)
	ctx := context.Background()

	first, resolved, result, err := service.Create(ctx, "school-a", "actor", AutoManagedAPIConfigInput{
		APIKey: "deepseek-secret-at-least-16", ModelName: "deepseek-v4-pro",
	})
	if err != nil || !result.OK || resolved.Provider.Key != "deepseek" || first.IsDefault || first.Status != "active" {
		t.Fatalf("unexpected first auto config: %#v %#v %#v %v", first, resolved, result, err)
	}
	if first.ConfigSource != "auto" || first.ProviderRegistryVersion != ManagedProviderRegistryVersion || first.LastTestStatus != "success" {
		t.Fatalf("auto metadata or validation health missing: %#v", first)
	}

	second, _, _, err := service.Create(ctx, "school-a", "actor", AutoManagedAPIConfigInput{
		APIKey: "qwen-secret-value-at-least-16", ModelName: "qwen-plus",
	})
	if err != nil || second.IsDefault || second.Status != "active" {
		t.Fatalf("second config must be an active backup: %#v %v", second, err)
	}
	if calls != 2 || result.ProbeMode != "quick" || result.GeneratedRequest {
		t.Fatalf("expected one zero-generation connection check per saved config, calls=%d result=%#v", calls, result)
	}
}

func TestAutoManagedAPIConfigReusesSchoolCredentialForAnotherModel(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	seen := []ManagedAPIConnection{}
	service := NewAutoManagedAPIConfigService(store, recordingManagedProber{seen: &seen})
	secret := "deepseek-secret-at-least-16"
	first, _, _, err := service.Create(ctx, "school-a", "actor", AutoManagedAPIConfigInput{
		APIKey: secret, Provider: "deepseek", ModelName: "deepseek-v4-pro",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, _, result, err := service.Create(ctx, "school-a", "actor", AutoManagedAPIConfigInput{
		CredentialSourceID: first.ID, Provider: "deepseek", ModelName: "deepseek-v4-flash",
	})
	if err != nil || !result.OK || second.ID == first.ID || second.IsDefault || second.ModelName != "deepseek-v4-flash" {
		t.Fatalf("same key must create a separately checked backup model: %#v %#v %v", second, result, err)
	}
	if len(seen) != 2 || seen[1].APIKey != secret || seen[1].Config.ModelName != second.ModelName || seen[1].Config.BaseURL != first.BaseURL {
		t.Fatalf("reuse must probe the new model at the original endpoint: %#v", seen)
	}
	connection, err := store.GetManagedAPIConnection(ctx, "school-a", second.ID)
	if err != nil || connection.APIKey != secret {
		t.Fatalf("new model did not store its own credential: %v", err)
	}
	if _, _, _, err = service.Create(ctx, "school-a", "actor", AutoManagedAPIConfigInput{
		CredentialSourceID: first.ID, Provider: "deepseek", ModelName: "deepseek-v4-flash",
	}); !errors.Is(err, ErrConflict) || len(seen) != 2 {
		t.Fatalf("duplicate provider/model must fail before probing: %v calls=%d", err, len(seen))
	}
	if _, _, _, err = service.Create(ctx, "school-b", "actor", AutoManagedAPIConfigInput{
		CredentialSourceID: first.ID, Provider: "deepseek", ModelName: "deepseek-v4-flash",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-school credential reuse must fail: %v", err)
	}
	if _, _, _, err = service.Create(ctx, "school-a", "actor", AutoManagedAPIConfigInput{
		CredentialSourceID: first.ID, Provider: "custom", BaseURL: "https://other.example/v1", ModelName: "other-model",
	}); !errors.Is(err, ErrInvalidManagedConfig) {
		t.Fatalf("credential must not be forwarded to another endpoint: %v", err)
	}
	if _, _, _, err = service.Create(ctx, "school-a", "actor", AutoManagedAPIConfigInput{
		CredentialSourceID: first.ID, APIKey: "another-secret-at-least-16", Provider: "deepseek", ModelName: "deepseek-v5",
	}); !errors.Is(err, ErrInvalidManagedConfig) {
		t.Fatalf("ambiguous credential source must fail: %v", err)
	}
	if _, err := store.UpdateManagedAPIConfig(ctx, "school-a", second.ID, ManagedAPIConfigUpdateInput{
		DisplayName: second.DisplayName, AdapterType: second.AdapterType, BaseURL: second.BaseURL,
		ModelName: first.ModelName, ModelVersion: first.ModelName, Region: second.Region,
		Status: "active",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("editing another model into a duplicate must fail: %v", err)
	}
	if _, err := store.UpdateManagedAPIConfig(ctx, "school-a", first.ID, ManagedAPIConfigUpdateInput{
		DisplayName: first.DisplayName, AdapterType: first.AdapterType, BaseURL: first.BaseURL,
		ModelName: first.ModelName, ModelVersion: first.ModelVersion, Region: first.Region,
		Status: "disabled",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = service.Create(ctx, "school-a", "actor", AutoManagedAPIConfigInput{
		CredentialSourceID: first.ID, Provider: "deepseek", ModelName: "deepseek-v5",
	}); !errors.Is(err, ErrInvalidManagedConfig) {
		t.Fatalf("disabled credential source must not be reused: %v", err)
	}
}

func TestManagedAPIModelDiscoveryCanReuseSavedCredential(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	source, err := store.CreateManagedAPIConfig(ctx, "school-a", "actor", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "deepseek-secret-at-least-16",
		ModelName: "deepseek-v4-pro", ModelVersion: "deepseek-v4-pro", Region: "global", Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	providerSeen := ""
	service := NewAutoManagedAPIConfigService(store, listingManagedProber{models: []string{"deepseek-v4-flash"}, providerSeen: &providerSeen})
	_, list, result, err := service.ListModels(ctx, "school-a", AutoManagedAPIConfigInput{
		CredentialSourceID: source.ID, Provider: "deepseek",
	})
	if err != nil || !result.OK || providerSeen != "deepseek" || len(list.Models) != 1 {
		t.Fatalf("saved credential discovery failed: %#v %#v %v", list, result, err)
	}
}

func TestReusedCredentialSavedIsTheOneActuallyProbed(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	oldKey, newKey := "original-secret-at-least-16", "rotated-secret-at-least-16"
	source, err := store.CreateManagedAPIConfig(ctx, "school-a", "actor", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: oldKey,
		ModelName: "deepseek-v4-pro", ModelVersion: "deepseek-v4-pro", Region: "global", Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := []ManagedAPIConnection{}
	prober := recordingManagedProber{seen: &seen, onQuick: func() {
		_, updateErr := store.UpdateManagedAPIConfig(ctx, "school-a", source.ID, ManagedAPIConfigUpdateInput{
			DisplayName: source.DisplayName, AdapterType: source.AdapterType, BaseURL: source.BaseURL,
			APIKey: newKey, ModelName: source.ModelName, ModelVersion: source.ModelVersion,
			Region: source.Region, Status: "active",
		})
		if updateErr != nil {
			t.Fatal(updateErr)
		}
	}}
	created, _, _, err := NewAutoManagedAPIConfigService(store, prober).Create(ctx, "school-a", "actor", AutoManagedAPIConfigInput{
		CredentialSourceID: source.ID, Provider: "deepseek", ModelName: "deepseek-v4-flash",
	})
	if err != nil || len(seen) != 1 || seen[0].APIKey != oldKey {
		t.Fatalf("quick probe did not use original key: %v %#v", err, seen)
	}
	connection, err := store.GetManagedAPIConnection(ctx, "school-a", created.ID)
	if err != nil || connection.APIKey != oldKey {
		t.Fatalf("saved key diverged from probed key: %v", err)
	}
}

func TestManagedQuickProbeNeverFallsBackToGeneratedRequest(t *testing.T) {
	calls := 0
	result := managedQuickProbe(context.Background(), generatedOnlyManagedProber{calls: &calls}, ManagedAPIConnection{
		Config: ManagedAPIConfig{ProviderKey: "custom", ModelName: "school-model-v2"},
	})
	if result.OK || result.ErrorCode != "quick_probe_unsupported" || result.GeneratedRequest || calls != 0 {
		t.Fatalf("quick probe must fail closed without generating: calls=%d result=%#v", calls, result)
	}
}

func TestAutoManagedAPIConfigNeverSavesFailedOrUnknownConfiguration(t *testing.T) {
	store := NewMemoryStore()
	calls := 0
	failed := successfulManagedProbe()
	failed.OK = false
	failed.ErrorCode = "credential_invalid"
	failed.Message = "API Key 无效或已被禁用"
	service := NewAutoManagedAPIConfigService(store, countingManagedProber{calls: &calls, result: failed})

	_, _, result, err := service.Create(context.Background(), "school-a", "actor", AutoManagedAPIConfigInput{
		APIKey: "invalid-secret-at-least-16", ModelName: "deepseek-v4-pro",
	})
	if err != nil || result.OK {
		t.Fatalf("failed validation returned unexpected result: %#v %v", result, err)
	}
	items, _ := store.ListManagedAPIConfigs(context.Background(), "school-a")
	if len(items) != 0 {
		t.Fatalf("failed validation was saved: %#v", items)
	}

	_, _, _, err = service.Create(context.Background(), "school-a", "actor", AutoManagedAPIConfigInput{
		APIKey: "must-never-be-probed-123", ModelName: "school-model-v2",
	})
	if !errors.Is(err, ErrManagedProviderUnknown) || calls != 1 {
		t.Fatalf("unknown provider must fail before probing: calls=%d err=%v", calls, err)
	}
}

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
	if err = store.DeleteManagedAPIConfig(context.Background(), "school-a", item.ID); !errors.Is(err, ErrManagedDefaultMutation) {
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
	if err = store.DeleteManagedAPIConfig(context.Background(), "school-a", item.ID); err != nil {
		t.Fatal(err)
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
	body := `{"display_name":"阿里云百炼","adapter_type":"openai_compatible",` +
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
	body := `{"display_name":"DeepSeek","adapter_type":"openai_compatible","base_url":"https://api.deepseek.com",` +
		`"api_key":"","model_name":"deepseek-v4-pro","model_version":"deepseek-v4-pro","region":"global","status":"active","is_default":true}`
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/platform/model-api-configs/"+backup.ID+"?tenant_id="+tenantID, strings.NewReader(body))
	request.SetPathValue("id", backup.ID)
	request = request.WithContext(auth.WithUser(request.Context(), user))
	response := httptest.NewRecorder()
	handler.UpdateManagedAPIConfig(response, request)
	if response.Code != http.StatusConflict || calls != 0 || !strings.Contains(response.Body.String(), "managed_model_capability_required") {
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

func successfulManagedProbe() ManagedAPIProbeResult {
	return ManagedAPIProbeResult{
		OK: true, StatusCode: 200, LatencyMS: 18, Message: "配置验证成功",
		CredentialCheck: ManagedAPICheckResult{OK: true}, ModelCheck: ManagedAPICheckResult{OK: true},
		CapabilityCheck: ManagedAPICheckResult{OK: true},
	}
}

type countingManagedProber struct {
	calls  *int
	result ManagedAPIProbeResult
}

type recordingManagedProber struct {
	seen    *[]ManagedAPIConnection
	onQuick func()
}

func (p recordingManagedProber) ProbeQuick(_ context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	*p.seen = append(*p.seen, connection)
	if p.onQuick != nil {
		p.onQuick()
	}
	return successfulManagedProbe()
}

func (p recordingManagedProber) Probe(_ context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	*p.seen = append(*p.seen, connection)
	return successfulManagedProbe()
}

type generatedOnlyManagedProber struct {
	calls *int
}

func (p generatedOnlyManagedProber) Probe(_ context.Context, _ ManagedAPIConnection) ManagedAPIProbeResult {
	*p.calls++
	return successfulManagedProbe()
}

func (p countingManagedProber) Probe(_ context.Context, _ ManagedAPIConnection) ManagedAPIProbeResult {
	*p.calls++
	return p.result
}

func (p countingManagedProber) ProbeQuick(_ context.Context, _ ManagedAPIConnection) ManagedAPIProbeResult {
	*p.calls++
	result := p.result
	result.ProbeMode = "quick"
	result.GeneratedRequest = false
	return result
}

type listingManagedProber struct {
	models       []string
	providerSeen *string
	keySeen      *string
}

func (p listingManagedProber) Probe(_ context.Context, _ ManagedAPIConnection) ManagedAPIProbeResult {
	return successfulManagedProbe()
}

func (p listingManagedProber) ListModels(_ context.Context, connection ManagedAPIConnection) (ManagedAPIModelListResult, ManagedAPIProbeResult) {
	provider := connection.Config.ProviderKey
	if p.providerSeen != nil {
		*p.providerSeen = provider
	}
	if p.keySeen != nil {
		*p.keySeen = connection.APIKey
	}
	return ManagedAPIModelListResult{Provider: provider, Models: p.models}, successfulManagedProbe()
}
