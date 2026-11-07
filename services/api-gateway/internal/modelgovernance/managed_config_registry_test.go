package modelgovernance

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"errors"
	"net/http"
	"strings"
	"testing"
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
	thirdKey := "another-deepseek-secret-at-least-16"
	third, _, _, err := service.Create(ctx, "school-a", "actor", AutoManagedAPIConfigInput{
		APIKey: thirdKey, Provider: "deepseek", ModelName: "deepseek-v4-flash",
	})
	if err != nil || third.ID == second.ID || len(seen) != 3 || seen[2].APIKey != thirdKey {
		t.Fatalf("same provider/model must accept a separate key: %#v %v calls=%d", third, err, len(seen))
	}
	connection, err = store.GetManagedAPIConnection(ctx, "school-a", third.ID)
	if err != nil || connection.APIKey != thirdKey {
		t.Fatalf("separate configuration did not keep its own key: %v", err)
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
	}); err != nil {
		t.Fatalf("editing a model to match another configuration must succeed: %v", err)
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
	}); !errors.Is(err, ErrManagedCredentialSourceDisabled) {
		t.Fatalf("disabled credential source must not be reused: %v", err)
	}
}

func TestManagedAPIHandlersExplainDisabledCredentialSource(t *testing.T) {
	for _, action := range []string{"models", "validate", "auto"} {
		t.Run(action, func(t *testing.T) {
			store := NewMemoryStore()
			const tenantID = "00000000-0000-0000-0000-000000000020"
			const secret = "disabled-secret-at-least-16"
			source, err := store.CreateManagedAPIConfig(context.Background(), tenantID, "actor", ManagedAPIConfigInput{
				ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
				BaseURL: "https://api.deepseek.com", APIKey: secret, ModelName: "deepseek-chat",
				ModelVersion: "deepseek-chat", Region: "global", Status: "disabled",
			})
			if err != nil {
				t.Fatal(err)
			}
			seenKey := ""
			handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).
				WithManagedAPIProber(listingManagedProber{keySeen: &seenKey})
			user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
			handle := handler.ListAvailableManagedAPIModels
			if action == "validate" {
				handle = handler.ValidateManagedAPIConfig
			} else if action == "auto" {
				handle = handler.AutoCreateManagedAPIConfig
			}
			response := performHandlerRequest(t, user, http.MethodPost, "/api/v1/platform/model-api-configs/"+action, `{
				"tenant_id":"`+tenantID+`","credential_source_id":"`+source.ID+`",
				"provider":"deepseek","model_name":"deepseek-reasoner"
			}`, handle)
			if response.Code != http.StatusConflict ||
				!strings.Contains(response.Body.String(), `"code":"managed_model_credential_source_disabled"`) ||
				!strings.Contains(response.Body.String(), "请先启用") || strings.Contains(response.Body.String(), secret) || seenKey != "" {
				t.Fatalf("disabled source must return an actionable conflict before discovery: status=%d body=%s", response.Code, response.Body.String())
			}
			items, err := store.ListManagedAPIConfigs(context.Background(), tenantID)
			if err != nil || len(items) != 1 || items[0].Status != "disabled" {
				t.Fatalf("rejected reuse must not create or enable a config: count=%d err=%v", len(items), err)
			}
		})
	}
}

func TestManagedAPIModelDiscoveryResumesAfterSourceEnabled(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	source, err := store.CreateManagedAPIConfig(ctx, "school-a", "actor", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "disabled-secret-at-least-16", ModelName: "deepseek-chat",
		ModelVersion: "deepseek-chat", Region: "global", Status: "disabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	seenKey := ""
	service := NewAutoManagedAPIConfigService(store, listingManagedProber{models: []string{"deepseek-reasoner"}, keySeen: &seenKey})
	input := AutoManagedAPIConfigInput{CredentialSourceID: source.ID, Provider: "deepseek"}
	if _, _, _, err = service.ListModels(ctx, "school-a", input); !errors.Is(err, ErrManagedCredentialSourceDisabled) || seenKey != "" {
		t.Fatalf("disabled source must stop before the provider request: err=%v", err)
	}
	if _, err = store.UpdateManagedAPIConfig(ctx, "school-a", source.ID, ManagedAPIConfigUpdateInput{
		DisplayName: source.DisplayName, AdapterType: source.AdapterType, BaseURL: source.BaseURL,
		ModelName: source.ModelName, ModelVersion: source.ModelVersion, Region: source.Region, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	_, list, result, err := service.ListModels(ctx, "school-a", input)
	if err != nil || !result.OK || len(list.Models) != 1 || seenKey != "disabled-secret-at-least-16" {
		t.Fatalf("enabled source should allow discovery without a model name: err=%v", err)
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
