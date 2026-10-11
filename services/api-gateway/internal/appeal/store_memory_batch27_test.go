package appeal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestBatch27ListAppealsKeepsTenantAndOptionalFilters(t *testing.T) {
	store, _ := batch27ListStore()
	for _, tc := range []struct {
		name   string
		tenant string
		filter ListFilter
		want   []string
	}{
		{name: "all narrowed conditions", tenant: "tenant-1", filter: batch27NarrowListFilter(), want: []string{"z", "c", "b", "a", "o"}},
		{name: "exam filter", tenant: "tenant-1", filter: ListFilter{ExamID: "exam-2"}, want: []string{"other-exam"}},
		{name: "student filter", tenant: "tenant-1", filter: ListFilter{StudentID: "student-2"}, want: []string{"other-student"}},
		{name: "status filter", tenant: "tenant-1", filter: ListFilter{Status: "rejected"}, want: []string{"other-status"}},
		{name: "assignment filter", tenant: "tenant-1", filter: ListFilter{AssignedTo: "teacher-2"}, want: []string{"other-assignee"}},
		{name: "other tenant", tenant: "tenant-2", filter: batch27NarrowListFilter(), want: []string{"other-tenant"}},
		{name: "filter case remains exact", tenant: "tenant-1", filter: ListFilter{ExamID: "EXAM-1"}, want: []string{}},
		{name: "filter spaces remain exact", tenant: "tenant-1", filter: ListFilter{AssignedTo: " teacher-1 "}, want: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.ListAppeals(context.Background(), tc.tenant, tc.filter)
			batch27AssertAppealIDs(t, got, err, tc.want)
		})
	}
}

func TestBatch27ListAppealsKeepsStrictCursorAndLimitOrder(t *testing.T) {
	store, created := batch27ListStore()
	for _, tc := range []struct {
		name   string
		cursor string
		at     time.Time
		limit  int
		want   []string
	}{
		{name: "zero limit leaves list unlimited", want: []string{"z", "c", "b", "a", "o"}},
		{name: "negative limit leaves list unlimited", limit: -1, want: []string{"z", "c", "b", "a", "o"}},
		{name: "limit follows cursor", cursor: "c", at: created, limit: 2, want: []string{"b", "a"}},
		{name: "cursor need not identify stored item", cursor: "bb", at: created, want: []string{"b", "a", "o"}},
		{name: "cursor item is excluded", cursor: "o", at: created.Add(-time.Hour), want: []string{}},
		{name: "ID enables zero timestamp cursor", cursor: "c", want: []string{}},
		{name: "timestamp alone does not enable cursor", at: created, limit: 1, want: []string{"z"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filter := batch27NarrowListFilter()
			filter.CursorID, filter.CursorCreatedAt, filter.Limit = tc.cursor, tc.at, tc.limit
			got, err := store.ListAppeals(context.Background(), "tenant-1", filter)
			batch27AssertAppealIDs(t, got, err, tc.want)
		})
	}
}

func batch27NarrowListFilter() ListFilter {
	return ListFilter{ExamID: "exam-1", StudentID: "student-1", Status: "submitted", AssignedTo: "teacher-1"}
}

func batch27ListStore() (*MemoryStore, time.Time) {
	store := NewMemoryStore()
	created := time.Date(2026, 10, 6, 1, 0, 0, 0, time.UTC)
	base := Appeal{TenantID: "tenant-1", ExamID: "exam-1", StudentID: "student-1", Status: "submitted", AssignedTo: "teacher-1", CreatedAt: created}
	for id, at := range map[string]time.Time{"z": created.Add(time.Hour), "c": created, "b": created, "a": created, "o": created.Add(-time.Hour)} {
		item := base
		item.ID, item.CreatedAt = id, at
		store.appeals[id] = item
	}
	for _, id := range []string{"other-exam", "other-student", "other-status", "other-assignee", "other-tenant"} {
		item := base
		item.ID = id
		switch id {
		case "other-exam":
			item.ExamID = "exam-2"
		case "other-student":
			item.StudentID = "student-2"
		case "other-status":
			item.Status = "rejected"
		case "other-assignee":
			item.AssignedTo = "teacher-2"
		case "other-tenant":
			item.TenantID = "tenant-2"
		}
		store.appeals[id] = item
	}
	return store, created
}

func batch27AssertAppealIDs(t *testing.T, items []Appeal, err error, want []string) {
	t.Helper()
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	if err != nil || items == nil || !reflect.DeepEqual(ids, want) {
		t.Fatalf("appeal IDs=%v want=%v nil=%t err=%v", ids, want, items == nil, err)
	}
}

func TestBatch27ReviewAppealFailureKeepsFactsAndErrorOrder(t *testing.T) {
	for _, tc := range []appealReviewFailureCase{
		{name: "tenant boundary", tenant: "other-tenant", score: 5, want: ErrNotFound},
		{name: "missing appeal", missingAppeal: true, score: 5, want: ErrNotFound},
		{name: "closed before stale revision", status: "closed", stale: true, score: 5, want: ErrInvalidTransition},
		{name: "revision before other terminal transition", status: "accepted", stale: true, score: 5, want: ErrRevisionConflict},
		{name: "terminal transition", status: "accepted", score: 5, want: ErrInvalidTransition},
		{name: "stale before invalid final", stale: true, finalID: "missing", score: 5, want: ErrRevisionConflict},
		{name: "missing fallback final", examTarget: true, score: 5, want: ErrInvalidInput},
		{name: "missing explicit final", finalID: "missing", score: 5, want: ErrInvalidInput},
		{name: "different final exam", finalID: "foreign-exam", score: 5, want: ErrInvalidInput},
		{name: "different final submission", finalID: "foreign-submission", score: 5, want: ErrInvalidInput},
		{name: "score below zero", score: -1, want: ErrInvalidInput},
		{name: "score above maximum", score: 6, want: ErrInvalidInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertAppealReviewFailureKeepsFacts(t, tc)
		})
	}
}

func TestBatch27ReviewAppealKeepsAdjustmentHistoryAndDelta(t *testing.T) {
	for _, tc := range []appealAdjustmentHistoryCase{
		{name: "fallback final includes zero score", score: 0},
		{name: "explicit final includes maximum score", examTarget: true, finalID: " final-1 ", score: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertAppealAdjustmentHistoryAndDelta(t, tc)
		})
	}
}

func TestBatch27ReviewAppealNonScoreDecisionLeavesGrades(t *testing.T) {
	store, item := batch27ReviewStore(t, false)
	final, submission, next := store.finals["final-1"], store.submissions[key(item.ExamID, item.StudentID)], store.next
	score := 2.0
	got, adjustment, err := store.ReviewAppeal(context.Background(), tenantID, item.ID, "manager-1", ReviewAppealInput{Status: "accepted", Reason: " accepted ", FinalGradeID: "missing", AdjustedScore: &score, ExpectedRevision: item.Revision})
	if err != nil || adjustment != nil || got.Status != "accepted" || got.Revision != item.Revision+1 || got.ResultReason != "accepted" || !reflect.DeepEqual(store.finals["final-1"], final) || !reflect.DeepEqual(store.submissions[key(item.ExamID, item.StudentID)], submission) || store.next != next || len(store.adjustments[item.ID]) != 0 {
		t.Fatalf("appeal=%#v adjustment=%#v err=%v", got, adjustment, err)
	}
}

func batch27ReviewStore(t *testing.T, examTarget bool) (*MemoryStore, Appeal) {
	t.Helper()
	store := seededAppealStore()
	input := CreateAppealInput{ExamID: "exam-1", StudentID: "student-1", TargetType: "question", FinalGradeID: "final-1", Reason: "requested"}
	if examTarget {
		input.TargetType, input.FinalGradeID = "exam", ""
	}
	item, err := store.CreateAppeal(context.Background(), tenantID, "student-user-1", input)
	if err != nil {
		t.Fatal(err)
	}
	return store, item
}

func batch27AppealFacts(t *testing.T, store *MemoryStore) []byte {
	t.Helper()
	raw, err := json.Marshal([]any{store.appeals, store.finals, store.submissions, store.adjustments, store.next})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type appealReviewFailureCase struct {
	name          string
	tenant        string
	missingAppeal bool
	status        string
	stale         bool
	examTarget    bool
	finalID       string
	score         float64
	want          error
}

func assertAppealReviewFailureKeepsFacts(t *testing.T, tc appealReviewFailureCase) {
	t.Helper()
	store, item := batch27ReviewStore(t, tc.examTarget)
	store.finals["foreign-exam"] = FinalGradeSeed{ID: "foreign-exam", ExamID: "exam-2", SubmissionID: item.SubmissionID, Score: 2, MaxScore: 5}
	store.finals["foreign-submission"] = FinalGradeSeed{ID: "foreign-submission", ExamID: item.ExamID, SubmissionID: "submission-2", Score: 2, MaxScore: 5}
	if tc.status != "" {
		item.Status = tc.status
		store.appeals[item.ID] = item
	}
	input := ReviewAppealInput{Status: " score_adjusted ", Reason: " evidence ", AssignedTo: " teacher-2 ", FinalGradeID: tc.finalID, AdjustedScore: &tc.score, ExpectedRevision: item.Revision}
	if tc.stale {
		input.ExpectedRevision = 0
	}
	requestTenant, requestID := tenantID, item.ID
	if tc.tenant != "" {
		requestTenant = tc.tenant
	}
	if tc.missingAppeal {
		requestID = "missing-appeal"
	}
	// 连同 ID 计数记录所有持久事实；调分失败不能留下局部裁决字段或半次分数变更。
	before := batch27AppealFacts(t, store)
	got, adjustment, err := store.ReviewAppeal(context.Background(), requestTenant, requestID, "manager-1", input)
	if !errors.Is(err, tc.want) || got.ID != "" || adjustment != nil || !bytes.Equal(before, batch27AppealFacts(t, store)) {
		t.Fatalf("appeal=%#v adjustment=%#v err=%v want=%v", got, adjustment, err, tc.want)
	}
}

type appealAdjustmentHistoryCase struct {
	name       string
	examTarget bool
	finalID    string
	score      float64
}

func assertAppealAdjustmentHistoryAndDelta(t *testing.T, tc appealAdjustmentHistoryCase) {
	t.Helper()
	store, item := batch27ReviewStore(t, tc.examTarget)
	next := store.next
	got, adjustment, err := store.ReviewAppeal(context.Background(), tenantID, item.ID, "manager-1", ReviewAppealInput{
		Status: " score_adjusted ", Reason: " evidence ", AssignedTo: " teacher-2 ", FinalGradeID: tc.finalID, AdjustedScore: &tc.score, ExpectedRevision: item.Revision,
	})
	if err != nil || adjustment == nil {
		t.Fatalf("adjustment=%#v err=%v", adjustment, err)
	}
	wantAdjustment := ScoreAdjustment{
		ID: adjustment.ID, TenantID: tenantID, AppealID: item.ID, ExamID: item.ExamID, SubmissionID: item.SubmissionID,
		SubmissionGradeID: item.SubmissionGradeID, FinalGradeID: "final-1", QuestionID: "question-1", QuestionNo: "Q1",
		PreviousScore: 4, AdjustedScore: tc.score, Delta: tc.score - 4, Reason: "evidence", AdjustedBy: "manager-1", CreatedAt: got.UpdatedAt,
	}
	final, submission := store.finals["final-1"], store.submissions[key(item.ExamID, item.StudentID)]
	if got.Status != "score_adjusted" || got.Revision != item.Revision+1 || got.AssignedTo != "teacher-2" || got.ReviewedBy != "manager-1" || got.ResultReason != "evidence" || got.ReviewedAt == nil || !got.ReviewedAt.Equal(got.UpdatedAt) || got.FinalGradeID != "final-1" || got.QuestionNo != "Q1" {
		t.Fatalf("reviewed appeal=%#v", got)
	}
	if !reflect.DeepEqual(*adjustment, wantAdjustment) || adjustment.ID == "" || !reflect.DeepEqual(got.Adjustments, []ScoreAdjustment{wantAdjustment}) || store.next != next+1 || final.Score != tc.score || final.Status != "locked" || !final.Locked || submission.TotalScore != 8+tc.score-4 || submission.Status != "published" || !submission.Locked {
		t.Fatalf("adjustment=%#v final=%#v submission=%#v next=%d", adjustment, final, submission, store.next)
	}
	before := batch27AppealFacts(t, store)
	_, _, err = store.ReviewAppeal(context.Background(), tenantID, item.ID, "manager-1", ReviewAppealInput{Status: "score_adjusted", Reason: "repeat", AdjustedScore: &tc.score, ExpectedRevision: got.Revision})
	if !errors.Is(err, ErrInvalidTransition) || !bytes.Equal(before, batch27AppealFacts(t, store)) {
		t.Fatalf("repeat terminal review err=%v", err)
	}
}
