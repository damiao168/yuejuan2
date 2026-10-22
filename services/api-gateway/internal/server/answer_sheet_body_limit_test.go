package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnswerSheetPageOrderBodyLimitIsIsolatedFromOrdinaryJSONAndFiles(t *testing.T) {
	for _, test := range []struct {
		method, path          string
		skipGlobal, skipFiles bool
	}{
		{http.MethodPost, "/api/v1/exams/exam/answer-sheet-page-order", true, true},
		{http.MethodPost, "/api/v1/files", true, false},
		{http.MethodPost, "/api/v1/exams/exam/papers", false, true},
		{http.MethodPost, "/api/v1/exams/exam/nested/answer-sheet-page-order", false, true},
		{http.MethodGet, "/api/v1/exams/exam/answer-sheet-page-order", false, true},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		if skipGlobalBodyLimit(request) != test.skipGlobal || skipNonFileUpload(request) != test.skipFiles {
			t.Fatalf("body limit dispatch changed for %s %s", test.method, test.path)
		}
	}
}
