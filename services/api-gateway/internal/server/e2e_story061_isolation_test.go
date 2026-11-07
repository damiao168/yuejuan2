package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"
)

// 沙箱批准必须绑定库存和审阅事实；重复批准、重复撤销冲突，明确撤销后才允许替代批准。
func e2eAssertGovernanceSandboxApprovalLifecycle(t *testing.T, ctx context.Context, db *sql.DB, governanceStore *modelgovernance.PostgresStore, tenantID string) modelgovernance.SandboxApprovalInput {
	t.Helper()
	dashscopeProvider, err := governanceStore.CreateProvider(ctx, tenantID, "", modelgovernance.ProviderInput{
		Key:           "dashscope-sandbox-" + uuid.NewString(),
		DisplayName:   "DashScope synthetic sandbox",
		Kind:          modelgovernance.ProviderExternal,
		AdapterType:   modelgovernance.SandboxProtocolDashScopeNative,
		CredentialRef: "vault://edugrade/dashscope-sandbox",
		Region:        "cn-beijing",
		DataPolicy:    modelgovernance.DataPolicy{RetentionMode: "no_store"},
		Status:        "unverified",
	})
	if err != nil {
		t.Fatalf("create DashScope sandbox provider: %v", err)
	}
	dashscopeDeployment, err := governanceStore.CreateDeployment(ctx, tenantID, "", modelgovernance.DeploymentInput{
		ProviderID:        dashscopeProvider.ID,
		Key:               "qwen-synthetic-" + uuid.NewString(),
		ModelName:         "Qwen synthetic sandbox",
		ModelVersion:      "pinned-synthetic-version",
		Region:            dashscopeProvider.Region,
		CapabilityProfile: "synthetic-text-v1",
		Modalities:        []string{"text", "image"},
		PricingPolicy:     map[string]any{"meter": "token"},
		Status:            "unverified",
		HealthState:       "unverified",
	})
	if err != nil {
		t.Fatalf("create DashScope sandbox deployment: %v", err)
	}
	approvalNow := time.Now().UTC()
	approvalInput := modelgovernance.SandboxApprovalInput{
		ProviderID:            dashscopeProvider.ID,
		DeploymentID:          dashscopeDeployment.ID,
		Protocol:              modelgovernance.SandboxProtocolDashScopeNative,
		ApprovalReference:     "approval-" + uuid.NewString(),
		ApprovedRegion:        dashscopeProvider.Region,
		SandboxAccount:        true,
		ContractReviewed:      true,
		RetentionReviewed:     true,
		DataResidencyReviewed: true,
		PricingReviewed:       true,
		SyntheticDataOnly:     true,
		ImageExportReviewed:   false,
		ExpiresAt:             approvalNow.Add(30 * 24 * time.Hour),
		Reason:                "approve bounded synthetic sandbox preparation",
	}
	approval, err := governanceStore.CreateSandboxApproval(ctx, tenantID, "", approvalInput)
	if err != nil {
		t.Fatalf("create persisted sandbox approval: %v", err)
	}
	if approval.ProviderKey != dashscopeProvider.Key ||
		approval.DeploymentKey != dashscopeDeployment.Key ||
		!approval.IsActive(approvalNow) {
		t.Fatalf("sandbox approval was not bound to governed inventory: %#v", approval)
	}
	approvals, err := governanceStore.ListSandboxApprovals(ctx, tenantID)
	if err != nil || len(approvals) != 1 || approvals[0].ID != approval.ID {
		t.Fatalf("unexpected sandbox approval history: %#v %v", approvals, err)
	}
	if _, err := governanceStore.CreateSandboxApproval(ctx, tenantID, "", approvalInput); !errors.Is(err, modelgovernance.ErrConflict) {
		t.Fatalf("duplicate active sandbox approval was not rejected: %v", err)
	}
	revokedApproval, err := governanceStore.RevokeSandboxApproval(
		ctx, tenantID, "", approval.ID, "synthetic sandbox approval withdrawn",
	)
	if err != nil || revokedApproval.RevokedAt == nil {
		t.Fatalf("revoke sandbox approval: %#v %v", revokedApproval, err)
	}
	if _, err := governanceStore.RevokeSandboxApproval(
		ctx, tenantID, "", approval.ID, "duplicate revoke",
	); !errors.Is(err, modelgovernance.ErrConflict) {
		t.Fatalf("duplicate sandbox approval revocation was not rejected: %v", err)
	}
	approvalInput.ApprovalReference = "approval-" + uuid.NewString()
	replacementApproval, err := governanceStore.CreateSandboxApproval(ctx, tenantID, "", approvalInput)
	if err != nil {
		t.Fatalf("replacement approval after explicit revocation failed: %v", err)
	}
	if _, err := governanceStore.RevokeSandboxApproval(
		ctx, tenantID, "", replacementApproval.ID, "release deployment for constraint verification",
	); err != nil {
		t.Fatalf("revoke replacement sandbox approval: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO model_sandbox_approval (
  tenant_id, provider_id, deployment_id, protocol,
  approval_reference, approved_region, sandbox_account,
  contract_reviewed, retention_reviewed, data_residency_reviewed,
  pricing_reviewed, synthetic_data_only, expires_at
)
VALUES (
  $1, $2, $3, 'dashscope_native',
  $4, 'cn-beijing', TRUE,
  FALSE, TRUE, TRUE,
  TRUE, TRUE, now() + INTERVAL '1 day'
)
`, tenantID, dashscopeProvider.ID, dashscopeDeployment.ID, "invalid-"+uuid.NewString()); err == nil {
		t.Fatal("database accepted incomplete sandbox approval facts")
	}

	return approvalInput
}

// 新租户由触发器建立关闭的默认策略，并拒绝跨租户沙箱、评测候选和提供商创建者。
func e2eAssertGovernanceNewTenantIsolation(t *testing.T, ctx context.Context, db *sql.DB, governanceStore *modelgovernance.PostgresStore, tenantID string, evaluation story061EvaluationFixture, approvalInput modelgovernance.SandboxApprovalInput) {
	t.Helper()
	guardRun, candidateInput, createdDeployment := evaluation.GuardRun, evaluation.CandidateInput, evaluation.CreatedDeployment
	newTenantID := uuid.NewString()
	if _, err := db.ExecContext(ctx, `
INSERT INTO tenant (id, tenant_id, name, code, status, settings)
VALUES ($1, $1, 'STORY-061 Trigger Tenant', $2, 'active', '{}')
`, newTenantID, "story061-"+uuid.NewString()); err != nil {
		t.Fatalf("insert tenant for default-policy trigger: %v", err)
	}
	var triggeredMode string
	var triggeredExternal bool
	if err := db.QueryRowContext(ctx, `
SELECT mode, external_enabled
FROM tenant_model_policy
WHERE tenant_id = $1 AND policy_key = 'default'
`, newTenantID).Scan(&triggeredMode, &triggeredExternal); err != nil {
		t.Fatalf("default policy trigger did not create a policy: %v", err)
	}
	if triggeredMode != "local_only" || triggeredExternal {
		t.Fatalf("new tenant did not fail closed: mode=%s external=%v", triggeredMode, triggeredExternal)
	}
	if _, err := governanceStore.CreateSandboxApproval(ctx, newTenantID, "", approvalInput); !errors.Is(err, modelgovernance.ErrNotFound) {
		t.Fatalf("cross-tenant sandbox approval did not fail closed: %v", err)
	}
	if _, err := governanceStore.AddEvaluationCandidate(
		ctx, newTenantID, "", guardRun.ID, candidateInput(createdDeployment.ID),
	); !errors.Is(err, modelgovernance.ErrNotFound) {
		t.Fatalf("cross-tenant model evaluation did not fail closed: %v", err)
	}

	var demoUserID string
	if err := db.QueryRowContext(ctx, `
SELECT id::text
FROM app_user
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY created_at
LIMIT 1
`, tenantID).Scan(&demoUserID); err != nil {
		t.Fatalf("lookup demo user for tenant-isolation check: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO model_provider (
  tenant_id, provider_key, display_name, provider_kind, adapter_type,
  region, data_policy, created_by
)
VALUES (
  $1, 'cross-tenant-provider', 'Cross Tenant Provider', 'local', 'local_llama_cpp',
  'on_premise', '{"training_allowed":false,"retention_mode":"no_store"}', $2
)
`, newTenantID, demoUserID); err == nil {
		t.Fatal("database accepted a provider creator from another tenant")
	}

}
