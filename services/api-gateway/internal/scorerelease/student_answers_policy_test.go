package scorerelease

import (
	"context"
	"errors"
	"testing"
)

func TestLegacyPublishedAnswersUseFrozenValuesWithoutChangingOtherVisibility(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	service := NewService(store)
	fact := fixtureFact(4)
	fact.Questions[0].Explanation.QuestionType = "fill_blank"
	fact.Questions[0].Explanation.Stem = "冻结题干"
	fact.Questions[0].Explanation.CorrectAnswer = "1/2"
	fact.Questions[0].Explanation.ActualAnswer = "0.5"
	store.SeedFacts(exam, []SubmissionFact{fact})
	first, err := service.Create(ctx, tenant, exam, actor, CreateInput{
		Reason: "answers accompany published questions", IdempotencyKey: "legacy-published-answers",
		VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true, ShowAnswers: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first.VisibilityPolicy.ShowAnswers {
		t.Fatal("new releases must normalize a disabled answer switch")
	}
	if _, err = service.Publish(ctx, tenant, first.ID, actor); err != nil {
		t.Fatal(err)
	}
	// 模拟数据库中仍保留 false 的历史发布；兼容读取不能改写旧策略或改用当前工作表的答案。
	store.mu.Lock()
	legacy := store.releases[first.ID]
	legacy.VisibilityPolicy.ShowAnswers = false
	store.releases[first.ID] = legacy
	store.mu.Unlock()
	changed := fixtureFact(5)
	changed.Questions[0].Explanation.CorrectAnswer = "new mutable standard"
	changed.Questions[0].Explanation.ActualAnswer = "new mutable answer"
	store.SeedFacts(exam, []SubmissionFact{changed})
	if _, err = service.Create(ctx, tenant, exam, actor, CreateInput{
		Reason: "unpublished working version", IdempotencyKey: "unpublished-new-answers",
		VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := service.StudentResult(ctx, tenant, exam, "student-1")
	if err != nil || result.ReleaseID != first.ID || len(result.Questions) != 1 {
		t.Fatalf("student must retain the published version: %#v, %v", result, err)
	}
	question, err := service.StudentQuestion(ctx, tenant, exam, "student-1", "question-1", first.ID)
	if err != nil || question.CorrectAnswer != "1/2" || question.ActualAnswer != "0.5" {
		t.Fatalf("legacy answers must come from the frozen release: %#v, %v", question, err)
	}
	if question.Stem != "" || question.Feedback != "" || len(question.RubricSummary) != 0 {
		t.Fatalf("answer visibility must not enable other content: %#v", question)
	}
	if result.Questions[0].CorrectAnswer != question.CorrectAnswer || result.Questions[0].ActualAnswer != question.ActualAnswer {
		t.Fatalf("table and question detail must share frozen answers: %#v", result.Questions[0])
	}
	unchanged, err := service.Get(ctx, tenant, first.ID)
	if err != nil || unchanged.Release.VisibilityPolicy.ShowAnswers || unchanged.Questions[0].Explanation.CorrectAnswer != "1/2" {
		t.Fatalf("legacy release data must remain unchanged: %#v, %v", unchanged, err)
	}
	for _, scope := range [][2]string{{"other-tenant", "student-1"}, {tenant, "other-student"}} {
		if _, err = service.StudentResult(ctx, scope[0], exam, scope[1]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign student release must remain unavailable: %v", err)
		}
	}
	rollback, err := service.CreateRollback(ctx, tenant, exam, actor, RollbackInput{
		SourceReleaseID: first.ID, Reason: "copy legacy snapshot", IdempotencyKey: "legacy-answer-rollback",
	})
	if err != nil || !rollback.VisibilityPolicy.ShowAnswers {
		t.Fatalf("copied legacy releases must also normalize answer visibility: %#v, %v", rollback, err)
	}
	// 独立答案开关取消后，隐藏全部逐题成绩仍不能通过单题接口取回答案。
	store.mu.Lock()
	legacy.VisibilityPolicy.ShowQuestionScores = false
	store.releases[first.ID] = legacy
	store.mu.Unlock()
	hidden, err := service.StudentResult(ctx, tenant, exam, "student-1")
	if err != nil || hidden.Questions != nil {
		t.Fatalf("hidden questions must remain absent: %#v, %v", hidden, err)
	}
	if _, err = service.StudentQuestion(ctx, tenant, exam, "student-1", "question-1", first.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("hidden question details must remain forbidden: %v", err)
	}
}
