package paper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPDocumentParserResponses(t *testing.T) {
	resultFrame := `{"documents":[],"question_candidates":[],"answer_candidates":[],"solution_candidates":[],"rubric_candidates":[],"issues":[]}`
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		wantErr     string
		wantEvents  int
	}{
		{name: "regular json", status: http.StatusOK, contentType: "application/json", body: resultFrame},
		{name: "ndjson progress and result", status: http.StatusOK, contentType: "application/x-ndjson", body: `{"type":"progress","progress":{"phase":"parse"}}` + "\n" + `{"type":"result","result":` + resultFrame + `}` + "\n", wantEvents: 1},
		{name: "error frame", status: http.StatusOK, contentType: "application/x-ndjson", body: `{"type":"error","error":{"status":422,"error":{"code":"parser_failed","message":"failed"}}}` + "\n", wantErr: "parser_failed"},
		{name: "malformed frame", status: http.StatusOK, contentType: "application/x-ndjson", body: "{bad}\n", wantErr: "invalid JSON"},
		{name: "duplicate result", status: http.StatusOK, contentType: "application/x-ndjson", body: `{"type":"result","result":` + resultFrame + `}` + "\n" + `{"type":"result","result":` + resultFrame + `}` + "\n", wantErr: "invalid result"},
		{name: "stream without result", status: http.StatusOK, contentType: "application/x-ndjson", body: `{"type":"progress","progress":{}}` + "\n", wantErr: "without a result", wantEvents: 1},
		{name: "non 2xx", status: http.StatusBadGateway, contentType: "application/json", body: `{}`, wantErr: "returned 502"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			parser := NewHTTPDocumentParser(server.URL, strings.Repeat("t", 32), time.Second)
			events := 0
			_, err := parser.Parse(context.Background(), DocumentParseRequest{}, func(map[string]any) error {
				events++
				return nil
			})
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("error = %v, want containing %q", err, test.wantErr)
			}
			if events != test.wantEvents {
				t.Fatalf("progress events = %d, want %d", events, test.wantEvents)
			}
		})
	}
}

func TestHTTPDocumentParserRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"padding":"` + strings.Repeat("x", maxDocumentParseResponseBytes) + `"}`))
	}))
	defer server.Close()
	parser := NewHTTPDocumentParser(server.URL, strings.Repeat("t", 32), 2*time.Second)
	if _, err := parser.Parse(context.Background(), DocumentParseRequest{}, nil); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestHTTPDocumentParserHonorsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	parser := NewHTTPDocumentParser(server.URL, strings.Repeat("t", 32), 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := parser.Parse(ctx, DocumentParseRequest{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}
