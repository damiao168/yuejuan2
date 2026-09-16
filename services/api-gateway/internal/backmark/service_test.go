package backmark

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/review"
)

func TestBackmarkListsAreCursorPaginated(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	for index := 0; index < DefaultPageSize+5; index++ {
		id := fmt.Sprintf("task-%03d", index)
		store.SeedSource(SourceTask{ReviewTaskID: id, OriginalGradeID: "grade-" + id, OriginalReviewer: "grader-a", OriginalScore: 1, MaxScore: 2, GradedAt: now})
	}
	service := NewService(store).WithContextSource(backmarkContextSource{})
	summary, err := service.Create(context.Background(), "tenant-1", "exam-1", "question-1", "manager-1", CreateInput{
		SourceIncidentID: "incident-page", ReassignedTo: "grader-b", Policy: Policy{Disposition: DispositionConfirm},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Get(context.Background(), "tenant-1", summary.Batch.ID)
	if err != nil || len(first.Items) != DefaultPageSize {
		t.Fatalf("first page items=%d err=%v", len(first.Items), err)
	}
	last := first.Items[len(first.Items)-1]
	second, err := service.GetPage(context.Background(), "tenant-1", summary.Batch.ID, PageOptions{
		Limit: DefaultPageSize, CursorCreatedAt: last.CreatedAt, CursorID: last.ID,
	})
	if err != nil || len(second.Items) != 5 {
		t.Fatalf("second page items=%d err=%v", len(second.Items), err)
	}
	if second.Items[0].ID == first.Items[0].ID {
		t.Fatal("cursor page repeated an item")
	}
	assigned, err := service.ListAssigned(context.Background(), "tenant-1", "grader-b")
	if err != nil || len(assigned) != DefaultPageSize {
		t.Fatalf("assigned first page items=%d err=%v", len(assigned), err)
	}
}

func TestBackmarkRejectsOversizedExplicitSelector(t *testing.T) {
	ids := make([]string, MaxSelectorTaskIDs+1)
	for index := range ids {
		ids[index] = fmt.Sprintf("task-%d", index)
	}
	_, err := NewService(NewMemoryStore()).Preview(context.Background(), "tenant-1", "exam-1", "question-1", Selector{TaskIDs: ids})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized selector error=%v", err)
	}
}

func TestBackmarkSeparatesCandidateGradeFromOriginalFact(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	store.SeedSource(SourceTask{ReviewTaskID: "task-1", OriginalGradeID: "grade-1", OriginalReviewer: "grader-a", OriginalScore: 4, MaxScore: 5, GradedAt: now.Add(-time.Hour)})
	service := NewService(store).WithContextSource(backmarkContextSource{})

	preview, err := service.Preview(context.Background(), "tenant-1", "exam-1", "question-1", Selector{GraderID: "grader-a"})
	if err != nil || preview.AffectedCount != 1 || len(preview.ScoreBands) != 1 || preview.ScoreBands[0].Score != 4 {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	summary, err := service.Create(context.Background(), "tenant-1", "exam-1", "question-1", "manager-1", CreateInput{
		SourceIncidentID: "incident-1", Selector: Selector{TaskIDs: []string{"task-1"}},
		Policy: Policy{Disposition: DispositionRegrade}, ReassignedTo: "grader-b",
	})
	if err != nil || len(summary.Items) != 1 || summary.Items[0].OriginalGradeID != "grade-1" {
		t.Fatalf("create = %#v, %v", summary, err)
	}
	item, err := service.Claim(context.Background(), "tenant-1", summary.Items[0].ID, "grader-b")
	if err != nil || item.Status != ItemInProgress {
		t.Fatalf("claim = %#v, %v", item, err)
	}
	assigned, err := service.ListAssigned(context.Background(), "tenant-1", "grader-b")
	if err != nil || len(assigned) != 1 || assigned[0].ID != item.ID || assigned[0].MaxScore != 5 {
		t.Fatalf("assigned view = %#v, %v", assigned, err)
	}
	item, grade, err := service.Submit(context.Background(), "tenant-1", item.ID, "grader-b", SubmitInput{Score: 2, ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if grade.ID == "grade-1" || item.NewGradeID != grade.ID || item.Status != ItemRegradeRequired || item.Diff == nil || *item.Diff != -2 {
		t.Fatalf("unsafe candidate result: item=%#v grade=%#v", item, grade)
	}
	// The original source remains an immutable selection fact; only a separate
	// backmark_grade is written until an explicit later regrade/release path.
	selected, err := store.SelectSourceTasks(context.Background(), "tenant-1", "exam-1", "question-1", Selector{TaskIDs: []string{"task-1"}}, MaxSynchronousItems)
	if err != nil || selected[0].OriginalGradeID != "grade-1" || selected[0].OriginalScore != 4 {
		t.Fatalf("source fact mutated: %#v, %v", selected, err)
	}
	loaded, err := service.Get(context.Background(), "tenant-1", summary.Batch.ID)
	if err != nil || loaded.Batch.Status != BatchReadyForConfirmation || len(loaded.Histogram) != 1 || loaded.Histogram[0].Delta != -2 {
		t.Fatalf("summary = %#v, %v", loaded, err)
	}
}

func TestBackmarkRejectsOriginalGrader(t *testing.T) {
	store := NewMemoryStore()
	store.SeedSource(SourceTask{ReviewTaskID: "task-1", OriginalGradeID: "grade-1", OriginalReviewer: "grader-a", OriginalScore: 1, MaxScore: 2, GradedAt: time.Now().UTC()})
	_, err := NewService(store).Create(context.Background(), "tenant-1", "exam-1", "question-1", "manager-1", CreateInput{
		SourceIncidentID: "incident-1", ReassignedTo: "grader-a", Policy: Policy{}, Selector: Selector{},
	})
	if !errors.Is(err, ErrOriginalGrader) {
		t.Fatalf("expected ErrOriginalGrader, got %v", err)
	}
}

func TestBackmarkGraderContextIsBlindAndUsesFrozenEvidence(t *testing.T) {
	store := NewMemoryStore()
	store.SeedSource(SourceTask{ReviewTaskID: "source-task", OriginalGradeID: "old-grade", OriginalReviewer: "grader-a", OriginalScore: 4, MaxScore: 5, GradedAt: time.Now().UTC()})
	service := NewService(store).WithContextSource(backmarkContextSource{context: review.TaskContext{
		Question:       paper.Question{ID: "question-1", QuestionNo: "Q1", QuestionType: "extended_response", Score: 5, Stem: "Explain."},
		FrozenRubric:   paper.Rubric{ID: "rubric-1", QuestionID: "question-1", MaxScore: 5, Points: []paper.RubricPoint{{ID: "point-1", Description: "reasoning", Score: 5}}},
		AnswerArtifact: review.AnswerArtifact{AnswerSegmentID: "segment-secret", RawAnswer: "student response", Status: "completed"},
	}})
	summary, err := service.Create(context.Background(), "tenant-1", "exam-1", "question-1", "manager-1", CreateInput{
		SourceIncidentID: "incident-1", ReassignedTo: "grader-b", Policy: Policy{Disposition: DispositionConfirm},
	})
	if err != nil {
		t.Fatal(err)
	}
	contextValue, err := service.GetGraderContext(context.Background(), "tenant-1", summary.Items[0].ID, "grader-b")
	if err != nil {
		t.Fatal(err)
	}
	if contextValue.Question.QuestionNo != "Q1" || contextValue.FrozenRubric.ID != "rubric-1" || contextValue.Answer.RawAnswer != "student response" {
		t.Fatalf("missing independent scoring facts: %#v", contextValue)
	}
	if contextValue.Item.ReviewTaskID != "source-task" || contextValue.Answer.SegmentImageURL == "" || contextValue.ExpectedRevision != 1 {
		t.Fatalf("invalid grader work surface: %#v", contextValue)
	}
	if got, err := service.GetSegmentID(context.Background(), "tenant-1", summary.Items[0].ID, "grader-b"); err != nil || got != "segment-secret" {
		t.Fatalf("segment bridge = %q, %v", got, err)
	}
	claimed, err := service.Claim(context.Background(), "tenant-1", summary.Items[0].ID, "grader-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Submit(context.Background(), "tenant-1", claimed.ID, "grader-b", SubmitInput{Score: 3, RubricSelections: []RubricSelection{{PointID: "point-1", Score: 3}}, ExpectedRevision: claimed.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetSegmentID(context.Background(), "tenant-1", summary.Items[0].ID, "grader-b"); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("finished item image error = %v", err)
	}
	if _, err := service.GetGraderContext(context.Background(), "tenant-1", summary.Items[0].ID, "grader-a"); !errors.Is(err, ErrAssigneeForbidden) {
		t.Fatalf("original grader context error = %v", err)
	}
}

type backmarkContextSource struct{ context review.TaskContext }

func (s backmarkContextSource) GetTaskContext(context.Context, string, string) (review.TaskContext, error) {
	return s.context, nil
}

type backmarkTaskSource map[string]review.ReviewTask

func (s backmarkTaskSource) GetTask(_ context.Context, _ string, id string) (review.ReviewTask, error) {
	value, ok := s[id]
	if !ok {
		return review.ReviewTask{}, review.ErrNotFound
	}
	return value, nil
}

func TestBackmarkRegradeSelectionRequiresCompletedBatchAndIncludesOnlyRegradeItems(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	store.SeedSource(SourceTask{ReviewTaskID: "task-regrade", OriginalGradeID: "grade-regrade", OriginalReviewer: "grader-a", OriginalScore: 4, MaxScore: 5, GradedAt: now})
	store.SeedSource(SourceTask{ReviewTaskID: "task-same-score", OriginalGradeID: "grade-same", OriginalReviewer: "grader-a", OriginalScore: 3, MaxScore: 5, GradedAt: now})
	service := NewService(store).WithContextSource(backmarkContextSource{}).WithTaskSource(backmarkTaskSource{
		"task-regrade":    {SubmissionID: "submission-1"},
		"task-same-score": {SubmissionID: "submission-2"},
	})
	summary, err := service.Create(context.Background(), "tenant-1", "exam-1", "question-1", "manager-1", CreateInput{
		SourceIncidentID: "incident-1", ReassignedTo: "grader-b", Policy: Policy{Disposition: DispositionRegrade},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.RegradeSelection(context.Background(), "tenant-1", summary.Batch.ID); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("incomplete batch selection error = %v", err)
	}
	for _, initial := range summary.Items {
		item, claimErr := service.Claim(context.Background(), "tenant-1", initial.ID, "grader-b")
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		score := item.OriginalScore
		if item.ReviewTaskID == "task-regrade" {
			score = 2
		}
		if _, _, submitErr := service.Submit(context.Background(), "tenant-1", item.ID, "grader-b", SubmitInput{Score: score, ExpectedRevision: item.Revision}); submitErr != nil {
			t.Fatal(submitErr)
		}
	}
	batch, submissions, err := service.RegradeSelection(context.Background(), "tenant-1", summary.Batch.ID)
	if err != nil || batch.ID != summary.Batch.ID || len(submissions) != 1 || submissions[0] != "submission-1" {
		t.Fatalf("regrade selection batch=%#v submissions=%#v err=%v", batch, submissions, err)
	}
}
