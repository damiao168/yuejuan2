package subjective

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestBatch35GradingV2PreservesValidationPriority(t *testing.T) {
	cases := []struct {
		name   string
		code   string
		mutate func(*AdapterInput, *ResolvedActiveCrop, *ModelPolicy)
	}{
		{"identifier before context", "grading_v2_identifier_invalid", func(input *AdapterInput, _ *ResolvedActiveCrop, _ *ModelPolicy) {
			input.RequestID, input.Question.Stem = "short", ""
		}},
		{"context before rubric", "grading_context_incomplete", func(input *AdapterInput, _ *ResolvedActiveCrop, _ *ModelPolicy) {
			input.Question.Stem, input.Rubric.ID = "", ""
		}},
		{"rubric before policy", "rubric_context_incomplete", func(input *AdapterInput, _ *ResolvedActiveCrop, policy *ModelPolicy) {
			input.Rubric.ID, policy.ModelVersion = "", ""
		}},
		{"policy before math evidence", "grading_v2_policy_invalid", func(input *AdapterInput, _ *ResolvedActiveCrop, policy *ModelPolicy) {
			policy.ModelVersion, input.MathEvidence = "", nil
		}},
		{"math before reference", "math_evidence_invalid", func(input *AdapterInput, _ *ResolvedActiveCrop, _ *ModelPolicy) {
			input.MathEvidence, input.ReferenceContext = nil, &GradingReferenceContext{Source: "unconfirmed"}
		}},
		{"reference before crop", "grading_reference_invalid", func(input *AdapterInput, crop *ResolvedActiveCrop, _ *ModelPolicy) {
			input.ReferenceContext, crop.Kind = &GradingReferenceContext{Source: "unconfirmed"}, "whole_page"
		}},
		{"crop before ocr", "grading_v2_crop_invalid", func(input *AdapterInput, crop *ResolvedActiveCrop, _ *ModelPolicy) {
			confidence := 2.0
			input.OCRConfidence, crop.Kind = &confidence, "whole_page"
		}},
		{"ocr before guard count", "grading_v2_ocr_confidence_invalid", func(input *AdapterInput, _ *ResolvedActiveCrop, _ *ModelPolicy) {
			confidence := 2.0
			input.OCRConfidence, input.PromptGuard.Signals = &confidence, make([]string, 33)
		}},
		{"guard count before authority", "grading_v2_prompt_guard_invalid", func(input *AdapterInput, _ *ResolvedActiveCrop, _ *ModelPolicy) {
			input.PromptGuard.Signals, input.OutputConstraint.AllowModelFinalScore = make([]string, 33), true
		}},
		{"authority before guard entry", "grading_v2_output_constraint_invalid", func(input *AdapterInput, _ *ResolvedActiveCrop, _ *ModelPolicy) {
			input.OutputConstraint.AllowModelFinalScore, input.PromptGuard.Signals = true, []string{" "}
		}},
		{"guard entry before rubric point", "grading_v2_prompt_guard_invalid", func(input *AdapterInput, _ *ResolvedActiveCrop, _ *ModelPolicy) {
			input.PromptGuard.Signals, input.Rubric.Points[0].Description = []string{" "}, ""
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, crop, policy := validMathV2AdapterInput(), validResolvedActiveCrop(t), batch35V2Policy()
			tc.mutate(&input, &crop, &policy)
			_, err := BuildGradingAgentV2Request(input.RequestID, input, crop, policy)
			assertGradingAgentV2ErrorCode(t, err, tc.code)
		})
	}
}

func TestBatch35GradingReferenceUsesEncodedByteLimits(t *testing.T) {
	cases := []struct {
		name       string
		standard   any
		equivalent []any
		parts      []GradingAnswerPart
		valid      bool
	}{
		{"standard exact limit", strings.Repeat("a", 19_998), nil, nil, true},
		{"standard over limit", strings.Repeat("a", 19_999), nil, nil, false},
		{"escaped standard exact limit", strings.Repeat("<", 3_333), nil, nil, true},
		{"escaped standard over limit", strings.Repeat("<", 3_334), nil, nil, false},
		{"equivalent exact limit", "x=2", []any{strings.Repeat("a", 3_998)}, nil, true},
		{"equivalent over limit", "x=2", []any{strings.Repeat("a", 3_999)}, nil, false},
		{"standard cannot encode", math.NaN(), nil, nil, false},
		{"equivalent cannot encode", "x=2", []any{math.Inf(1)}, nil, false},
		{"answer part exact bytes", "x=2", nil, []GradingAnswerPart{{PartNo: 1, AnswerText: strings.Repeat("a", 20_000)}}, true},
		{"answer part over bytes", "x=2", nil, []GradingAnswerPart{{PartNo: 1, AnswerText: strings.Repeat("a", 20_001)}}, false},
		{"answer part sequence", "x=2", nil, []GradingAnswerPart{{PartNo: 2, AnswerText: "x=2"}}, false},
		{"answer part blank", "x=2", nil, []GradingAnswerPart{{PartNo: 1, AnswerText: " "}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := validMathV2AdapterInput()
			input.ReferenceContext = &GradingReferenceContext{
				Source: "confirmed_exam_import_snapshot", SnapshotHash: strings.Repeat("a", 64),
				StandardAnswer: tc.standard, EquivalentAnswers: tc.equivalent, AnswerParts: tc.parts,
			}
			built, err := BuildGradingAgentV2Request(input.RequestID, input, validResolvedActiveCrop(t), batch35V2Policy())
			if !tc.valid {
				assertGradingAgentV2ErrorCode(t, err, "grading_reference_invalid")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer built.Clear()
		})
	}
}

func TestBatch35GradingReferenceNormalizesOnlyRequestCopy(t *testing.T) {
	input := validMathV2AdapterInput()
	input.ReferenceContext = &GradingReferenceContext{
		Source: "confirmed_exam_import_snapshot", SnapshotHash: strings.Repeat("a", 64), StandardAnswer: "x=2",
	}
	built, err := BuildGradingAgentV2Request(input.RequestID, input, validResolvedActiveCrop(t), batch35V2Policy())
	if err != nil {
		t.Fatal(err)
	}
	defer built.Clear()
	if input.ReferenceContext.EquivalentAnswers != nil || input.ReferenceContext.SolutionSteps != nil {
		t.Fatal("builder mutated the caller's reference arrays")
	}
	var request gradingAgentV2Request
	if err := json.Unmarshal(built.Body, &request); err != nil {
		t.Fatal(err)
	}
	if request.ReferenceContext == nil || request.ReferenceContext.EquivalentAnswers == nil || request.ReferenceContext.SolutionSteps == nil {
		t.Fatal("request copy lost normalized empty reference arrays")
	}
}

func TestBatch35GradingV2ProjectsAndValidatesRubricPoints(t *testing.T) {
	cases := []gradingV2RubricProjectionCase{
		{"trimmed ids preserve order", func(rubric *paper.Rubric) {
			rubric.Points = []paper.RubricPoint{
				{ID: " first ", Description: "first point", Score: rubric.MaxScore / 2, Required: true},
				{ID: " second ", Description: "second point", Score: rubric.MaxScore / 2},
			}
		}, true},
		{"duplicate after trimming", func(rubric *paper.Rubric) {
			point := rubric.Points[0]
			point.ID = " " + point.ID + " "
			rubric.Points = append(rubric.Points, point)
		}, false},
		{"point total mismatch", func(rubric *paper.Rubric) { rubric.Points[0].Score /= 2 }, false},
		{"nonfinite point score", func(rubric *paper.Rubric) { rubric.Points[0].Score = math.NaN() }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGradingV2RubricPointProjection(t, tc)
		})
	}
}

func batch35V2Policy() ModelPolicy {
	return ModelPolicy{ModelVersion: "fixture-v1", PromptVersion: "prompt-v2", MinConfidence: 0.8}
}

type gradingV2RubricProjectionCase struct {
	name   string
	mutate func(*paper.Rubric)
	valid  bool
}

func assertGradingV2RubricPointProjection(t *testing.T, tc gradingV2RubricProjectionCase) {
	t.Helper()
	input := validMathV2AdapterInput()
	tc.mutate(&input.Rubric)
	built, err := BuildGradingAgentV2Request(input.RequestID, input, validResolvedActiveCrop(t), batch35V2Policy())
	if !tc.valid {
		assertGradingAgentV2ErrorCode(t, err, "rubric_context_invalid")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer built.Clear()
	var request gradingAgentV2Request
	if err := json.Unmarshal(built.Body, &request); err != nil {
		t.Fatal(err)
	}
	points := request.Rubric.Points
	if len(points) != 2 || points[0].ID != "first" || points[1].ID != "second" || !points[0].Required || points[1].Required {
		t.Fatalf("rubric point identity or order changed: %#v", points)
	}
	for _, point := range points {
		if point.Aliases == nil || !point.EvidenceRequired || point.MatchPolicy != "semantic" {
			t.Fatalf("rubric point projection changed: %#v", point)
		}
	}
}
