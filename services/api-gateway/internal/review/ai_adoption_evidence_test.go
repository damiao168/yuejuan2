package review

import "testing"

func TestRecordedAIAdoptionRequiresExplicitCurrentModelChoice(t *testing.T) {
	const id = "suggestion"
	for _, tc := range []struct {
		name, kind, mode string
		adoption         *AIAdoption
		score            float64
		want             bool
	}{
		{"accepted", "llm_subjective", "teacher_suggestion", &AIAdoption{AIGradeID: id, Accepted: true}, 4, true},
		{"same score without explicit adoption", "llm_subjective", "teacher_suggestion", &AIAdoption{AIGradeID: id}, 4, true},
		{"historical client", "llm_subjective", "teacher_suggestion", nil, 4, false},
		{"answer changed", "llm_subjective", "teacher_suggestion", &AIAdoption{AIGradeID: "outdated", Accepted: true}, 4, false},
		{"score changed", "llm_subjective", "teacher_suggestion", &AIAdoption{AIGradeID: id, Accepted: true}, 3, false},
		{"shadow", "llm_subjective", "shadow_only", &AIAdoption{AIGradeID: id, Accepted: true}, 4, false},
		{"rule", "rule_based_objective", "teacher_suggestion", &AIAdoption{AIGradeID: id, Accepted: true}, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := Context{AISuggestion: map[string]any{"ai_grade_id": id, "grader_type": tc.kind, "delivery_mode": tc.mode, "status": "succeeded", "mock": false, "suggested_score": 4.0}}
			got := recordedAIAdoption(SubmitGradeInput{Score: tc.score, AIAdoption: tc.adoption}, ctx)
			if (got != nil) != tc.want || (got != nil && *got != *tc.adoption) {
				t.Fatalf("unexpected persisted evidence: %+v", got)
			}
		})
	}
}
