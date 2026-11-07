package review

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"errors"
	"fmt"
	"testing"
)

func reviewContext() Context {
	return Context{
		ExamID:          "exam-1",
		SubmissionID:    "submission-1",
		AnswerSegmentID: "segment-1",
		AnonymousCode:   "ANON-001",
		Question: paper.Question{
			ID:           "question-1",
			TenantID:     tenantID,
			ExamID:       "exam-1",
			QuestionNo:   "Q1",
			QuestionType: "short_answer",
			Score:        5,
		},
		Rubric: paper.Rubric{
			ID:         "rubric-1",
			QuestionID: "question-1",
			Version:    "v1",
			Status:     "approved",
			MaxScore:   5,
			Points: []paper.RubricPoint{
				{ID: "p1", Description: "main idea", Score: 5, Required: true},
			},
		},
		RawAnswer: "student wrote the main idea",
		OCRText:   "student wrote the main idea",
		AISuggestion: map[string]any{
			"suggested_score": 4,
			"mock":            true,
		},
	}
}

type reviewDoubleMarkStrategyCase struct {
	strategy string
	want     float64
}

func assertDoubleMarkResolutionStrategy(t *testing.T, tt reviewDoubleMarkStrategyCase) {
	t.Helper()
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	if _, err := store.SetExamDoubleMarkPolicy(context.Background(), tenantID, "exam-1", "manager-1", SetDoubleMarkPolicyInput{
		Enabled:            true,
		Threshold:          2,
		ResolutionStrategy: tt.strategy,
	}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	session, err := store.CreateDoubleMarkSession(context.Background(), tenantID, "manager-1", CreateDoubleMarkSessionInput{
		AnswerSegmentID:  "segment-1",
		FirstReviewerID:  "reviewer-1",
		SecondReviewerID: "reviewer-2",
	})
	if err != nil {
		t.Fatalf("create double mark session: %v", err)
	}
	if session.FirstReviewTaskID == session.SecondReviewTaskID {
		t.Fatalf("double mark tasks must be independent: %#v", session)
	}

	first, err := store.SubmitGrade(context.Background(), tenantID, session.FirstReviewTaskID, "reviewer-1", SubmitGradeInput{
		ExpectedRevision: 1,
		Score:            4,
		RubricSelections: []RubricSelection{{PointID: "p1", Score: 4}},
	})
	if err != nil {
		t.Fatalf("submit first mark: %v", err)
	}
	if first.FinalGrade != nil || first.ArbitrationTask != nil || first.DoubleMarkSession == nil || first.DoubleMarkSession.Status != "first_submitted" {
		t.Fatalf("first mark should only update session state, got %#v", first)
	}

	second, err := store.SubmitGrade(context.Background(), tenantID, session.SecondReviewTaskID, "reviewer-2", SubmitGradeInput{
		ExpectedRevision: 1,
		Score:            5,
		RubricSelections: []RubricSelection{{PointID: "p1", Score: 5}},
	})
	if err != nil {
		t.Fatalf("submit second mark: %v", err)
	}
	if second.FinalGrade == nil || second.FinalGrade.Score != tt.want {
		t.Fatalf("expected final score %.2f, got %#v", tt.want, second.FinalGrade)
	}
	if second.DoubleMarkSession == nil || second.DoubleMarkSession.Status != "auto_finalized" {
		t.Fatalf("session should auto finalize, got %#v", second.DoubleMarkSession)
	}
	firstTask, err := store.GetTask(context.Background(), tenantID, session.FirstReviewTaskID)
	if err != nil {
		t.Fatalf("get first task: %v", err)
	}
	if firstTask.Status != "completed" || firstTask.GradeRound != "first_mark" {
		t.Fatalf("first task should be completed first_mark, got %#v", firstTask)
	}
}

func assertConcurrentReviewSubmitAndReturn(t *testing.T, iteration int) {
	t.Helper()
	store := NewMemoryStore()
	segmentID := fmt.Sprintf("segment-%d", iteration)
	store.AddContext(tenantID, segmentID, reviewContext())
	task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{
		AnswerSegmentID: segmentID,
		Source:          "manual_sample",
		AssignedTo:      "reviewer-1",
	})
	if err != nil {
		t.Fatalf("iteration %d create task: %v", iteration, err)
	}

	// 两个操作拿同一旧版本同时出发；只允许一个成功，成绩记录必须与胜出的状态一致。
	start := make(chan struct{})
	submitResult := make(chan error, 1)
	returnResult := make(chan error, 1)
	go func() {
		<-start
		_, submitErr := store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{
			ExpectedRevision: task.Revision,
			Score:            4,
			RubricSelections: []RubricSelection{{PointID: "p1", Score: 4}},
		})
		submitResult <- submitErr
	}()
	go func() {
		<-start
		_, returnErr := store.ReturnTask(context.Background(), tenantID, task.ID, "manager-1", ReturnTaskInput{Reason: "concurrent return", ExpectedRevision: task.Revision})
		returnResult <- returnErr
	}()
	close(start)

	submitErr := <-submitResult
	returnErr := <-returnResult
	if (submitErr == nil) == (returnErr == nil) {
		t.Fatalf("iteration %d exactly one concurrent transition must succeed, submit=%v return=%v", iteration, submitErr, returnErr)
	}
	if submitErr != nil && !errors.Is(submitErr, ErrRevisionConflict) {
		t.Fatalf("iteration %d submit has unexpected error: %v", iteration, submitErr)
	}
	if returnErr != nil && !errors.Is(returnErr, ErrInvalidTransition) && !errors.Is(returnErr, ErrRevisionConflict) {
		t.Fatalf("iteration %d return has unexpected error: %v", iteration, returnErr)
	}
	stored, err := store.GetTask(context.Background(), tenantID, task.ID)
	if err != nil || (stored.Status != "submitted" && stored.Status != "returned") {
		t.Fatalf("iteration %d final task must reflect the winning transition, task=%#v err=%v", iteration, stored, err)
	}
	grades := store.grades[key(tenantID, task.ID)]
	if (stored.Status == "submitted" && len(grades) != 1) || (stored.Status == "returned" && len(grades) != 0) {
		t.Fatalf("iteration %d grade facts must match the winning transition: task=%#v grades=%#v", iteration, stored, grades)
	}
}
