package review

import (
	"context"
	"errors"
	"testing"
)

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
		ExpectedRevision: 1,
		Score:            5,
		RubricSelections: []RubricSelection{{PointID: "p1", Score: 5}},
	}); err != nil {
		t.Fatalf("submit first: %v", err)
	}
	result, err := store.SubmitGrade(context.Background(), tenantID, session.SecondReviewTaskID, "reviewer-2", SubmitGradeInput{
		ExpectedRevision: 1,
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
		FinalScore:       4,
		Reason:           "must be explicitly assigned first",
		ExpectedRevision: result.ArbitrationTask.Revision,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("unassigned arbitration submission must be forbidden, got %v", err)
	}

	_, err = store.AssignArbitrationTask(context.Background(), tenantID, result.ArbitrationTask.ID, "manager-1", AssignArbitrationTaskInput{AssignedTo: "reviewer-1", ExpectedRevision: result.ArbitrationTask.Revision})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected first reviewer to be blocked as arbitrator, got %v", err)
	}
	assigned, err := store.AssignArbitrationTask(context.Background(), tenantID, result.ArbitrationTask.ID, "manager-1", AssignArbitrationTaskInput{AssignedTo: "arbitrator-1", ExpectedRevision: result.ArbitrationTask.Revision})
	if err != nil {
		t.Fatalf("assign arbitrator: %v", err)
	}
	if assigned.Status != "assigned" || assigned.AssignedTo != "arbitrator-1" {
		t.Fatalf("assigned arbitration mismatch: %#v", assigned)
	}
	submitted, finalGrade, err := store.SubmitArbitration(context.Background(), tenantID, result.ArbitrationTask.ID, "arbitrator-1", SubmitArbitrationInput{
		FinalScore:       4,
		Reason:           "accepted stronger rubric evidence",
		StudentFeedback:  "Final score set after arbitration.",
		ExpectedRevision: assigned.Revision,
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
	if _, err := store.SubmitGrade(context.Background(), tenantID, session.FirstReviewTaskID, "reviewer-1", SubmitGradeInput{ExpectedRevision: 1, Score: 5, RubricSelections: []RubricSelection{{PointID: "p1", Score: 5}}}); err != nil {
		t.Fatalf("submit first: %v", err)
	}
	result, err := store.SubmitGrade(context.Background(), tenantID, session.SecondReviewTaskID, "reviewer-2", SubmitGradeInput{ExpectedRevision: 1, Score: 2, RubricSelections: []RubricSelection{{PointID: "p1", Score: 2}}})
	if err != nil {
		t.Fatalf("submit second: %v", err)
	}
	assigned, err := store.AssignArbitrationTask(context.Background(), tenantID, result.ArbitrationTask.ID, "manager-1", AssignArbitrationTaskInput{AssignedTo: "reviewer-1", ExpectedRevision: result.ArbitrationTask.Revision})
	if err != nil {
		t.Fatalf("same arbitrator should be allowed by policy: %v", err)
	}
	if !assigned.AllowSameArbitrator || assigned.AssignedTo != "reviewer-1" {
		t.Fatalf("expected same-reviewer arbitrator assignment, got %#v", assigned)
	}
}

func TestArbitrationRejectsStaleRevision(t *testing.T) {
	store := NewMemoryStore()
	task := ArbitrationTask{
		ID: "arbitration-stale", TenantID: tenantID, Status: "pending", Revision: 1,
		FirstReviewerID: "reviewer-1", SecondReviewerID: "reviewer-2", AllowSameArbitrator: true,
	}
	store.arbitrations[key(tenantID, task.ID)] = task
	updated, err := store.AssignArbitrationTask(context.Background(), tenantID, task.ID, "manager-1", AssignArbitrationTaskInput{
		AssignedTo: "arbitrator-1", ExpectedRevision: task.Revision,
	})
	if err != nil {
		t.Fatalf("assign arbitration: %v", err)
	}
	_, err = store.AssignArbitrationTask(context.Background(), tenantID, task.ID, "manager-1", AssignArbitrationTaskInput{
		AssignedTo: "arbitrator-2", ExpectedRevision: task.Revision,
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision must be rejected, got %v", err)
	}
	current, err := store.GetArbitrationTask(context.Background(), tenantID, task.ID)
	if err != nil || current.AssignedTo != "arbitrator-1" || current.Revision != updated.Revision {
		t.Fatalf("stale update changed arbitration: %#v err=%v", current, err)
	}
}
