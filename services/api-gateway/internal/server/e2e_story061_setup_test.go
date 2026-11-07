package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"github.com/google/uuid"
	"os"
	"strings"
	"testing"
)

func TestStory061GovernanceFoundationE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping STORY-061 PostgreSQL E2E")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	ctx := context.Background()

	var tenantID string
	if err := db.QueryRowContext(ctx, `
SELECT id::text
FROM tenant
WHERE code = 'demo' AND deleted_at IS NULL
`).Scan(&tenantID); err != nil {
		t.Fatalf("lookup demo tenant: %v", err)
	}

	e2eAssertGovernanceDefaultAndSQLConstraints(t, ctx, db, tenantID)
	e2eAssertGovernanceCallFactsAndRBAC(t, ctx, db, tenantID)
	governanceStore := modelgovernance.NewPostgresStore(db)
	baseline := e2eRegisterGovernanceLocalBaseline(t, ctx, governanceStore, tenantID)
	createdDeployment := e2eCreateGovernedExternalInventory(t, ctx, governanceStore, tenantID)
	e2eCloseGovernanceExternalPolicy(t, ctx, governanceStore, tenantID)
	evaluation := e2eCompleteGovernanceOfflineEvaluation(t, ctx, governanceStore, tenantID, baseline, createdDeployment)
	evaluation.GuardRun = e2eAssertGovernanceEvaluationSQLGuards(t, ctx, db, governanceStore, tenantID, evaluation)
	approvalInput := e2eAssertGovernanceSandboxApprovalLifecycle(t, ctx, db, governanceStore, tenantID)
	e2eAssertGovernanceNewTenantIsolation(t, ctx, db, governanceStore, tenantID, evaluation, approvalInput)
}

type story061EvaluationFixture struct {
	Input                              modelgovernance.EvaluationRunInput
	CandidateInput                     func(string) modelgovernance.EvaluationCandidateInput
	LocalDeployment, CreatedDeployment modelgovernance.Deployment
	GuardRun                           modelgovernance.EvaluationRun
}

// 租户模型策略默认关闭外部访问；数据库必须拒绝明文凭据和不兼容适配器，显式授权后也不能保留禁用外部路由。
func e2eAssertGovernanceDefaultAndSQLConstraints(t *testing.T, ctx context.Context, db *sql.DB, tenantID string) {
	t.Helper()
	var mode, fallback string
	var externalEnabled, textEnabled, imageEnabled bool
	var allowed string
	if err := db.QueryRowContext(ctx, `
SELECT mode, external_enabled, text_export_enabled, image_export_enabled,
       allowed_deployments::text, fallback_mode
FROM tenant_model_policy
WHERE tenant_id = $1 AND policy_key = 'default' AND deleted_at IS NULL
`, tenantID).Scan(&mode, &externalEnabled, &textEnabled, &imageEnabled, &allowed, &fallback); err != nil {
		t.Fatalf("read default model policy: %v", err)
	}
	if mode != "local_only" || externalEnabled || textEnabled || imageEnabled || allowed != "[]" || fallback != "manual_only" {
		t.Fatalf("external model access must default closed: mode=%s external=%v text=%v image=%v allowed=%s fallback=%s",
			mode, externalEnabled, textEnabled, imageEnabled, allowed, fallback)
	}

	if _, err := db.ExecContext(ctx, `
INSERT INTO model_provider (
  tenant_id, provider_key, display_name, provider_kind, adapter_type,
  credential_ref, region, data_policy
)
VALUES ($1, 'plaintext-provider', 'Plaintext Provider', 'external', 'vendor_native',
        'actual-secret-value', 'cn-east', '{"training_allowed":false,"retention_mode":"no_store"}')
`, tenantID); err == nil {
		t.Fatal("database accepted a plaintext provider credential")
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO model_provider (
  tenant_id, provider_key, display_name, provider_kind, adapter_type,
  credential_ref, region, data_policy
)
VALUES ($1, 'compat-provider', 'Compatibility Provider', 'external', 'openai_compatible',
        'vault://edugrade/vendor-a', 'cn-east', '{"training_allowed":false,"retention_mode":"no_store"}')
`, tenantID); err == nil {
		t.Fatal("database accepted an OpenAI-compatible external adapter")
	}

	var providerID string
	if err := db.QueryRowContext(ctx, `
INSERT INTO model_provider (
  tenant_id, provider_key, display_name, provider_kind, adapter_type,
  credential_ref, region, data_policy, status
)
VALUES ($1, 'vendor-a', 'Vendor A', 'external', 'vendor_a_native',
        'vault://edugrade/vendor-a', 'cn-east',
        '{"training_allowed":false,"retention_mode":"no_store"}', 'unverified')
RETURNING id::text
`, tenantID).Scan(&providerID); err != nil {
		t.Fatalf("insert reference-only external provider: %v", err)
	}

	var deploymentID string
	if err := db.QueryRowContext(ctx, `
INSERT INTO model_deployment (
  tenant_id, provider_id, deployment_key, model_name, model_version,
  region, capability_profile, modalities, capability_policy, pricing_policy
)
VALUES (
  $1, $2, 'vendor-a-text-v1', 'Vendor A Text', 'vendor-a-model-v1',
  'cn-east', 'subjective-shadow-v1', '["text"]',
  '{"subjects":["chinese"],"question_types":["short_answer"]}',
  '{"meter":"token","input_micros":1,"output_micros":2}'
)
RETURNING id::text
`, tenantID, providerID).Scan(&deploymentID); err != nil {
		t.Fatalf("insert governed deployment: %v", err)
	}
	if deploymentID == "" {
		t.Fatal("deployment id was not returned")
	}

	if _, err := db.ExecContext(ctx, `
UPDATE tenant_model_policy
SET mode = 'shadow_compare',
    external_enabled = TRUE,
    text_export_enabled = TRUE,
    allowed_deployments = '["vendor-a-text-v1"]',
    updated_at = now(),
    version = version + 1
WHERE tenant_id = $1 AND policy_key = 'default'
`, tenantID); err != nil {
		t.Fatalf("explicitly authorize shadow text deployment: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
UPDATE tenant_model_policy
SET external_enabled = FALSE
WHERE tenant_id = $1 AND policy_key = 'default'
`, tenantID); err == nil {
		t.Fatal("database allowed external opt-out while retaining an external route")
	}

}

// 同一请求只产生一个可计费调用事实，平台供应商管理与租户策略、评测权限分别核对。
func e2eAssertGovernanceCallFactsAndRBAC(t *testing.T, ctx context.Context, db *sql.DB, tenantID string) {
	t.Helper()
	requestID := "story061-" + uuid.NewString()
	if _, err := db.ExecContext(ctx, `
INSERT INTO model_call_fact (
  tenant_id, request_id, provider_key, deployment_key, adapter_type,
  model_version, prompt_version, rubric_version, capability_profile,
  deployment_region, route_mode, route_reason, status, attempts, latency_ms
)
VALUES (
  $1, $2, 'vendor-a', 'vendor-a-text-v1', 'vendor_a_native',
  'vendor-a-model-v1', 'prompt-v1', 'rubric-v1', 'subjective-shadow-v1',
  'cn-east', 'shadow_compare', 'explicit test authorization', 'succeeded', 1, 120
)
`, tenantID, requestID); err != nil {
		t.Fatalf("insert provider-neutral call fact: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO model_call_fact (
  tenant_id, request_id, provider_key, deployment_key, adapter_type,
  model_version, prompt_version, rubric_version, capability_profile,
  deployment_region, route_mode, status
)
VALUES (
  $1, $2, 'vendor-a', 'vendor-a-text-v1', 'vendor_a_native',
  'vendor-a-model-v1', 'prompt-v1', 'rubric-v1', 'subjective-shadow-v1',
  'cn-east', 'shadow_compare', 'replayed'
)
	`, tenantID, requestID); err == nil {
		t.Fatal("idempotent replay created a duplicate billable call fact")
	}

	var platformProviderManage, demoPolicyManage, demoEvaluationManage bool
	if err := db.QueryRowContext(ctx, `
SELECT
  EXISTS (
    SELECT 1
    FROM role
    JOIN role_permission ON role_permission.tenant_id = role.tenant_id AND role_permission.role_id = role.id
    JOIN permission ON permission.tenant_id = role_permission.tenant_id AND permission.id = role_permission.permission_id
    JOIN tenant ON tenant.id = role.tenant_id
    WHERE tenant.code = 'platform' AND role.code = 'platform_admin'
      AND permission.code = 'model:provider:manage'
      AND role.deleted_at IS NULL AND role_permission.deleted_at IS NULL AND permission.deleted_at IS NULL
  ),
  EXISTS (
    SELECT 1
    FROM role
    JOIN role_permission ON role_permission.tenant_id = role.tenant_id AND role_permission.role_id = role.id
    JOIN permission ON permission.tenant_id = role_permission.tenant_id AND permission.id = role_permission.permission_id
    JOIN tenant ON tenant.id = role.tenant_id
    WHERE tenant.code = 'demo' AND role.code = 'tenant_admin'
      AND permission.code = 'model:policy:manage'
      AND role.deleted_at IS NULL AND role_permission.deleted_at IS NULL AND permission.deleted_at IS NULL
  ),
  EXISTS (
    SELECT 1
    FROM role
    JOIN role_permission ON role_permission.tenant_id = role.tenant_id AND role_permission.role_id = role.id
    JOIN permission ON permission.tenant_id = role_permission.tenant_id AND permission.id = role_permission.permission_id
    JOIN tenant ON tenant.id = role.tenant_id
    WHERE tenant.code = 'demo' AND role.code = 'tenant_admin'
      AND permission.code = 'model:evaluation:manage'
      AND role.deleted_at IS NULL AND role_permission.deleted_at IS NULL AND permission.deleted_at IS NULL
  )
`).Scan(&platformProviderManage, &demoPolicyManage, &demoEvaluationManage); err != nil {
		t.Fatalf("read STORY-061 governance RBAC: %v", err)
	}
	if !platformProviderManage || !demoPolicyManage || !demoEvaluationManage {
		t.Fatalf(
			"governance RBAC was not assigned: platform_provider=%v demo_policy=%v demo_evaluation=%v",
			platformProviderManage, demoPolicyManage, demoEvaluationManage,
		)
	}

}
