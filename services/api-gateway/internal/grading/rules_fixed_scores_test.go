package grading

import (
	"math"
	"testing"
)

func TestFixedChoiceRuleValidationChecksBothScoresAndMaximum(t *testing.T) {
	for _, config := range []map[string]any{
		{"max_score": 6, "partial_score": 2},
		{"max_score": 6, "partial_score": 7, "wrong_answer_score": 0},
		{"max_score": 6, "partial_score": 2, "wrong_answer_score": -1},
		{"max_score": 6, "partial_score": 2, "wrong_answer_score": math.NaN()},
	} {
		if err := validateRuleConfig("multiple_choice", config); err != ErrInvalidInput {
			t.Fatalf("invalid fixed config accepted: %#v, %v", config, err)
		}
	}
	if err := validateRuleConfig("multiple_choice", map[string]any{
		"max_score": 6, "partial_score": 2, "wrong_answer_score": 0, "wrong_option_penalty": -1,
	}); err != nil {
		t.Fatalf("inactive legacy fields affected fixed scores: %v", err)
	}
}
