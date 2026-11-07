package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"errors"
	"github.com/google/uuid"
	"strings"
	"testing"
)

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
