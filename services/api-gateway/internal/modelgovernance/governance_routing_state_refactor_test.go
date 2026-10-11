package modelgovernance

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestRoutingRefactorPreservesValidationOrderAndProviderOverrides(t *testing.T) {
	provider := Provider{Key: "local", Kind: ProviderLocal, AdapterType: "local_native", Region: "on_premise",
		DataPolicy: DataPolicy{RetentionMode: "no_store"}, Status: "active"}
	deployment := Deployment{Key: "local-model", ProviderKey: provider.Key, ModelVersion: "v1", Region: provider.Region,
		CapabilityProfile: "local-v1", Modalities: []string{"text"}, Status: "shadow_only", HealthState: "available"}
	invalidProvider := provider
	invalidProvider.Key = "invalid key"
	invalidPolicy := DefaultTenantPolicy()
	invalidPolicy.FallbackMode = "invalid"
	cases := []struct {
		name      string
		policy    TenantPolicy
		modality  string
		providers []Provider
		want      error
	}{
		{"policy before provider", invalidPolicy, "text", []Provider{invalidProvider}, ErrInvalidPolicy},
		{"modality before provider", DefaultTenantPolicy(), "audio", []Provider{invalidProvider}, ErrInvalidPolicy},
		{"all providers validated", DefaultTenantPolicy(), "text", []Provider{provider, invalidProvider}, ErrInvalidProvider},
		{"last duplicate provider wins", DefaultTenantPolicy(), "text", []Provider{provider, func() Provider { p := provider; p.Status = "disabled"; return p }()}, ErrNoDeployment},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := SelectDeployment(tt.policy, RouteRequest{Modality: tt.modality}, tt.providers, []Deployment{deployment}); !errors.Is(err, tt.want) {
				t.Fatalf("route error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRoutingRefactorSelectsSortedEligibleDeploymentWithoutMutatingInput(t *testing.T) {
	external := Provider{Key: "a-vendor", Kind: ProviderExternal, AdapterType: "vendor_native", CredentialRef: "vault://vendor/key",
		Region: "cn-east", DataPolicy: DataPolicy{RetentionMode: "no_store"}, Status: "active"}
	local := Provider{Key: "z-local", Kind: ProviderLocal, AdapterType: "local_native", Region: "on_premise",
		DataPolicy: DataPolicy{RetentionMode: "no_store"}, Status: "active"}
	good := Deployment{Key: "b-good", ProviderKey: external.Key, ModelVersion: "v1", Region: external.Region,
		CapabilityProfile: "shadow-v1", Modalities: []string{"text"}, Status: "shadow_only", HealthState: "available"}
	invalid := good
	invalid.Key, invalid.CapabilityProfile = "a-invalid", ""
	localDeployment := good
	localDeployment.Key, localDeployment.ProviderKey = "local-model", local.Key
	deployments := []Deployment{localDeployment, good, invalid}
	before := append([]Deployment(nil), deployments...)
	policy := TenantPolicy{Mode: ModeShadowCompare, ExternalEnabled: true, TextExportEnabled: true,
		AllowedDeployments: []string{good.Key}, FallbackMode: "manual_only"}
	decision, err := SelectDeployment(policy, RouteRequest{Modality: "text"}, []Provider{local, external}, deployments)
	if err != nil || decision.DeploymentKey != good.Key || decision.Reason != "explicit tenant external authorization and deployment allowlist" {
		t.Fatalf("external route = %#v, %v", decision, err)
	}
	policy.TextExportEnabled = false
	decision, err = SelectDeployment(policy, RouteRequest{Modality: "text"}, []Provider{local, external}, deployments)
	if err != nil || decision.DeploymentKey != localDeployment.Key || decision.Reason != "local governed deployment selected without external data transfer" {
		t.Fatalf("local fallback = %#v, %v", decision, err)
	}
	if !reflect.DeepEqual(before, deployments) {
		t.Fatal("routing reordered or modified caller deployments")
	}
}

func TestManagedRoutingRefactorFiltersBeforeChoosingStableIdentity(t *testing.T) {
	const firstID = "11111111-1111-4111-8111-111111111111"
	const secondID = "22222222-2222-4222-8222-222222222222"
	const thirdID = "33333333-3333-4333-8333-333333333333"
	policy := TenantPolicy{TenantID: "school", Mode: ModeCloudSuggestion, ExternalEnabled: true, TextExportEnabled: true,
		AllowedModelConfigIDs: []string{firstID, secondID, thirdID}, FallbackMode: "manual_only"}
	base := ManagedAPIConfig{TenantID: policy.TenantID, ProviderKey: "vendor", ModelName: "same", ModelVersion: "frozen-v1",
		Status: "active", Modalities: []string{"text"}, LastTestStatus: "success",
		LastCapabilityStatus: "success", LastCapabilityVersion: "structured-json-v3"}
	first, second, third := base, base, base
	first.ID, first.LastTestStatus = firstID, "failed"
	second.ID = secondID
	third.ID = thirdID
	configs := []ManagedAPIConfig{third, second, first}
	before := append([]ManagedAPIConfig(nil), configs...)
	decision, err := SelectManagedModel(policy, RouteRequest{Modality: "text"}, configs)
	if err != nil || decision.ModelConfigID != secondID || decision.ModelVersion != base.ModelVersion {
		t.Fatalf("managed route = %#v, %v", decision, err)
	}
	if !reflect.DeepEqual(before, configs) {
		t.Fatal("routing modified caller managed configurations")
	}
}

type routingPolicyReadStore struct {
	*MemoryStore
	configs       []ManagedAPIConfig
	configErr     error
	deploymentErr error
	updateCalls   int
}

func (s *routingPolicyReadStore) ListManagedAPIConfigs(context.Context, string) ([]ManagedAPIConfig, error) {
	return s.configs, s.configErr
}

func (s *routingPolicyReadStore) ListDeployments(ctx context.Context, tenantID string) ([]Deployment, error) {
	if s.deploymentErr != nil {
		return nil, s.deploymentErr
	}
	return s.MemoryStore.ListDeployments(ctx, tenantID)
}

func (s *routingPolicyReadStore) UpdatePolicy(ctx context.Context, tenantID, actorID string, input PolicyUpdateInput) (TenantPolicy, error) {
	s.updateCalls++
	return s.MemoryStore.UpdatePolicy(ctx, tenantID, actorID, input)
}

func TestPolicyRefactorPreservesValidationOrderAndFailureAtomicity(t *testing.T) {
	const modelID = "11111111-1111-4111-8111-111111111111"
	const tenantID = "school"
	managedBody := `{"mode":"cloud_suggestion","external_enabled":true,"text_export_enabled":true,"allowed_model_config_ids":["` + modelID + `"],"fallback_mode":"manual_only","expected_version":99,"reason":"review"}`
	legacyBody := `{"mode":"cloud_suggestion","external_enabled":true,"text_export_enabled":true,"allowed_deployments":["missing"],"fallback_mode":"manual_only","expected_version":99,"reason":"review"}`
	cases := []struct {
		name          string
		body          string
		target        string
		configErr     error
		deploymentErr error
		status        int
		code          string
		updateCalls   int
	}{
		{name: "strict json before tenant", body: `{"unknown":true}`, target: "?tenant_id=other", status: http.StatusBadRequest, code: "invalid_request"},
		{name: "tenant before policy", body: `{}`, target: "?tenant_id=other", status: http.StatusForbidden, code: "tenant_scope_forbidden"},
		{name: "policy before mixed sources", body: strings.Replace(managedBody, `"reason":"review"`, `"reason":"","allowed_deployments":["missing"]`, 1), status: http.StatusBadRequest, code: "invalid_model_policy"},
		{name: "mixed sources before inventory", body: strings.Replace(managedBody, `"reason":"review"`, `"reason":"review","allowed_deployments":["missing"]`, 1), configErr: errors.New("read failed"), status: http.StatusBadRequest, code: "mixed_model_policy_sources"},
		{name: "managed read failure", body: managedBody, configErr: errors.New("read failed"), status: http.StatusInternalServerError, code: "model_config_list_failed"},
		{name: "legacy read failure", body: legacyBody, deploymentErr: errors.New("read failed"), status: http.StatusInternalServerError, code: "model_deployment_list_failed"},
		{name: "managed target before version", body: managedBody, status: http.StatusBadRequest, code: "unknown_model_config"},
		{name: "legacy target before version", body: legacyBody, status: http.StatusBadRequest, code: "unknown_model_deployment"},
		{name: "version after empty sources", body: `{"mode":"local_only","fallback_mode":"manual_only","expected_version":99,"reason":"review"}`, status: http.StatusConflict, code: "model_governance_conflict", updateCalls: 1},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			store := &routingPolicyReadStore{MemoryStore: NewMemoryStore(), configErr: tt.configErr, deploymentErr: tt.deploymentErr}
			before, err := store.GetPolicy(context.Background(), tenantID)
			if err != nil {
				t.Fatal(err)
			}
			audits := auth.NewMemoryStore()
			handler := NewHandler(store, audits, nil, testBaseline())
			response := performHandlerRequest(t, auth.User{ID: "actor", TenantID: tenantID}, http.MethodPut, "/api/v1/model-policy"+tt.target, tt.body, handler.UpdatePolicy)
			if response.Code != tt.status || !strings.Contains(response.Body.String(), `"`+tt.code+`"`) || store.updateCalls != tt.updateCalls {
				t.Fatalf("response = %d %s, writes = %d", response.Code, response.Body.String(), store.updateCalls)
			}
			after, _ := store.GetPolicy(context.Background(), tenantID)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("failed update changed policy: %#v", after)
			}
			records, auditErr := audits.ListAudits(context.Background(), tenantID, auth.AuditFilter{Limit: 10})
			if auditErr != nil || len(records) != 0 {
				t.Fatalf("failed update recorded audit: %#v, %v", records, auditErr)
			}
		})
	}
}

func TestPolicyRefactorKeepsLastDuplicateEligibilityAndDoesNotRequireConnectionTest(t *testing.T) {
	const modelID = "11111111-1111-4111-8111-111111111111"
	valid := ManagedAPIConfig{ID: modelID, TenantID: "school", Status: "active", LastTestStatus: "failed",
		LastCapabilityStatus: "success", LastCapabilityVersion: "structured-json-v3"}
	disabled := valid
	disabled.Status = "disabled"
	body := `{"mode":"cloud_suggestion","external_enabled":true,"text_export_enabled":true,"allowed_model_config_ids":["` + modelID + `"],"fallback_mode":"manual_only","expected_version":1,"reason":"review"}`
	for _, tt := range []struct {
		name    string
		configs []ManagedAPIConfig
		status  int
	}{
		{"last disabled", []ManagedAPIConfig{valid, disabled}, http.StatusBadRequest},
		{"last active", []ManagedAPIConfig{disabled, valid}, http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &routingPolicyReadStore{MemoryStore: NewMemoryStore(), configs: tt.configs}
			store.managedConfigs[modelID] = valid
			handler := NewHandler(store, nil, nil, testBaseline())
			response := performHandlerRequest(t, auth.User{ID: "actor", TenantID: valid.TenantID}, http.MethodPut, "/api/v1/model-policy", body, handler.UpdatePolicy)
			if response.Code != tt.status {
				t.Fatalf("policy response = %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

// 将已有审批改为过期后再创建，保护“必须显式撤销”的重复审批规则；此测试不等待真实时间流逝。
// 过期审批仍占用重复创建身份，必须显式撤销后再建；旧记录不能被失败的创建改写。
func exerciseExpiredSandboxApproval(t *testing.T, managed bool) {
	t.Helper()
	store := NewMemoryStore()
	input := validSandboxApprovalInput("", "", time.Now().UTC())
	if managed {
		input.ModelConfigID = "model"
		store.managedConfigs[input.ModelConfigID] = ManagedAPIConfig{ID: input.ModelConfigID, TenantID: "school", Status: "active", ProviderKey: "aliyun", Region: input.ApprovedRegion, ModelName: "qwen", ModelVersion: "v1"}
	} else {
		provider, deployment := seedSandboxInventory(t, store, "school")
		input.ProviderID, input.DeploymentID = provider.ID, deployment.ID
	}
	approval, err := store.CreateSandboxApproval(context.Background(), "school", "actor", input)
	if err != nil {
		t.Fatal(err)
	}
	approval.ExpiresAt = time.Now().Add(-time.Hour)
	store.approvals[approval.ID] = approval
	if _, err := store.CreateSandboxApproval(context.Background(), "school", "actor", input); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired unrevoked approval = %v", err)
	}
	if len(store.approvals) != 1 || !reflect.DeepEqual(store.approvals[approval.ID], approval) {
		t.Fatal("duplicate failure changed approval history")
	}
	if _, err := store.RevokeSandboxApproval(context.Background(), "school", "actor", approval.ID, "replace expired approval"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSandboxApproval(context.Background(), "school", "actor", input); err != nil || len(store.approvals) != 2 {
		t.Fatalf("replacement after revocation = %v", err)
	}
}

func TestSandboxApprovalRefactorRequiresRevocationEvenAfterExpiry(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "managed"}[managed], func(t *testing.T) {
			exerciseExpiredSandboxApproval(t, managed)
		})
	}
}

func TestSandboxApprovalRefactorSerializesConcurrentDuplicateCreation(t *testing.T) {
	store := NewMemoryStore()
	input := validSandboxApprovalInput("", "", time.Now().UTC())
	input.ModelConfigID = "model"
	store.managedConfigs["model"] = ManagedAPIConfig{ID: "model", TenantID: "school", Status: "active", ProviderKey: "aliyun", Region: input.ApprovedRegion}
	const attempts = 12
	start := make(chan struct{})
	results := make(chan error, attempts)
	var workers sync.WaitGroup
	for i := 0; i < attempts; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := store.CreateSandboxApproval(context.Background(), "school", "actor", input)
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatalf("unexpected concurrent create error: %v", err)
		}
	}
	if successes != 1 || len(store.approvals) != 1 {
		t.Fatalf("concurrent creations = %d, stored approvals = %d", successes, len(store.approvals))
	}
}

func TestEvaluationCandidateRefactorPreservesErrorOrderAndFailureState(t *testing.T) {
	run := EvaluationRun{ID: "run", TenantID: "school", Status: EvaluationStatusDraft, Modality: "text", SampleCount: 10, RepeatCount: 2, EvidenceClass: EvaluationEvidenceProtocolFixture}
	input := validEvaluationCandidateInput("", run)
	input.ModelConfigID = "model"
	cases := []struct {
		name   string
		change func(*MemoryStore, *EvaluationCandidateInput)
		want   error
	}{
		{"tenant before state", func(s *MemoryStore, _ *EvaluationCandidateInput) {
			r := run
			r.TenantID, r.Status = "other", EvaluationStatusCompleted
			s.evaluations[run.ID] = r
		}, ErrNotFound},
		{"state before invalid measurements", func(s *MemoryStore, i *EvaluationCandidateInput) {
			r := run
			r.Status = EvaluationStatusCompleted
			s.evaluations[run.ID] = r
			i.EvaluatedSamples = 0
		}, ErrConflict},
		{"measurements before inventory", func(s *MemoryStore, i *EvaluationCandidateInput) {
			delete(s.managedConfigs, "model")
			i.EvaluatedSamples = 0
		}, ErrInvalidEvaluation},
		{"inventory before duplicate", func(s *MemoryStore, _ *EvaluationCandidateInput) { delete(s.managedConfigs, "model") }, ErrNotFound},
		{"managed image not supported", func(s *MemoryStore, _ *EvaluationCandidateInput) {
			r := run
			r.Modality = "image"
			s.evaluations[run.ID] = r
		}, ErrNotFound},
		{"valid duplicate", func(*MemoryStore, *EvaluationCandidateInput) {}, ErrConflict},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStore()
			store.evaluations[run.ID] = run
			store.managedConfigs["model"] = ManagedAPIConfig{ID: "model", TenantID: "school", Status: "active", Modalities: []string{"text", "image"}, LastCapabilityStatus: "success", LastCapabilityVersion: "structured-json-v3"}
			candidate := EvaluationCandidate{ID: "candidate", RunID: run.ID, TenantID: run.TenantID, ModelConfigID: "model", ModelVersion: "frozen-v1"}
			store.candidates[candidate.ID] = candidate
			request := input
			tt.change(store, &request)
			if _, err := store.AddEvaluationCandidate(context.Background(), "school", "actor", run.ID, request); !errors.Is(err, tt.want) {
				t.Fatalf("candidate error = %v, want %v", err, tt.want)
			}
			if len(store.candidates) != 1 || !reflect.DeepEqual(store.candidates[candidate.ID], candidate) {
				t.Fatal("failed candidate addition changed frozen candidate")
			}
		})
	}
}

func routingApprovalFixture() (*MemoryStore, EvaluationRun, EvaluationCandidate, ModelApprovalInput) {
	store := NewMemoryStore()
	run := EvaluationRun{ID: "run", TenantID: "school", Status: EvaluationStatusCompleted, EvidenceClass: EvaluationEvidenceAuthorizedFrozenSet,
		DatasetReference: "dataset", DatasetSHA256: strings.Repeat("a", 64), AuthorizationRef: "dataset-approval", Subject: "math", Grade: "grade-9", QuestionType: "short_answer", Modality: "text"}
	candidate := EvaluationCandidate{ID: "candidate", TenantID: run.TenantID, RunID: run.ID, ModelConfigID: "model", ProviderKey: "aliyun",
		ModelName: "qwen", ModelVersion: "frozen-v1", PromptVersion: "prompt-v1", RubricVersion: "rubric-v1"}
	store.evaluations[run.ID], store.candidates[candidate.ID] = run, candidate
	input := ModelApprovalInput{EvaluationRunID: run.ID, ModelConfigID: candidate.ModelConfigID, ManualReviewRate: 0.25,
		DecisionReference: "decision", ExpiresAt: time.Now().UTC().Add(24 * time.Hour), Reason: "approve frozen evidence"}
	return store, run, candidate, input
}

func TestModelApprovalRefactorPreservesEvidenceAndCandidateErrorOrder(t *testing.T) {
	cases := []struct {
		name   string
		change func(*MemoryStore, *ModelApprovalInput)
		want   error
	}{
		{"invalid input before missing run", func(s *MemoryStore, i *ModelApprovalInput) { delete(s.evaluations, "run"); i.Reason = "" }, ErrInvalidPromotion},
		{"tenant before evidence", func(s *MemoryStore, _ *ModelApprovalInput) {
			r := s.evaluations["run"]
			r.TenantID, r.Status = "other", EvaluationStatusDraft
			s.evaluations[r.ID] = r
		}, ErrNotFound},
		{"draft before missing candidate", func(s *MemoryStore, _ *ModelApprovalInput) {
			r := s.evaluations["run"]
			r.Status = EvaluationStatusDraft
			s.evaluations[r.ID] = r
			delete(s.candidates, "candidate")
		}, ErrInvalidPromotion},
		{"fixture evidence before candidate", func(s *MemoryStore, _ *ModelApprovalInput) {
			r := s.evaluations["run"]
			r.EvidenceClass = EvaluationEvidenceProtocolFixture
			s.evaluations[r.ID] = r
			delete(s.candidates, "candidate")
		}, ErrInvalidPromotion},
		{"candidate before duplicate decision", func(s *MemoryStore, _ *ModelApprovalInput) { delete(s.candidates, "candidate") }, ErrNotFound},
		{"valid duplicate decision", func(*MemoryStore, *ModelApprovalInput) {}, ErrConflict},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			store, _, _, input := routingApprovalFixture()
			existing := ModelApproval{ID: "existing", TenantID: "school", DecisionReference: input.DecisionReference}
			store.modelApprovals[existing.ID] = existing
			tt.change(store, &input)
			if _, err := store.CreateModelApproval(context.Background(), "school", "actor", input); !errors.Is(err, tt.want) {
				t.Fatalf("approval error = %v, want %v", err, tt.want)
			}
			if len(store.modelApprovals) != 1 || !reflect.DeepEqual(store.modelApprovals[existing.ID], existing) {
				t.Fatal("failed promotion changed approval history")
			}
		})
	}
}

// 直接构造已冻结评测，再更换当前库存，专门保护审批读冻结证据的边界，不模拟完整评测集成。
func TestModelApprovalRefactorRetainsFrozenVersionsAndDecisionHistory(t *testing.T) {
	store, run, candidate, input := routingApprovalFixture()
	store.managedConfigs[candidate.ModelConfigID] = ManagedAPIConfig{ID: candidate.ModelConfigID, TenantID: run.TenantID, Status: "disabled", ModelVersion: "current-v2"}
	approval, err := store.CreateModelApproval(context.Background(), run.TenantID, "actor", input)
	if err != nil {
		t.Fatal(err)
	}
	if approval.EvaluationCandidateID != candidate.ID || approval.ModelVersion != candidate.ModelVersion ||
		approval.PromptVersion != candidate.PromptVersion || approval.RubricVersion != candidate.RubricVersion ||
		approval.DatasetSHA256 != run.DatasetSHA256 || approval.AuthorizationRef != run.AuthorizationRef || approval.Revision != 1 {
		t.Fatalf("promotion did not retain frozen evidence: %#v", approval)
	}
	input.DecisionReference = "another-decision"
	if _, err := store.CreateModelApproval(context.Background(), run.TenantID, "actor", input); !errors.Is(err, ErrConflict) {
		t.Fatalf("same active scope with another decision = %v", err)
	}
	approval.ExpiresAt = time.Now().Add(-time.Hour)
	store.modelApprovals[approval.ID] = approval
	input.DecisionReference = "decision"
	if _, err := store.CreateModelApproval(context.Background(), run.TenantID, "actor", input); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired decision reference reused = %v", err)
	}
	input.DecisionReference = "another-decision"
	replacement, err := store.CreateModelApproval(context.Background(), run.TenantID, "actor", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RevokeModelApproval(context.Background(), run.TenantID, "actor", replacement.ID, ModelApprovalRevokeInput{ExpectedRevision: 1, Reason: "withdraw"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateModelApproval(context.Background(), run.TenantID, "actor", input); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoked decision reference reused = %v", err)
	}
	input.DecisionReference = "third-decision"
	if _, err := store.CreateModelApproval(context.Background(), run.TenantID, "actor", input); err != nil || len(store.modelApprovals) != 3 {
		t.Fatalf("new decision after revocation = %v", err)
	}
}

// 原实现查重使用未裁剪的请求值，写入才裁剪；本批重构保留此行为，编号归一化缺陷另行报告。
func TestModelApprovalRefactorPreservesRawDecisionReferenceComparison(t *testing.T) {
	store, run, candidate, input := routingApprovalFixture()
	store.modelApprovals["expired"] = ModelApproval{
		ID: "expired", TenantID: run.TenantID, ModelConfigID: candidate.ModelConfigID,
		ModelVersion: candidate.ModelVersion, PromptVersion: candidate.PromptVersion, RubricVersion: candidate.RubricVersion,
		Subject: run.Subject, Grade: run.Grade, QuestionType: run.QuestionType, Modality: run.Modality,
		DecisionReference: input.DecisionReference, ExpiresAt: time.Now().Add(-time.Hour),
	}
	input.DecisionReference = " " + input.DecisionReference + " "
	approval, err := store.CreateModelApproval(context.Background(), run.TenantID, "actor", input)
	if err != nil || approval.DecisionReference != "decision" || len(store.modelApprovals) != 2 {
		t.Fatalf("raw decision comparison changed: %#v, %v", approval, err)
	}
}
