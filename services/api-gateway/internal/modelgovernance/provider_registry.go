package modelgovernance

import "strings"

const ManagedProviderRegistryVersion = "2026-09-11"

type ProviderDefinition struct {
	Key           string   `json:"key"`
	DisplayName   string   `json:"display_name"`
	ModelPrefixes []string `json:"-"`
	BaseURL       string   `json:"base_url"`
	AdapterType   string   `json:"adapter_type"`
	Region        string   `json:"region"`
	ProbePolicy   string   `json:"-"`
}

type ProviderRegistry struct {
	version   string
	providers []ProviderDefinition
}

func NewProviderRegistry() ProviderRegistry {
	return ProviderRegistry{
		version: ManagedProviderRegistryVersion,
		providers: []ProviderDefinition{
			{Key: "deepseek", DisplayName: "DeepSeek", ModelPrefixes: []string{"deepseek-"}, BaseURL: "https://api.deepseek.com", AdapterType: "openai_compatible", Region: "global", ProbePolicy: "deepseek_hybrid"},
			{Key: "aliyun", DisplayName: "阿里云百炼", ModelPrefixes: []string{"qwen"}, BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", AdapterType: "openai_compatible", Region: "cn", ProbePolicy: "qwen_hybrid"},
			{Key: "openai", DisplayName: "OpenAI", ModelPrefixes: []string{"gpt-", "o1", "o3", "o4"}, BaseURL: "https://api.openai.com/v1", AdapterType: "openai_compatible", Region: "global"},
			{Key: "zhipu", DisplayName: "智谱 AI", ModelPrefixes: []string{"glm-"}, BaseURL: "https://open.bigmodel.cn/api/paas/v4", AdapterType: "openai_compatible", Region: "cn"},
			{Key: "moonshot", DisplayName: "Moonshot / Kimi", ModelPrefixes: []string{"kimi-", "moonshot-"}, BaseURL: "https://api.moonshot.cn/v1", AdapterType: "openai_compatible", Region: "cn"},
			{Key: "anthropic", DisplayName: "Anthropic", ModelPrefixes: []string{"claude-"}, BaseURL: "https://api.anthropic.com/v1", AdapterType: "openai_compatible", Region: "global"},
			{Key: "gemini", DisplayName: "Google Gemini", ModelPrefixes: []string{"gemini-"}, BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", AdapterType: "openai_compatible", Region: "global"},
		},
	}
}

func (r ProviderRegistry) Version() string {
	return r.version
}

func (r ProviderRegistry) Providers() []ProviderDefinition {
	items := make([]ProviderDefinition, len(r.providers), len(r.providers)+1)
	copy(items, r.providers)
	items = append(items, ProviderDefinition{Key: "custom", DisplayName: "其他兼容接口", AdapterType: "openai_compatible", Region: "custom"})
	return items
}

func (r ProviderRegistry) ProbePolicy(providerKey string) string {
	for _, provider := range r.providers {
		if provider.Key == providerKey {
			return provider.ProbePolicy
		}
	}
	return ""
}

// 显式 provider 优先于模型名前缀；custom 必须带受 HTTPS 校验的地址，避免把未知模型误发到任意端点。
func (r ProviderRegistry) Resolve(modelName, providerKey, customBaseURL string) (ProviderDefinition, error) {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	providerKey = strings.ToLower(strings.TrimSpace(providerKey))
	customBaseURL = strings.TrimRight(strings.TrimSpace(customBaseURL), "/")
	if modelName != "" && !boundedManagedValue(modelName) {
		return ProviderDefinition{}, ErrInvalidManagedConfig
	}
	if modelName == "" && providerKey == "" {
		return ProviderDefinition{}, ErrManagedProviderUnknown
	}
	if providerKey == "custom" {
		if customBaseURL == "" {
			return ProviderDefinition{}, ErrInvalidManagedConfig
		}
		return ProviderDefinition{Key: "custom", DisplayName: "其他兼容接口", BaseURL: customBaseURL, AdapterType: "openai_compatible", Region: "custom"}, nil
	}
	for _, provider := range r.providers {
		if providerKey != "" {
			if provider.Key == providerKey {
				return provider, nil
			}
			continue
		}
		for _, prefix := range provider.ModelPrefixes {
			if strings.HasPrefix(modelName, prefix) {
				return provider, nil
			}
		}
	}
	return ProviderDefinition{}, ErrManagedProviderUnknown
}
