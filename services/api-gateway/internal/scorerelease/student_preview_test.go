package scorerelease

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStudentPreviewReadsChosenFrozenDraftWithoutPublishingOrLeaking(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	service := NewService(store)
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	first, err := service.Create(ctx, tenant, exam, actor, CreateInput{Reason: "first", IdempotencyKey: "preview-first-1", VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, tenant, first.ID, actor); err != nil {
		t.Fatal(err)
	}
	fact := fixtureFact(5)
	fact.Questions[0].Explanation.Stem = "冻结题干"
	fact.Questions[0].Explanation.CorrectAnswer = "隐藏答案"
	store.SeedFacts(exam, []SubmissionFact{fact})
	draft, err := service.Create(ctx, tenant, exam, actor, CreateInput{Reason: "preview", IdempotencyKey: "preview-draft-2", VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true, ShowQuestionStem: true}})
	if err != nil {
		t.Fatal(err)
	}
	// 预览后不能让学生读到草稿，也不能将管理员可见的内部来源序列化到学生投影。
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(1)})
	preview, err := service.StudentPreview(ctx, tenant, draft.ID, "student-1")
	if err != nil || preview.TotalScore != 5 || preview.ReleaseID != draft.ID || preview.Questions[0].Stem != "冻结题干" {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	if preview.Questions[0].CorrectAnswer != "" || preview.Questions[0].Feedback != "" || len(preview.Questions[0].RubricSummary) != 0 {
		t.Fatalf("hidden fields leaked: %#v", preview.Questions[0])
	}
	encoded, _ := json.Marshal(preview)
	for _, forbidden := range []string{"final_grade_id", "source_id", "private_note", "reviewer", "prompt", "snapshot_hash"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("preview leaked %s", forbidden)
		}
	}
	current, err := service.StudentResult(ctx, tenant, exam, "student-1")
	if err != nil || current.ReleaseID != first.ID || current.TotalScore != 4 {
		t.Fatalf("preview changed current release: %#v, %v", current, err)
	}
	unchanged, err := service.Get(ctx, tenant, draft.ID)
	if err != nil || unchanged.Release.Status != StatusDraft {
		t.Fatalf("preview published draft: %#v, %v", unchanged, err)
	}
	for _, ids := range [][2]string{{"other-tenant", "student-1"}, {tenant, "other-student"}} {
		if _, err := service.StudentPreview(ctx, ids[0], draft.ID, ids[1]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign preview should fail: %v", err)
		}
	}
}

func TestStudentPreviewHidesQuestionsAndSmallCohort(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	release, err := service.Create(context.Background(), tenant, exam, actor, CreateInput{Reason: "restricted", IdempotencyKey: "preview-restricted", VisibilityPolicy: VisibilityPolicy{ShowCohortStatistics: true, ShowExactRank: true}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.StudentPreview(context.Background(), tenant, release.ID, "student-1")
	if err != nil || result.Questions != nil || result.Rankings == nil || result.Rankings.ClassRank != 0 || result.Rankings.GradeRank != 0 || result.Rankings.ClassUnavailableReason != "small_cohort" || result.Reference == nil || result.Reference.StatisticsAvailable {
		t.Fatalf("restricted preview=%#v err=%v", result, err)
	}
}
