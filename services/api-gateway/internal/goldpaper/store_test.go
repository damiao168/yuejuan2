package goldpaper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestGoldPaperVersionApprovalIsolationAndActiveQuery(t *testing.T) {
	store := NewMemoryStore()
	seedSource(store, "tenant-a", "exam-1", "question-1", "submission-1", "grade-1", 5)
	item, err := store.Nominate(context.Background(), "tenant-a", "exam-1", "question-1", "chief-1", NominateInput{
		SubmissionID: "submission-1", ReferenceScore: 2, Explanation: "关键概念正确但论证不完整", ErrorTags: []string{"missing_evidence"}, SourceGradeIDs: []string{"grade-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != StatusPendingApproval || len(item.Versions) != 1 || item.Versions[0].ApprovedAt != nil {
		t.Fatalf("unexpected nomination: %+v", item)
	}
	item, err = store.Approve(context.Background(), "tenant-a", item.ID, "chief-2", 1)
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != StatusActive || item.ActiveVersion != 1 || item.Versions[0].ApprovedAt == nil {
		t.Fatalf("unexpected approval: %+v", item)
	}
	if _, err = store.Approve(context.Background(), "tenant-a", item.ID, "chief-2", 1); err != ErrAlreadyApproved {
		t.Fatalf("expected immutable approved version, got %v", err)
	}
	if _, err = store.Get(context.Background(), "tenant-b", item.ID); err != ErrNotFound {
		t.Fatalf("cross-tenant read leaked: %v", err)
	}

	item, err = store.CreateVersion(context.Background(), "tenant-a", item.ID, "chief-1", CreateVersionInput{ReferenceScore: 3, Explanation: "复核后更新标准分，保留旧版", ErrorTags: []string{"missing_evidence"}, SourceGradeIDs: []string{"grade-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != StatusActive || item.ActiveVersion != 1 || len(item.Versions) != 2 {
		t.Fatalf("pending revision must not deactivate approved version: %+v", item)
	}
	active, err := store.ListActiveApproved(context.Background(), "tenant-a", "exam-1", "question-1")
	if err != nil || len(active) != 1 || active[0].ActiveVersion != 1 {
		t.Fatalf("active query failed: %+v %v", active, err)
	}
	item, err = store.Approve(context.Background(), "tenant-a", item.ID, "chief-2", 2)
	if err != nil {
		t.Fatal(err)
	}
	if item.ActiveVersion != 2 || item.Versions[0].ReferenceScore != 2 || item.Versions[0].ApprovedAt == nil {
		t.Fatalf("old approved version mutated: %+v", item)
	}
}

func TestGoldCoverageUsesOnlyApprovedActiveVersions(t *testing.T) {
	store := NewMemoryStore()
	for i, score := range []float64{0, 2, 5} {
		submission, grade := string(rune('a'+i)), string(rune('x'+i))
		seedSource(store, "tenant-a", "exam-1", "question-1", submission, grade, 5)
		item, err := store.Nominate(context.Background(), "tenant-a", "exam-1", "question-1", "chief", NominateInput{SubmissionID: submission, ReferenceScore: score, Explanation: "覆盖评分尺度", ErrorTags: []string{"typical_error"}, SourceGradeIDs: []string{grade}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.Approve(context.Background(), "tenant-a", item.ID, "approver", 1); err != nil {
			t.Fatal(err)
		}
	}
	coverage, err := store.Coverage(context.Background(), "tenant-a", "exam-1", "question-1")
	if err != nil {
		t.Fatal(err)
	}
	if !coverage.Ready || coverage.ActiveApprovedCount != 3 || len(coverage.Gaps) != 0 {
		t.Fatalf("unexpected coverage: %+v", coverage)
	}
}

func TestNominationRejectsUnrelatedGrade(t *testing.T) {
	store := NewMemoryStore()
	seedSource(store, "tenant-a", "exam-1", "question-1", "submission-1", "grade-1", 5)
	_, err := store.Nominate(context.Background(), "tenant-a", "exam-1", "question-1", "chief", NominateInput{SubmissionID: "submission-1", ReferenceScore: 2, Explanation: "candidate", SourceGradeIDs: []string{"another-grade"}})
	if err != ErrSourceGradeMissing {
		t.Fatalf("expected source-grade rejection, got %v", err)
	}
}

func TestRoutesRequireManageAndDTOIsDeidentified(t *testing.T) {
	store := NewMemoryStore()
	seedSource(store, "tenant-a", "exam-1", "question-1", "submission-1", "grade-1", 5)
	item, err := store.Nominate(context.Background(), "tenant-a", "exam-1", "question-1", "chief", NominateInput{SubmissionID: "submission-1", ReferenceScore: 2, Explanation: "candidate", SourceGradeIDs: []string{"grade-1"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, nil)
	mux := http.NewServeMux()
	read := func(next http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next(w, r.WithContext(auth.WithUser(r.Context(), auth.User{ID: "reader", TenantID: "tenant-a"})))
		})
	}
	manage := func(http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "forbidden", http.StatusForbidden) })
	}
	RegisterRoutes(mux, handler, read, manage)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/exam-1/questions/question-1/gold-papers", strings.NewReader(`{"submission_id":"submission-1","reference_score":2,"explanation":"x","source_grade_ids":["grade-1"]}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("management route bypassed permission wrapper: %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/gold-papers/"+item.ID, nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET failed: %d %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	for _, forbidden := range []string{"candidate_no", "student_id", "student_name", "private_note", "prompt"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("sensitive field %q leaked: %s", forbidden, body)
		}
	}
}

func seedSource(store *MemoryStore, tenant, exam, question, submission, grade string, maxScore float64) {
	store.SetSource(tenant, Source{ExamID: exam, QuestionID: question, SubmissionID: submission, SnapshotID: "snapshot-1", SubjectCode: "general", ArchetypeCode: "short_constructed", RiskTier: "R3", MaxScore: maxScore, RubricSnapshot: map[string]any{"criteria": []any{}}, AvailableGradeIDs: []string{grade}, ImageBinding: ImageBinding{SegmentID: "segment-" + submission, AssetID: "asset-" + submission, SHA256: strings.Repeat("a", 64), OwnerType: "answer_segment_crop", OwnerID: "run-" + submission}})
}
