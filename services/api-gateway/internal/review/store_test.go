package review

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

const tenantID = "tenant-1"

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

	task, err = store.AssignTask(context.Background(), tenantID, task.ID, "manager-1", AssignTaskInput{AssignedTo: "reviewer-1"})
	if err != nil {
		t.Fatalf("assign task: %v", err)
	}
	if task.Status != "assigned" || task.AssignedTo != "reviewer-1" {
		t.Fatalf("assigned task mismatch: %#v", task)
	}

	returned, err := store.ReturnTask(context.Background(), tenantID, task.ID, "manager-1", ReturnTaskInput{Reason: "needs second look"})
	if err != nil {
		t.Fatalf("return task before submission: %v", err)
	}
	if returned.Status != "returned" || returned.ReturnReason != "needs second look" {
		t.Fatalf("returned task mismatch: %#v", returned)
	}

	submission, err := store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{
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

	_, err = store.ReturnTask(context.Background(), tenantID, task.ID, "manager-1", ReturnTaskInput{Reason: "must not invalidate committed grade"})
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

		start := make(chan struct{})
		submitResult := make(chan error, 1)
		returnResult := make(chan error, 1)
		go func() {
			<-start
			_, submitErr := store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{
				Score:            4,
				RubricSelections: []RubricSelection{{PointID: "p1", Score: 4}},
			})
			submitResult <- submitErr
		}()
		go func() {
			<-start
			_, returnErr := store.ReturnTask(context.Background(), tenantID, task.ID, "manager-1", ReturnTaskInput{Reason: "concurrent return"})
			returnResult <- returnErr
		}()
		close(start)

		if submitErr := <-submitResult; submitErr != nil {
			t.Fatalf("iteration %d submit must succeed before or after a return: %v", iteration, submitErr)
		}
		if returnErr := <-returnResult; returnErr != nil && !errors.Is(returnErr, ErrInvalidTransition) {
			t.Fatalf("iteration %d return has unexpected error: %v", iteration, returnErr)
		}
		stored, err := store.GetTask(context.Background(), tenantID, task.ID)
		if err != nil || stored.Status != "submitted" {
			t.Fatalf("iteration %d final task must be submitted, task=%#v err=%v", iteration, stored, err)
		}
		if grades := store.grades[key(tenantID, task.ID)]; len(grades) != 1 {
			t.Fatalf("iteration %d concurrent return must not duplicate or remove grades: %#v", iteration, grades)
		}
	}
}

func TestReviewerCanOnlySubmitAssignedTask(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{AnswerSegmentID: "segment-1", Source: "ai_low_confidence", AssignedTo: "reviewer-1"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	_, err = store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-2", SubmitGradeInput{Score: 3, RubricSelections: []RubricSelection{{PointID: "p1", Score: 3}}})
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
		TaskIDs:    []string{first.ID, second.ID},
		AssignedTo: "reviewer-1",
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

func TestReleaseTaskClaimPreservesAssignmentAndProgressDenominator(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{
		AnswerSegmentID: "segment-1",
		Source:          "manual_sample",
		AssignedTo:      "reviewer-1",
	})
	if err != nil {
		t.Fatalf("create assigned task: %v", err)
	}

	before, err := store.ListTasks(context.Background(), tenantID, ListFilter{AssignedTo: "reviewer-1"})
	if err != nil || len(before) != 1 {
		t.Fatalf("assigned progress denominator should start at one, tasks=%#v err=%v", before, err)
	}
	if _, err := store.ClaimNextTask(context.Background(), tenantID, "reviewer-1", NextTaskInput{}, ClaimTaskOptions{}); err != nil {
		t.Fatalf("claim assigned task: %v", err)
	}

	released, err := store.ReleaseTaskClaim(context.Background(), tenantID, task.ID, "reviewer-1")
	if err != nil {
		t.Fatalf("release task claim: %v", err)
	}
	if released.ID != task.ID || released.AssignedTo != "reviewer-1" || released.Status != "assigned" {
		t.Fatalf("release must clear only the short-lived claim, got %#v", released)
	}
	after, err := store.ListTasks(context.Background(), tenantID, ListFilter{AssignedTo: "reviewer-1"})
	if err != nil || len(after) != len(before) || after[0].ID != task.ID {
		t.Fatalf("release must not change assigned progress denominator, before=%#v after=%#v err=%v", before, after, err)
	}
	if _, err := store.ReleaseTaskClaim(context.Background(), tenantID, task.ID, "reviewer-2"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("another reviewer must not release the assignment, got %v", err)
	}
	if resumed, err := store.ClaimNextTask(context.Background(), tenantID, "reviewer-1", NextTaskInput{}, ClaimTaskOptions{}); err != nil || resumed.ID != task.ID || resumed.AssignedTo != "reviewer-1" {
		t.Fatalf("assigned reviewer should be able to resume after release, task=%#v err=%v", resumed, err)
	}
}

func TestSubmitGradeValidatesScoreAndRubricSelections(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{AnswerSegmentID: "segment-1", Source: "manual_sample", AssignedTo: "reviewer-1"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	_, err = store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{Score: 6, RubricSelections: []RubricSelection{{PointID: "p1", Score: 5}}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid score, got %v", err)
	}

	_, err = store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{Score: 4, RubricSelections: []RubricSelection{{PointID: "missing", Score: 4}}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid rubric selection, got %v", err)
	}

	_, err = store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{Score: 5, RubricSelections: []RubricSelection{{PointID: "p1", Score: 6}}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected rubric point score above its maximum to be rejected, got %v", err)
	}

	_, err = store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{Score: 4, RubricSelections: []RubricSelection{{PointID: "p1", Score: 2}, {PointID: "p1", Score: 2}}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected duplicate rubric point to be rejected, got %v", err)
	}

	_, err = store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{Score: 4, RubricSelections: []RubricSelection{{PointID: "p1", Score: 3}}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected final score inconsistent with rubric total to be rejected, got %v", err)
	}

	malformed := reviewContext()
	malformed.Rubric.Points = append(malformed.Rubric.Points, paper.RubricPoint{ID: "p1", Score: 1})
	if err := validateSubmit(SubmitGradeInput{Score: 4, RubricSelections: []RubricSelection{{PointID: "p1", Score: 4}}}, malformed); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate point definitions must be rejected, got %v", err)
	}

	rubricMax := reviewContext()
	rubricMax.Rubric.MaxScore = 3
	if err := validateSubmit(SubmitGradeInput{Score: 4, RubricSelections: []RubricSelection{{PointID: "p1", Score: 4}}}, rubricMax); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("final score above rubric max must be rejected, got %v", err)
	}

	result, err := store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{Score: 4.00005, RubricSelections: []RubricSelection{{PointID: "p1", Score: 4}}})
	if err != nil || math.Abs(result.Grade.Score-4.00005) > 0.000001 {
		t.Fatalf("small floating point difference should be accepted, result=%#v err=%v", result, err)
	}
}

func TestRejectedReturnPreservesDoubleMarkProgress(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	if _, err := store.SetExamDoubleMarkPolicy(context.Background(), tenantID, "exam-1", "manager-1", SetDoubleMarkPolicyInput{
		Enabled:            true,
		Threshold:          1,
		ResolutionStrategy: "average",
	}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	session, err := store.CreateDoubleMarkSession(context.Background(), tenantID, "manager-1", CreateDoubleMarkSessionInput{
		AnswerSegmentID:  "segment-1",
		FirstReviewerID:  "reviewer-1",
		SecondReviewerID: "reviewer-2",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	first, err := store.SubmitGrade(context.Background(), tenantID, session.FirstReviewTaskID, "reviewer-1", SubmitGradeInput{
		Score:            4,
		RubricSelections: []RubricSelection{{PointID: "p1", Score: 4}},
	})
	if err != nil || first.DoubleMarkSession == nil || first.DoubleMarkSession.Status != "first_submitted" {
		t.Fatalf("submit first mark: result=%#v err=%v", first, err)
	}
	progressAt := first.DoubleMarkSession.UpdatedAt

	if _, err := store.ReturnTask(context.Background(), tenantID, session.FirstReviewTaskID, "manager-1", ReturnTaskInput{Reason: "invalid after submit"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("submitted first mark must not be returned, got %v", err)
	}
	preserved, err := store.GetDoubleMarkSession(context.Background(), tenantID, session.ID)
	if err != nil || preserved.Status != "first_submitted" || !preserved.UpdatedAt.Equal(progressAt) {
		t.Fatalf("rejected return must preserve double-mark progress, session=%#v err=%v", preserved, err)
	}
	if grades := store.grades[key(tenantID, session.FirstReviewTaskID)]; len(grades) != 1 || grades[0].ID != first.Grade.ID {
		t.Fatalf("rejected return must preserve first-mark grade, got %#v", grades)
	}
}

func TestSubmitGradeAllowsDirectScoreWithoutRubric(t *testing.T) {
	store := NewMemoryStore()
	ctx := reviewContext()
	ctx.Rubric = paper.Rubric{}
	store.AddContext(tenantID, "segment-1", ctx)
	task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{AnswerSegmentID: "segment-1", Source: "manual_sample", AssignedTo: "reviewer-1"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	result, err := store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{Score: 3.5})
	if err != nil || result.Grade.Score != 3.5 {
		t.Fatalf("question without rubric should accept direct score, result=%#v err=%v", result, err)
	}
}

func TestSubmitGradeRecordsAISuggestionLinkFromContext(t *testing.T) {
	store := NewMemoryStore()
	withAI := reviewContext()
	withAI.AISuggestion["ai_grade_id"] = "ai-grade-77"
	store.AddContext(tenantID, "segment-1", withAI)
	task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{
		AnswerSegmentID: "segment-1",
		Source:          "ai_low_confidence",
		AssignedTo:      "reviewer-1",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	submission, err := store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{
		Score:            4,
		RubricSelections: []RubricSelection{{PointID: "p1", Score: 4}},
	})
	if err != nil {
		t.Fatalf("submit grade: %v", err)
	}
	if submission.Grade.AIGradeID != "ai-grade-77" {
		t.Fatalf("human grade must record the AI suggestion it overrode, got %#v", submission.Grade)
	}
	stored := store.grades[key(tenantID, task.ID)]
	if len(stored) != 1 || stored[0].AIGradeID != "ai-grade-77" {
		t.Fatalf("persisted grade must keep ai_grade_id from context, got %#v", stored)
	}

	withoutAI := reviewContext()
	withoutAI.AISuggestion = nil
	store.AddContext(tenantID, "segment-2", withoutAI)
	task, err = store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{
		AnswerSegmentID: "segment-2",
		Source:          "manual_sample",
		AssignedTo:      "reviewer-1",
	})
	if err != nil {
		t.Fatalf("create task without AI suggestion: %v", err)
	}
	submission, err = store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{
		Score:            4,
		RubricSelections: []RubricSelection{{PointID: "p1", Score: 4}},
	})
	if err != nil {
		t.Fatalf("submit grade without AI suggestion: %v", err)
	}
	if submission.Grade.AIGradeID != "" {
		t.Fatalf("grade without AI suggestion must not fabricate a link, got %#v", submission.Grade)
	}
}

func TestCreateTaskRejectsInvalidSource(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())

	_, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{AnswerSegmentID: "segment-1", Source: "made_up_source"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid source, got %v", err)
	}
}

func TestDoubleMarkAutoFinalizesByResolutionStrategy(t *testing.T) {
	tests := []struct {
		strategy string
		want     float64
	}{
		{strategy: "average", want: 4.5},
		{strategy: "first", want: 4},
		{strategy: "second", want: 5},
		{strategy: "higher", want: 5},
		{strategy: "lower", want: 4},
	}
	for _, tt := range tests {
		t.Run(tt.strategy, func(t *testing.T) {
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
		})
	}
}

func TestDoubleMarkCreatesArbitrationAndBlocksSameArbitratorByDefault(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	if _, err := store.SetExamDoubleMarkPolicy(context.Background(), tenantID, "exam-1", "manager-1", SetDoubleMarkPolicyInput{
		Enabled:            true,
		Threshold:          1,
		ResolutionStrategy: "average",
	}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	session, err := store.CreateDoubleMarkSession(context.Background(), tenantID, "manager-1", CreateDoubleMarkSessionInput{
		AnswerSegmentID:  "segment-1",
		FirstReviewerID:  "reviewer-1",
		SecondReviewerID: "reviewer-2",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := store.SubmitGrade(context.Background(), tenantID, session.FirstReviewTaskID, "reviewer-1", SubmitGradeInput{
		Score:            5,
		RubricSelections: []RubricSelection{{PointID: "p1", Score: 5}},
	}); err != nil {
		t.Fatalf("submit first: %v", err)
	}
	result, err := store.SubmitGrade(context.Background(), tenantID, session.SecondReviewTaskID, "reviewer-2", SubmitGradeInput{
		Score:            2,
		RubricSelections: []RubricSelection{{PointID: "p1", Score: 2}},
	})
	if err != nil {
		t.Fatalf("submit second: %v", err)
	}
	if result.ArbitrationTask == nil || result.FinalGrade != nil {
		t.Fatalf("expected arbitration task without final grade, got %#v", result)
	}
	if result.ArbitrationTask.ScoreDifference != 3 || result.DoubleMarkSession.Status != "needs_arbitration" {
		t.Fatalf("arbitration mismatch: %#v", result.ArbitrationTask)
	}
	_, _, err = store.SubmitArbitration(context.Background(), tenantID, result.ArbitrationTask.ID, "arbitrator-1", SubmitArbitrationInput{
		FinalScore: 4,
		Reason:     "must be explicitly assigned first",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("unassigned arbitration submission must be forbidden, got %v", err)
	}

	_, err = store.AssignArbitrationTask(context.Background(), tenantID, result.ArbitrationTask.ID, "manager-1", AssignArbitrationTaskInput{AssignedTo: "reviewer-1"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected first reviewer to be blocked as arbitrator, got %v", err)
	}
	assigned, err := store.AssignArbitrationTask(context.Background(), tenantID, result.ArbitrationTask.ID, "manager-1", AssignArbitrationTaskInput{AssignedTo: "arbitrator-1"})
	if err != nil {
		t.Fatalf("assign arbitrator: %v", err)
	}
	if assigned.Status != "assigned" || assigned.AssignedTo != "arbitrator-1" {
		t.Fatalf("assigned arbitration mismatch: %#v", assigned)
	}
	submitted, finalGrade, err := store.SubmitArbitration(context.Background(), tenantID, result.ArbitrationTask.ID, "arbitrator-1", SubmitArbitrationInput{
		FinalScore:      4,
		Reason:          "accepted stronger rubric evidence",
		StudentFeedback: "Final score set after arbitration.",
	})
	if err != nil {
		t.Fatalf("submit arbitration: %v", err)
	}
	if submitted.Status != "submitted" || submitted.FinalScore == nil || *submitted.FinalScore != 4 {
		t.Fatalf("submitted arbitration mismatch: %#v", submitted)
	}
	if finalGrade.Score != 4 || finalGrade.Source != "arbitration" {
		t.Fatalf("final grade mismatch: %#v", finalGrade)
	}
}

func TestQuestionDoubleMarkPolicyOverridesExamPolicy(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	if _, err := store.SetExamDoubleMarkPolicy(context.Background(), tenantID, "exam-1", "manager-1", SetDoubleMarkPolicyInput{
		Enabled:            true,
		Threshold:          10,
		ResolutionStrategy: "average",
	}); err != nil {
		t.Fatalf("set exam policy: %v", err)
	}
	if _, err := store.SetQuestionDoubleMarkPolicy(context.Background(), tenantID, "question-1", "manager-1", SetDoubleMarkPolicyInput{
		Enabled:            true,
		Threshold:          1,
		ResolutionStrategy: "higher",
	}); err != nil {
		t.Fatalf("set question policy: %v", err)
	}
	session, err := store.CreateDoubleMarkSession(context.Background(), tenantID, "manager-1", CreateDoubleMarkSessionInput{
		AnswerSegmentID:  "segment-1",
		FirstReviewerID:  "reviewer-1",
		SecondReviewerID: "reviewer-2",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if session.Threshold != 1 || session.ResolutionStrategy != "higher" {
		t.Fatalf("question policy should override exam policy, got %#v", session)
	}

	if _, err := store.SetQuestionDoubleMarkPolicy(context.Background(), tenantID, "question-1", "manager-1", SetDoubleMarkPolicyInput{
		Enabled:            false,
		Threshold:          1,
		ResolutionStrategy: "higher",
	}); err != nil {
		t.Fatalf("disable question policy: %v", err)
	}
	_, err = store.CreateDoubleMarkSession(context.Background(), tenantID, "manager-1", CreateDoubleMarkSessionInput{
		AnswerSegmentID:  "segment-1",
		FirstReviewerID:  "reviewer-1",
		SecondReviewerID: "reviewer-2",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("disabled question policy should override enabled exam policy, got %v", err)
	}
}

func TestAllowSameArbitratorPolicyAllowsReviewerAsArbitrator(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	if _, err := store.SetExamDoubleMarkPolicy(context.Background(), tenantID, "exam-1", "manager-1", SetDoubleMarkPolicyInput{
		Enabled:             true,
		Threshold:           1,
		ResolutionStrategy:  "average",
		AllowSameArbitrator: true,
	}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	session, err := store.CreateDoubleMarkSession(context.Background(), tenantID, "manager-1", CreateDoubleMarkSessionInput{
		AnswerSegmentID:  "segment-1",
		FirstReviewerID:  "reviewer-1",
		SecondReviewerID: "reviewer-2",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := store.SubmitGrade(context.Background(), tenantID, session.FirstReviewTaskID, "reviewer-1", SubmitGradeInput{Score: 5, RubricSelections: []RubricSelection{{PointID: "p1", Score: 5}}}); err != nil {
		t.Fatalf("submit first: %v", err)
	}
	result, err := store.SubmitGrade(context.Background(), tenantID, session.SecondReviewTaskID, "reviewer-2", SubmitGradeInput{Score: 2, RubricSelections: []RubricSelection{{PointID: "p1", Score: 2}}})
	if err != nil {
		t.Fatalf("submit second: %v", err)
	}
	assigned, err := store.AssignArbitrationTask(context.Background(), tenantID, result.ArbitrationTask.ID, "manager-1", AssignArbitrationTaskInput{AssignedTo: "reviewer-1"})
	if err != nil {
		t.Fatalf("same arbitrator should be allowed by policy: %v", err)
	}
	if !assigned.AllowSameArbitrator || assigned.AssignedTo != "reviewer-1" {
		t.Fatalf("expected same-reviewer arbitrator assignment, got %#v", assigned)
	}
}

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
