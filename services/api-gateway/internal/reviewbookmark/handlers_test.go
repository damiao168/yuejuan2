package reviewbookmark

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestPutRequiresExplicitVersionAndReason(t *testing.T) {
	for _, body := range []string{`{}`, `{"reason":""}`, `{"expected_revision":0}`, `{"reason":"","expected_revision":0,"owner_id":"other"}`, `{"reason":"","expected_revision":0} {}`, `null`, `{"reason":null,"expected_revision":0}`, `{"reason":"` + strings.Repeat("x", 8192) + `","expected_revision":0}`, "{\"reason\":\"\xff\",\"expected_revision\":0}"} {
		t.Run(body[:min(len(body), 40)], func(t *testing.T) {
			store, _ := testStore()
			handler := NewHandler(store)
			request := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
			request.SetPathValue("taskId", "task")
			request = request.WithContext(auth.WithUser(context.Background(), testUser()))
			response := httptest.NewRecorder()
			handler.Put(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("got %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestPutAllowsEmptyReasonAndUnicodeCodepoints(t *testing.T) {
	store, _ := testStore()
	handler := NewHandler(store)
	for _, entry := range []struct {
		body string
		want int
	}{{`{"reason":"","expected_revision":0}`, http.StatusOK}, {`{"reason":"` + strings.Repeat("😀", 200) + `","expected_revision":1}`, http.StatusOK}, {`{"reason":"` + strings.Repeat("😀", 201) + `","expected_revision":2}`, http.StatusBadRequest}} {
		request := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(entry.body))
		request.SetPathValue("taskId", "task")
		request = request.WithContext(auth.WithUser(context.Background(), testUser()))
		response := httptest.NewRecorder()
		handler.Put(response, request)
		if response.Code != entry.want {
			t.Fatalf("got %d want %d: %s", response.Code, entry.want, response.Body.String())
		}
	}
}
