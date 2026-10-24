package review

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
)

type autoSubmitBookmarkStub struct{ marked bool }

func (s *autoSubmitBookmarkStub) WithReviewTaskBookmarkLock(_ context.Context, _, _, _ string, operation func(bool) error) error {
	return operation(s.marked)
}

func TestAutoSubmitDirectFinalScoreAndZeroRemainFormalGrades(t *testing.T) {
	for _, score := range []float64{0, 3.5} {
		store, task := submissionQualificationFixture(t, assessment.RiskR2)
		input := SubmitGradeInput{AutoSubmit: true, Score: score, ExpectedRevision: task.Revision}
		ctx := commandreceipt.WithID(context.Background(), "automatic-final-score")
		result, err := store.SubmitGrade(ctx, tenantID, task.ID, "reviewer-1", input)
		if err != nil || result.Task.Status != "submitted" || result.Grade.Score != score || result.Grade.ID == "" {
			t.Fatalf("direct final score %v: result=%#v err=%v", score, result, err)
		}
		// 输入另一个分数只读回已提交事实，不能把返回成功解释成已改分。
		readback, err := store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{AutoSubmit: true, Score: 5, ExpectedRevision: task.Revision})
		if err != nil || readback.Grade.ID != result.Grade.ID || readback.Grade.Score != score || len(store.grades[key(tenantID, task.ID)]) != 1 {
			t.Fatalf("submitted task was rewritten: readback=%#v err=%v", readback, err)
		}
		if _, err := store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{Score: score, ExpectedRevision: result.Task.Revision}); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("ordinary resubmission unexpectedly changed its boundary: %v", err)
		}
	}
}

func TestAutoSubmitMarkedTaskBlocksAllGradeWritesAndPreservesReplay(t *testing.T) {
	store, task := submissionQualificationFixture(t, assessment.RiskR2)
	guard := &autoSubmitBookmarkStub{marked: true}
	store.WithAutoSubmitBookmarkGuard(guard)
	input := SubmitGradeInput{AutoSubmit: true, Score: 4, ExpectedRevision: task.Revision}
	ctx := commandreceipt.WithID(context.Background(), "automatic-marked")
	if _, err := store.SubmitGrade(ctx, tenantID, task.ID, "reviewer-1", input); !errors.Is(err, ErrTaskMarked) {
		t.Fatalf("marked task accepted automatic grading: %v", err)
	}
	if len(store.grades[key(tenantID, task.ID)]) != 0 || len(store.finalGrades) != 0 || store.tasks[key(tenantID, task.ID)].Revision != task.Revision {
		t.Fatal("marked task changed grades or task state")
	}
	response := submitQualificationRequest(NewHandler(store, nil, nil, nil), task.ID, "automatic-marked-http", `{"auto_submit":true,"expected_revision":1,"score":0}`)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"review_task_marked"`) {
		t.Fatalf("mark did not return its actionable conflict: %d %s", response.Code, response.Body.String())
	}
	guard.marked = false
	first, err := store.SubmitGrade(ctx, tenantID, task.ID, "reviewer-1", input)
	if err != nil {
		t.Fatal(err)
	}
	guard.marked = true
	replayed, err := store.SubmitGrade(ctx, tenantID, task.ID, "reviewer-1", input)
	if err != nil || replayed.Grade.ID != first.Grade.ID || len(store.grades[key(tenantID, task.ID)]) != 1 {
		t.Fatalf("later bookmark invalidated a successful command receipt: %#v %v", replayed, err)
	}
}

func TestAutoSubmitPreservesOwnershipVersionsSelectionsAndQualification(t *testing.T) {
	for _, test := range []struct {
		name     string
		reviewer string
		input    SubmitGradeInput
		want     error
	}{
		{"another reviewer", "other", SubmitGradeInput{AutoSubmit: true, Score: 3, ExpectedRevision: 1}, ErrForbidden},
		{"stale revision", "reviewer-1", SubmitGradeInput{AutoSubmit: true, Score: 3, ExpectedRevision: 2}, ErrRevisionConflict},
		{"score exceeds maximum", "reviewer-1", SubmitGradeInput{AutoSubmit: true, Score: 6, ExpectedRevision: 1}, ErrInvalidInput},
		{"real selections disagree", "reviewer-1", SubmitGradeInput{AutoSubmit: true, Score: 4, ExpectedRevision: 1, RubricSelections: []RubricSelection{{PointID: "p1", Score: 3}}}, ErrInvalidInput},
		{"ordinary direct score", "reviewer-1", SubmitGradeInput{Score: 4, ExpectedRevision: 1}, ErrInvalidInput},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, task := submissionQualificationFixture(t, assessment.RiskR2)
			if _, err := store.SubmitGrade(context.Background(), tenantID, task.ID, test.reviewer, test.input); !errors.Is(err, test.want) {
				t.Fatalf("gate was bypassed: %v, want %v", err, test.want)
			}
		})
	}
	store, task := submissionQualificationFixture(t, assessment.RiskR3)
	response := submitQualificationRequest(NewHandler(store, nil, nil, nil).WithQualificationGate(&mutableSubmissionQualification{allowed: false}), task.ID, "auto-unqualified", `{"auto_submit":true,"expected_revision":1,"score":4}`)
	if response.Code != http.StatusForbidden || len(store.grades[key(tenantID, task.ID)]) != 0 {
		t.Fatalf("R3 qualification was bypassed: %d %s", response.Code, response.Body.String())
	}
}

func TestAutoSubmitDoubleMarkStillWaitsForBothIndependentGrades(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", reviewContext())
	_, err := store.SetExamDoubleMarkPolicy(context.Background(), tenantID, "exam-1", "manager-1", SetDoubleMarkPolicyInput{Enabled: true, Threshold: 2, ResolutionStrategy: "average"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateDoubleMarkSession(context.Background(), tenantID, "manager-1", CreateDoubleMarkSessionInput{AnswerSegmentID: "segment-1", FirstReviewerID: "reviewer-1", SecondReviewerID: "reviewer-2"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.SubmitGrade(context.Background(), tenantID, session.FirstReviewTaskID, "reviewer-1", SubmitGradeInput{AutoSubmit: true, Score: 4, ExpectedRevision: 1})
	if err != nil || first.FinalGrade != nil || first.DoubleMarkSession == nil || first.DoubleMarkSession.Status != "first_submitted" {
		t.Fatalf("first auto mark bypassed independent second mark: %#v %v", first, err)
	}
	second, err := store.SubmitGrade(context.Background(), tenantID, session.SecondReviewTaskID, "reviewer-2", SubmitGradeInput{AutoSubmit: true, Score: 3, ExpectedRevision: 1})
	if err != nil || second.FinalGrade == nil || second.FinalGrade.Score != 3.5 {
		t.Fatalf("double mark resolution changed: %#v %v", second, err)
	}
}

func autoSubmitTaskRow(status string) []driver.Value {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	return []driver.Value{"task", "tenant", "exam", "question", "Q1", "segment", "submission", "ANON", "manual_sample", status, int64(0), "reviewer", "", "single", nil, int64(1), "reviewer", now, now, ""}
}

func TestPostgresAutoSubmitChecksBookmarkAfterParentAndTaskLocks(t *testing.T) {
	steps := []reviewBatch34Step{
		{name: "parent identity", fragment: "SELECT scoring_run_id::text FROM review_task", columns: 1, args: []any{"tenant", "task"}, rows: [][]driver.Value{{"run"}}},
		{name: "parent lock", fragment: "FROM scoring_run", lock: true, columns: 1, args: []any{"tenant", "run"}, rows: [][]driver.Value{{"needs_review"}}},
		{name: "task lock", fragment: "FROM review_task", lock: true, columns: 20, args: []any{"tenant", "task"}, rows: [][]driver.Value{autoSubmitTaskRow("assigned")}},
		{name: "active personal mark", fragment: "FROM review_bookmark WHERE tenant_id=$1::uuid AND owner_id=$2::uuid AND review_task_id=$3::uuid AND deleted_at IS NULL", columns: 1, args: []any{"tenant", "reviewer", "task"}, rows: [][]driver.Value{{true}}},
	}
	store, script := openReviewBatch34Store(t, steps)
	_, err := store.SubmitGrade(context.Background(), "tenant", "task", "reviewer", SubmitGradeInput{AutoSubmit: true, Score: 0, ExpectedRevision: 1})
	if !errors.Is(err, ErrTaskMarked) {
		t.Fatalf("marked transaction proceeded to grading: %v", err)
	}
	assertReviewBatch34Script(t, script)
}

func TestPostgresAutoSubmitReadsActualSubmittedScoreWithoutGradeWrites(t *testing.T) {
	steps := []reviewBatch34Step{
		{name: "parent identity", fragment: "SELECT scoring_run_id::text FROM review_task", columns: 1, args: []any{"tenant", "task"}, rows: [][]driver.Value{{"run"}}},
		{name: "completed parent lock", fragment: "FROM scoring_run", lock: true, columns: 1, args: []any{"tenant", "run"}, rows: [][]driver.Value{{"completed"}}},
		{name: "submitted task lock", fragment: "FROM review_task", lock: true, columns: 20, args: []any{"tenant", "task"}, rows: [][]driver.Value{autoSubmitTaskRow("submitted")}},
		{name: "actual own grade", fragment: "FROM human_grade WHERE tenant_id=$1::uuid AND review_task_id=$2::uuid AND reviewer_id=$3::uuid", columns: 15, args: []any{"tenant", "task", "reviewer"}, rows: [][]driver.Value{{"original-grade", "tenant", "task", "segment", "reviewer", float64(0), float64(5), []byte(`[]`), "", "", "", "", "single", "", time.Now().UTC()}}},
	}
	store, script := openReviewBatch34Store(t, steps)
	result, err := store.SubmitGrade(context.Background(), "tenant", "task", "reviewer", SubmitGradeInput{AutoSubmit: true, Score: 5, ExpectedRevision: 99})
	if err != nil || result.Grade.ID != "original-grade" || result.Grade.Score != 0 || result.Task.Status != "submitted" {
		t.Fatalf("readback accepted new input as a score change: %#v %v", result, err)
	}
	if script.next != len(script.steps) || script.commits != 1 || script.rolls != 0 {
		t.Fatalf("readback wrote grades or failed to close transaction: %#v", script)
	}
}

func TestSubmitGradeOptionalAutoFlagPreservesExistingCommandPayload(t *testing.T) {
	raw, err := json.Marshal(SubmitGradeInput{Score: 0})
	if err != nil || strings.Contains(string(raw), "auto_submit") {
		t.Fatalf("omitted auto flag changed old command fingerprints: %s %v", raw, err)
	}
}
