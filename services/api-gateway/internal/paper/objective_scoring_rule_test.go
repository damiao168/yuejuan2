package paper

import "testing"

func TestObjectiveRuleConfigPreservesReviewedAnswerPolicy(t *testing.T) {
	draft := PaperImportDraftQuestion{
		QuestionType: "multiple_choice", Score: 6,
		AnswerKey: &AnswerKeyInput{StandardAnswer: []any{"A", "C"}, Tolerance: map[string]any{
			"allow_partial": true, "score_per_correct_option": 2.0, "wrong_option_penalty": 1.0,
			"minimum_score": 0.0, "unrelated_field": "ignored",
		}},
	}
	config, ok := objectiveRuleConfig(draft)
	if !ok || config["allow_partial"] != true || config["score_per_correct_option"] != 2.0 || config["wrong_option_penalty"] != 1.0 {
		t.Fatalf("reviewed multiple-choice policy was replaced by defaults: %#v", config)
	}
	if _, ok := config["unrelated_field"]; ok {
		t.Fatalf("unrelated answer metadata copied to scoring rule: %#v", config)
	}
	draft.QuestionType = "fill_blank"
	draft.AnswerKey.Tolerance = map[string]any{"ignore_spaces": true, "ignore_case": true}
	config, ok = objectiveRuleConfig(draft)
	if !ok || config["ignore_spaces"] != true || config["ignore_case"] != true || config["ignore_punctuation"] != false {
		t.Fatalf("fill-blank normalization settings lost: %#v", config)
	}
}

func TestObjectiveRuleConfigRequiresAnswerAndScore(t *testing.T) {
	for _, draft := range []PaperImportDraftQuestion{
		{QuestionType: "single_choice", Score: 5},
		{QuestionType: "single_choice", Score: 0, AnswerKey: &AnswerKeyInput{StandardAnswer: "A"}},
		{QuestionType: "calculation", Score: 10, AnswerKey: &AnswerKeyInput{StandardAnswer: "42"}},
	} {
		if config, ok := objectiveRuleConfig(draft); ok {
			t.Fatalf("ineligible question created a fixed scoring rule: %#v", config)
		}
	}
}
