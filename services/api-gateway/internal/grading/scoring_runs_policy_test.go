package grading

import (
	"math"
	"testing"
)

func TestCropRelativeOptionRegionsConvertsPageCoordinates(t *testing.T) {
	area := map[string]any{"x": 0.5, "y": 0.2, "width": 0.4, "height": 0.2}
	options := []any{map[string]any{"label": "A", "x": 0.6, "y": 0.25, "width": 0.04, "height": 0.02}}

	result := cropRelativeOptionRegions(area, options)
	option := result[0].(map[string]any)
	if math.Abs(option["x"].(float64)-0.25) > 0.000001 ||
		math.Abs(option["y"].(float64)-0.25) > 0.000001 ||
		math.Abs(option["width"].(float64)-0.1) > 0.000001 ||
		math.Abs(option["height"].(float64)-0.1) > 0.000001 {
		t.Fatalf("unexpected crop-relative option: %#v", option)
	}
}

func TestCropRelativeOptionRegionsPreservesAlreadyRelativeCoordinates(t *testing.T) {
	area := map[string]any{"x": 0.7, "y": 0.2, "width": 0.2, "height": 0.1}
	options := []any{map[string]any{"label": "A", "x": 0.1, "y": 0.2, "width": 0.2, "height": 0.3}}

	result := cropRelativeOptionRegions(area, options)
	option := result[0].(map[string]any)
	if option["x"].(float64) != 0.1 || option["y"].(float64) != 0.2 {
		t.Fatalf("already relative option was converted twice: %#v", option)
	}
}

func TestCropRelativeOptionRegionsExplicitSpaceOverridesAmbiguousGeometry(t *testing.T) {
	// 相对题区的坐标恰好也落在页面题区内，不能按历史页面坐标规则再换算一次。
	area := map[string]any{"x": 0.1, "y": 0.1, "width": 0.8, "height": 0.8, "option_coordinate_space": "question_relative"}
	options := []any{map[string]any{"label": "A", "x": 0.2, "y": 0.2, "width": 0.1, "height": 0.1}}
	result := cropRelativeOptionRegions(area, options)
	option := result[0].(map[string]any)
	if option["x"] != 0.2 || option["width"] != 0.1 {
		t.Fatalf("explicit question-relative coordinates were converted: %#v", option)
	}
}

func TestOMRAutoConfirmationRequiresServerEligibility(t *testing.T) {
	input := OMRResultInput{Decision: "selected", Selected: []string{"A"}, Confidence: 0.99, NeedsHumanReview: false}
	if omrResultCanAutoConfirm(input, false, 0.9) {
		t.Fatal("high worker confidence must not bypass the server eligibility snapshot")
	}
	if got := omrReviewReason(input, false, "manual_only_profile", 0.9); got != "omr_manual_only_profile" {
		t.Fatalf("manual-only result should have an actionable review reason, got %q", got)
	}
	if !omrResultCanAutoConfirm(input, true, 0.98) {
		t.Fatal("an eligible profile should retain the normal confidence gate")
	}
	if omrResultCanAutoConfirm(OMRResultInput{Decision: "selected", Selected: []string{"A", "B"}, Confidence: 1}, true, 0.98) {
		t.Fatal("multiple selected options must remain outside the calibrated single-choice auto-confirm branch")
	}
}

func TestOMRReviewReasonKeepsWorkerAmbiguity(t *testing.T) {
	input := OMRResultInput{Decision: "selected", Confidence: 0.99, NeedsHumanReview: true}
	if got := omrReviewReason(input, false, "manual_only_profile", 0.9); got != "omr_selected" {
		t.Fatalf("worker-requested review should keep the extraction reason, got %q", got)
	}
	input = OMRResultInput{Decision: "blank", Confidence: 0.1}
	if got := omrReviewReason(input, false, "manual_only_profile", 0.9); got != "omr_blank" {
		t.Fatalf("non-selected result should keep its extraction reason, got %q", got)
	}
}
