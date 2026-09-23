package modelgovernance

import (
	"strings"
	"time"
)

type SandboxApproval struct {
	ID                    string     `json:"id"`
	TenantID              string     `json:"tenant_id,omitempty"`
	ModelConfigID         string     `json:"model_config_id,omitempty"`
	ModelName             string     `json:"model_name,omitempty"`
	ModelVersion          string     `json:"model_version,omitempty"`
	ProviderID            string     `json:"provider_id"`
	DeploymentID          string     `json:"deployment_id"`
	ProviderKey           string     `json:"provider_key"`
	DeploymentKey         string     `json:"deployment_key"`
	Protocol              string     `json:"protocol"`
	ApprovalReference     string     `json:"approval_reference"`
	ApprovedRegion        string     `json:"approved_region"`
	SandboxAccount        bool       `json:"sandbox_account"`
	ContractReviewed      bool       `json:"contract_reviewed"`
	RetentionReviewed     bool       `json:"retention_reviewed"`
	DataResidencyReviewed bool       `json:"data_residency_reviewed"`
	PricingReviewed       bool       `json:"pricing_reviewed"`
	SyntheticDataOnly     bool       `json:"synthetic_data_only"`
	ImageExportReviewed   bool       `json:"image_export_reviewed"`
	ExpiresAt             time.Time  `json:"expires_at"`
	RevokedAt             *time.Time `json:"revoked_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
}

type SandboxApprovalInput struct {
	TenantID              string    `json:"tenant_id,omitempty"`
	ModelConfigID         string    `json:"model_config_id,omitempty"`
	ProviderID            string    `json:"provider_id"`
	DeploymentID          string    `json:"deployment_id"`
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
	Reason                string    `json:"reason"`
}

type SandboxApprovalRevokeInput struct {
	Reason string `json:"reason"`
}

func ValidateSandboxApprovalInput(input SandboxApprovalInput, now time.Time) error {
	managed := strings.TrimSpace(input.ModelConfigID) != ""
	legacy := strings.TrimSpace(input.ProviderID) != "" && strings.TrimSpace(input.DeploymentID) != ""
	if managed == legacy ||
		input.Protocol != SandboxProtocolDashScopeNative ||
		!governanceKey.MatchString(strings.TrimSpace(input.ApprovalReference)) ||
		strings.TrimSpace(input.ApprovedRegion) == "" ||
		!input.SandboxAccount ||
		!input.ContractReviewed ||
		!input.RetentionReviewed ||
		!input.DataResidencyReviewed ||
		!input.PricingReviewed ||
		!input.SyntheticDataOnly ||
		now.IsZero() ||
		input.ExpiresAt.IsZero() ||
		!input.ExpiresAt.After(now) ||
		input.ExpiresAt.Sub(now) > maxSandboxApprovalLifetime ||
		strings.TrimSpace(input.Reason) == "" {
		return ErrInvalidApproval
	}
	return nil
}

func (approval SandboxApproval) IsActive(now time.Time) bool {
	return approval.RevokedAt == nil && !now.IsZero() && approval.ExpiresAt.After(now)
}

func (approval SandboxApproval) Evidence() SandboxAdmissionEvidence {
	return SandboxAdmissionEvidence{
		Protocol:              approval.Protocol,
		ApprovalReference:     approval.ApprovalReference,
		ApprovedRegion:        approval.ApprovedRegion,
		SandboxAccount:        approval.SandboxAccount,
		ContractReviewed:      approval.ContractReviewed,
		RetentionReviewed:     approval.RetentionReviewed,
		DataResidencyReviewed: approval.DataResidencyReviewed,
		PricingReviewed:       approval.PricingReviewed,
		SyntheticDataOnly:     approval.SyntheticDataOnly,
		ImageExportReviewed:   approval.ImageExportReviewed,
		ExpiresAt:             approval.ExpiresAt,
	}
}
