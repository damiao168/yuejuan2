package modelgovernance

import (
	"sort"
	"strings"
	"time"
)

const (
	SandboxProtocolDashScopeNative = "dashscope_native"
	maxSandboxApprovalLifetime     = 90 * 24 * time.Hour
)

type SandboxAdmissionEvidence struct {
	Protocol              string    `json:"protocol"`
	ApprovalReference     string    `json:"approval_reference"`
	ApprovedRegion        string    `json:"approved_region"`
	SandboxAccount        bool      `json:"sandbox_account"`
	ContractReviewed      bool      `json:"contract_reviewed"`
	RetentionReviewed     bool      `json:"retention_reviewed"`
	DataResidencyReviewed bool      `json:"data_residency_reviewed"`
	PricingReviewed       bool      `json:"pricing_reviewed"`
	SyntheticDataOnly     bool      `json:"synthetic_data_only"`
	ImageExportReviewed   bool      `json:"image_export_reviewed"`
	ExpiresAt             time.Time `json:"expires_at"`
}

type SandboxAdmissionDecision struct {
	Allowed  bool     `json:"allowed"`
	Blockers []string `json:"blockers"`
}

// 先做离线准入判断，再核对审批记录与供应商/部署的租户和 ID 绑定；任何解绑都会强制拒绝。
func AssessStoredSandboxAdmission(
	provider Provider,
	deployment Deployment,
	policy TenantPolicy,
	secret SecretProbe,
	approval SandboxApproval,
	modality string,
	now time.Time,
) SandboxAdmissionDecision {
	decision := AssessSandboxAdmission(
		provider, deployment, policy, secret, approval.Evidence(), modality, now,
	)
	validBinding := approval.TenantID != "" &&
		approval.TenantID == provider.TenantID &&
		approval.TenantID == deployment.TenantID &&
		approval.ProviderID == provider.ID &&
		approval.DeploymentID == deployment.ID &&
		approval.ProviderKey == provider.Key &&
		approval.DeploymentKey == deployment.Key &&
		approval.IsActive(now)
	if validBinding {
		return decision
	}
	if !contains(decision.Blockers, "sandbox_approval_record_invalid") {
		decision.Blockers = append(decision.Blockers, "sandbox_approval_record_invalid")
		sort.Strings(decision.Blockers)
	}
	decision.Allowed = false
	return decision
}

// AssessSandboxAdmission is the final offline gate before a future native
// provider sandbox invocation. It does not resolve credentials or perform I/O.
func AssessSandboxAdmission(
	provider Provider,
	deployment Deployment,
	policy TenantPolicy,
	secret SecretProbe,
	evidence SandboxAdmissionEvidence,
	modality string,
	now time.Time,
) SandboxAdmissionDecision {
	blockers := make(map[string]bool)
	block := func(code string) {
		blockers[code] = true
	}

	if ValidateProvider(provider) != nil ||
		provider.Kind != ProviderExternal ||
		provider.AdapterType != SandboxProtocolDashScopeNative {
		block("provider_not_approved")
	}
	if ValidateDeployment(deployment, provider) != nil ||
		deployment.ProviderKey != provider.Key {
		block("deployment_not_approved")
	}
	if ValidateTenantPolicy(policy) != nil {
		block("tenant_policy_invalid")
	}
	if modality != "text" && modality != "image" {
		block("modality_invalid")
	}
	if provider.Status != "active" ||
		deployment.Status != "shadow_only" ||
		deployment.HealthState != "available" {
		block("sandbox_not_active")
	}
	if !contains(deployment.Modalities, modality) {
		block("deployment_modality_unavailable")
	}
	if policy.Mode != ModeShadowCompare ||
		!policy.ExternalEnabled ||
		policy.FallbackMode != "manual_only" ||
		!contains(policy.AllowedDeployments, deployment.Key) {
		block("shadow_policy_not_approved")
	}
	if modality == "text" && !policy.TextExportEnabled {
		block("text_export_not_approved")
	}
	if modality == "image" && (!policy.ImageExportEnabled || !evidence.ImageExportReviewed) {
		block("image_export_not_approved")
	}
	if policy.MaxCostMicrosPerQuestion <= 0 || policy.MaxCostMicrosPerExam <= 0 {
		block("budget_not_configured")
	}
	if !secret.ResolverSupported || !secret.Configured || !secret.MeetsMinimumStrength {
		block("credential_not_ready")
	}
	if evidence.Protocol != SandboxProtocolDashScopeNative {
		block("native_protocol_not_approved")
	}
	if !governanceKey.MatchString(strings.TrimSpace(evidence.ApprovalReference)) {
		block("approval_reference_invalid")
	}
	approvedRegion := strings.TrimSpace(evidence.ApprovedRegion)
	if approvedRegion == "" ||
		approvedRegion != strings.TrimSpace(provider.Region) ||
		approvedRegion != strings.TrimSpace(deployment.Region) {
		block("region_not_approved")
	}
	if !evidence.SandboxAccount {
		block("sandbox_account_missing")
	}
	if !evidence.ContractReviewed {
		block("contract_not_reviewed")
	}
	if !evidence.RetentionReviewed ||
		(provider.DataPolicy.RetentionMode != "no_store" &&
			provider.DataPolicy.RetentionMode != "contractual") {
		block("retention_not_reviewed")
	}
	if !evidence.DataResidencyReviewed {
		block("data_residency_not_reviewed")
	}
	if !evidence.PricingReviewed ||
		strings.TrimSpace(stringValue(deployment.PricingPolicy["meter"])) == "" ||
		strings.TrimSpace(stringValue(deployment.PricingPolicy["meter"])) == "not_configured" {
		block("pricing_not_reviewed")
	}
	if !evidence.SyntheticDataOnly {
		block("synthetic_data_only_required")
	}
	if now.IsZero() ||
		evidence.ExpiresAt.IsZero() ||
		!evidence.ExpiresAt.After(now) ||
		evidence.ExpiresAt.Sub(now) > maxSandboxApprovalLifetime {
		block("approval_expired_or_unbounded")
	}

	codes := make([]string, 0, len(blockers))
	for code := range blockers {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return SandboxAdmissionDecision{Allowed: len(codes) == 0, Blockers: codes}
}
