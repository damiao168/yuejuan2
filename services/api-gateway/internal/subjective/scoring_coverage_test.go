package subjective_test

import (
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
)

// TestAIAssistedQuestionTypesMatchAdapter 防止准备度提示发生偏移。
// paper.HasAutomatedScoringPath 告知考试负责人哪些题目完全由人工评分；此适配器决定哪些题目实际获得 AI 建议。
func TestAIAssistedQuestionTypesMatchAdapter(t *testing.T) {
	for _, questionType := range paper.AIAssistedQuestionTypes() {
		if !subjective.IsSupportedQuestionType(questionType) {
			t.Fatalf("%q is advertised as AI assisted but the adapter rejects it", questionType)
		}
	}
	for _, questionType := range paper.ManualOnlyQuestionTypes() {
		if subjective.IsSupportedQuestionType(questionType) {
			t.Fatalf("%q is advertised as human graded but the adapter accepts it", questionType)
		}
	}
	for _, questionType := range paper.RuleGradedQuestionTypes() {
		if subjective.IsSupportedQuestionType(questionType) {
			t.Fatalf("%q is rule graded and must not also enter the subjective path", questionType)
		}
	}
}
