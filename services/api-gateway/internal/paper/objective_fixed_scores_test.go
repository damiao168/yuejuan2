package paper

import (
	"errors"
	"math"
	"testing"
)

func TestObjectiveFixedScoresSurviveGenerationAndConfirmation(t *testing.T) {
	question := confirmationQuestion("multiple_choice")
	question.AnswerKey.Tolerance = map[string]any{"partial_score": 2.0, "wrong_answer_score": 1.0}
	rule, err := objectiveRuleForConfirmation(question, nil)
	if err != nil || rule.Config["partial_score"] != 2.0 || rule.Config["wrong_answer_score"] != 1.0 {
		t.Fatalf("explicit scores lost before freezing: rule=%#v err=%v", rule, err)
	}
	question.AnswerKey.Tolerance = nil
	rule, err = objectiveRuleForConfirmation(question, nil)
	if err != nil || rule.Config["partial_score"] != 0 || rule.Config["wrong_answer_score"] != 0 {
		t.Fatalf("default fixed scores must both be zero: rule=%#v err=%v", rule, err)
	}
}

func TestObjectiveFixedScoreValidationRejectsMissingInvalidAndExcessiveValues(t *testing.T) {
	for _, config := range []map[string]any{
		{"partial_score": 2.0},
		{"partial_score": -1.0, "wrong_answer_score": 0.0},
		{"partial_score": 7.0, "wrong_answer_score": 0.0},
		{"partial_score": 2.0, "wrong_answer_score": math.Inf(1)},
		{"partial_score": "2", "wrong_answer_score": 0.0},
	} {
		if err := validateObjectiveConfirmationConfig("multiple_choice", config, 6); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid fixed score accepted: config=%#v err=%v", config, err)
		}
	}
	// 用户明确切换后，历史按项字段不再决定固定规则是否有效。
	if err := validateObjectiveConfirmationConfig("multiple_choice", map[string]any{
		"partial_score": 2.0, "wrong_answer_score": 0.0, "score_per_correct_option": -1.0,
	}, 6); err != nil {
		t.Fatal(err)
	}
}
