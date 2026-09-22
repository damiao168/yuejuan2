package subjective

import (
	"context"
	"errors"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
)

func shadowRunnerFixture(t *testing.T) (*PanelShadowRunner, *gradingevaluation.MemoryStore, *panelTestAdapter, PanelShadowSample, string) {
	t.Helper()
	a := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .9}
	b := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .9}
	c := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .99}
	panel := newTestPanelOrchestrator(t, a, b, c)
	store := gradingevaluation.NewMemoryStore()
	run, err := gradingevaluation.NewService(store).CreateRun(context.Background(), "tenant-1", "actor-1", gradingevaluation.CreateRunInput{
		Key: "senior-shadow-v1", DisplayName: "Senior shadow", ModelReference: PanelModelSetReference(panel.agents),
		PromptVersion: "panel-prompt-v1", RubricVersion: "rubric-v1", DatasetReference: "deidentified-v1",
		DatasetSHA256: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewPanelShadowRunner(panel, store)
	if err != nil {
		t.Fatal(err)
	}
	value := panelTestContext()
	value.AssessmentSnapshot.TenantID = "tenant-1"
	value.AssessmentSnapshot.QuestionID = value.Question.ID
	value.AssessmentSnapshot.EducationStage = assessment.StageSenior
	value.AssessmentSnapshot.SubjectCode = assessment.SubjectMathematics
	value.AssessmentSnapshot.ArchetypeCode = "short_answer"
	sample := PanelShadowSample{
		Context: value, ResponseKey: "opaque-response-1", ResponseFingerprint: strings.Repeat("b", 64),
		ReferenceScore: 2, ReferenceReviewers: 2, ReferenceAdjudicated: true,
		PrimaryACostMicros: 10, PrimaryBCostMicros: 11,
	}
	return runner, store, a, sample, run.ID
}

func TestPanelShadowRunnerRecordsOnlyDeidentifiedServerScoresAndReplays(t *testing.T) {
	runner, store, a, sample, runID := shadowRunnerFixture(t)
	config := PanelDecisionConfig{PolicyVersion: "shadow-v1"}
	items, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, config, []PanelShadowSample{sample})
	if err != nil || len(items) != 1 || items[0].ScoreA != 2 || items[0].ScoreB != 2 ||
		items[0].ResolvedScore == nil || *items[0].ResolvedScore != 2 || items[0].ResolutionSource != ResolutionPrimaryConsensus {
		t.Fatalf("shadow observation was not server-scored: %#v %v", items, err)
	}
	if len(a.inputs) != 1 || a.inputs[0].AnswerText != sample.Context.AnswerText || a.inputs[0].OutputConstraint.AllowModelFinalScore {
		t.Fatalf("blind agent was given score authority or wrong evidence: %#v", a.inputs)
	}
	stored, err := store.ListPanelObservations(context.Background(), "tenant-1", runID)
	if err != nil || len(stored) != 1 || stored[0].ResponseFingerprint != sample.ResponseFingerprint {
		t.Fatalf("evaluation did not persist the opaque observation: %#v %v", stored, err)
	}
	second, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, config, []PanelShadowSample{sample})
	if err != nil || len(second) != 1 || second[0].ID != items[0].ID || len(a.inputs) != 1 {
		t.Fatalf("replay duplicated model call or observation: %#v %v", second, err)
	}
	if _, _, err := gradingevaluation.NewService(store).CompletePanel(context.Background(), "tenant-1", runID); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, config, []PanelShadowSample{sample}); !errors.Is(err, ErrPanelConfiguration) {
		t.Fatalf("completed run was still executable: %v", err)
	}
}

type failOnceShadowStore struct {
	PanelShadowEvaluationStore
	fail bool
}

func (s *failOnceShadowStore) AddPanelObservation(ctx context.Context, tenantID, runID string, item gradingevaluation.PanelObservation) (gradingevaluation.PanelObservation, error) {
	if s.fail {
		s.fail = false
		return gradingevaluation.PanelObservation{}, errors.New("transient observation write failure")
	}
	return s.PanelShadowEvaluationStore.AddPanelObservation(ctx, tenantID, runID, item)
}

func TestPanelShadowRunnerResumesAfterObservationWriteFailureWithoutRepeatingModels(t *testing.T) {
	runner, store, a, sample, runID := shadowRunnerFixture(t)
	b := runner.panel.agents.PrimaryB.Adapter.(*panelTestAdapter)
	runner.store = &failOnceShadowStore{PanelShadowEvaluationStore: store, fail: true}
	config := PanelDecisionConfig{PolicyVersion: "shadow-v1"}
	if _, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, config, []PanelShadowSample{sample}); err == nil {
		t.Fatal("observation write failure was suppressed")
	}
	if len(a.inputs) != 1 || len(b.inputs) != 1 {
		t.Fatalf("first attempt did not finish A/B: A=%d B=%d", len(a.inputs), len(b.inputs))
	}
	items, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, config, []PanelShadowSample{sample})
	if err != nil || len(items) != 1 || items[0].ScoreA != 2 || items[0].ScoreB != 2 || len(a.inputs) != 1 || len(b.inputs) != 1 {
		t.Fatalf("terminal panel replay did not recover saved grades: %#v %v A=%d B=%d", items, err, len(a.inputs), len(b.inputs))
	}
}

func TestPanelShadowRunnerResumesSavedBlindArbitrationWithoutRepeatingModels(t *testing.T) {
	runner, store, a, sample, runID := shadowRunnerFixture(t)
	b := runner.panel.agents.PrimaryB.Adapter.(*panelTestAdapter)
	c := runner.panel.agents.Arbiter.Adapter.(*panelTestAdapter)
	b.supported = map[string]bool{"p2": true}
	sample.ArbiterCostMicros = 20
	runner.store = &failOnceShadowStore{PanelShadowEvaluationStore: store, fail: true}
	config := PanelDecisionConfig{PolicyVersion: "shadow-v1", ArbiterAgreementThreshold: .5}
	if _, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, config, []PanelShadowSample{sample}); err == nil {
		t.Fatal("arbitration observation write failure was suppressed")
	}
	items, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, config, []PanelShadowSample{sample})
	if err != nil || len(items) != 1 || !items[0].ArbitrationTriggered || items[0].ScoreC == nil ||
		len(a.inputs) != 1 || len(b.inputs) != 1 || len(c.inputs) != 1 {
		t.Fatalf("saved arbitration was not resumed: %#v %v A=%d B=%d C=%d", items, err, len(a.inputs), len(b.inputs), len(c.inputs))
	}
}

func TestPanelShadowRunnerRejectsBadReferencesBeforeModelCalls(t *testing.T) {
	runner, store, a, sample, runID := shadowRunnerFixture(t)
	bad := sample
	bad.ResponseKey = "opaque-response-2"
	bad.ResponseFingerprint = strings.Repeat("c", 64)
	bad.ReferenceAdjudicated = false
	if _, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, PanelDecisionConfig{PolicyVersion: "shadow-v1"}, []PanelShadowSample{sample, bad}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unadjudicated batch was accepted: %v", err)
	}
	if len(a.inputs) != 0 {
		t.Fatal("invalid batch called a model before preflight completed")
	}
	items, _ := store.ListPanelObservations(context.Background(), "tenant-1", runID)
	if len(items) != 0 {
		t.Fatal("invalid batch wrote evaluation evidence")
	}
	for _, mutation := range []func(*PanelShadowSample){
		func(s *PanelShadowSample) { s.Context.AssessmentSnapshot.TenantID = "other-tenant" },
		func(s *PanelShadowSample) { s.Context.AssessmentSnapshot.EducationStage = assessment.StageJunior },
		func(s *PanelShadowSample) { s.ReferenceReviewers = 1 },
		func(s *PanelShadowSample) { s.PrimaryACostMicros = 0 },
		func(s *PanelShadowSample) { s.Context.Rubric.Version = "changed-rubric" },
	} {
		changed := sample
		mutation(&changed)
		if _, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, PanelDecisionConfig{PolicyVersion: "shadow-v1"}, []PanelShadowSample{changed}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid Shadow sample was accepted: %v", err)
		}
	}
	if len(a.inputs) != 0 {
		t.Fatal("invalid reference reached a model")
	}
}

func TestPanelShadowRunnerRejectsModelSetDriftAndDuplicateAnswers(t *testing.T) {
	runner, _, a, sample, runID := shadowRunnerFixture(t)
	config := PanelDecisionConfig{PolicyVersion: "shadow-v1"}
	duplicate := sample
	duplicate.ResponseKey = "opaque-response-2"
	if _, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, config, []PanelShadowSample{sample, duplicate}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate fingerprint was counted twice: %v", err)
	}
	runner.panel.agents.PrimaryA.Policy.ModelVersion = "changed-model"
	if _, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, config, []PanelShadowSample{sample}); !errors.Is(err, ErrPanelConfiguration) {
		t.Fatalf("changed role model reused the old evaluation run: %v", err)
	}
	if len(a.inputs) != 0 {
		t.Fatal("model-set drift reached an adapter")
	}
}

func TestPanelShadowRunnerDoesNotCountIncompletePrimaryFailure(t *testing.T) {
	runner, store, a, sample, runID := shadowRunnerFixture(t)
	a.err = ErrPanelAgentFailure
	if _, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, PanelDecisionConfig{PolicyVersion: "shadow-v1"}, []PanelShadowSample{sample}); !errors.Is(err, ErrPanelShadowSampleIncomplete) {
		t.Fatalf("incomplete A/B was counted as a rated answer: %v", err)
	}
	items, err := store.ListPanelObservations(context.Background(), "tenant-1", runID)
	if err != nil || len(items) != 0 {
		t.Fatalf("incomplete rating was persisted: %#v %v", items, err)
	}
}

func TestPanelShadowRunnerRecordsBlindArbitrationAndHumanEscalation(t *testing.T) {
	for _, unresolved := range []bool{false, true} {
		t.Run(map[bool]string{false: "arbiter", true: "human"}[unresolved], func(t *testing.T) {
			runner, store, _, sample, runID := shadowRunnerFixture(t)
			b := runner.panel.agents.PrimaryB.Adapter.(*panelTestAdapter)
			c := runner.panel.agents.Arbiter.Adapter.(*panelTestAdapter)
			b.supported = map[string]bool{"p2": true}
			if unresolved {
				c.confidence = .5
			}
			sample.ArbiterCostMicros = 20
			config := PanelDecisionConfig{PolicyVersion: "shadow-v1", ArbiterAgreementThreshold: .5}
			items, err := runner.RunBatch(context.Background(), "tenant-1", "actor-1", runID, config, []PanelShadowSample{sample})
			if err != nil || len(items) != 1 || !items[0].ArbitrationTriggered || items[0].ScoreC == nil {
				t.Fatalf("arbitration was not captured: %#v %v", items, err)
			}
			if unresolved {
				if !items[0].HumanEscalated || items[0].ResolutionSource != ResolutionHuman || items[0].ResolvedScore != nil {
					t.Fatalf("unresolved C became a final candidate: %#v", items[0])
				}
			} else if items[0].HumanEscalated || items[0].ResolutionSource != ResolutionArbiter || items[0].ResolvedScore == nil {
				t.Fatalf("independently resolved C was not recorded: %#v", items[0])
			}
			metrics, err := gradingevaluation.CalculateStoredPanelMetrics(context.Background(), store, "tenant-1", runID)
			if err != nil || metrics.Arbiter.SampleCount != 1 || metrics.ArbitrationTriggerRate != 1 {
				t.Fatalf("saved arbitration could not be evaluated: %#v %v", metrics, err)
			}
		})
	}
}
