package review

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestReviewTaskWorkflowReturnsBeforeSubmissionAndRejectsSubmittedReturn(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())

	task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{
		AnswerSegmentID: "segment-1",
		Source:          "evidence_verification_failed",
		Priority:        5,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.Status != "pending" || task.AnonymousCode != "ANON-001" {
		t.Fatalf("created task should be pending with anonymous code, got %#v", task)
	}

	task, err = store.AssignTask(context.Background(), tenantID, task.ID, "manager-1", AssignTaskInput{AssignedTo: "reviewer-1", ExpectedRevision: task.Revision})
	if err != nil {
		t.Fatalf("assign task: %v", err)
	}
	if task.Status != "assigned" || task.AssignedTo != "reviewer-1" {
		t.Fatalf("assigned task mismatch: %#v", task)
	}

	returned, err := store.ReturnTask(context.Background(), tenantID, task.ID, "manager-1", ReturnTaskInput{Reason: "needs second look", ExpectedRevision: task.Revision})
	if err != nil {
		t.Fatalf("return task before submission: %v", err)
	}
	if returned.Status != "returned" || returned.ReturnReason != "needs second look" {
		t.Fatalf("returned task mismatch: %#v", returned)
	}

	submission, err := store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{
		ExpectedRevision: returned.Revision,
		Score:            4,
		RubricSelections: []RubricSelection{{PointID: "p1", Score: 4}},
		Comments:         "clear answer",
		StudentFeedback:  "Good work.",
		Reason:           "manual review completed",
	})
	if err != nil {
		t.Fatalf("submit grade: %v", err)
	}
	if submission.Task.Status != "submitted" {
		t.Fatalf("submitted task should be submitted, got %#v", submission.Task)
	}
	if submission.Grade.Score != 4 || submission.Grade.MaxScore != 5 || submission.Grade.ReviewerID != "reviewer-1" {
		t.Fatalf("human grade mismatch: %#v", submission.Grade)
	}
	gradeKey := key(tenantID, task.ID)
	if len(store.grades[gradeKey]) != 1 {
		t.Fatalf("submit should create exactly one grade, got %#v", store.grades[gradeKey])
	}
	submittedAt := submission.Task.UpdatedAt
	submittedReason := submission.Task.ReturnReason

	_, err = store.ReturnTask(context.Background(), tenantID, task.ID, "manager-1", ReturnTaskInput{Reason: "must not invalidate committed grade", ExpectedRevision: submission.Task.Revision})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("submitted task must not be returned, got %v", err)
	}
	unchanged, err := store.GetTask(context.Background(), tenantID, task.ID)
	if err != nil || unchanged.Status != "submitted" || unchanged.ReturnReason != submittedReason || !unchanged.UpdatedAt.Equal(submittedAt) {
		t.Fatalf("rejected return must leave submitted task unchanged, task=%#v err=%v", unchanged, err)
	}
	grades := store.grades[gradeKey]
	if len(grades) != 1 || grades[0].ID != submission.Grade.ID || grades[0].Score != submission.Grade.Score {
		t.Fatalf("rejected return must preserve the committed grade, got %#v", grades)
	}
}

func TestSubmitAndReturnAreLinearizable(t *testing.T) {
	for iteration := 0; iteration < 32; iteration++ {
		assertConcurrentReviewSubmitAndReturn(t, iteration)
	}
}

func TestReviewerCanOnlySubmitAssignedTask(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{AnswerSegmentID: "segment-1", Source: "ai_low_confidence", AssignedTo: "reviewer-1"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	_, err = store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-2", SubmitGradeInput{ExpectedRevision: task.Revision, Score: 3, RubricSelections: []RubricSelection{{PointID: "p1", Score: 3}}})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for unassigned reviewer, got %v", err)
	}
}

func TestBatchAssignTasks(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	store.AddContext(tenantID, "segment-2", reviewContext())
	first, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{AnswerSegmentID: "segment-1", Source: "manual_sample"})
	if err != nil {
		t.Fatalf("create first task: %v", err)
	}
	second, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{AnswerSegmentID: "segment-2", Source: "score_anomaly"})
	if err != nil {
		t.Fatalf("create second task: %v", err)
	}

	tasks, err := store.BatchAssignTasks(context.Background(), tenantID, "manager-1", BatchAssignInput{
		TaskIDs:           []string{first.ID, second.ID},
		AssignedTo:        "reviewer-1",
		ExpectedRevisions: map[string]int64{first.ID: first.Revision, second.ID: second.Revision},
	})
	if err != nil {
		t.Fatalf("batch assign: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected two assigned tasks, got %#v", tasks)
	}
	for _, task := range tasks {
		if task.Status != "assigned" || task.AssignedTo != "reviewer-1" {
			t.Fatalf("batch assigned task mismatch: %#v", task)
		}
	}
}

func TestListTasksFiltersByExam(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	secondContext := reviewContext()
	secondContext.ExamID = "exam-2"
	secondContext.SubmissionID = "submission-2"
	secondContext.Question.ID = "question-2"
	secondContext.Question.ExamID = "exam-2"
	secondContext.Question.QuestionNo = "Q2"
	store.AddContext(tenantID, "segment-2", secondContext)

	first, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{AnswerSegmentID: "segment-1", Source: "manual_sample"})
	if err != nil {
		t.Fatalf("create first task: %v", err)
	}
	second, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{AnswerSegmentID: "segment-2", Source: "manual_sample"})
	if err != nil {
		t.Fatalf("create second task: %v", err)
	}

	tasks, err := store.ListTasks(context.Background(), tenantID, ListFilter{ExamID: "exam-2"})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != second.ID || tasks[0].ID == first.ID {
		t.Fatalf("exam filter should return only the matching task, got %#v", tasks)
	}
}

func TestListTasksUsesStablePriorityCursor(t *testing.T) {
	store := NewMemoryStore()
	for index, segmentID := range []string{"segment-1", "segment-2", "segment-3"} {
		ctx := reviewContext()
		ctx.AnswerSegmentID = segmentID
		ctx.SubmissionID = fmt.Sprintf("submission-%d", index+1)
		store.AddContext(tenantID, segmentID, ctx)
		if _, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{
			AnswerSegmentID: segmentID, Source: "manual_sample", Priority: 3 - index,
		}); err != nil {
			t.Fatalf("create task %d: %v", index, err)
		}
	}
	firstPage, err := store.ListTasks(context.Background(), tenantID, ListFilter{Limit: 2})
	if err != nil || len(firstPage) != 2 {
		t.Fatalf("first page: %#v %v", firstPage, err)
	}
	last := firstPage[len(firstPage)-1]
	secondPage, err := store.ListTasks(context.Background(), tenantID, ListFilter{
		Limit: 2, CursorPriority: last.Priority, CursorCreatedAt: last.CreatedAt, CursorID: last.ID,
	})
	if err != nil || len(secondPage) != 1 || secondPage[0].ID == firstPage[0].ID || secondPage[0].ID == firstPage[1].ID {
		t.Fatalf("second page must contain only the remaining task: %#v %v", secondPage, err)
	}
}

func TestClaimNextTaskRequiresExplicitUnassignedPermission(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	pending, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{AnswerSegmentID: "segment-1", Source: "manual_sample"})
	if err != nil {
		t.Fatalf("create pending task: %v", err)
	}

	if _, err = store.ClaimNextTask(context.Background(), tenantID, "reviewer-1", NextTaskInput{}, ClaimTaskOptions{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reviewer must not claim an unassigned task, got %v", err)
	}
	unchanged, err := store.GetTask(context.Background(), tenantID, pending.ID)
	if err != nil {
		t.Fatalf("get pending task: %v", err)
	}
	if unchanged.Status != "pending" || unchanged.AssignedTo != "" {
		t.Fatalf("denied claim must not mutate task, got %#v", unchanged)
	}

	claimed, err := store.ClaimNextTask(context.Background(), tenantID, "manager-1", NextTaskInput{}, ClaimTaskOptions{AllowUnassigned: true})
	if err != nil {
		t.Fatalf("manager claim pending task: %v", err)
	}
	if claimed.ID != pending.ID || claimed.Status != "assigned" || claimed.AssignedTo != "manager-1" {
		t.Fatalf("manager should claim and assign pending task, got %#v", claimed)
	}
}

func TestClaimNextTaskContinuesOnlyMatchingAssignedTask(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	secondContext := reviewContext()
	secondContext.ExamID = "exam-2"
	secondContext.SubmissionID = "submission-2"
	secondContext.Question.ID = "question-2"
	secondContext.Question.ExamID = "exam-2"
	secondContext.Question.QuestionNo = "Q2"
	store.AddContext(tenantID, "segment-2", secondContext)

	if _, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{
		AnswerSegmentID: "segment-1", Source: "manual_sample", AssignedTo: "reviewer-1",
	}); err != nil {
		t.Fatalf("create first assigned task: %v", err)
	}
	second, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{
		AnswerSegmentID: "segment-2", Source: "manual_sample", AssignedTo: "reviewer-1",
	})
	if err != nil {
		t.Fatalf("create second assigned task: %v", err)
	}

	claimed, err := store.ClaimNextTask(context.Background(), tenantID, "reviewer-1", NextTaskInput{ExamID: "exam-2"}, ClaimTaskOptions{})
	if err != nil {
		t.Fatalf("continue assigned task: %v", err)
	}
	if claimed.ID != second.ID || claimed.ExamID != "exam-2" || claimed.AssignedTo != "reviewer-1" {
		t.Fatalf("claim should honor assignment and exam filter, got %#v", claimed)
	}
}
