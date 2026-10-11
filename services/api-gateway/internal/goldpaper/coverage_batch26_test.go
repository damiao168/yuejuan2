package goldpaper

import (
	"reflect"
	"testing"
	"time"
)

func TestBatch26CoverageKeepsActiveApprovalBandsAndGapOrder(t *testing.T) {
	approved := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	items := []GoldPaper{{ActiveVersion: 2, Versions: []Version{
		{Version: 1, ApprovedAt: &approved, ReferenceScore: 5, TraitScores: map[string]any{"ignored": true}, ErrorTags: []string{"old"}},
		{Version: 2, ApprovedAt: &approved, ReferenceScore: 0, TraitScores: map[string]any{"trait": true}, ErrorTags: []string{"typical"}},
		{Version: 3, ReferenceScore: 2, ErrorTags: []string{"unapproved"}},
	}}}
	result := buildCoverage("exam", "question", "chinese", "extended_response", "R3", 5, items)
	wantGaps := []string{"missing_full_score", "missing_middle_score", "insufficient_trait_boundaries", "missing_pattern:content_strong_language_weak", "missing_pattern:language_strong_content_off_topic", "missing_pattern:structural_imbalance"}
	if result.Ready || result.ActiveApprovedCount != 1 || !reflect.DeepEqual(result.ScoreBands, []ScoreBandCoverage{{"zero", 1}, {"middle", 0}, {"full", 0}}) || !reflect.DeepEqual(result.ErrorTags, []string{"typical"}) || !reflect.DeepEqual(result.TraitPatterns, []string{"trait=true"}) || !reflect.DeepEqual(result.Gaps, wantGaps) {
		t.Fatalf("approval, facts or gap precedence changed: %#v", result)
	}
}

func TestBatch26CoverageKeepsSmallMaximumAndZeroPriority(t *testing.T) {
	approved := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	items := []GoldPaper{
		{ActiveVersion: 1, Versions: []Version{{Version: 1, ApprovedAt: &approved, ReferenceScore: 0, ErrorTags: []string{"typical"}}}},
		{ActiveVersion: 1, Versions: []Version{{Version: 1, ApprovedAt: &approved, ReferenceScore: 1, ErrorTags: []string{"typical"}}}},
	}
	result := buildCoverage("exam", "question", "general", "short_constructed", "R1", 1, items)
	if !result.Ready || len(result.Gaps) != 0 || result.ActiveApprovedCount != 2 || result.ScoreBands[1].Count != 0 {
		t.Fatalf("small maximum incorrectly requires a middle band: %#v", result)
	}
	zero := buildCoverage("exam", "question", "general", "short_constructed", "R1", 0, items[:1])
	if !reflect.DeepEqual(zero.Gaps, []string{"missing_full_score"}) || zero.ScoreBands[0].Count != 1 || zero.ScoreBands[2].Count != 0 {
		t.Fatalf("zero band must precede equal maximum: %#v", zero)
	}
}
