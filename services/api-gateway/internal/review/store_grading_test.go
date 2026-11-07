package review

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"errors"
	"math"
	"testing"
)

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

	_, err = store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{ExpectedRevision: task.Revision, Score: 6, RubricSelections: []RubricSelection{{PointID: "p1", Score: 5}}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid score, got %v", err)
	}

	_, err = store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{ExpectedRevision: task.Revision, Score: 4, RubricSelections: []RubricSelection{{PointID: "missing", Score: 4}}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid rubric selection, got %v", err)
	}

	_, err = store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{ExpectedRevision: task.Revision, Score: 5, RubricSelections: []RubricSelection{{PointID: "p1", Score: 6}}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected rubric point score above its maximum to be rejected, got %v", err)
	}

	_, err = store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{ExpectedRevision: task.Revision, Score: 4, RubricSelections: []RubricSelection{{PointID: "p1", Score: 2}, {PointID: "p1", Score: 2}}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected duplicate rubric point to be rejected, got %v", err)
	}

	_, err = store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{ExpectedRevision: task.Revision, Score: 4, RubricSelections: []RubricSelection{{PointID: "p1", Score: 3}}})
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

	result, err := store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{ExpectedRevision: task.Revision, Score: 4.00005, RubricSelections: []RubricSelection{{PointID: "p1", Score: 4}}})
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
		ExpectedRevision: 1,
		Score:            4,
		RubricSelections: []RubricSelection{{PointID: "p1", Score: 4}},
	})
	if err != nil || first.DoubleMarkSession == nil || first.DoubleMarkSession.Status != "first_submitted" {
		t.Fatalf("submit first mark: result=%#v err=%v", first, err)
	}
	progressAt := first.DoubleMarkSession.UpdatedAt

	if _, err := store.ReturnTask(context.Background(), tenantID, session.FirstReviewTaskID, "manager-1", ReturnTaskInput{Reason: "invalid after submit", ExpectedRevision: first.Task.Revision}); !errors.Is(err, ErrInvalidTransition) {
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

	result, err := store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{ExpectedRevision: task.Revision, Score: 3.5})
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
		ExpectedRevision: task.Revision,
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
		ExpectedRevision: task.Revision,
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
	tests := []reviewDoubleMarkStrategyCase{
		{strategy: "average", want: 4.5},
		{strategy: "first", want: 4},
		{strategy: "second", want: 5},
		{strategy: "higher", want: 5},
		{strategy: "lower", want: 4},
	}
	for _, tt := range tests {
		t.Run(tt.strategy, func(t *testing.T) {
			assertDoubleMarkResolutionStrategy(t, tt)
		})
	}
}
