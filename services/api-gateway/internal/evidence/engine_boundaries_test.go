package evidence

import (
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestVerifyPreservesFailureAndReviewFlagOrder(t *testing.T) {
	// 多条规则同时失败时仍依次报告；OCR 警告在校验失败标记之前，便于调用方解释复核原因。
	ctx := validEvidenceContext()
	ctx.Grade.SuggestedScore = 3
	ctx.Grade.MatchedPoints[0].Code = "unknown-matched"
	ctx.Grade.MissingPoints = []grading.PointResult{{Code: "unknown-missing"}}
	ctx.Grade.Evidence = []grading.Evidence{
		{AnswerText: "  ", BBox: []float64{90, 90, 20, 20}},
		{AnswerText: "not present", BBox: []float64{1}},
	}
	confidence := 0.79
	ctx.OCRConfidence = &confidence
	result := NewEngine().Verify(ctx)
	want := VerificationResult{
		Passed: false,
		Failed: []Issue{
			{Code: "score_exceeds_max", Message: "suggested_score exceeds max_score"},
			{Code: "rubric_point_not_found", Message: "point unknown-matched is not present in rubric"},
			{Code: "rubric_point_not_found", Message: "point unknown-missing is not present in rubric"},
			{Code: "matched_score_mismatch", Message: "suggested_score does not equal matched_points score total"},
			{Code: "empty_evidence_text", Message: "evidence answer_text is empty"},
			{Code: "evidence_bbox_out_of_segment", Message: "evidence bbox is outside answer segment bbox"},
			{Code: "evidence_text_not_found", Message: "evidence answer_text is not found in answer segment text"},
			{Code: "evidence_bbox_out_of_segment", Message: "evidence bbox is outside answer segment bbox"},
		},
		Warnings:         []Issue{{Code: "low_ocr_confidence", Message: "answer OCR confidence is below evidence verification threshold"}},
		CorrectedFlags:   []string{"low_ocr_confidence", "evidence_verification_failed"},
		NeedsHumanReview: true,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("verification order or values changed: got %#v, want %#v", result, want)
	}
}

func TestVerifyMissingRubricDoesNotEmitUnknownPointIssues(t *testing.T) {
	ctx := validEvidenceContext()
	ctx.Rubric = paper.Rubric{Points: []paper.RubricPoint{{ID: ""}}}
	ctx.Grade.MatchedPoints = nil
	ctx.Grade.MissingPoints = []grading.PointResult{{Code: "p1"}}
	ctx.Grade.SuggestedScore = 0
	result := NewEngine().Verify(ctx)
	want := []Issue{{Code: "rubric_missing", Message: "rubric points are required when grade references matched or missing points"}}
	if !reflect.DeepEqual(result.Failed, want) {
		t.Fatalf("missing rubric diagnostics changed: %#v", result.Failed)
	}
	if result.Passed || !result.NeedsHumanReview {
		t.Fatalf("missing rubric must fail and require review: %#v", result)
	}
}

func TestVerifyConfidenceThresholdAndUpstreamReviewRemainIndependent(t *testing.T) {
	for _, tc := range []struct {
		name           string
		confidence     *float64
		upstreamReview bool
		wantReview     bool
	}{
		{name: "unknown confidence"},
		{name: "exact threshold", confidence: evidenceConfidence(0.8)},
		{name: "upstream review", confidence: evidenceConfidence(0.8), upstreamReview: true, wantReview: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := validEvidenceContext()
			ctx.OCRConfidence = tc.confidence
			ctx.Grade.NeedsHumanReview = tc.upstreamReview
			result := NewEngine().Verify(ctx)
			if !result.Passed || result.NeedsHumanReview != tc.wantReview || len(result.Warnings) != 0 || len(result.CorrectedFlags) != 0 {
				t.Fatalf("confidence boundary or upstream review changed: %#v", result)
			}
		})
	}
}

func TestVerifyTextNormalizationAndBBoxBoundaries(t *testing.T) {
	ctx := validEvidenceContext()
	ctx.AnswerText = "PHOTOSYNTHESIS\nuses\tSUNLIGHT"
	ctx.Grade.Evidence[0].AnswerText = " photosynthesis\tuses sunlight "
	for _, bbox := range [][]float64{nil, {0, 0, 100, 100}} {
		ctx.Grade.Evidence[0].BBox = bbox
		result := NewEngine().Verify(ctx)
		if !result.Passed || result.NeedsHumanReview || len(result.Failed) != 0 {
			t.Fatalf("normalized text or optional/exact bbox rejected: bbox=%v, result=%#v", bbox, result)
		}
	}
}

func evidenceConfidence(value float64) *float64 {
	return &value
}
