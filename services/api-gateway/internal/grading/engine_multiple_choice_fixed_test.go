package grading_test

import (
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/grading"
)

func TestMultipleChoiceFixedScoresDistinguishCompleteSubsetAndWrongOptions(t *testing.T) {
	for _, test := range []struct {
		answer string
		want   float64
	}{
		{"D,A,C", 6}, {"A", 2}, {"A,C", 2}, {"A,B", 1}, {"B", 1},
	} {
		t.Run(test.answer, func(t *testing.T) {
			// 旧字段刻意保留冲突值：明确切换到固定得分后，不再叠加每项分数和罚分。
			ctx := gradingContext("multiple_choice", []any{"A", "C", "D"}, nil, map[string]any{
				"partial_score": 2.0, "wrong_answer_score": 1.0,
				"allow_partial": true, "score_per_correct_option": 3.0, "wrong_option_penalty": 6.0,
			}, test.answer, nil)
			ctx.Question.Score = 6
			grade, err := grading.NewEngine().Grade(ctx)
			if err != nil || grade.SuggestedScore != test.want || !grade.AutoPass {
				t.Fatalf("answer=%s score=%v want=%v review=%v error=%v", test.answer, grade.SuggestedScore, test.want, grade.NeedsHumanReview, err)
			}
		})
	}
}

func TestMultipleChoiceInvalidFixedScoreDoesNotFallBackToLegacyRules(t *testing.T) {
	for _, config := range []map[string]any{
		{"partial_score": 2.0},
		{"partial_score": 7.0, "wrong_answer_score": 0.0},
		{"partial_score": 2.0, "wrong_answer_score": -1.0},
	} {
		ctx := gradingContext("multiple_choice", []any{"A", "C"}, nil, config, "A", nil)
		ctx.Question.Score = 6
		grade, err := grading.NewEngine().Grade(ctx)
		if err != nil || grade.AutoPass || !grade.NeedsHumanReview {
			t.Fatalf("invalid fixed policy silently graded: grade=%#v error=%v", grade, err)
		}
	}
}
