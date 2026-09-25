package subjective_test

import (
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
)

// TestAIAssistedQuestionTypesMatchAdapter guards the readiness advisory against
// drift. paper.HasAutomatedScoringPath tells the exam owner which questions are
// 100% human graded; this adapter decides which ones actually get an AI
// suggestion.
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
