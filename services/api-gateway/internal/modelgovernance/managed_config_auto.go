package modelgovernance

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

type AutoManagedAPIConfigInput struct {
	TenantID           string `json:"tenant_id"`
	APIKey             string `json:"api_key"`
	CredentialSourceID string `json:"credential_source_id,omitempty"`
	ModelName          string `json:"model_name"`
	Provider           string `json:"provider,omitempty"`
	BaseURL            string `json:"base_url,omitempty"`
}

type ResolveManagedProviderInput struct {
	ModelName string `json:"model_name"`
	Provider  string `json:"provider,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
}

type ResolvedManagedProvider struct {
	Provider        ProviderDefinition `json:"provider"`
	ModelName       string             `json:"model_name"`
	RegistryVersion string             `json:"registry_version"`
}

type AutoManagedAPIConfigService struct {
	store    ManagedAPIConfigStore
	prober   ManagedAPIProber
	registry ProviderRegistry
}

func NewAutoManagedAPIConfigService(store ManagedAPIConfigStore, prober ManagedAPIProber) *AutoManagedAPIConfigService {
	if prober == nil {
		prober = NewHTTPManagedAPIProber(0)
	}
	return &AutoManagedAPIConfigService{store: store, prober: prober, registry: NewProviderRegistry()}
}

func (s *AutoManagedAPIConfigService) Resolve(input ResolveManagedProviderInput) (ResolvedManagedProvider, error) {
	modelName := strings.TrimSpace(input.ModelName)
	provider, err := s.registry.Resolve(modelName, input.Provider, input.BaseURL)
	if err != nil {
		return ResolvedManagedProvider{}, err
	}
	return ResolvedManagedProvider{Provider: provider, ModelName: modelName, RegistryVersion: s.registry.Version()}, nil
}

func (s *AutoManagedAPIConfigService) Validate(ctx context.Context, tenantID string, input AutoManagedAPIConfigInput) (ResolvedManagedProvider, ManagedAPIProbeResult, error) {
	resolved, err := s.Resolve(ResolveManagedProviderInput{ModelName: input.ModelName, Provider: input.Provider, BaseURL: input.BaseURL})
	if err != nil {
		return ResolvedManagedProvider{}, ManagedAPIProbeResult{}, err
	}
	apiKey, err := s.resolveCredential(ctx, tenantID, input, resolved)
	if err != nil {
		return ResolvedManagedProvider{}, ManagedAPIProbeResult{}, err
	}
	configInput := s.configInput(tenantID, input, resolved, false)
	configInput.APIKey = apiKey
	normalized, err := normalizeManagedAPIInput(configInput, true)
	if err != nil {
		return ResolvedManagedProvider{}, ManagedAPIProbeResult{}, err
	}
	connection := ManagedAPIConnection{Config: ManagedAPIConfig{
		TenantID: tenantID, ProviderKey: normalized.ProviderKey, DisplayName: normalized.DisplayName,
		AdapterType: normalized.AdapterType, BaseURL: normalized.BaseURL, ModelName: normalized.ModelName,
		ModelVersion: normalized.ModelVersion, Region: normalized.Region, Status: "active",
	}, APIKey: normalized.APIKey}
	result := managedQuickProbe(ctx, s.prober, connection)
	result.Provider = normalized.ProviderKey
	result.Model = normalized.ModelName
	return resolved, result, nil
}

func (s *AutoManagedAPIConfigService) ListModels(ctx context.Context, tenantID string, input AutoManagedAPIConfigInput) (ResolvedManagedProvider, ManagedAPIModelListResult, ManagedAPIProbeResult, error) {
	resolved, err := s.Resolve(ResolveManagedProviderInput{ModelName: input.ModelName, Provider: input.Provider, BaseURL: input.BaseURL})
	if err != nil {
		return ResolvedManagedProvider{}, ManagedAPIModelListResult{}, ManagedAPIProbeResult{}, err
	}
	apiKey, err := s.resolveCredential(ctx, tenantID, input, resolved)
	if err != nil {
		return ResolvedManagedProvider{}, ManagedAPIModelListResult{}, ManagedAPIProbeResult{}, err
	}
	configInput := s.configInput(tenantID, input, resolved, false)
	configInput.APIKey = apiKey
	// Model discovery only needs a resolved provider, endpoint and credential.
	// Use an internal placeholder to reuse the strict config validator, then keep
	// the externally visible connection model empty until the user selects one.
	if resolved.ModelName == "" {
		configInput.ModelName = "model-discovery"
		configInput.ModelVersion = "model-discovery"
	}
	normalized, err := normalizeManagedAPIInput(configInput, true)
	if err != nil {
		return ResolvedManagedProvider{}, ManagedAPIModelListResult{}, ManagedAPIProbeResult{}, err
	}
	lister, ok := s.prober.(ManagedAPIModelLister)
	if !ok {
		return ResolvedManagedProvider{}, ManagedAPIModelListResult{}, ManagedAPIProbeResult{}, ErrManagedConfigUnavailable
	}
	list, result := lister.ListModels(ctx, ManagedAPIConnection{Config: ManagedAPIConfig{
		TenantID: tenantID, ProviderKey: normalized.ProviderKey, DisplayName: normalized.DisplayName,
		AdapterType: normalized.AdapterType, BaseURL: normalized.BaseURL, ModelName: resolved.ModelName,
		ModelVersion: resolved.ModelName, Region: normalized.Region, Status: "active",
	}, APIKey: normalized.APIKey})
	return resolved, list, result, nil
}

// 创建前必须完成零生成快速检查；连接可用但结构化能力未验证时只能保存为备用配置。
func (s *AutoManagedAPIConfigService) Create(ctx context.Context, tenantID, actorID string, input AutoManagedAPIConfigInput) (ManagedAPIConfig, ResolvedManagedProvider, ManagedAPIProbeResult, error) {
	resolved, err := s.Resolve(ResolveManagedProviderInput{ModelName: input.ModelName, Provider: input.Provider, BaseURL: input.BaseURL})
	if err != nil {
		return ManagedAPIConfig{}, ResolvedManagedProvider{}, ManagedAPIProbeResult{}, err
	}
	// Resolve once: the exact credential checked by the quick probe must be
	// encrypted into the new model even if the source rotates concurrently.
	apiKey, err := s.resolveCredential(ctx, tenantID, input, resolved)
	if err != nil {
		return ManagedAPIConfig{}, resolved, ManagedAPIProbeResult{}, err
	}
	input.APIKey, input.CredentialSourceID = apiKey, ""
	resolved, result, err := s.Validate(ctx, tenantID, input)
	if err != nil {
		return ManagedAPIConfig{}, ResolvedManagedProvider{}, ManagedAPIProbeResult{}, err
	}
	if !result.OK {
		return ManagedAPIConfig{}, resolved, result, nil
	}
	// A newly saved credential is an active backup until it passes the explicit
	// structure capability check. Connectivity alone must never make it current.
	configInput := s.configInput(tenantID, input, resolved, false)
	configInput.InitialProbe = &result
	item, err := s.store.CreateManagedAPIConfig(ctx, tenantID, actorID, configInput)
	return item, resolved, result, err
}

// A saved credential may only be reused inside the same school and against its
// original provider endpoint. Never forward a decrypted key to a new URL.
func (s *AutoManagedAPIConfigService) resolveCredential(ctx context.Context, tenantID string, input AutoManagedAPIConfigInput, resolved ResolvedManagedProvider) (string, error) {
	sourceID := strings.TrimSpace(input.CredentialSourceID)
	if sourceID == "" {
		return input.APIKey, nil
	}
	if strings.TrimSpace(input.APIKey) != "" {
		return "", ErrInvalidManagedConfig
	}
	if _, err := uuid.Parse(sourceID); err != nil || s.store == nil {
		return "", ErrInvalidManagedConfig
	}
	source, err := s.store.GetManagedAPIConnection(ctx, tenantID, sourceID)
	if err != nil {
		return "", err
	}
	if source.Config.Status != "active" || source.Config.ProviderKey != resolved.Provider.Key ||
		source.Config.AdapterType != resolved.Provider.AdapterType || source.Config.BaseURL != resolved.Provider.BaseURL {
		return "", ErrInvalidManagedConfig
	}
	return source.APIKey, nil
}

func managedQuickProbe(ctx context.Context, prober ManagedAPIProber, connection ManagedAPIConnection) ManagedAPIProbeResult {
	if quick, ok := prober.(ManagedAPIQuickProber); ok {
		result := quick.ProbeQuick(ctx, connection)
		result.ProbeMode = "quick"
		result.GeneratedRequest = false
		return result
	}
	return failedManagedProbe(ManagedAPIProbeResult{
		ProbeMode: "quick", Provider: connection.Config.ProviderKey, Model: connection.Config.ModelName,
	}, "quick_probe_unsupported", "当前适配器不支持零生成 Token 快速检查", "credential")
}

func (s *AutoManagedAPIConfigService) configInput(tenantID string, input AutoManagedAPIConfigInput, resolved ResolvedManagedProvider, isDefault bool) ManagedAPIConfigInput {
	return ManagedAPIConfigInput{
		TenantID: tenantID, ProviderKey: resolved.Provider.Key, DisplayName: resolved.Provider.DisplayName,
		AdapterType: resolved.Provider.AdapterType, BaseURL: resolved.Provider.BaseURL,
		APIKey: input.APIKey, ModelName: resolved.ModelName, ModelVersion: resolved.ModelName,
		Region: resolved.Provider.Region, Status: "active", IsDefault: isDefault,
		ConfigSource: "auto", ProviderRegistryVersion: resolved.RegistryVersion,
	}
}
