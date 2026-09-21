package subjective

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type panelTestAdapter struct {
	mu         sync.Mutex
	inputs     []AdapterInput
	supported  map[string]bool
	confidence float64
	pointScore float64
	riskFlags  []string
	err        error
	started    chan<- struct{}
	release    <-chan struct{}
}

type panelMathTestAdapter struct {
	inputs []AdapterInput
}

func (a *panelMathTestAdapter) Name() string              { return "panel-math-v2-test" }
func (a *panelMathTestAdapter) SupportsPanelMathV2() bool { return true }
func (a *panelMathTestAdapter) Grade(_ context.Context, input AdapterInput) (AdapterOutput, error) {
	a.inputs = append(a.inputs, input)
	return AdapterOutput{
		SchemaVersion: gradingAgentV2BuilderSchemaVersion, RequestID: input.RequestID,
		DeliveryMode: "teacher_suggestion", NeedsHumanReview: true, SuggestedScore: 999,
		ModelVersion: input.ModelPolicy.ModelVersion, PromptVersion: input.ModelPolicy.PromptVersion,
		RubricVersion: input.Rubric.Version, CapabilityProfile: "panel-math-v2-test",
		MathCandidates: []MathCriterionCandidate{{RubricPointID: "p1", Status: "supported", EvidenceIDs: []string{"step-1"}, Confidence: .99, ReasonCode: "semantic_alignment"}},
	}, nil
}

func (a *panelTestAdapter) Name() string { return "panel-test" }
func (a *panelTestAdapter) Grade(_ context.Context, input AdapterInput) (AdapterOutput, error) {
	if a.started != nil {
		a.started <- struct{}{}
		<-a.release
	}
	a.mu.Lock()
	a.inputs = append(a.inputs, input)
	a.mu.Unlock()
	if a.err != nil {
		return AdapterOutput{}, a.err
	}
	matched, missing, evidence := []grading.PointResult{}, []grading.PointResult{}, []grading.Evidence{}
	for _, point := range input.Rubric.Points {
		if a.supported[point.ID] {
			evidenceID := "e-" + point.ID
			modelScore := point.Score
			if a.pointScore > 0 {
				modelScore = a.pointScore
			}
			matched = append(matched, grading.PointResult{Code: point.ID, Label: point.Description, Score: modelScore, EvidenceIDs: []string{evidenceID}})
			excerpt := "alpha"
			if point.ID == "p2" {
				excerpt = "beta"
			}
			evidence = append(evidence, grading.Evidence{Type: "text", EvidenceID: evidenceID, RubricPointID: point.ID, AnswerText: excerpt, Location: "answer_text", Confidence: .9})
		} else {
			missing = append(missing, grading.PointResult{Code: point.ID, Label: point.Description, Reason: "unsupported"})
		}
	}
	return AdapterOutput{
		RequestID: input.RequestID, SuggestedScore: 999, Confidence: a.confidence,
		MatchedPoints: matched, MissingPoints: missing, Evidence: evidence, RiskFlags: cloneStrings(a.riskFlags),
		NeedsHumanReview: true, StudentFeedback: "review", TeacherNote: "review",
		ModelVersion: input.ModelPolicy.ModelVersion, PromptVersion: input.ModelPolicy.PromptVersion,
		RubricVersion: input.Rubric.Version, DeliveryMode: "teacher_suggestion", CapabilityProfile: "panel-shadow-v1",
		Telemetry: AdapterTelemetry{Adapter: "panel-test", Provider: "test", Deployment: input.ModelPolicy.ModelVersion, Region: "local", Attempts: 1, PriorErrorCodes: []string{}},
		RawOutput: map[string]any{"criterion_only": true},
	}, nil
}

func TestPanelRunsPrimariesConcurrentlyAndResolvesExactConsensusWithoutArbiter(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	a := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .9, started: started, release: release}
	b := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .88, started: started, release: release}
	c := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .99}
	go func() {
		<-started
		<-started
		close(release)
	}()
	orchestrator := newTestPanelOrchestrator(t, a, b, c)
	result, err := orchestrator.Grade(context.Background(), "tenant-1", "actor-1", panelTestContext(), PanelDecisionConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Panel.Status != PanelResolved || result.Panel.ResolutionSource != ResolutionPrimaryConsensus || result.Panel.ResolvedScore == nil || *result.Panel.ResolvedScore != 2 {
		t.Fatalf("unexpected consensus result: %#v", result.Panel)
	}
	if len(c.inputs) != 0 {
		t.Fatal("arbiter must not run when blind primaries agree")
	}
	assertPanelRunRoles(t, orchestrator.store.(*MemoryStore), result.Panel.ID, AgentRolePrimaryA, AgentRolePrimaryB)
}

func TestPanelConcurrentRetryDoesNotDuplicatePrimaryModelCalls(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	a := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .9, started: started, release: release}
	b := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .9, started: started, release: release}
	c := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .99}
	orchestrator := newTestPanelOrchestrator(t, a, b, c)
	type outcome struct {
		result PanelResult
		err    error
	}
	firstDone := make(chan outcome, 1)
	go func() {
		result, err := orchestrator.Grade(context.Background(), "tenant-1", "actor-1", panelTestContext(), PanelDecisionConfig{})
		firstDone <- outcome{result: result, err: err}
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("both primary roles did not start concurrently")
		}
	}
	second, err := orchestrator.Grade(context.Background(), "tenant-1", "actor-1", panelTestContext(), PanelDecisionConfig{})
	if !errors.Is(err, ErrPanelRunInProgress) || second.Panel.ID == "" {
		t.Fatalf("concurrent replay did not report an in-flight panel: %#v err=%v", second.Panel, err)
	}
	close(release)
	first := <-firstDone
	if first.err != nil || first.result.Panel.Status != PanelResolved {
		t.Fatalf("owning execution did not finish: %#v err=%v", first.result.Panel, first.err)
	}
	for name, adapter := range map[string]*panelTestAdapter{"A": a, "B": b} {
		adapter.mu.Lock()
		calls := len(adapter.inputs)
		adapter.mu.Unlock()
		if calls != 1 {
			t.Fatalf("primary %s was called %d times for one panel role", name, calls)
		}
	}
	replay, err := orchestrator.Grade(context.Background(), "tenant-1", "actor-1", panelTestContext(), PanelDecisionConfig{})
	if err != nil || replay.Panel.ID != first.result.Panel.ID || replay.Panel.Status != PanelResolved {
		t.Fatalf("completed panel replay was not stable: %#v err=%v", replay.Panel, err)
	}
}

func TestPanelUsesBlindStrongerArbiterAndServerDerivedScore(t *testing.T) {
	a := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .9}
	b := &panelTestAdapter{supported: map[string]bool{"p2": true}, confidence: .9}
	c := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .96, pointScore: .1}
	orchestrator := newTestPanelOrchestrator(t, a, b, c)
	config := PanelDecisionConfig{ArbiterAgreementThreshold: .5}
	result, err := orchestrator.Grade(context.Background(), "tenant-1", "actor-1", panelTestContext(), config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Panel.ResolutionSource != ResolutionArbiter || result.Panel.ResolvedScore == nil || *result.Panel.ResolvedScore != 2 || result.Arbiter == nil {
		t.Fatalf("unexpected arbitration result: %#v", result)
	}
	if result.Arbiter.SuggestedScore != 2 {
		t.Fatal("server did not replace the model-authored total with frozen-rubric scoring")
	}
	if result.Arbiter.MatchedPoints[0].Score != 2 {
		t.Fatal("server did not replace the model-authored rubric point score")
	}
	if len(c.inputs) != 1 || c.inputs[0].RequestID == a.inputs[0].RequestID || c.inputs[0].RequestID == b.inputs[0].RequestID {
		t.Fatalf("arbiter did not receive an independent request: %#v", c.inputs)
	}
	if a.inputs[0].AgentRole != AgentRolePrimaryA || b.inputs[0].AgentRole != AgentRolePrimaryB || c.inputs[0].AgentRole != AgentRoleArbiter {
		t.Fatalf("panel role isolation was lost: A=%q B=%q C=%q", a.inputs[0].AgentRole, b.inputs[0].AgentRole, c.inputs[0].AgentRole)
	}
	if c.inputs[0].OutputConstraint.AllowModelFinalScore || c.inputs[0].OutputConstraint.FinalScoreAuthority != "server_rubric_and_deterministic_rule" {
		t.Fatalf("arbiter was granted score authority: %#v", c.inputs[0].OutputConstraint)
	}
	assertPanelRunRoles(t, orchestrator.store.(*MemoryStore), result.Panel.ID, AgentRolePrimaryA, AgentRolePrimaryB, AgentRoleArbiter)
}

func TestPanelRoutesUncertainArbiterToHumanReview(t *testing.T) {
	a := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .9}
	b := &panelTestAdapter{supported: map[string]bool{"p2": true}, confidence: .9}
	c := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .3}
	orchestrator := newTestPanelOrchestrator(t, a, b, c)
	result, err := orchestrator.Grade(context.Background(), "tenant-1", "actor-1", panelTestContext(), PanelDecisionConfig{ArbiterAgreementThreshold: .5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Panel.Status != PanelHumanReview || result.Panel.ResolutionSource != ResolutionHuman || result.Panel.ReviewTaskID == "" || result.Panel.ResolvedScore != nil {
		t.Fatalf("uncertain arbitration was not routed safely: %#v", result.Panel)
	}
}

func TestPanelRoutesPrimaryFailureToHumanWithoutCallingArbiter(t *testing.T) {
	a := &panelTestAdapter{err: errors.New("unavailable"), supported: map[string]bool{}}
	b := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .9}
	c := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .99}
	orchestrator := newTestPanelOrchestrator(t, a, b, c)
	result, err := orchestrator.Grade(context.Background(), "tenant-1", "actor-1", panelTestContext(), PanelDecisionConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Panel.Status != PanelHumanReview || result.Panel.ReviewTaskID == "" || len(c.inputs) != 0 || !containsString(result.Panel.TriggerCodes, "primary_agent_failure") {
		t.Fatalf("primary failure was not routed to human review: %#v", result.Panel)
	}
}

func TestPanelMathV2UsesVerifiedCropAndFrozenRuleScore(t *testing.T) {
	handler, store, _, value := newMathHandlerFixture(t, false)
	a, b, c := &panelMathTestAdapter{}, &panelMathTestAdapter{}, &panelMathTestAdapter{}
	agents := PanelAgents{
		PrimaryA: PanelAgentBinding{Adapter: a, Policy: ModelPolicy{ModelVersion: "math-a", PromptVersion: "panel-prompt-v1", MinConfidence: .8}, StrengthRank: 1},
		PrimaryB: PanelAgentBinding{Adapter: b, Policy: ModelPolicy{ModelVersion: "math-b", PromptVersion: "panel-prompt-v1", MinConfidence: .8}, StrengthRank: 1},
		Arbiter:  PanelAgentBinding{Adapter: c, Policy: ModelPolicy{ModelVersion: "math-c", PromptVersion: "panel-prompt-v1", MinConfidence: .8}, StrengthRank: 2},
	}
	panel, err := NewPanelOrchestrator(store, agents, allowPanelAdmission)
	if err != nil {
		t.Fatal(err)
	}
	panel.math = &panelMathRuntime{prepare: handler.prepareMathEvidence, input: handler.buildPanelMathInput, settle: handler.settleMathOutput}
	result, err := panel.Grade(context.Background(), "tenant-a", "actor-1", value, PanelDecisionConfig{})
	if err != nil || result.Panel.Status != PanelResolved || result.Panel.ResolvedScore == nil || *result.Panel.ResolvedScore != 2 {
		t.Fatalf("math panel did not use frozen scoring: %#v err=%v", result.Panel, err)
	}
	if len(a.inputs) != 1 || len(b.inputs) != 1 || len(c.inputs) != 0 ||
		a.inputs[0].ActiveCrop == nil || b.inputs[0].ActiveCrop == nil ||
		a.inputs[0].AgentRole != AgentRolePrimaryA || b.inputs[0].AgentRole != AgentRolePrimaryB ||
		a.inputs[0].MathEvidence == nil || b.inputs[0].MathEvidence == nil ||
		result.PrimaryA.SuggestedScore != 2 || result.PrimaryB.SuggestedScore != 2 {
		t.Fatalf("math v2 blind input or server score was lost: A=%#v B=%#v panel=%#v", a.inputs, b.inputs, result.Panel)
	}
}

func TestPanelMathV2WithoutRoleCapableAdapterRoutesHuman(t *testing.T) {
	handler, store, _, value := newMathHandlerFixture(t, false)
	a, b, c := &panelTestAdapter{}, &panelTestAdapter{}, &panelTestAdapter{}
	panel := newTestPanelOrchestrator(t, a, b, c)
	panel.store = store
	panel.math = &panelMathRuntime{prepare: handler.prepareMathEvidence, input: handler.buildPanelMathInput, settle: handler.settleMathOutput}
	result, err := panel.Grade(context.Background(), "tenant-a", "actor-1", value, PanelDecisionConfig{})
	if err != nil || result.Panel.Status != PanelHumanReview || len(a.inputs) != 0 || len(b.inputs) != 0 {
		t.Fatalf("math v1 fallback was not blocked: %#v err=%v", result.Panel, err)
	}
}

func newTestPanelOrchestrator(t *testing.T, a, b, c LLMGradingAdapter) *PanelOrchestrator {
	t.Helper()
	policy := func(model string) ModelPolicy {
		return ModelPolicy{ModelVersion: model, PromptVersion: "panel-prompt-v1", MinConfidence: .8}
	}
	orchestrator, err := NewPanelOrchestrator(NewMemoryStore(), PanelAgents{
		PrimaryA: PanelAgentBinding{Adapter: a, Policy: policy("model-a"), StrengthRank: 1},
		PrimaryB: PanelAgentBinding{Adapter: b, Policy: policy("model-b"), StrengthRank: 1},
		Arbiter:  PanelAgentBinding{Adapter: c, Policy: policy("model-c-strong"), StrengthRank: 2},
	}, allowPanelAdmission)
	if err != nil {
		t.Fatal(err)
	}
	return orchestrator
}

func TestPanelOrchestratorRejectsReusedArbiterModel(t *testing.T) {
	policy := func(model string) ModelPolicy {
		return ModelPolicy{ModelVersion: model, PromptVersion: "panel-prompt-v1", MinConfidence: .8}
	}
	adapter := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .95}
	_, err := NewPanelOrchestrator(NewMemoryStore(), PanelAgents{
		PrimaryA: PanelAgentBinding{Adapter: adapter, Policy: policy("model-a"), StrengthRank: 1},
		PrimaryB: PanelAgentBinding{Adapter: adapter, Policy: policy("model-b"), StrengthRank: 1},
		Arbiter:  PanelAgentBinding{Adapter: adapter, Policy: policy("model-a"), StrengthRank: 2},
	}, allowPanelAdmission)
	if !errors.Is(err, ErrPanelConfiguration) {
		t.Fatalf("expected reused arbiter model to be rejected, got %v", err)
	}
}

func TestShadowPanelFactoryRequiresExistingEligibilityGate(t *testing.T) {
	adapter := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .95}
	policy := func(model string) ModelPolicy {
		return ModelPolicy{ModelVersion: model, PromptVersion: "panel-prompt-v1", MinConfidence: .8}
	}
	agents := PanelAgents{
		PrimaryA: PanelAgentBinding{Adapter: adapter, Policy: policy("model-a"), StrengthRank: 1},
		PrimaryB: PanelAgentBinding{Adapter: adapter, Policy: policy("model-b"), StrengthRank: 1},
		Arbiter:  PanelAgentBinding{Adapter: adapter, Policy: policy("model-c"), StrengthRank: 2},
	}
	handler := NewHandler(NewMemoryStore(), adapter, nil)
	if _, err := handler.NewShadowPanelOrchestrator(agents); !errors.Is(err, ErrPanelConfiguration) {
		t.Fatalf("factory bypassed A14 admission gate: %v", err)
	}
}

func TestPanelRejectsAdapterPolicyMismatchBeforeAnyModelCall(t *testing.T) {
	policy := func(model string) ModelPolicy {
		return ModelPolicy{ModelVersion: model, PromptVersion: "panel-prompt-v1", MinConfidence: .8}
	}
	other := NewHTTPAdapter(HTTPAdapterConfig{ModelVersion: "other-model", PromptVersion: "panel-prompt-v1", MinConfidence: .8})
	fake := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .95}
	_, err := NewPanelOrchestrator(NewMemoryStore(), PanelAgents{
		PrimaryA: PanelAgentBinding{Adapter: other, Policy: policy("model-a"), StrengthRank: 1},
		PrimaryB: PanelAgentBinding{Adapter: fake, Policy: policy("model-b"), StrengthRank: 1},
		Arbiter:  PanelAgentBinding{Adapter: fake, Policy: policy("model-c"), StrengthRank: 2},
	}, allowPanelAdmission)
	if !errors.Is(err, ErrPanelConfiguration) {
		t.Fatalf("adapter policy mismatch was accepted: %v", err)
	}
}

func allowPanelAdmission(_ context.Context, _, _ string, _ Context, _ ModelPolicy) (aieligibility.Decision, bool, error) {
	return aieligibility.Decision{ExternalAIAllowed: true, OutputConstraint: aieligibility.OutputConstraint{
		CriteriaEvidenceOnly: true, AllowModelFinalScore: false, FinalScoreAuthority: "server_rubric_and_deterministic_rule",
	}}, true, nil
}

func TestPanelRequiresAdmissionAndRoutesAbstentionToHuman(t *testing.T) {
	adapter := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .95}
	policy := func(model string) ModelPolicy {
		return ModelPolicy{ModelVersion: model, PromptVersion: "panel-prompt-v1", MinConfidence: .8}
	}
	agents := PanelAgents{
		PrimaryA: PanelAgentBinding{Adapter: adapter, Policy: policy("model-a"), StrengthRank: 1},
		PrimaryB: PanelAgentBinding{Adapter: adapter, Policy: policy("model-b"), StrengthRank: 1},
		Arbiter:  PanelAgentBinding{Adapter: adapter, Policy: policy("model-c"), StrengthRank: 2},
	}
	if _, err := NewPanelOrchestrator(NewMemoryStore(), agents, nil); !errors.Is(err, ErrPanelConfiguration) {
		t.Fatalf("panel accepted missing admission gate: %v", err)
	}
	deny := func(_ context.Context, _, _ string, _ Context, _ ModelPolicy) (aieligibility.Decision, bool, error) {
		return aieligibility.Decision{}, false, nil
	}
	orchestrator, err := NewPanelOrchestrator(NewMemoryStore(), agents, deny)
	if err != nil {
		t.Fatal(err)
	}
	result, err := orchestrator.Grade(context.Background(), "tenant-1", "actor-1", panelTestContext(), PanelDecisionConfig{})
	if err != nil || result.Panel.Status != PanelHumanReview || len(adapter.inputs) != 0 {
		t.Fatalf("admission abstention did not fail closed: panel=%#v calls=%d err=%v", result.Panel, len(adapter.inputs), err)
	}
}

func panelTestContext() Context {
	return Context{
		SegmentID: "segment-1", AnswerVersion: "answer-v1", Subject: "mathematics", GradeLevel: "senior",
		Question: paper.Question{ID: "question-1", QuestionNo: "1", QuestionType: "short_answer", Score: 4, Stem: "show alpha and beta"},
		Rubric: paper.Rubric{ID: "rubric-1", Version: "rubric-v1", MaxScore: 4, Points: []paper.RubricPoint{
			{ID: "p1", Description: "alpha", Score: 2, Required: true},
			{ID: "p2", Description: "beta", Score: 2, Required: false},
		}},
		AnswerText: "alpha beta",
	}
}

func assertPanelRunRoles(t *testing.T, store *MemoryStore, panelID string, roles ...string) {
	t.Helper()
	wanted := map[string]bool{}
	for _, role := range roles {
		wanted[role] = true
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, run := range store.runs {
		if run.PanelID == panelID {
			delete(wanted, run.AgentRole)
		}
	}
	if len(wanted) != 0 {
		t.Fatalf("missing panel run roles: %s", strings.Join(roles, ","))
	}
}
