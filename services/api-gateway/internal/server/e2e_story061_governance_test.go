package server

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"

	"github.com/google/uuid"
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

// 先登记本地基线，提供商读模型必须隐藏凭据引用，不能把凭据配置误当成公开字段。
func e2eRegisterGovernanceLocalBaseline(t *testing.T, ctx context.Context, governanceStore *modelgovernance.PostgresStore, tenantID string) modelgovernance.LocalBaseline {
	t.Helper()
	baseline := modelgovernance.LocalBaseline{
		ProviderKey:       "local",
		ProviderName:      "Local grading runtime",
		DeploymentKey:     "local-qwen3-4b-q4-k-m",
		ModelName:         "Qwen/Qwen3-4B-GGUF",
		ModelVersion:      "Qwen/Qwen3-4B-GGUF:Q4_K_M",
		AdapterType:       "local_llama_cpp",
		Region:            "on_premise",
		CapabilityProfile: "local-pilot-v1",
	}
	if err := governanceStore.EnsureLocalBaseline(ctx, tenantID, baseline); err != nil {
		t.Fatalf("register local baseline: %v", err)
	}
	providers, err := governanceStore.ListProviders(ctx, tenantID)
	if err != nil {
		t.Fatalf("list governed providers: %v", err)
	}
	var localRegistered bool
	for _, provider := range providers {
		if provider.Key == baseline.ProviderKey {
			localRegistered = true
		}
		if provider.CredentialRef != "" {
			t.Fatalf("provider read model exposed credential_ref: %#v", provider)
		}
	}
	if !localRegistered {
		t.Fatal("local baseline provider was not registered")
	}
	return baseline
}

// 存储层创建外部提供商和部署时保留凭据脱敏及库存绑定核对。
func e2eCreateGovernedExternalInventory(t *testing.T, ctx context.Context, governanceStore *modelgovernance.PostgresStore, tenantID string) modelgovernance.Deployment {
	t.Helper()
	createdProvider, err := governanceStore.CreateProvider(ctx, tenantID, "", modelgovernance.ProviderInput{
		Key:           "vendor-b",
		DisplayName:   "Vendor B",
		Kind:          modelgovernance.ProviderExternal,
		AdapterType:   "vendor_b_native",
		CredentialRef: "vault://edugrade/vendor-b",
		Region:        "cn-east",
		DataPolicy:    modelgovernance.DataPolicy{RetentionMode: "no_store"},
		Status:        "unverified",
	})
	if err != nil {
		t.Fatalf("create provider through governance store: %v", err)
	}
	if createdProvider.CredentialRef != "" || !createdProvider.CredentialConfigured || createdProvider.CredentialScheme != "vault" {
		t.Fatalf("provider store did not redact credential reference: %#v", createdProvider)
	}
	createdDeployment, err := governanceStore.CreateDeployment(ctx, tenantID, "", modelgovernance.DeploymentInput{
		ProviderID:        createdProvider.ID,
		Key:               "vendor-b-text-v1",
		ModelName:         "Vendor B Text",
		ModelVersion:      "vendor-b-model-v1",
		Region:            "cn-east",
		CapabilityProfile: "subjective-shadow-v1",
		Modalities:        []string{"text"},
		CapabilityPolicy:  map[string]any{"question_types": []string{"short_answer"}},
		PricingPolicy:     map[string]any{"meter": "token", "input_micros": 1, "output_micros": 2},
		Status:            "unverified",
		HealthState:       "unverified",
	})
	if err != nil {
		t.Fatalf("create deployment through governance store: %v", err)
	}
	if createdDeployment.ProviderKey != createdProvider.Key || createdDeployment.Key != "vendor-b-text-v1" {
		t.Fatalf("unexpected governed deployment: %#v", createdDeployment)
	}
	return createdDeployment
}

// 先按当前策略版本关闭外部路由，再用旧版本更新验证乐观锁冲突。
func e2eCloseGovernanceExternalPolicy(t *testing.T, ctx context.Context, governanceStore *modelgovernance.PostgresStore, tenantID string) {
	t.Helper()
	policy, err := governanceStore.GetPolicy(ctx, tenantID)
	if err != nil {
		t.Fatalf("get tenant model policy through store: %v", err)
	}
	updatedPolicy, err := governanceStore.UpdatePolicy(ctx, tenantID, "", modelgovernance.PolicyUpdateInput{
		DisplayName:        policy.DisplayName,
		Mode:               modelgovernance.ModeLocalOnly,
		AllowedDeployments: []string{},
		FallbackMode:       "manual_only",
		ExpectedVersion:    policy.Version,
		Reason:             "close external route after governance E2E",
	})
	if err != nil {
		t.Fatalf("update tenant model policy through store: %v", err)
	}
	if updatedPolicy.ExternalEnabled || updatedPolicy.Mode != modelgovernance.ModeLocalOnly {
		t.Fatalf("store policy update did not close external routing: %#v", updatedPolicy)
	}
	if _, err := governanceStore.UpdatePolicy(ctx, tenantID, "", modelgovernance.PolicyUpdateInput{
		Mode:               modelgovernance.ModeLocalOnly,
		AllowedDeployments: []string{},
		FallbackMode:       "manual_only",
		ExpectedVersion:    policy.Version,
		Reason:             "stale update must fail",
	}); !errors.Is(err, modelgovernance.ErrConflict) {
		t.Fatalf("stale policy version was not rejected: %v", err)
	}

}

// 离线协议评测冻结本地和外部两个候选；完成后不能追加候选，随后明确作废证据。
func e2eCompleteGovernanceOfflineEvaluation(t *testing.T, ctx context.Context, governanceStore *modelgovernance.PostgresStore, tenantID string, baseline modelgovernance.LocalBaseline, createdDeployment modelgovernance.Deployment) story061EvaluationFixture {
	t.Helper()
	deployments, err := governanceStore.ListDeployments(ctx, tenantID)
	if err != nil {
		t.Fatalf("list deployments for offline evaluation: %v", err)
	}
	var localDeployment modelgovernance.Deployment
	for _, deployment := range deployments {
		if deployment.Key == baseline.DeploymentKey {
			localDeployment = deployment
			break
		}
	}
	if localDeployment.ID == "" {
		t.Fatal("local baseline deployment missing from evaluation inventory")
	}
	evaluationInput := modelgovernance.EvaluationRunInput{
		Key:              "story061-fixture-" + uuid.NewString(),
		DisplayName:      "STORY-061 offline protocol comparison",
		DatasetReference: "fixture-" + uuid.NewString(),
		DatasetSHA256:    strings.Repeat("a", 64),
		EvidenceClass:    modelgovernance.EvaluationEvidenceProtocolFixture,
		Subject:          "数学",
		Grade:            "九年级",
		QuestionType:     "short_answer",
		Modality:         "text",
		SampleCount:      10,
		RepeatCount:      2,
		Reason:           "create offline protocol comparison",
	}
	evaluation, err := governanceStore.CreateEvaluationRun(ctx, tenantID, "", evaluationInput)
	if err != nil {
		t.Fatalf("create offline evaluation run: %v", err)
	}
	candidateInput := func(deploymentID string) modelgovernance.EvaluationCandidateInput {
		return modelgovernance.EvaluationCandidateInput{
			DeploymentID:         deploymentID,
			PromptVersion:        "prompt-v1",
			RubricVersion:        "rubric-v1",
			EvaluatedSamples:     evaluation.SampleCount,
			SeriousErrorSamples:  1,
			EvidenceValidSamples: evaluation.SampleCount - 1,
			RepeatComparisons:    evaluation.SampleCount,
			StableRepeatSamples:  evaluation.SampleCount - 2,
			P95LatencyMS:         120,
			TotalCostMicros:      1000,
			Reason:               "record offline candidate measurement",
		}
	}
	if _, err := governanceStore.AddEvaluationCandidate(
		ctx, tenantID, "", evaluation.ID, candidateInput(localDeployment.ID),
	); err != nil {
		t.Fatalf("add local evaluation candidate: %v", err)
	}
	if _, err := governanceStore.AddEvaluationCandidate(
		ctx, tenantID, "", evaluation.ID, candidateInput(createdDeployment.ID),
	); err != nil {
		t.Fatalf("add external fixture evaluation candidate: %v", err)
	}
	completedEvaluation, err := governanceStore.CompleteEvaluationRun(
		ctx, tenantID, "", evaluation.ID, "freeze offline comparison evidence",
	)
	if err != nil {
		t.Fatalf("complete offline evaluation: %v", err)
	}
	if completedEvaluation.Status != modelgovernance.EvaluationStatusCompleted ||
		len(completedEvaluation.Candidates) != 2 {
		t.Fatalf("unexpected completed evaluation: %#v", completedEvaluation)
	}
	if _, err := governanceStore.AddEvaluationCandidate(
		ctx, tenantID, "", evaluation.ID, candidateInput(createdDeployment.ID),
	); !errors.Is(err, modelgovernance.ErrConflict) {
		t.Fatalf("completed evaluation accepted another candidate: %v", err)
	}
	if _, err := governanceStore.InvalidateEvaluationRun(
		ctx, tenantID, "", evaluation.ID, "fixture evidence superseded",
	); err != nil {
		t.Fatalf("invalidate offline evaluation: %v", err)
	}

	return story061EvaluationFixture{Input: evaluationInput, CandidateInput: candidateInput, LocalDeployment: localDeployment, CreatedDeployment: createdDeployment}
}

// 新建独立评测检查数据库护栏：测量不可变、候选不足不能完成、样本数量不能超出冻结范围。
func e2eAssertGovernanceEvaluationSQLGuards(t *testing.T, ctx context.Context, db *sql.DB, governanceStore *modelgovernance.PostgresStore, tenantID string, evaluation story061EvaluationFixture) modelgovernance.EvaluationRun {
	t.Helper()
	evaluationInput, candidateInput, localDeployment, createdDeployment := evaluation.Input, evaluation.CandidateInput, evaluation.LocalDeployment, evaluation.CreatedDeployment
	guardInput := evaluationInput
	guardInput.Key = "story061-guard-" + uuid.NewString()
	guardInput.DatasetReference = "guard-" + uuid.NewString()
	guardRun, err := governanceStore.CreateEvaluationRun(ctx, tenantID, "", guardInput)
	if err != nil {
		t.Fatalf("create trigger guard evaluation: %v", err)
	}
	guardCandidate, err := governanceStore.AddEvaluationCandidate(
		ctx, tenantID, "", guardRun.ID, candidateInput(localDeployment.ID),
	)
	if err != nil {
		t.Fatalf("add trigger guard local candidate: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
UPDATE model_evaluation_candidate
SET serious_error_samples = 0
WHERE tenant_id = $1 AND id = $2
`, tenantID, guardCandidate.ID); err == nil {
		t.Fatal("database mutated immutable evaluation measurements")
	}
	if _, err := db.ExecContext(ctx, `
UPDATE model_evaluation_run
SET status = 'completed', completed_at = now()
WHERE tenant_id = $1 AND id = $2
`, tenantID, guardRun.ID); err == nil {
		t.Fatal("database completed an evaluation without two candidates")
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO model_evaluation_candidate (
  tenant_id, run_id, deployment_id,
  provider_key, deployment_key, model_version, prompt_version, rubric_version,
  evaluated_samples, teacher_reviewed_samples, teacher_accepted_samples,
  serious_error_samples, evidence_valid_samples,
  repeat_comparisons, stable_repeat_samples, p95_latency_ms, total_cost_micros
)
VALUES (
  $1, $2, $3,
  $4, $5, $6, 'prompt-v1', 'rubric-v1',
  9, 0, 0,
  0, 9,
  9, 8, 100, 0
)
`, tenantID, guardRun.ID, createdDeployment.ID,
		createdDeployment.ProviderKey, createdDeployment.Key, createdDeployment.ModelVersion); err == nil {
		t.Fatal("database accepted evaluation counts outside the frozen run scope")
	}

	return guardRun
}

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
