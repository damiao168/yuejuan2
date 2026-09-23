package modelgovernance

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

var (
	ErrManagedConfigUnavailable  = errors.New("managed model API configuration is unavailable")
	ErrInvalidManagedConfig      = errors.New("invalid managed model API configuration")
	ErrManagedProviderUnknown    = errors.New("managed model API provider is unknown")
	ErrManagedDefaultMutation    = errors.New("current managed model API must remain active")
	ErrManagedCapabilityRequired = errors.New("managed model API capability verification is required")
	ErrManagedProbeStale         = errors.New("managed model API configuration changed during probe")
)

type ManagedAPIConfig struct {
	ID                       string                    `json:"id"`
	TenantID                 string                    `json:"tenant_id"`
	ProviderKey              string                    `json:"provider_key"`
	DisplayName              string                    `json:"display_name"`
	AdapterType              string                    `json:"adapter_type"`
	BaseURL                  string                    `json:"base_url"`
	ModelName                string                    `json:"model_name"`
	ModelVersion             string                    `json:"model_version"`
	Region                   string                    `json:"region"`
	Modalities               []string                  `json:"modalities"`
	CapabilityProfile        string                    `json:"capability_profile"`
	PricingPolicy            map[string]any            `json:"pricing_policy"`
	DataPolicy               map[string]any            `json:"data_policy"`
	HealthState              string                    `json:"health_state"`
	CredentialConfigured     bool                      `json:"credential_configured"`
	CredentialHint           string                    `json:"credential_hint,omitempty"`
	Status                   string                    `json:"status"`
	IsDefault                bool                      `json:"is_default"`
	LastTestStatus           string                    `json:"last_test_status"`
	LastTestMessage          string                    `json:"last_test_message,omitempty"`
	LastTestLatencyMS        int64                     `json:"last_test_latency_ms,omitempty"`
	LastTestedAt             *time.Time                `json:"last_tested_at,omitempty"`
	LastSuccessfulTestedAt   *time.Time                `json:"last_successful_tested_at,omitempty"`
	LastProbeMode            string                    `json:"last_probe_mode,omitempty"`
	LastCapabilityStatus     string                    `json:"last_capability_status"`
	LastCapabilityMessage    string                    `json:"last_capability_message,omitempty"`
	LastCapabilityTestedAt   *time.Time                `json:"last_capability_tested_at,omitempty"`
	LastCapabilityVersion    string                    `json:"last_capability_probe_version,omitempty"`
	LastCapabilityUsage      ManagedAPIProbeUsage      `json:"last_capability_usage"`
	LastCapabilityDiagnostic ManagedAPIProbeDiagnostic `json:"last_capability_diagnostic"`
	ConfigSource             string                    `json:"config_source"`
	ProviderRegistryVersion  string                    `json:"provider_registry_version,omitempty"`
	CreatedAt                time.Time                 `json:"created_at"`
	UpdatedAt                time.Time                 `json:"updated_at"`
}

type ManagedAPIConfigInput struct {
	TenantID                string                 `json:"tenant_id"`
	ProviderKey             string                 `json:"provider_key"`
	DisplayName             string                 `json:"display_name"`
	AdapterType             string                 `json:"adapter_type"`
	BaseURL                 string                 `json:"base_url"`
	ModelName               string                 `json:"model_name"`
	ModelVersion            string                 `json:"model_version"`
	Region                  string                 `json:"region"`
	APIKey                  string                 `json:"api_key"`
	Status                  string                 `json:"status"`
	IsDefault               bool                   `json:"is_default"`
	ConfigSource            string                 `json:"-"`
	ProviderRegistryVersion string                 `json:"-"`
	InitialProbe            *ManagedAPIProbeResult `json:"-"`
}

type ManagedAPIConfigUpdateInput struct {
	DisplayName  string                 `json:"display_name"`
	AdapterType  string                 `json:"adapter_type"`
	BaseURL      string                 `json:"base_url"`
	ModelName    string                 `json:"model_name"`
	ModelVersion string                 `json:"model_version"`
	Region       string                 `json:"region"`
	APIKey       string                 `json:"api_key,omitempty"`
	Status       string                 `json:"status"`
	IsDefault    bool                   `json:"is_default"`
	InitialProbe *ManagedAPIProbeResult `json:"-"`
}

type ManagedAPIConnection struct {
	Config ManagedAPIConfig
	APIKey string
}

type ManagedAPICheckResult struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type ManagedAPIProbeResult struct {
	OK                   bool                           `json:"ok"`
	ProbeMode            string                         `json:"probe_mode"`
	GeneratedRequest     bool                           `json:"generated_request"`
	Reused               bool                           `json:"reused,omitempty"`
	Coalesced            bool                           `json:"coalesced,omitempty"`
	Provider             string                         `json:"provider,omitempty"`
	Model                string                         `json:"model,omitempty"`
	StatusCode           int                            `json:"status_code,omitempty"`
	LatencyMS            int64                          `json:"latency_ms"`
	Message              string                         `json:"message"`
	ErrorCode            string                         `json:"error_code,omitempty"`
	CredentialCheck      ManagedAPICheckResult          `json:"credential_check"`
	ModelCheck           ManagedAPICheckResult          `json:"model_check"`
	CapabilityCheck      ManagedAPICheckResult          `json:"capability_check"`
	Usage                ManagedAPIProbeUsage           `json:"usage"`
	Diagnostic           ManagedAPIProbeDiagnostic      `json:"diagnostic"`
	ConnectionDiagnostic ManagedAPIConnectionDiagnostic `json:"connection_diagnostic"`
}

// Connection diagnostics contain timing only; never record endpoint URLs or credentials.
type ManagedAPIConnectionDiagnostic struct {
	Attempts     int    `json:"attempts,omitempty"`
	DNSMS        int64  `json:"dns_ms,omitempty"`
	ConnectMS    int64  `json:"connect_ms,omitempty"`
	TLSMS        int64  `json:"tls_ms,omitempty"`
	TTFBMS       int64  `json:"ttfb_ms,omitempty"`
	FailureStage string `json:"failure_stage,omitempty"`
}

type ManagedAPIProbeUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	ReasoningTokens   int64 `json:"reasoning_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
}

// ManagedAPIProbeDiagnostic contains bounded, non-secret evidence explaining a
// capability result. ContentPreview is returned only for the current request;
// stores must clear it before persistence.
type ManagedAPIProbeDiagnostic struct {
	FinishReason   string `json:"finish_reason,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	ContentLength  int    `json:"content_length,omitempty"`
	ContentSHA256  string `json:"content_sha256,omitempty"`
	ContentPreview string `json:"content_preview,omitempty"`
}

type ManagedAPIModelListResult struct {
	Provider  string   `json:"provider"`
	Models    []string `json:"models"`
	LatencyMS int64    `json:"latency_ms"`
}

type ManagedAPIConfigStore interface {
	ListManagedAPIConfigs(ctx context.Context, tenantID string) ([]ManagedAPIConfig, error)
	CreateManagedAPIConfig(ctx context.Context, tenantID, actorID string, input ManagedAPIConfigInput) (ManagedAPIConfig, error)
	UpdateManagedAPIConfig(ctx context.Context, tenantID, id string, input ManagedAPIConfigUpdateInput) (ManagedAPIConfig, error)
	DeleteManagedAPIConfig(ctx context.Context, tenantID, id string) error
	GetManagedAPIConnection(ctx context.Context, tenantID, id string) (ManagedAPIConnection, error)
	RecordManagedAPIProbe(ctx context.Context, tenantID, id string, expectedUpdatedAt time.Time, result ManagedAPIProbeResult) (ManagedAPIConfig, error)
}

type ManagedAPIProber interface {
	Probe(ctx context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult
}

type ManagedAPIModelLister interface {
	ListModels(ctx context.Context, connection ManagedAPIConnection) (ManagedAPIModelListResult, ManagedAPIProbeResult)
}

type ManagedAPIQuickProber interface {
	ProbeQuick(ctx context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult
}

// No default means the existing local parser remains in use. An explicitly
// selected but disabled/unreadable default must not silently switch providers.
func ResolveDefaultManagedAPI(ctx context.Context, store ManagedAPIConfigStore, tenantID string) (*ManagedAPIConnection, error) {
	items, err := store.ListManagedAPIConfigs(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.TenantID != tenantID || !item.IsDefault {
			continue
		}
		connection, err := store.GetManagedAPIConnection(ctx, tenantID, item.ID)
		if err != nil {
			return nil, err
		}
		if connection.Config.TenantID != tenantID || !connection.Config.IsDefault || connection.Config.Status != "active" ||
			connection.Config.LastCapabilityStatus != "success" || connection.Config.LastCapabilityVersion != ManagedCapabilityProbeVersion {
			return nil, ErrManagedConfigUnavailable
		}
		return &connection, nil
	}
	return nil, nil
}

type CredentialCipher struct {
	aead cipher.AEAD
}

func NewCredentialCipher(masterKey string) (*CredentialCipher, error) {
	if len(strings.TrimSpace(masterKey)) < 32 {
		return nil, ErrManagedConfigUnavailable
	}
	key := sha256.Sum256([]byte(masterKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &CredentialCipher{aead: aead}, nil
}

func (c *CredentialCipher) Encrypt(plaintext, tenantID, configID string) ([]byte, []byte, error) {
	if c == nil || strings.TrimSpace(plaintext) == "" {
		return nil, nil, ErrInvalidManagedConfig
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext := c.aead.Seal(nil, nonce, []byte(plaintext), managedCredentialAAD(tenantID, configID))
	return ciphertext, nonce, nil
}

func (c *CredentialCipher) Decrypt(ciphertext, nonce []byte, tenantID, configID string) (string, error) {
	if c == nil || len(nonce) != c.aead.NonceSize() || len(ciphertext) < c.aead.Overhead() {
		return "", ErrManagedConfigUnavailable
	}
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, managedCredentialAAD(tenantID, configID))
	if err != nil {
		return "", ErrManagedConfigUnavailable
	}
	return string(plaintext), nil
}

func managedCredentialAAD(tenantID, configID string) []byte {
	return []byte("edugrade:model-api:v1:" + tenantID + ":" + configID)
}

func normalizeManagedAPIInput(input ManagedAPIConfigInput, requireKey bool) (ManagedAPIConfigInput, error) {
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.ProviderKey = strings.ToLower(strings.TrimSpace(input.ProviderKey))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.AdapterType = strings.TrimSpace(input.AdapterType)
	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	input.ModelName = strings.TrimSpace(input.ModelName)
	input.ModelVersion = strings.TrimSpace(input.ModelVersion)
	input.Region = strings.TrimSpace(input.Region)
	input.APIKey = strings.TrimSpace(input.APIKey)
	input.Status = strings.TrimSpace(input.Status)
	input.ConfigSource = strings.TrimSpace(input.ConfigSource)
	input.ProviderRegistryVersion = strings.TrimSpace(input.ProviderRegistryVersion)
	if input.Status == "" {
		input.Status = "active"
	}
	if input.ConfigSource == "" {
		input.ConfigSource = "manual"
	}
	if input.Region == "" {
		input.Region = "global"
	}
	if !governanceKey.MatchString(input.ProviderKey) ||
		(input.AdapterType != "openai_compatible" && input.AdapterType != "dashscope_native") ||
		(input.Status != "active" && input.Status != "disabled") ||
		!boundedManagedValue(input.DisplayName) || !boundedManagedValue(input.ModelName) ||
		!boundedManagedValue(input.ModelVersion) || !boundedManagedValue(input.Region) ||
		(requireKey && len(input.APIKey) < 16) || (!requireKey && input.APIKey != "" && len(input.APIKey) < 16) {
		return ManagedAPIConfigInput{}, ErrInvalidManagedConfig
	}
	if input.IsDefault && input.Status != "active" {
		return ManagedAPIConfigInput{}, ErrManagedDefaultMutation
	}
	if input.ConfigSource != "manual" && input.ConfigSource != "auto" && input.ConfigSource != "imported" {
		return ManagedAPIConfigInput{}, ErrInvalidManagedConfig
	}
	parsed, err := url.Parse(input.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ManagedAPIConfigInput{}, ErrInvalidManagedConfig
	}
	if input.AdapterType == "dashscope_native" && (parsed.Host != "dashscope.aliyuncs.com" || strings.TrimRight(parsed.Path, "/") != "/api/v1") {
		return ManagedAPIConfigInput{}, ErrInvalidManagedConfig
	}
	if len(input.APIKey) > 1024 || strings.ContainsAny(input.APIKey, "\r\n\x00") {
		return ManagedAPIConfigInput{}, ErrInvalidManagedConfig
	}
	return input, nil
}

func normalizeManagedAPIUpdate(input ManagedAPIConfigUpdateInput) (ManagedAPIConfigUpdateInput, error) {
	normalized, err := normalizeManagedAPIInput(ManagedAPIConfigInput{
		ProviderKey: "update-placeholder", DisplayName: input.DisplayName,
		AdapterType: input.AdapterType, BaseURL: input.BaseURL, ModelName: input.ModelName,
		ModelVersion: input.ModelVersion, Region: input.Region, APIKey: input.APIKey,
		Status: input.Status, IsDefault: input.IsDefault,
	}, false)
	if err != nil {
		return ManagedAPIConfigUpdateInput{}, err
	}
	return ManagedAPIConfigUpdateInput{
		DisplayName: normalized.DisplayName, AdapterType: normalized.AdapterType,
		BaseURL: normalized.BaseURL, ModelName: normalized.ModelName,
		ModelVersion: normalized.ModelVersion, Region: normalized.Region,
		APIKey: normalized.APIKey, Status: normalized.Status, IsDefault: normalized.IsDefault,
		InitialProbe: input.InitialProbe,
	}, nil
}

func boundedManagedValue(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, "\r\n\x00")
}

func credentialHint(apiKey string) string {
	runes := []rune(strings.TrimSpace(apiKey))
	if len(runes) <= 4 {
		return "••••"
	}
	return fmt.Sprintf("•••• %s", string(runes[len(runes)-4:]))
}
