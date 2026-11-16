package capture

import (
	"context"
	"net/http"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
)

type collectionHTTPFixture struct {
	Store
	summary       CollectionSummary
	readErr       error
	fileExamErr   error
	completeErr   error
	excludeErr    error
	completeInput CompleteCollectionInput
	excludeInput  ExcludeFileInput
	exclusions    int
}

func (s *collectionHTTPFixture) GetCollectionFileExam(_ context.Context, tenantID, fileID string) (string, error) {
	if tenantID != "tenant" || fileID != "source" {
		return "", ErrNotFound
	}
	return s.summary.ExamID, s.fileExamErr
}

func (s *collectionHTTPFixture) GetCollectionSummary(_ context.Context, tenantID, examID string) (CollectionSummary, error) {
	if tenantID != "tenant" || examID != s.summary.ExamID {
		return CollectionSummary{}, ErrNotFound
	}
	return s.summary, s.readErr
}

func (s *collectionHTTPFixture) CompleteCollection(_ context.Context, tenantID, examID, actorID string, input CompleteCollectionInput) (CollectionSummary, error) {
	if tenantID != "tenant" || examID != s.summary.ExamID || actorID != "operator" {
		return CollectionSummary{}, ErrNotFound
	}
	s.completeInput = input
	return s.summary, s.completeErr
}

func (s *collectionHTTPFixture) ExcludeFile(_ context.Context, tenantID, fileID, actorID string, input ExcludeFileInput) (Batch, error) {
	if tenantID != "tenant" || fileID != "source" || actorID != "operator" {
		return Batch{}, ErrNotFound
	}
	s.exclusions++
	s.excludeInput = input
	return Batch{ID: "batch", ExamID: s.summary.ExamID, Status: "processing"}, s.excludeErr
}

type collectionHTTPExam struct{ exam.Store }

func (collectionHTTPExam) GetExam(_ context.Context, scope auth.AccessScope, id string) (exam.Exam, error) {
	if scope.TenantID != "tenant" || !scope.AllowsExam(id) {
		return exam.Exam{}, exam.ErrNotFound
	}
	return exam.Exam{ID: id}, nil
}

type collectionAuditRecorder struct {
	auth.Store
	events []auth.AuditEvent
}

func (r *collectionAuditRecorder) Audit(_ context.Context, event auth.AuditEvent) error {
	r.events = append(r.events, event)
	return nil
}

func collectionHTTPRoutes(handler *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/exams/{examId}/capture-summary", handler.GetCollectionSummary)
	mux.HandleFunc("POST /api/v1/exams/{examId}/complete-collection", handler.CompleteCollection)
	mux.HandleFunc("POST /api/v1/capture-files/{id}/exclude", handler.ExcludeFile)
	return mux
}

func TestCollectionHTTPShowsIncompleteRosterAndMapsCompletionConflicts(t *testing.T) {
	store := &collectionHTTPFixture{summary: CollectionSummary{
		ExamID: "exam", ExamStatus: "collecting", Revision: 8, PendingCount: 1, PagesPerStudent: 2,
		Entries: []CollectionEntry{{StudentID: "student", Status: "needs_attention", Code: "missing_pages", ExpectedPages: 2, ActualPages: 1}},
	}}
	audit := &collectionAuditRecorder{}
	mux := collectionHTTPRoutes(NewHandler(store, nil, collectionHTTPExam{}, nil, audit))
	const summary = "/api/v1/exams/exam/capture-summary"
	const complete = "/api/v1/exams/exam/complete-collection"
	type summaryResponse struct{ Summary CollectionSummary }
	state := captureWorkflowJSON[summaryResponse](t, captureWorkflowRequest(t, mux, "GET", summary, "", nil, nil), http.StatusOK).Summary
	if state.CanComplete || state.Revision != 8 || len(state.Entries) != 1 || state.Entries[0].Code != "missing_pages" || state.Entries[0].ActualPages != 1 {
		t.Fatalf("incomplete roster was not reflected by the summary: %+v", state)
	}
	store.readErr = ErrNotFound
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "GET", summary, "", nil, nil), http.StatusNotFound, "capture_not_found")
	store.completeErr = ErrInvalidTransition
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", complete, `{"revision":8,"idempotency_key":"blocked"}`, nil, nil), http.StatusConflict, "capture_invalid_transition")
	store.completeErr = ErrConflict
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", complete, `{"revision":7,"idempotency_key":"stale"}`, nil, nil), http.StatusConflict, "capture_revision_conflict")
	if len(audit.events) != 0 {
		t.Fatalf("unsuccessful operations wrote audit events: %+v", audit.events)
	}
	store.completeErr = nil
	store.summary = CollectionSummary{ExamID: "exam", ExamStatus: "collecting", Revision: 10, Closed: true, ReadyCount: 1}
	state = captureWorkflowJSON[summaryResponse](t, captureWorkflowRequest(t, mux, "POST", complete, `{"revision":9,"idempotency_key":"finish","reason":"roster reconciled"}`, nil, nil), http.StatusOK).Summary
	if !state.Closed || state.Revision != 10 || store.completeInput.Revision != 9 || store.completeInput.IdempotencyKey != "finish" {
		t.Fatalf("completion lost the command identity or receipt: input=%+v state=%+v", store.completeInput, state)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "capture_collection.completed" || audit.events[0].TargetID != "exam" || audit.events[0].Reason != "roster reconciled" {
		t.Fatalf("completion audit missing or inaccurate: %+v", audit.events)
	}
}

func TestCollectionHTTPChecksExamScopeBeforeExcludingOwnedFile(t *testing.T) {
	store := &collectionHTTPFixture{summary: CollectionSummary{ExamID: "exam"}}
	audit := &collectionAuditRecorder{}
	mux := collectionHTTPRoutes(NewHandler(store, nil, collectionHTTPExam{}, nil, audit))
	const path = "/api/v1/capture-files/source/exclude"
	const body = `{"reason":"superseded scan","replacement_file_id":"replacement"}`
	allowed := auth.AccessScope{TenantID: "tenant", ExamIDs: []string{"exam"}}
	otherExam := auth.AccessScope{TenantID: "tenant", ExamIDs: []string{"other-exam"}}

	// 文件属于当前租户时仍需核对考试范围。授权失败不能调用排除写入，也不能记成功审计。
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path, body, nil, nil), http.StatusForbidden, "access_scope_missing")
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path, body, &otherExam, nil), http.StatusNotFound, "exam_not_found")
	store.fileExamErr = ErrNotFound
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path, body, &allowed, nil), http.StatusNotFound, "capture_not_found")
	store.fileExamErr = nil
	if store.exclusions != 0 || len(audit.events) != 0 {
		t.Fatalf("scope checks reached mutation or audit: exclusions=%d events=%+v", store.exclusions, audit.events)
	}
	store.excludeErr = ErrFileHasGrades
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path, body, &allowed, nil), http.StatusConflict, "capture_file_has_grades")
	if store.exclusions != 1 || len(audit.events) != 0 {
		t.Fatalf("protecting graded file wrote success audit: exclusions=%d events=%+v", store.exclusions, audit.events)
	}
	store.excludeErr = nil
	batch := captureWorkflowJSON[map[string]Batch](t, captureWorkflowRequest(t, mux, "POST", path, body, &allowed, nil), http.StatusOK)["batch"]
	if batch.ID != "batch" || store.exclusions != 2 || store.excludeInput.ReplacementFileID != "replacement" {
		t.Fatalf("exclusion did not preserve replacement or update batch: batch=%+v input=%+v", batch, store.excludeInput)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "capture_file.excluded" || audit.events[0].TargetID != "source" || audit.events[0].Reason != "superseded scan" {
		t.Fatalf("exclusion audit missing or inaccurate: %+v", audit.events)
	}
}
