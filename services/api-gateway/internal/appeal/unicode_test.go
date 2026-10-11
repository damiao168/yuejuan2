package appeal

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPublishedQuestionAppealTextLimitsCountRunes(t *testing.T) {
	for _, char := range []string{"a", "你", "🙂"} {
		atLimit := "  " + strings.Repeat(char, 2000) + "  "
		overLimit := strings.Repeat(char, 2001)
		assertAppealReasonRuneBoundary(t, char, atLimit, overLimit)
		assertAppealDecisionRuneBoundary(t, char, atLimit, overLimit)
		assertAppealResolveRuneBoundary(t, char)
	}
}

// 三种字符都按码点计数；去掉两端空白以后，2000 个字符仍应处在理由上限内。
func assertAppealReasonRuneBoundary(t *testing.T, char, atLimit, overLimit string) {
	t.Helper()
	input := CreatePublishedQuestionAppealInput{ExamID: "exam-1", SourceReleaseID: "release-1", QuestionID: "question-1", ReasonCode: ReasonCalculationError, Reason: atLimit}
	if !validPublishedQuestionAppealCreate(input) {
		t.Fatalf("valid reason char=%s rejected", char)
	}
	input.Reason = overLimit
	if validPublishedQuestionAppealCreate(input) {
		t.Fatalf("long reason char=%s accepted", char)
	}
}

func assertAppealDecisionRuneBoundary(t *testing.T, char, atLimit, overLimit string) {
	t.Helper()
	decision := DecideQuestionAppealInput{Decision: QuestionAppealDecisionReject, PublicResponse: atLimit, PrivateNote: strings.Repeat(char, 4000)}
	if !validPublishedQuestionAppealDecision(decision) {
		t.Fatalf("valid decision char=%s rejected", char)
	}
	decision.PrivateNote += char
	if validPublishedQuestionAppealDecision(decision) {
		t.Fatalf("long private note char=%s accepted", char)
	}
	decision.PrivateNote = ""
	decision.PublicResponse = overLimit
	if validPublishedQuestionAppealDecision(decision) {
		t.Fatalf("long public response char=%s accepted", char)
	}
}

func assertAppealResolveRuneBoundary(t *testing.T, char string) {
	t.Helper()
	service := NewPublishedQuestionAppealService(NewPublishedQuestionAppealMemoryStore())
	for _, size := range []int{2000, 2001} {
		_, err := service.Resolve(context.Background(), "tenant-1", "missing", "manager-1", ResolveQuestionAppealInput{NewReleaseID: "release-2", PublicResponse: strings.Repeat(char, size), ExpectedRevision: 1})
		if errors.Is(err, ErrInvalidInput) != (size == 2001) {
			t.Fatalf("resolve char=%s size=%d err=%v", char, size, err)
		}
	}
}
