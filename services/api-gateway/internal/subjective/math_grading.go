package subjective

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
)

func (h *Handler) WithMathGradingV2(enabled bool, adapter LLMGradingAdapter, source MathEvidenceSource, crops *ActiveCropResolver) *Handler {
	h.mathV2Enabled = enabled
	h.mathAdapter = adapter
	source.requireCrop = enabled
	if source.Crops == nil && crops != nil {
		source.Crops = crops.evidence
	}
	h.mathEvidence = source
	h.activeCrops = crops
	return h
}

func (h *Handler) prepareMathEvidence(ctx context.Context, tenantID string, value *Context) error {
	if value == nil || !h.mathV2Enabled || !mathSubject(*value) {
		return nil
	}
	evidence, err := h.mathEvidence.Prepare(ctx, tenantID, *value)
	if err != nil {
		return err
	}
	value.MathEvidence = evidence
	return nil
}

func (h *Handler) useMathV2(value Context) bool {
	return h.mathV2Enabled && mathSubject(value) && value.MathEvidence != nil && h.mathAdapter != nil
}

func runInputFor(value Context, batchID string, policy ModelPolicy, requestID string) CreateRunInput {
	input := CreateRunInput{AnswerSegmentID: value.SegmentID, BatchID: batchID, AnswerVersion: value.AnswerVersion,
		QuestionID: value.Question.ID, RubricVersion: value.Rubric.Version, ModelVersion: policy.ModelVersion,
		PromptVersion: policy.PromptVersion, MinConfidence: policy.MinConfidence, RequestID: requestID}
	if value.MathEvidence != nil {
		input.MathArtifactID = value.MathEvidence.ArtifactID
		input.MathArtifactVersion = value.MathEvidence.ArtifactVersion
		input.MathCorrectionRevision = value.MathEvidence.CorrectionRevision
		input.MathScoringVersion = value.MathEvidence.ScoringVersion
	}
	return input
}

func (h *Handler) buildAdapterInput(ctx context.Context, tenantID, requestID string, value Context, policy ModelPolicy, guard PromptGuard, constraint aieligibility.OutputConstraint) (AdapterInput, LLMGradingAdapter, bool, error) {
	input := AdapterInput{RequestID: requestID, SegmentID: value.SegmentID, Subject: value.Subject, GradeLevel: value.GradeLevel,
		Question: value.Question, Rubric: value.Rubric, AnswerText: value.AnswerText, AnswerImageRef: value.AnswerImageRef,
		OCRConfidence: value.OCRConfidence, AssessmentSnapshot: value.AssessmentSnapshot, ModelPolicy: policy, PromptGuard: guard,
		MathEvidence: value.MathEvidence, OutputConstraint: constraint}
	if !h.useMathV2(value) {
		return input, h.adapter, false, nil
	}
	if h.activeCrops == nil {
		return AdapterInput{}, nil, true, ErrActiveCropUnavailable
	}
	crop, err := h.activeCrops.Resolve(ctx, tenantID, value.SegmentID, value.Question.ID)
	if err != nil {
		return AdapterInput{}, nil, true, err
	}
	if !mathCropHashMatches(value.MathEvidence.cropInputHash, crop.SHA256) {
		return AdapterInput{}, nil, true, mathunderstanding.ErrRevisionConflict
	}
	input.ActiveCrop = &crop
	return input, h.mathAdapter, true, nil
}

func (h *Handler) settleMathOutput(ctx context.Context, tenantID string, value Context, policy ModelPolicy, output *AdapterOutput) error {
	if output == nil || value.MathEvidence == nil || output.SchemaVersion != gradingAgentV2BuilderSchemaVersion || output.Mock || output.DeliveryMode != "teacher_suggestion" {
		return ErrInvalidModelOutput
	}
	// The worker result envelope may carry a previously settled suggestion,
	// but none of its score-bearing fields survive without server recomputation.
	output.SuggestedScore, output.MathScore = 0, nil
	output.MatchedPoints, output.MissingPoints, output.Evidence = nil, nil, nil
	output.NeedsHumanReview = true
	if len(output.MathCandidates) > 100 {
		return ErrInvalidModelOutput
	}
	candidates := make([]mathunderstanding.CriterionCandidate, 0, len(output.MathCandidates))
	for _, candidate := range output.MathCandidates {
		if (candidate.Status != "supported" && candidate.Status != "contradicted" && candidate.Status != "uncertain") ||
			math.IsNaN(candidate.Confidence) || math.IsInf(candidate.Confidence, 0) || candidate.Confidence < 0 || candidate.Confidence > 1 ||
			strings.TrimSpace(candidate.ReasonCode) == "" || len(candidate.ReasonCode) > 256 || len(candidate.EvidenceIDs) > 100 {
			return ErrInvalidModelOutput
		}
		seenEvidence := map[string]bool{}
		for _, id := range candidate.EvidenceIDs {
			if seenEvidence[id] {
				return ErrInvalidModelOutput
			}
			seenEvidence[id] = true
		}
		candidates = append(candidates, mathunderstanding.CriterionCandidate{RubricPointID: candidate.RubricPointID, Status: candidate.Status, EvidenceIDs: nonNilStrings(candidate.EvidenceIDs)})
	}
	score, err := mathunderstanding.ScoreFrozenRubric(value.MathEvidence.frozen, value.MathEvidence.effective, candidates)
	if err != nil {
		if errors.Is(err, mathunderstanding.ErrInvalidInput) {
			return fmt.Errorf("%w: invalid math candidate mapping", ErrInvalidModelOutput)
		}
		return err
	}
	latest, err := h.mathEvidence.Prepare(ctx, tenantID, value)
	if err != nil {
		return err
	}
	if latest == nil || latest.ArtifactID != value.MathEvidence.ArtifactID || latest.ArtifactVersion != value.MathEvidence.ArtifactVersion || latest.CorrectionRevision != value.MathEvidence.CorrectionRevision {
		return mathunderstanding.ErrRevisionConflict
	}
	output.MathScore = &score
	output.RawOutput = map[string]any{"schema_version": output.SchemaVersion, "criterion_candidates": output.MathCandidates,
		"alternative_solution_candidate": output.AlternativeSolutionCandidate, "telemetry": output.Telemetry}
	output.RawOutput["math_rubric_score"] = score
	output.RawOutput["math_evidence_binding"] = map[string]any{"artifact_id": latest.ArtifactID, "artifact_version": latest.ArtifactVersion, "correction_revision": latest.CorrectionRevision, "scoring_version": latest.ScoringVersion}
	if score.SuggestedScore == nil || score.UnresolvedScore > 0 || output.AlternativeSolutionCandidate || containsString(output.RiskFlags, "alternative_solution_candidate") || value.MathEvidence.HasDiagram || value.MathEvidence.GraphUncertain || value.MathEvidence.RequiredRubricUncertain || value.MathEvidence.Quality.Critical < policy.MinConfidence {
		output.RiskFlags = appendFlag(output.RiskFlags, "math_human_review_required")
		return ErrMathHumanReviewRequired
	}
	output.SuggestedScore = *score.SuggestedScore
	output.MatchedPoints = append([]grading.PointResult{}, score.MatchedPoints...)
	output.MissingPoints = append([]grading.PointResult{}, score.MissingPoints...)
	output.Evidence = mathScoreEvidence(value.SegmentID, score)
	output.Confidence = value.MathEvidence.Quality.Critical
	output.NeedsHumanReview = true
	output.RiskFlags = appendFlag(output.RiskFlags, "human_review_required")
	return nil
}

func mathScoreEvidence(segmentID string, score mathunderstanding.RubricScore) []grading.Evidence {
	out := []grading.Evidence{}
	for _, evidence := range score.RubricEvidence {
		for _, sourceID := range evidence.SourceArtifactIDs {
			out = append(out, grading.Evidence{Type: "math_artifact", EvidenceID: evidence.ID + ":" + sourceID,
				RubricPointID: evidence.RubricCriterionKey, AnswerSegment: segmentID, Rule: evidence.Explanation,
				Location: sourceID, Confidence: evidence.Confidence})
		}
	}
	return out
}

func mathReviewPayload(value Context, output AdapterOutput) map[string]any {
	candidates := output.MathCandidates
	if candidates == nil {
		candidates = []MathCriterionCandidate{}
	}
	payload := map[string]any{"review_required": true, "code": "math_human_review_required", "message": "数学证据仍有未决项，未生成 AI 总分。", "criterion_candidates": candidates}
	if output.MathScore != nil {
		payload["math_rubric_score"] = output.MathScore
	}
	if value.MathEvidence != nil {
		payload["math_evidence_quality"] = value.MathEvidence.Quality
	}
	return payload
}

func ensureRunMathBinding(run GradingRun, value Context) error {
	if run.MathScoringVersion == "" && value.MathEvidence == nil {
		return nil
	}
	if value.MathEvidence == nil || !value.MathEvidence.bindingMatches(run) {
		return fmt.Errorf("%w: math artifact binding changed", mathunderstanding.ErrRevisionConflict)
	}
	return nil
}

func isMathRevisionConflict(err error) bool {
	return errors.Is(err, mathunderstanding.ErrRevisionConflict)
}
