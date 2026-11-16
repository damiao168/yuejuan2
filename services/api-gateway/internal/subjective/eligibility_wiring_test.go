package subjective

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
	"edugrade-enterprise/services/api-gateway/internal/modelcalibration"
)

type eligibilityGateFunc func(context.Context, string, aieligibility.DecisionInput) (aieligibility.Decision, error)

func (f eligibilityGateFunc) Decide(ctx context.Context, tenant string, input aieligibility.DecisionInput) (aieligibility.Decision, error) {
	return f(ctx, tenant, input)
}

type evaluationEvidenceFunc func(string, string, string, string, assessment.SubjectCode, string) (gradingevaluation.AdmissionEvidence, error)

func (f evaluationEvidenceFunc) AdmissionEvidenceFor(_ context.Context, tenant, model, prompt, rubric string, subject assessment.SubjectCode, archetype string) (gradingevaluation.AdmissionEvidence, error) {
	return f(tenant, model, prompt, rubric, subject, archetype)
}

type parserQualityFunc func(string, string, assessment.SubjectCode, string) (*float64, error)

func (f parserQualityFunc) ParserQualityForSegment(_ context.Context, tenant, segment string, subject assessment.SubjectCode, archetype string) (*float64, error) {
	return f(tenant, segment, subject, archetype)
}

type syntheticCalibrationEvidence struct {
	approved func(string, modelcalibration.Axis) (modelcalibration.ApprovedEvidence, error)
	record   func(string, modelcalibration.RecordCandidateInput) (modelcalibration.Candidate, error)
}

func (s syntheticCalibrationEvidence) Approved(_ context.Context, tenant string, axis modelcalibration.Axis) (modelcalibration.ApprovedEvidence, error) {
	return s.approved(tenant, axis)
}

func (s syntheticCalibrationEvidence) RecordCandidate(_ context.Context, tenant string, input modelcalibration.RecordCandidateInput) (modelcalibration.Candidate, error) {
	return s.record(tenant, input)
}

func TestDirectGradeUsesScopedEligibilityEvidenceBeforeModel(t *testing.T) {
	const tenant = "synthetic-school"
	value := serviceTestContext()
	value.AssessmentSnapshot.SubjectCode = assessment.SubjectHistory
	value.AssessmentSnapshot.ArchetypeCode = "material"
	value.AssessmentSnapshot.ScoringPolicySnapshot.Mode = assessment.ScoringAIAssist
	ocrQuality := .96
	value.OCRConfidence = &ocrQuality
	store := NewMemoryStore()
	store.AddContext(tenant, value.SegmentID, value)

	adapter := &failingSubjectiveAdapter{err: errors.New("inference must not run")}
	policy := NormalizePolicy(ModelPolicy{})
	expected := modelcalibration.Axis{
		ModelReference: policy.ModelVersion, PromptVersion: policy.PromptVersion, RubricVersion: value.Rubric.Version,
		Subject: string(assessment.SubjectHistory), Archetype: "material", SliceKey: "all",
	}
	handler := NewHandler(store, adapter, nil).
		WithEvaluationEvidence(evaluationEvidenceFunc(func(actualTenant, model, prompt, rubric string, subject assessment.SubjectCode, archetype string) (gradingevaluation.AdmissionEvidence, error) {
			if actualTenant != tenant || model != expected.ModelReference || prompt != expected.PromptVersion || rubric != expected.RubricVersion ||
				subject != assessment.SubjectHistory || archetype != expected.Archetype {
				t.Fatal("evaluation was read outside the frozen grading axis")
			}
			return gradingevaluation.AdmissionEvidence{Approved: true, SampleCount: 40, SevereErrorRate: .05, EvaluationRef: "synthetic-evaluation"}, nil
		})).
		WithCalibrationEvidence(syntheticCalibrationEvidence{approved: func(actualTenant string, axis modelcalibration.Axis) (modelcalibration.ApprovedEvidence, error) {
			if actualTenant != tenant || axis != expected {
				t.Fatal("calibration was read outside the frozen grading axis")
			}
			return modelcalibration.ApprovedEvidence{Available: true, CalibrationRef: "synthetic-calibration"}, nil
		}}).
		WithParserQuality(parserQualityFunc(func(actualTenant, segment string, subject assessment.SubjectCode, archetype string) (*float64, error) {
			if actualTenant != tenant || segment != value.SegmentID || subject != assessment.SubjectHistory || archetype != expected.Archetype {
				t.Fatal("parser lookup used a different answer or subject")
			}
			return nil, nil
		})).
		WithEligibilityGate(eligibilityGateFunc(func(_ context.Context, actualTenant string, input aieligibility.DecisionInput) (aieligibility.Decision, error) {
			if actualTenant != tenant || input.RunItemID == "" || input.AssessmentSnapshot.SubjectCode != assessment.SubjectHistory ||
				input.RequestedMode != assessment.ScoringAIAssist || input.OCRQuality == nil || *input.OCRQuality != ocrQuality ||
				input.ParserQuality != nil || input.EvidenceAvailable || !input.RubricComplete {
				t.Fatalf("incomplete material or missing parser evidence was treated as available: %#v", input)
			}
			if !input.Evaluation.Approved || input.Evaluation.SampleCount != 40 || input.Evaluation.EvaluationRef != "synthetic-evaluation" ||
				!input.Calibration.Available || input.Calibration.CalibrationRef != "synthetic-calibration" {
				t.Fatalf("approved scoring evidence was lost: %#v", input)
			}
			return aieligibility.Decision{ID: "synthetic-denial"}, nil
		}))
	request := subjectiveTestRequest(t, http.MethodPost, auth.User{ID: "teacher-1", TenantID: tenant},
		GradeRequest{IdempotencyKey: "synthetic-eligibility"}, map[string]string{"id": value.SegmentID})
	response := httptest.NewRecorder()
	handler.Grade(response, request)
	var body struct {
		Grade    Grade                  `json:"grade"`
		Decision aieligibility.Decision `json:"eligibility_decision"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusCreated ||
		body.Decision.ID != "synthetic-denial" || adapter.calls != 0 || body.Grade.Status != RunFailed ||
		!body.Grade.NeedsHumanReview || !slices.Contains(body.Grade.RiskFlags, "ai_eligibility_abstained") {
		t.Fatalf("ineligible request did not produce the manual-review result: %v, %d %s", err, response.Code, response.Body)
	}
	run, err := store.GetRun(t.Context(), tenant, body.Grade.RunID)
	if err != nil || run.Status != RunFailed || run.GradeID != body.Grade.ID || run.ErrorCode != "ai_eligibility_abstained" {
		t.Fatalf("run is missing the admitted manual-review outcome: %#v, %v", run, err)
	}
}

func TestCalibrationKeepsRiskFallbackAndSeparatesVisualConfidence(t *testing.T) {
	const tenant = "synthetic-school"
	const runID = "synthetic-run"
	value := serviceTestContext()
	policy := ModelPolicy{ModelVersion: "model-v1", PromptVersion: "prompt-v1"}
	decision := aieligibility.Decision{ExternalAIAllowed: true, OutputConstraint: aieligibility.OutputConstraint{MaxSevereErrorRisk: .02}}
	calls := 0
	handler := NewHandler(nil, nil, nil).WithCalibrationEvidence(syntheticCalibrationEvidence{record: func(actualTenant string, input modelcalibration.RecordCandidateInput) (modelcalibration.Candidate, error) {
		calls++
		if actualTenant != tenant || input.CandidateKey != runID || input.Axis.ModelReference != policy.ModelVersion ||
			input.Axis.RubricVersion != value.Rubric.Version || input.RawConfidence != .93 || input.TargetRisk == nil || *input.TargetRisk != .02 {
			t.Fatalf("recorded wrong calibration axis or risk budget: %#v", input)
		}
		return modelcalibration.Candidate{ID: "synthetic-candidate", RawConfidence: .93, AbstainReason: "risk_budget_unmet"}, nil
	}})
	output := AdapterOutput{Confidence: .93}
	if err := handler.recordCalibrationCandidate(t.Context(), tenant, runID, value, policy, decision, &output); err != nil {
		t.Fatal(err)
	}
	candidate, ok := output.RawOutput["model_score_candidate"].(map[string]any)
	if calls != 1 || !ok || candidate["id"] != "synthetic-candidate" || candidate["abstain_reason"] != "risk_budget_unmet" ||
		!output.NeedsHumanReview || !slices.Contains(output.RiskFlags, "calibration_risk_coverage_abstained") {
		t.Fatalf("calibration abstention lost traceability or manual review: %#v", output)
	}

	// 新视觉输入未经过旧文本候选的校准，必须保留人工复核及准入依据。
	value.InputVersionID = "visual-v3"
	decision.Reasons = []aieligibility.Reason{{Code: "suggestion_uncalibrated"}, {Code: "suggestion_evaluation_unapproved"}, {Code: "unrelated"}}
	output = AdapterOutput{Confidence: .93}
	if err := handler.recordCalibrationCandidate(t.Context(), tenant, runID, value, policy, decision, &output); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !output.NeedsHumanReview || output.RawOutput != nil ||
		!slices.Equal(output.RiskFlags, []string{"suggestion_uncalibrated", "suggestion_evaluation_unapproved"}) {
		t.Fatalf("visual result used old confidence records or lost known risk flags: %#v", output)
	}
}
