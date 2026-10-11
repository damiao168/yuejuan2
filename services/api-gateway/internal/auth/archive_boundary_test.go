package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type archiveBoundaryFixture struct {
	*MemoryStore
	archived  bool
	lookupErr error
	lookups   int
}

func (s *archiveBoundaryFixture) IsExamArchived(context.Context, string, string) (bool, error) {
	s.lookups++
	return s.archived, s.lookupErr
}

func TestArchivedResourceBoundaryRejectsWritesAndPreservesReads(t *testing.T) {
	for _, scenario := range []struct {
		name, method string
		archived     bool
		err          error
		want         int
	}{
		{"archived mutation", http.MethodPost, true, nil, http.StatusConflict},
		{"archived result read", http.MethodGet, true, nil, http.StatusOK},
		{"restored mutation", http.MethodPost, false, nil, http.StatusOK},
		{"archive lookup unavailable", http.MethodPost, false, errors.New("source unavailable"), http.StatusInternalServerError},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store := &archiveBoundaryFixture{MemoryStore: NewMemoryStore(), archived: scenario.archived, lookupErr: scenario.err}
			store.AddResourceBoundary("exam", "exam-1", ResourceBoundary{TenantID: "tenant-1", ExamID: "exam-1"})
			mux := http.NewServeMux()
			mux.Handle(scenario.method+" /exams/{id}", RequireRequestResourceBoundary(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })))
			req := httptest.NewRequest(scenario.method, "/exams/exam-1", nil)
			req = req.WithContext(WithAccessScope(req.Context(), AccessScope{TenantID: "tenant-1", ExamIDs: []string{"exam-1"}}))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != scenario.want {
				t.Fatalf("expected %d, got %d: %s", scenario.want, rec.Code, rec.Body.String())
			}
			if scenario.want == http.StatusConflict && !strings.Contains(rec.Body.String(), "exam_archived") {
				t.Fatalf("missing actionable archive error: %s", rec.Body.String())
			}
			if scenario.method == http.MethodGet && store.lookups != 0 {
				t.Fatal("read must remain independent of archive mutation lookup")
			}
		})
	}
}

func TestArchiveBoundaryDoesNotLeakArchiveFactsBeforePermissionCheck(t *testing.T) {
	store := &archiveBoundaryFixture{MemoryStore: NewMemoryStore(), archived: true}
	store.AddResourceBoundary("exam", "foreign", ResourceBoundary{TenantID: "tenant-1", ExamID: "foreign"})
	mux := http.NewServeMux()
	mux.Handle("POST /exams/{id}", RequireRequestResourceBoundary(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })))
	req := httptest.NewRequest(http.MethodPost, "/exams/foreign", nil)
	req = req.WithContext(WithAccessScope(req.Context(), AccessScope{TenantID: "tenant-1", ExamIDs: []string{"allowed"}}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || store.lookups != 0 {
		t.Fatalf("permission must precede archive lookup: status=%d lookups=%d", rec.Code, store.lookups)
	}
}
