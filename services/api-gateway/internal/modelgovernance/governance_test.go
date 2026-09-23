package modelgovernance

import (
	"context"
	"errors"
	"testing"
)

func TestPolicyAuthorizesOnlyVerifiedModelsFromItsSchool(t *testing.T) {
	store := NewMemoryStore()
	const school = "school-a"
	const allowed = "11111111-1111-4111-8111-111111111111"
	const otherSchool = "22222222-2222-4222-8222-222222222222"
	const disabled = "33333333-3333-4333-8333-333333333333"
	store.managedConfigs[allowed] = ManagedAPIConfig{
		ID: allowed, TenantID: school, Status: "active",
		LastCapabilityStatus: "success", LastCapabilityVersion: "structured-json-v3",
	}
	store.managedConfigs[otherSchool] = ManagedAPIConfig{
		ID: otherSchool, TenantID: "school-b", Status: "active",
		LastCapabilityStatus: "success", LastCapabilityVersion: "structured-json-v3",
	}
	store.managedConfigs[disabled] = ManagedAPIConfig{
		ID: disabled, TenantID: school, Status: "disabled",
		LastCapabilityStatus: "success", LastCapabilityVersion: "structured-json-v3",
	}
	current, err := store.GetPolicy(context.Background(), school)
	if err != nil {
		t.Fatal(err)
	}
	input := PolicyUpdateInput{
		Mode: ModeCloudSuggestion, ExternalEnabled: true, TextExportEnabled: true,
		FallbackMode: "manual_only", ExpectedVersion: current.Version, Reason: "test",
		AllowedModelConfigIDs: []string{otherSchool},
	}
	if _, err := store.UpdatePolicy(context.Background(), school, "actor", input); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("cross-school model must be rejected: %v", err)
	}
	input.AllowedModelConfigIDs = []string{disabled}
	if _, err := store.UpdatePolicy(context.Background(), school, "actor", input); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("disabled model must be rejected: %v", err)
	}
	input.AllowedModelConfigIDs = []string{allowed}
	updated, err := store.UpdatePolicy(context.Background(), school, "actor", input)
	if err != nil || len(updated.AllowedModelConfigIDs) != 1 || updated.AllowedModelConfigIDs[0] != allowed ||
		len(updated.AllowedDeployments) != 0 {
		t.Fatalf("managed policy did not retain sole model identity: %#v %v", updated, err)
	}
}

func TestSelectManagedModelFailsClosedForDisabledOrUnverifiedConfig(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	policy := TenantPolicy{
		TenantID: "school-a", Mode: ModeCloudSuggestion, ExternalEnabled: true,
		TextExportEnabled: true, FallbackMode: "manual_only",
		AllowedModelConfigIDs: []string{id},
	}
	config := ManagedAPIConfig{
		ID: id, TenantID: "school-a", ProviderKey: "deepseek", ModelName: "model-a",
		ModelVersion: "v1", AdapterType: "openai_compatible", Region: "global",
		Modalities: []string{"text"}, Status: "active", LastTestStatus: "success",
		LastCapabilityStatus: "success", LastCapabilityVersion: "structured-json-v3",
	}
	decision, err := SelectManagedModel(policy, RouteRequest{Modality: "text"}, []ManagedAPIConfig{config})
	if err != nil || decision.ModelConfigID != id || decision.ModelVersion != "v1" {
		t.Fatalf("managed route did not freeze allowed identity: %#v %v", decision, err)
	}
	config.Status = "disabled"
	if _, err := SelectManagedModel(policy, RouteRequest{Modality: "text"}, []ManagedAPIConfig{config}); !errors.Is(err, ErrNoDeployment) {
		t.Fatalf("disabled model must not route: %v", err)
	}
	config.Status = "active"
	config.LastCapabilityVersion = "legacy-v1"
	if _, err := SelectManagedModel(policy, RouteRequest{Modality: "text"}, []ManagedAPIConfig{config}); !errors.Is(err, ErrNoDeployment) {
		t.Fatalf("stale capability must not route: %v", err)
	}
	config.LastCapabilityVersion = "structured-json-v3"
	config.TenantID = "school-b"
	if _, err := SelectManagedModel(policy, RouteRequest{Modality: "text"}, []ManagedAPIConfig{config}); !errors.Is(err, ErrNoDeployment) {
		t.Fatalf("cross-school model must not route: %v", err)
	}
}

func TestSelectManagedModelAuthorizationBoundaries(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	policy := TenantPolicy{
		TenantID: "school-a", Mode: ModeCloudSuggestion, ExternalEnabled: true,
		TextExportEnabled: true, ImageExportEnabled: true, FallbackMode: "manual_only",
		AllowedModelConfigIDs: []string{id},
	}
	config := ManagedAPIConfig{
		ID: id, TenantID: "school-a", ProviderKey: "deepseek", ModelName: "model-a",
		ModelVersion: "v1", Modalities: []string{"text", "image"}, Status: "active",
		LastTestStatus: "success", LastCapabilityStatus: "success",
		LastCapabilityVersion: ManagedCapabilityProbeVersion,
	}
	tests := []struct {
		name     string
		modality string
		change   func(*TenantPolicy, *ManagedAPIConfig)
	}{
		{name: "connection test failed", modality: "text", change: func(_ *TenantPolicy, c *ManagedAPIConfig) { c.LastTestStatus = "failed" }},
		{name: "capability test failed", modality: "text", change: func(_ *TenantPolicy, c *ManagedAPIConfig) { c.LastCapabilityStatus = "failed" }},
		{name: "not allowed", modality: "text", change: func(p *TenantPolicy, _ *ManagedAPIConfig) { p.AllowedModelConfigIDs = nil }},
		{name: "image modality absent", modality: "image", change: func(_ *TenantPolicy, c *ManagedAPIConfig) { c.Modalities = []string{"text"} }},
		{name: "external disabled", modality: "text", change: func(p *TenantPolicy, _ *ManagedAPIConfig) {
			p.Mode = ModeLocalOnly
			p.ExternalEnabled = false
			p.TextExportEnabled = false
			p.ImageExportEnabled = false
			p.AllowedModelConfigIDs = nil
		}},
		{name: "local only", modality: "text", change: func(p *TenantPolicy, _ *ManagedAPIConfig) { p.Mode = ModeLocalOnly }},
		{name: "text export disabled", modality: "text", change: func(p *TenantPolicy, _ *ManagedAPIConfig) { p.TextExportEnabled = false }},
		{name: "image export disabled", modality: "image", change: func(p *TenantPolicy, _ *ManagedAPIConfig) { p.ImageExportEnabled = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidatePolicy, candidateConfig := policy, config
			tt.change(&candidatePolicy, &candidateConfig)
			if _, err := SelectManagedModel(candidatePolicy, RouteRequest{Modality: tt.modality}, []ManagedAPIConfig{candidateConfig}); !errors.Is(err, ErrNoDeployment) {
				t.Fatalf("expected no deployment, got %v", err)
			}
		})
	}
}

func TestSelectManagedModelRejectsInvalidRequestAndSelectsStableAllowedModel(t *testing.T) {
	const firstID = "11111111-1111-4111-8111-111111111111"
	const secondID = "22222222-2222-4222-8222-222222222222"
	policy := TenantPolicy{
		TenantID: "school-a", Mode: ModeCloudSuggestion, ExternalEnabled: true,
		TextExportEnabled: true, ImageExportEnabled: true, FallbackMode: "manual_only",
		AllowedModelConfigIDs: []string{firstID, secondID},
	}
	models := []ManagedAPIConfig{
		{ID: secondID, TenantID: "school-a", ProviderKey: "z-provider", ModelName: "model-z", Status: "active", Modalities: []string{"text", "image"}, LastTestStatus: "success", LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion},
		{ID: firstID, TenantID: "school-a", ProviderKey: "a-provider", ModelName: "model-a", Status: "active", Modalities: []string{"text", "image"}, LastTestStatus: "success", LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion},
	}
	decision, err := SelectManagedModel(policy, RouteRequest{Modality: "image"}, models)
	if err != nil || decision.ModelConfigID != firstID {
		t.Fatalf("expected first authorized model in stable provider order, got %#v, %v", decision, err)
	}
	if models[0].ID != secondID {
		t.Fatal("selection changed caller model order")
	}
	for _, modality := range []string{"", "audio"} {
		if _, err := SelectManagedModel(policy, RouteRequest{Modality: modality}, models); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("invalid modality %q returned %v", modality, err)
		}
	}
	policy.FallbackMode = "unsafe"
	if _, err := SelectManagedModel(policy, RouteRequest{Modality: "text"}, models); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("invalid policy returned %v", err)
	}
}

func TestSelectManagedModelBreaksProviderAndNameTiesByModelID(t *testing.T) {
	const firstID = "11111111-1111-4111-8111-111111111111"
	const secondID = "22222222-2222-4222-8222-222222222222"
	policy := TenantPolicy{
		TenantID: "school-a", Mode: ModeCloudSuggestion, ExternalEnabled: true,
		TextExportEnabled: true, FallbackMode: "manual_only",
		AllowedModelConfigIDs: []string{firstID, secondID},
	}
	models := []ManagedAPIConfig{
		{ID: secondID, TenantID: "school-a", ProviderKey: "provider", ModelName: "same-name", Status: "active", Modalities: []string{"text"}, LastTestStatus: "success", LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion},
		{ID: firstID, TenantID: "school-a", ProviderKey: "provider", ModelName: "same-name", Status: "active", Modalities: []string{"text"}, LastTestStatus: "success", LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion},
	}
	decision, err := SelectManagedModel(policy, RouteRequest{Modality: "text"}, models)
	if err != nil || decision.ModelConfigID != firstID {
		t.Fatalf("expected lowest authorized model ID for tied provider and name, got %#v, %v", decision, err)
	}
	models[0].ModelName = "z-name"
	models[1].ModelName = "a-name"
	decision, err = SelectManagedModel(policy, RouteRequest{Modality: "text"}, models)
	if err != nil || decision.ModelConfigID != firstID {
		t.Fatalf("expected model name ordering within provider, got %#v, %v", decision, err)
	}
}

func TestDefaultPolicyCannotRouteToExternalDeployment(t *testing.T) {
	external := Provider{
		Key:           "vendor-a",
		Kind:          ProviderExternal,
		AdapterType:   "vendor_a_native",
		CredentialRef: "vault://edugrade/vendor-a",
		Region:        "cn-east",
		DataPolicy:    DataPolicy{RetentionMode: "no_store"},
		Status:        "active",
	}
	deployment := Deployment{
		Key:               "vendor-a-text-v1",
		ProviderKey:       external.Key,
		ModelVersion:      "vendor-a-model-v1",
		Region:            "cn-east",
		CapabilityProfile: "subjective-shadow-v1",
		Modalities:        []string{"text"},
		Status:            "shadow_only",
		HealthState:       "available",
	}
	_, err := SelectDeployment(DefaultTenantPolicy(), RouteRequest{Modality: "text"}, []Provider{external}, []Deployment{deployment})
	if !errors.Is(err, ErrNoDeployment) {
		t.Fatalf("default policy must fail closed instead of exporting data, got %v", err)
	}
}

func TestExternalDeploymentRequiresEveryAuthorizationLayer(t *testing.T) {
	external := Provider{
		Key:           "vendor-a",
		Kind:          ProviderExternal,
		AdapterType:   "vendor_a_native",
		CredentialRef: "vault://edugrade/vendor-a",
		Region:        "cn-east",
		DataPolicy:    DataPolicy{RetentionMode: "no_store"},
		Status:        "active",
	}
	deployment := Deployment{
		Key:               "vendor-a-text-v1",
		ProviderKey:       external.Key,
		ModelVersion:      "vendor-a-model-v1",
		Region:            "cn-east",
		CapabilityProfile: "subjective-shadow-v1",
		Modalities:        []string{"text"},
		Status:            "shadow_only",
		HealthState:       "available",
	}
	policy := TenantPolicy{
		Mode:               ModeShadowCompare,
		ExternalEnabled:    true,
		TextExportEnabled:  true,
		AllowedDeployments: []string{deployment.Key},
		FallbackMode:       "manual_only",
	}
	decision, err := SelectDeployment(policy, RouteRequest{Modality: "text"}, []Provider{external}, []Deployment{deployment})
	if err != nil {
		t.Fatalf("explicitly authorized shadow deployment should route: %v", err)
	}
	if decision.ProviderKey != external.Key || decision.DeploymentKey != deployment.Key {
		t.Fatalf("unexpected route decision: %#v", decision)
	}
	policy.TextExportEnabled = false
	if _, err := SelectDeployment(policy, RouteRequest{Modality: "text"}, []Provider{external}, []Deployment{deployment}); !errors.Is(err, ErrNoDeployment) {
		t.Fatalf("text export opt-out must not be bypassed, got %v", err)
	}
}

func TestProviderRejectsPlaintextCredentialAndCompatibilityAdapter(t *testing.T) {
	provider := Provider{
		Key:           "vendor-a",
		Kind:          ProviderExternal,
		AdapterType:   "vendor_a_native",
		CredentialRef: "secret-value",
		Region:        "cn-east",
		DataPolicy:    DataPolicy{RetentionMode: "no_store"},
		Status:        "unverified",
	}
	if !errors.Is(ValidateProvider(provider), ErrInvalidProvider) {
		t.Fatal("plaintext credential must be rejected")
	}
	provider.CredentialRef = "vault://edugrade/vendor-a"
	provider.AdapterType = "openai_compatible"
	if !errors.Is(ValidateProvider(provider), ErrInvalidProvider) {
		t.Fatal("OpenAI-compatible external adapter must be rejected")
	}
}

func TestLocalDeploymentRemainsAvailableWithoutExternalAuthorization(t *testing.T) {
	local := Provider{
		Key:         "local",
		Kind:        ProviderLocal,
		AdapterType: "local_llama_cpp",
		Region:      "on_premise",
		DataPolicy:  DataPolicy{RetentionMode: "no_store"},
		Status:      "active",
	}
	deployment := Deployment{
		Key:               "local-qwen3-4b-q4-k-m",
		ProviderKey:       local.Key,
		ModelVersion:      "Qwen/Qwen3-4B-GGUF:Q4_K_M",
		Region:            "on_premise",
		CapabilityProfile: "local-pilot-v1",
		Modalities:        []string{"text"},
		Status:            "shadow_only",
		HealthState:       "available",
	}
	decision, err := SelectDeployment(DefaultTenantPolicy(), RouteRequest{Modality: "text"}, []Provider{local}, []Deployment{deployment})
	if err != nil {
		t.Fatalf("local-only default should keep the governed local deployment available: %v", err)
	}
	if decision.ProviderKey != "local" || decision.DeploymentKey != deployment.Key {
		t.Fatalf("unexpected local decision: %#v", decision)
	}
}
