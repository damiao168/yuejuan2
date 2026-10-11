package appeal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPublishedQuestionAppealPinsReleaseFactAndRequiresRegradeRelease(t *testing.T) {
	store := NewPublishedQuestionAppealMemoryStore()
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	store.SetNow(func() time.Time { return now })
	store.SeedSource(PublishedQuestionAppealSource{
		TenantID: "tenant-1", ExamID: "exam-1", StudentID: "student-1", SubmissionID: "submission-1",
		ReleaseID: "release-1", ReleaseVersion: 1, Published: true, AppealEnabled: true,
		AllowedReasonCodes: []string{ReasonMissingStepCredit}, QuestionID: "question-4", QuestionNo: "Q4",
		FinalGradeID: "final-grade-original", Score: 4, MaxScore: 8,
	})
	service := NewPublishedQuestionAppealService(store)

	item, err := service.Create(context.Background(), "tenant-1", "student-1", "student-user-1", CreatePublishedQuestionAppealInput{
		ExamID: "exam-1", SourceReleaseID: "release-1", QuestionID: "question-4", ReasonCode: ReasonMissingStepCredit,
		Reason: "第二步的过程分没有计入", SelectedRegion: map[string]any{"coordinate_space": "canonical_image_normalized", "x": 0.2, "y": 0.4, "width": 0.3, "height": 0.2},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if item.SourceScore != 4 || item.SourceReleaseVersion != 1 || item.SourceFinalGradeID != "final-grade-original" {
		t.Fatalf("appeal did not freeze published source fact: %#v", item)
	}

	// 来源夹具现在表示另一个后续或当前分数；已提交的申诉必须保留学生申诉时的事实。
	store.SeedSource(PublishedQuestionAppealSource{
		TenantID: "tenant-1", ExamID: "exam-1", StudentID: "student-1", SubmissionID: "submission-1",
		ReleaseID: "release-1", ReleaseVersion: 1, Published: true, AppealEnabled: true,
		AllowedReasonCodes: []string{ReasonMissingStepCredit}, QuestionID: "question-4", QuestionNo: "Q4",
		FinalGradeID: "final-grade-current", Score: 7, MaxScore: 8,
	})
	got, err := service.Get(context.Background(), "tenant-1", item.ID)
	if err != nil || got.SourceScore != 4 || got.SourceFinalGradeID != "final-grade-original" {
		t.Fatalf("frozen source changed: item=%#v err=%v", got, err)
	}

	item, err = service.StartReview(context.Background(), "tenant-1", item.ID, "appeal-manager", StartQuestionAppealReviewInput{AssignedTo: "reviewer-1", ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatalf("start review: %v", err)
	}
	if _, err := service.Decide(context.Background(), "tenant-1", item.ID, "appeal-manager", DecideQuestionAppealInput{
		Decision: QuestionAppealDecisionReferRegrade, PublicResponse: "将进入复核流程", RegradeJobID: "unrelated-job", ExpectedRevision: item.Revision,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unlinked regrade job error = %v, want invalid input", err)
	}

	reviewedScore := 7.0
	store.SeedRegradeJob("regrade-1", "tenant-1", "exam-1", "question-4", "release-1", PublishedQuestionAppealRegradeItemSource{
		SubmissionID: "submission-1", OldFinalGradeID: "final-grade-original", OldScore: 4, MaxScore: 8,
		ReviewedScore: &reviewedScore, ReviewedGradeID: "reviewed-grade-1", Status: "resolved",
	})
	item, err = service.Decide(context.Background(), "tenant-1", item.ID, "appeal-manager", DecideQuestionAppealInput{
		Decision: QuestionAppealDecisionReferRegrade, PublicResponse: "将进入复核流程", PrivateNote: "staff only", RegradeJobID: "regrade-1", ExpectedRevision: item.Revision,
	})
	if err != nil || item.Status != QuestionAppealUpheldPendingRegrade {
		t.Fatalf("refer regrade: item=%#v err=%v", item, err)
	}
	if _, err := service.Resolve(context.Background(), "tenant-1", item.ID, "appeal-manager", ResolveQuestionAppealInput{NewReleaseID: "release-1", ExpectedRevision: item.Revision}); !errors.Is(err, ErrResolutionRelease) {
		t.Fatalf("same release resolution error = %v, want resolution release", err)
	}
	store.SeedRegradeJobState("regrade-1", "ready_for_release", &now)
	store.SeedSource(PublishedQuestionAppealSource{
		TenantID: "tenant-1", ExamID: "exam-1", StudentID: "student-1", SubmissionID: "submission-1",
		ReleaseID: "release-2", ReleaseVersion: 2, Published: true, ReleaseSource: "regrade", SourceReleaseID: "release-1", PublishedAt: &now,
		QuestionID: "question-4", QuestionNo: "Q4", FinalGradeID: "final-grade-original", Score: 7, MaxScore: 8,
		SourceType: "single_review", SourceID: "reviewed-grade-1",
	})
	item, err = service.Resolve(context.Background(), "tenant-1", item.ID, "appeal-manager", ResolveQuestionAppealInput{NewReleaseID: "release-2", PublicResponse: "已在新版成绩中处理", ExpectedRevision: item.Revision})
	if err != nil || item.Status != QuestionAppealResolved || item.NewReleaseID != "release-2" {
		t.Fatalf("resolve: item=%#v err=%v", item, err)
	}
	student := studentQuestionAppealView(item)
	if student.PublicResponse == "" || student.NewReleaseID != "release-2" || student.NewReleaseVersion != 2 || student.ID == "" {
		t.Fatalf("student view misses outcome: %#v", student)
	}
	encoded, err := json.Marshal(student)
	if err != nil {
		t.Fatalf("marshal student view: %v", err)
	}
	if string(encoded) == "" || strings.Contains(string(encoded), "staff only") || strings.Contains(string(encoded), "private_note") {
		t.Fatalf("student DTO leaked private resolution detail: %s", encoded)
	}
}

func TestPublishedQuestionAppealRejectsInvalidSelectedRegion(t *testing.T) {
	store := NewPublishedQuestionAppealMemoryStore()
	store.SeedSource(PublishedQuestionAppealSource{
		TenantID: "tenant-1", ExamID: "exam-1", StudentID: "student-1", SubmissionID: "submission-1",
		ReleaseID: "release-1", ReleaseVersion: 1, Published: true, AppealEnabled: true,
		QuestionID: "question-1", QuestionNo: "Q1", FinalGradeID: "grade-1", Score: 2, MaxScore: 4,
	})
	service := NewPublishedQuestionAppealService(store)
	_, err := service.Create(context.Background(), "tenant-1", "student-1", "student-user-1", CreatePublishedQuestionAppealInput{
		ExamID: "exam-1", SourceReleaseID: "release-1", QuestionID: "question-1", ReasonCode: ReasonCalculationError, Reason: "计算结果有误",
		SelectedRegion: map[string]any{"coordinate_space": "canonical_image_normalized", "x": 0.9, "y": 0.2, "width": 0.2, "height": 0.2},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid selected region error = %v, want ErrInvalidInput", err)
	}
}

func TestWorkerQuestionAppealContextDoesNotExposeInternalOrStudentIdentifiers(t *testing.T) {
	context := workerQuestionAppealContextView(PublishedQuestionAppealContext{
		Appeal: PublishedQuestionAppeal{
			StudentID: "student-1", SubmissionID: "submission-1", RegradeJobID: "regrade-1", NewReleaseID: "release-2",
			CreatedBy: "student-user-1", DecidedBy: "manager-1",
		},
		ReleaseHistory: []QuestionAppealReleaseVersion{{ID: "release-1", Version: 1}},
	})
	if context.Appeal.StudentID != "" || context.Appeal.SubmissionID != "" || context.Appeal.RegradeJobID != "" || context.Appeal.NewReleaseID != "" || context.Appeal.CreatedBy != "" || context.Appeal.DecidedBy != "" || context.ReleaseHistory[0].ID != "" {
		t.Fatalf("worker context leaked protected identifiers: %#v", context)
	}
}

func TestPublishedQuestionAppealRejectsClosedOrDisallowedReleaseWindow(t *testing.T) {
	store := NewPublishedQuestionAppealMemoryStore()
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	store.SetNow(func() time.Time { return now })
	store.SeedSource(PublishedQuestionAppealSource{
		TenantID: "tenant-1", ExamID: "exam-1", StudentID: "student-1", SubmissionID: "submission-1",
		ReleaseID: "release-1", ReleaseVersion: 1, Published: true, AppealEnabled: true,
		AppealClosesAt: ptrPublishedQuestionAppealTime(now), AllowedReasonCodes: []string{ReasonCalculationError},
		QuestionID: "question-1", QuestionNo: "Q1", FinalGradeID: "grade-1", Score: 2, MaxScore: 4,
	})
	service := NewPublishedQuestionAppealService(store)
	_, err := service.Create(context.Background(), "tenant-1", "student-1", "student-user-1", CreatePublishedQuestionAppealInput{
		ExamID: "exam-1", SourceReleaseID: "release-1", QuestionID: "question-1", ReasonCode: ReasonCalculationError, Reason: "计算结果有误",
	})
	if !errors.Is(err, ErrAppealWindowClosed) {
		t.Fatalf("closed appeal window error = %v", err)
	}
}

func ptrPublishedQuestionAppealTime(value time.Time) *time.Time { return &value }
