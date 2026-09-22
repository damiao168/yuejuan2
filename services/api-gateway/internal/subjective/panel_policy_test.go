package subjective

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
)

func TestPanelPolicyApprovalRequiresCompletedAlignedShadowEvidence(t *testing.T) {
	ctx := context.Background()
	evaluations := gradingevaluation.NewMemoryStore()
	run, err := evaluations.CreateRun(ctx, "tenant-1", "actor-1", gradingevaluation.CreateRunInput{Key: "senior-math-shadow", ModelReference: policyTestModelSetReference()})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range readyPolicyObservations() {
		if _, err := evaluations.AddPanelObservation(ctx, "tenant-1", run.ID, item); err != nil {
			t.Fatal(err)
		}
	}
	store := NewMemoryStore()
	service := NewPanelPolicyService(store, evaluations)
	policy, err := service.Create(ctx, "tenant-1", "actor-1", readyPanelPolicyInput("senior-panel-math-v1"))
	if err != nil {
		t.Fatal(err)
	}
	if policy.Status != PanelPolicyShadow || policy.SubjectCode != assessment.SubjectMathematics || policy.EducationStage != assessment.StageSenior {
		t.Fatalf("policy scope was not normalized into shadow: %#v", policy)
	}
	if _, _, err := service.EvaluateAndApprove(ctx, "tenant-1", "actor-1", policy.ID, run.ID); !errors.Is(err, ErrPanelPolicyNotReady) {
		t.Fatalf("draft evaluation was accepted: %v", err)
	}
	if _, _, err := gradingevaluation.NewService(evaluations).CompletePanel(ctx, "tenant-1", run.ID); err != nil {
		t.Fatal(err)
	}
	approved, report, err := service.EvaluateAndApprove(ctx, "tenant-1", "actor-1", policy.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || approved.Status != PanelPolicyApproved || approved.EvaluationRunID != run.ID || approved.ReadinessReport == nil || !approved.ReadinessReport.Ready {
		t.Fatalf("ready policy was not approved with frozen evidence: policy=%#v report=%#v", approved, report)
	}
	resolved, err := service.FindApproved(ctx, "tenant-1", "senior", "数学", "structured_steps")
	if err != nil || resolved.ID != approved.ID {
		t.Fatalf("approved canonical scope did not resolve: %#v err=%v", resolved, err)
	}
	a := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .9}
	b := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .9}
	c := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .99}
	orchestrator := newTestPanelOrchestrator(t, a, b, c).WithApprovedPolicyStore(store)
	gradingContext := panelTestContext()
	gradingContext.AssessmentSnapshot.EducationStage = assessment.StageSenior
	gradingContext.AssessmentSnapshot.SubjectCode = assessment.SubjectMathematics
	gradingContext.AssessmentSnapshot.ArchetypeCode = "structured_steps"
	if _, err := orchestrator.Grade(ctx, "tenant-1", "actor-1", gradingContext, PanelDecisionConfig{}); !errors.Is(err, ErrPanelPolicyRequired) {
		t.Fatalf("approved-only orchestrator accepted an unapproved explicit config: %v", err)
	}
	result, err := orchestrator.GradeWithApprovedPolicy(ctx, "tenant-1", "actor-1", gradingContext)
	if err != nil || result.Panel.Status != PanelResolved || result.Panel.PolicyVersion != approved.PolicyVersion {
		t.Fatalf("approved thresholds were not used by the governed entry point: %#v err=%v", result.Panel, err)
	}
	orchestrator.agents.PrimaryB.Policy.ModelVersion = "changed-model-b"
	if _, err := orchestrator.GradeWithApprovedPolicy(ctx, "tenant-1", "actor-1", gradingContext); !errors.Is(err, ErrPanelPolicyRequired) {
		t.Fatalf("changed model set reused old policy: %v", err)
	}
	invalidated, err := service.Invalidate(ctx, "tenant-1", "actor-1", policy.ID, "new calibration required")
	if err != nil || invalidated.Status != PanelPolicyInvalidated {
		t.Fatalf("approved policy was not invalidated: %#v err=%v", invalidated, err)
	}
	if _, err := service.FindApproved(ctx, "tenant-1", "senior", "mathematics", "structured_steps"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalidated policy remained active: %v", err)
	}
	if _, err := orchestrator.GradeWithApprovedPolicy(ctx, "tenant-1", "actor-1", gradingContext); !errors.Is(err, ErrPanelPolicyRequired) {
		t.Fatalf("invalidated policy remained runnable: %v", err)
	}
}

func TestPanelPolicyApprovalFailsClosedForWeakOrMismatchedEvidence(t *testing.T) {
	ctx := context.Background()
	evaluations := gradingevaluation.NewMemoryStore()
	run, _ := evaluations.CreateRun(ctx, "tenant-1", "actor-1", gradingevaluation.CreateRunInput{Key: "weak-shadow", ModelReference: policyTestModelSetReference()})
	items := readyPolicyObservations()[:2]
	for _, item := range items {
		if _, err := evaluations.AddPanelObservation(ctx, "tenant-1", run.ID, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := gradingevaluation.NewService(evaluations).CompletePanel(ctx, "tenant-1", run.ID); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	service := NewPanelPolicyService(store, evaluations)
	policy, err := service.Create(ctx, "tenant-1", "actor-1", readyPanelPolicyInput("senior-panel-math-v2"))
	if err != nil {
		t.Fatal(err)
	}
	unchanged, report, err := service.EvaluateAndApprove(ctx, "tenant-1", "actor-1", policy.ID, run.ID)
	if !errors.Is(err, ErrPanelPolicyNotReady) || report.Ready || unchanged.Status != PanelPolicyShadow || len(report.Reasons) == 0 {
		t.Fatalf("weak slice did not fail closed: policy=%#v report=%#v err=%v", unchanged, report, err)
	}

	bad := readyPanelPolicyInput("policy-v3")
	bad.ReadinessPolicy.PolicyVersion = "different-version"
	if _, err := service.Create(ctx, "tenant-1", "actor-1", bad); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("mismatched threshold provenance was accepted: %v", err)
	}
	bad = readyPanelPolicyInput("policy-v4")
	bad.ModelSetReference = ""
	if _, err := service.Create(ctx, "tenant-1", "actor-1", bad); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unbound model set was accepted: %v", err)
	}
	wrongStage := readyPolicyObservations()
	for index := range wrongStage {
		wrongStage[index].EducationStage = "junior"
	}
	if _, err := gradingevaluation.AssessPanelSliceShadowReadiness("senior", "mathematics", "structured_steps", wrongStage, bad.ReadinessPolicy); !errors.Is(err, gradingevaluation.ErrInvalidInput) {
		t.Fatalf("cross-stage evidence was accepted: %v", err)
	}
}

func TestPanelPolicyApprovalRejectsDifferentEvaluatedModelSet(t *testing.T) {
	ctx := context.Background()
	evaluations := gradingevaluation.NewMemoryStore()
	run, err := evaluations.CreateRun(ctx, "tenant-1", "actor-1", gradingevaluation.CreateRunInput{
		Key: "wrong-model-shadow", ModelReference: "panel:" + strings.Repeat("f", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range readyPolicyObservations() {
		if _, err := evaluations.AddPanelObservation(ctx, "tenant-1", run.ID, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := gradingevaluation.NewService(evaluations).CompletePanel(ctx, "tenant-1", run.ID); err != nil {
		t.Fatal(err)
	}
	service := NewPanelPolicyService(NewMemoryStore(), evaluations)
	policy, err := service.Create(ctx, "tenant-1", "actor-1", readyPanelPolicyInput("different-model-v1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.EvaluateAndApprove(ctx, "tenant-1", "actor-1", policy.ID, run.ID); !errors.Is(err, ErrPanelPolicyNotReady) {
		t.Fatalf("different evaluated models approved policy: %v", err)
	}
}

func TestPanelModelSetMigrationRequiresMatchingEvaluationAndImmutableIdentity(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000160_subjective_panel_model_set_provenance.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"ADD COLUMN model_set_reference TEXT", "trg_subjective_panel_model_set_guard",
		"NEW.model_set_reference IS DISTINCT FROM OLD.model_set_reference",
		"evaluated_model_set IS DISTINCT FROM NEW.model_set_reference",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("model provenance migration missing %q", required)
		}
	}
}

func TestPanelPolicyJSONContractKeepsFrozenThresholdAndEvidenceIdentity(t *testing.T) {
	input := readyPanelPolicyInput("contract-v1")
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	decision, decisionOK := decoded["decision_config"].(map[string]any)
	readiness, readinessOK := decoded["readiness_policy"].(map[string]any)
	if !decisionOK || !readinessOK || decision["policy_version"] != "contract-v1" || readiness["policy_version"] != "contract-v1" ||
		decision["score_gap_threshold"] != .15 || readiness["min_arbitrations_per_slice"] != float64(2) {
		t.Fatalf("panel policy JSON contract drifted: %s", encoded)
	}
	if _, found := decision["model_final_score"]; found {
		t.Fatal("policy contract grants model final-score authority")
	}
}

func TestPanelPolicyMigrationFreezesThresholdsAndRequiresReadyEvidence(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000158_subjective_panel_policy.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"ADD COLUMN education_stage TEXT", "CREATE TABLE subjective_panel_policy", "decision_config JSONB NOT NULL",
		"readiness_policy JSONB NOT NULL", "uq_subjective_panel_policy_approved_scope", "subjective_panel_policy_transition_guard",
		"evaluation_status<>'completed'", "NEW.readiness_report->'ready'<>'true'::jsonb", "grading_evaluation_approved_policy_guard",
		"ENABLE ROW LEVEL SECURITY",
		"CHECK (education_stage IS NOT NULL AND education_stage IN ('junior','senior')) NOT VALID",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("panel policy migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"answer_text", "student_id", "model_final_score"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("panel policy migration leaks or grants authority through %q", forbidden)
		}
	}
}

func readyPanelPolicyInput(version string) CreatePanelPolicyInput {
	return CreatePanelPolicyInput{
		PolicyVersion: version, ModelSetReference: policyTestModelSetReference(), EducationStage: "senior", SubjectCode: "math", ArchetypeCode: "structured_steps",
		DecisionConfig: PanelDecisionConfig{
			PolicyVersion: version, ScoreGapThreshold: .15, CriterionGapThreshold: .2, ConfidenceGapThreshold: .25,
			ArbiterMinConfidence: .85, ArbiterAgreementThreshold: .8, TriggerAnyCriterionConflict: true,
			TriggerEvidenceConflict: true, HardRiskCodes: []string{"prompt_injection_suspected"},
		},
		ReadinessPolicy: gradingevaluation.PanelReadinessPolicy{
			PolicyVersion: version, MinSamplesPerSlice: 4, MinArbitrationsPerSlice: 2,
			MaxFalseConsensusRate: 0, MaxArbitrationErrorRate: 0, MaxResolvedMAE: 0, MinResolvedQWK: .99,
			MinArbiterMAEImprovement: .5, MinResolvedCoverageRate: 1, MaxHumanEscalationRate: 0,
			MaxArbitrationTriggerRate: .5, MaxCostPer1000AnswersMicros: 0,
		},
	}
}

func policyTestModelSetReference() string {
	policy := func(model string) ModelPolicy {
		return ModelPolicy{ModelVersion: model, PromptVersion: "panel-prompt-v1", MinConfidence: .8}
	}
	return PanelModelSetReference(PanelAgents{
		PrimaryA: PanelAgentBinding{Policy: policy("model-a")},
		PrimaryB: PanelAgentBinding{Policy: policy("model-b")},
		Arbiter:  PanelAgentBinding{Policy: policy("model-c-strong")},
	})
}

func readyPolicyObservations() []gradingevaluation.PanelObservation {
	values := []gradingevaluation.PanelObservation{}
	for index, score := range []float64{0, 1} {
		resolved := score
		values = append(values, gradingevaluation.PanelObservation{
			ResponseKey: "consensus-" + string(rune('a'+index)), ResponseFingerprint: strings.Repeat(string(rune('a'+index)), 64),
			EducationStage: "senior", Subject: "mathematics", Archetype: "structured_steps",
			ReferenceKind: gradingevaluation.ReferenceHumanAdjudicated, ReferenceScore: score, MaxScore: 3, ScoreA: score, ScoreB: score,
			ResolvedScore: &resolved, ResolutionSource: ResolutionPrimaryConsensus, ReferenceReviewers: 2, ReferenceAdjudicated: true,
		})
	}
	for index, score := range []float64{2, 3} {
		arbiter, resolved := score, score
		values = append(values, gradingevaluation.PanelObservation{
			ResponseKey: "arbiter-" + string(rune('a'+index)), ResponseFingerprint: strings.Repeat(string(rune('c'+index)), 64),
			EducationStage: "senior", Subject: "mathematics", Archetype: "structured_steps",
			ReferenceKind: gradingevaluation.ReferenceHumanAdjudicated, ReferenceScore: score, MaxScore: 3, ScoreA: score - 2, ScoreB: score - 1,
			ScoreC: &arbiter, ResolvedScore: &resolved, ArbitrationTriggered: true, ResolutionSource: ResolutionArbiter,
			ReferenceReviewers: 2, ReferenceAdjudicated: true,
		})
	}
	return values
}
