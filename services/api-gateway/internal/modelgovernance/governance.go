package modelgovernance

import (
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ProviderLocal    = "local"
	ProviderExternal = "external"

	ModeLocalOnly          = "local_only"
	ModeShadowCompare      = "shadow_compare"
	ModeCloudSuggestion    = "cloud_suggestion"
	ModeHybridEscalation   = "hybrid_escalation"
	ModeDualProviderReview = "dual_provider_review"
)

var (
	ErrInvalidProvider   = errors.New("invalid model provider")
	ErrInvalidDeployment = errors.New("invalid model deployment")
	ErrInvalidPolicy     = errors.New("invalid tenant model policy")
	ErrNoDeployment      = errors.New("no governed model deployment is eligible")
	ErrNotFound          = errors.New("model governance resource not found")
	ErrConflict          = errors.New("model governance resource conflict")
	ErrRevisionConflict  = errors.New("model governance resource revision conflict")
	ErrProductionUnsafe  = errors.New("model governance production configuration is unsafe")
	ErrInvalidApproval   = errors.New("invalid model sandbox approval")
	ErrInvalidEvaluation = errors.New("invalid model evaluation")
	ErrInvalidPromotion  = errors.New("invalid model promotion")
	ErrModelNotApproved  = errors.New("model scope is not approved")

	governanceKey = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
)

type DataPolicy struct {
	TrainingAllowed bool   `json:"training_allowed"`
	RetentionMode   string `json:"retention_mode"`
}

type Provider struct {
	ID                   string     `json:"id,omitempty"`
	TenantID             string     `json:"tenant_id,omitempty"`
	Key                  string     `json:"provider_key"`
	DisplayName          string     `json:"display_name,omitempty"`
	Kind                 string     `json:"provider_kind"`
	AdapterType          string     `json:"adapter_type"`
	CredentialRef        string     `json:"-"`
	CredentialConfigured bool       `json:"credential_reference_set"`
	CredentialScheme     string     `json:"credential_scheme,omitempty"`
	Region               string     `json:"region"`
	DataPolicy           DataPolicy `json:"data_policy"`
	Status               string     `json:"status"`
	CreatedAt            time.Time  `json:"created_at,omitempty"`
	UpdatedAt            time.Time  `json:"updated_at,omitempty"`
}

type Deployment struct {
	ID                string         `json:"id,omitempty"`
	TenantID          string         `json:"tenant_id,omitempty"`
	ProviderID        string         `json:"provider_id,omitempty"`
	Key               string         `json:"deployment_key"`
	ProviderKey       string         `json:"provider_key"`
	ModelName         string         `json:"model_name,omitempty"`
	ModelVersion      string         `json:"model_version"`
	Region            string         `json:"region"`
	CapabilityProfile string         `json:"capability_profile"`
	Modalities        []string       `json:"modalities"`
	CapabilityPolicy  map[string]any `json:"capability_policy,omitempty"`
	PricingPolicy     map[string]any `json:"pricing_policy,omitempty"`
	Status            string         `json:"status"`
	HealthState       string         `json:"health_state"`
	CreatedAt         time.Time      `json:"created_at,omitempty"`
	UpdatedAt         time.Time      `json:"updated_at,omitempty"`
}

type TenantPolicy struct {
	ID                       string    `json:"id,omitempty"`
	TenantID                 string    `json:"tenant_id,omitempty"`
	PolicyKey                string    `json:"policy_key,omitempty"`
	DisplayName              string    `json:"display_name,omitempty"`
	Mode                     string    `json:"mode"`
	ExternalEnabled          bool      `json:"external_enabled"`
	TextExportEnabled        bool      `json:"text_export_enabled"`
	ImageExportEnabled       bool      `json:"image_export_enabled"`
	AllowedDeployments       []string  `json:"allowed_deployments"`
	AllowedModelConfigIDs    []string  `json:"allowed_model_config_ids"`
	MaxCostMicrosPerQuestion int64     `json:"max_cost_micros_per_question"`
	MaxCostMicrosPerExam     int64     `json:"max_cost_micros_per_exam"`
	FallbackMode             string    `json:"fallback_mode"`
	Status                   string    `json:"status,omitempty"`
	Version                  int64     `json:"version,omitempty"`
	UpdatedAt                time.Time `json:"updated_at,omitempty"`
}

type LocalBaseline struct {
	ProviderKey       string
	ProviderName      string
	DeploymentKey     string
	ModelName         string
	ModelVersion      string
	AdapterType       string
	Region            string
	CapabilityProfile string
}

type ProviderInput struct {
	TenantID      string     `json:"tenant_id,omitempty"`
	Key           string     `json:"provider_key"`
	DisplayName   string     `json:"display_name"`
	Kind          string     `json:"provider_kind"`
	AdapterType   string     `json:"adapter_type"`
	CredentialRef string     `json:"credential_ref,omitempty"`
	Region        string     `json:"region"`
	DataPolicy    DataPolicy `json:"data_policy"`
	Status        string     `json:"status,omitempty"`
}

type ProviderStatusInput struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type DeploymentInput struct {
	TenantID          string         `json:"tenant_id,omitempty"`
	ProviderID        string         `json:"provider_id"`
	Key               string         `json:"deployment_key"`
	ModelName         string         `json:"model_name"`
	ModelVersion      string         `json:"model_version"`
	Region            string         `json:"region"`
	CapabilityProfile string         `json:"capability_profile"`
	Modalities        []string       `json:"modalities"`
	CapabilityPolicy  map[string]any `json:"capability_policy"`
	PricingPolicy     map[string]any `json:"pricing_policy"`
	Status            string         `json:"status,omitempty"`
	HealthState       string         `json:"health_state,omitempty"`
}

type DeploymentStateInput struct {
	Status      string `json:"status"`
	HealthState string `json:"health_state"`
	Reason      string `json:"reason"`
}

type PolicyUpdateInput struct {
	DisplayName              string   `json:"display_name"`
	Mode                     string   `json:"mode"`
	ExternalEnabled          bool     `json:"external_enabled"`
	TextExportEnabled        bool     `json:"text_export_enabled"`
	ImageExportEnabled       bool     `json:"image_export_enabled"`
	AllowedDeployments       []string `json:"allowed_deployments"`
	AllowedModelConfigIDs    []string `json:"allowed_model_config_ids"`
	MaxCostMicrosPerQuestion int64    `json:"max_cost_micros_per_question"`
	MaxCostMicrosPerExam     int64    `json:"max_cost_micros_per_exam"`
	FallbackMode             string   `json:"fallback_mode"`
	ExpectedVersion          int64    `json:"expected_version"`
	Reason                   string   `json:"reason"`
}

type RouteRequest struct {
	Modality string
}

type RouteDecision struct {
	ProviderKey   string `json:"provider_key"`
	DeploymentKey string `json:"deployment_key"`
	Reason        string `json:"reason"`
}

type ManagedRouteDecision struct {
	ModelConfigID string `json:"model_config_id"`
	ProviderKey   string `json:"provider_key"`
	ModelName     string `json:"model_name"`
	ModelVersion  string `json:"model_version"`
	AdapterType   string `json:"adapter_type"`
	Region        string `json:"region"`
}

func DefaultTenantPolicy() TenantPolicy {
	return TenantPolicy{
		Mode:                  ModeLocalOnly,
		AllowedDeployments:    []string{},
		AllowedModelConfigIDs: []string{},
		FallbackMode:          "manual_only",
	}
}

// 外部供应商只能保存密钥引用，且必须声明不训练和受控留存策略；明文密钥不会通过校验。
func ValidateProvider(provider Provider) error {
	if !governanceKey.MatchString(provider.Key) ||
		!governanceKey.MatchString(provider.AdapterType) ||
		strings.Contains(strings.ToLower(strings.ReplaceAll(provider.AdapterType, "-", "_")), "openai_compatible") ||
		(provider.Kind != ProviderLocal && provider.Kind != ProviderExternal) ||
		strings.TrimSpace(provider.Region) == "" ||
		provider.DataPolicy.TrainingAllowed ||
		(provider.Status != "unverified" &&
			provider.Status != "active" &&
			provider.Status != "degraded" &&
			provider.Status != "rate_limited" &&
			provider.Status != "disabled") ||
		(provider.DataPolicy.RetentionMode != "no_store" && provider.DataPolicy.RetentionMode != "contractual") {
		return ErrInvalidProvider
	}
	if provider.Kind == ProviderLocal {
		if provider.CredentialRef != "" {
			return ErrInvalidProvider
		}
		return nil
	}
	if !validCredentialReference(provider.CredentialRef) {
		return ErrInvalidProvider
	}
	return nil
}

func ValidateDeployment(deployment Deployment, provider Provider) error {
	if !governanceKey.MatchString(deployment.Key) ||
		deployment.ProviderKey != provider.Key ||
		strings.TrimSpace(deployment.ModelVersion) == "" ||
		strings.TrimSpace(deployment.Region) == "" ||
		strings.TrimSpace(deployment.CapabilityProfile) == "" ||
		(deployment.Status != "shadow_only" && deployment.Status != "unverified" && deployment.Status != "disabled") ||
		(deployment.HealthState != "available" &&
			deployment.HealthState != "unverified" &&
			deployment.HealthState != "degraded" &&
			deployment.HealthState != "rate_limited" &&
			deployment.HealthState != "unavailable" &&
			deployment.HealthState != "disabled") ||
		len(deployment.Modalities) == 0 {
		return ErrInvalidDeployment
	}
	seen := map[string]bool{}
	for _, modality := range deployment.Modalities {
		if (modality != "text" && modality != "image") || seen[modality] {
			return ErrInvalidDeployment
		}
		seen[modality] = true
	}
	return nil
}

func ValidateTenantPolicy(policy TenantPolicy) error {
	switch policy.Mode {
	case ModeLocalOnly, ModeShadowCompare, ModeCloudSuggestion, ModeHybridEscalation, ModeDualProviderReview:
	default:
		return ErrInvalidPolicy
	}
	if policy.FallbackMode != "manual_only" && policy.FallbackMode != "approved_deployment_only" {
		return ErrInvalidPolicy
	}
	if policy.MaxCostMicrosPerQuestion < 0 || policy.MaxCostMicrosPerExam < 0 {
		return ErrInvalidPolicy
	}
	if !policy.ExternalEnabled &&
		(policy.Mode != ModeLocalOnly ||
			policy.TextExportEnabled ||
			policy.ImageExportEnabled ||
			len(policy.AllowedDeployments) != 0 ||
			len(policy.AllowedModelConfigIDs) != 0) {
		return ErrInvalidPolicy
	}
	for _, deployment := range policy.AllowedDeployments {
		if !governanceKey.MatchString(deployment) {
			return ErrInvalidPolicy
		}
	}
	seenConfigs := make(map[string]bool, len(policy.AllowedModelConfigIDs))
	for _, id := range policy.AllowedModelConfigIDs {
		if _, err := uuid.Parse(id); err != nil || seenConfigs[id] {
			return ErrInvalidPolicy
		}
		seenConfigs[id] = true
	}
	return nil
}

// 先按稳定键排序再筛选，保证同一策略下的路由结果可复现；外部模型还要同时满足租户授权和模态开关。
func SelectDeployment(
	policy TenantPolicy,
	request RouteRequest,
	providers []Provider,
	deployments []Deployment,
) (RouteDecision, error) {
	if err := ValidateTenantPolicy(policy); err != nil {
		return RouteDecision{}, err
	}
	if request.Modality != "text" && request.Modality != "image" {
		return RouteDecision{}, ErrInvalidPolicy
	}
	providerByKey := make(map[string]Provider, len(providers))
	for _, provider := range providers {
		if err := ValidateProvider(provider); err != nil {
			return RouteDecision{}, err
		}
		providerByKey[provider.Key] = provider
	}
	allowed := make(map[string]bool, len(policy.AllowedDeployments))
	for _, deployment := range policy.AllowedDeployments {
		allowed[deployment] = true
	}
	candidates := append([]Deployment(nil), deployments...)
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].ProviderKey == candidates[right].ProviderKey {
			return candidates[left].Key < candidates[right].Key
		}
		return candidates[left].ProviderKey < candidates[right].ProviderKey
	})
	for _, deployment := range candidates {
		provider, exists := providerByKey[deployment.ProviderKey]
		if !exists || ValidateDeployment(deployment, provider) != nil {
			continue
		}
		if provider.Status != "active" ||
			deployment.Status != "shadow_only" ||
			deployment.HealthState != "available" ||
			!contains(deployment.Modalities, request.Modality) {
			continue
		}
		if provider.Kind == ProviderExternal {
			if !policy.ExternalEnabled ||
				!allowed[deployment.Key] ||
				(request.Modality == "text" && !policy.TextExportEnabled) ||
				(request.Modality == "image" && !policy.ImageExportEnabled) ||
				policy.Mode == ModeLocalOnly {
				continue
			}
			return RouteDecision{
				ProviderKey:   provider.Key,
				DeploymentKey: deployment.Key,
				Reason:        "explicit tenant external authorization and deployment allowlist",
			}, nil
		}
		return RouteDecision{
			ProviderKey:   provider.Key,
			DeploymentKey: deployment.Key,
			Reason:        "local governed deployment selected without external data transfer",
		}, nil
	}
	return RouteDecision{}, ErrNoDeployment
}

// SelectManagedModel resolves current policy authorization through the school
// model inventory. Callers must still record the returned identity snapshot in
// the grading run and call ledger before dispatch.
func SelectManagedModel(policy TenantPolicy, request RouteRequest, configs []ManagedAPIConfig) (ManagedRouteDecision, error) {
	if err := ValidateTenantPolicy(policy); err != nil ||
		(request.Modality != "text" && request.Modality != "image") {
		return ManagedRouteDecision{}, ErrInvalidPolicy
	}
	if !policy.ExternalEnabled || policy.Mode == ModeLocalOnly ||
		(request.Modality == "text" && !policy.TextExportEnabled) ||
		(request.Modality == "image" && !policy.ImageExportEnabled) {
		return ManagedRouteDecision{}, ErrNoDeployment
	}
	allowed := make(map[string]bool, len(policy.AllowedModelConfigIDs))
	for _, id := range policy.AllowedModelConfigIDs {
		allowed[id] = true
	}
	candidates := append([]ManagedAPIConfig(nil), configs...)
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ProviderKey != candidates[j].ProviderKey {
			return candidates[i].ProviderKey < candidates[j].ProviderKey
		}
		if candidates[i].ModelName != candidates[j].ModelName {
			return candidates[i].ModelName < candidates[j].ModelName
		}
		return candidates[i].ID < candidates[j].ID
	})
	for _, config := range candidates {
		if config.TenantID != policy.TenantID || !allowed[config.ID] ||
			config.Status != "active" || config.LastTestStatus != "success" ||
			config.LastCapabilityStatus != "success" ||
			config.LastCapabilityVersion != "structured-json-v3" ||
			!contains(config.Modalities, request.Modality) {
			continue
		}
		return ManagedRouteDecision{
			ModelConfigID: config.ID, ProviderKey: config.ProviderKey,
			ModelName: config.ModelName, ModelVersion: config.ModelVersion,
			AdapterType: config.AdapterType, Region: config.Region,
		}, nil
	}
	return ManagedRouteDecision{}, ErrNoDeployment
}

func validCredentialReference(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	switch parsed.Scheme {
	case "env", "docker_secret", "vault", "aws_secrets_manager", "azure_key_vault", "gcp_secret_manager":
		return true
	default:
		return false
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
