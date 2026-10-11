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

func TestBatch43HTTPDocumentParserKeepsStreamFailureAfterCandidateResult(t *testing.T) {
	callbackErr := errors.New("synthetic progress persistence failure")
	resultFrame := `{"type":"result","result":{"issues":[{"code":"candidate_result"}]}}` + "\n"
	errorFrame := `{"type":"error","error":{"error":{"code":" late_failure "}}}` + "\n"
	progressFrame := `{"type":"progress","progress":{"phase":"parse"}}` + "\n"
	// result 只是候选结果；后续错误帧仍失败，回调失败则先于尚未分派的帧原样返回。
	tests := []struct {
		name        string
		body        string
		progressErr error
		wantErr     error
		wantText    string
		wantEvents  int
	}{
		{name: "error after result", body: resultFrame + errorFrame, wantText: "paper parser stream failed: late_failure"},
		{name: "callback stops later frames", body: progressFrame + resultFrame + errorFrame, progressErr: callbackErr, wantErr: callbackErr, wantEvents: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/x-ndjson")
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			parser := NewHTTPDocumentParser(server.URL, strings.Repeat("t", 32), time.Second)
			events := 0
			result, err := parser.Parse(context.Background(), DocumentParseRequest{}, func(map[string]any) error {
				events++
				return test.progressErr
			})
			if test.wantErr != nil {
				if err != test.wantErr {
					t.Fatalf("callback error = %v, want original %v", err, test.wantErr)
				}
			} else if err == nil || err.Error() != test.wantText {
				t.Fatalf("stream error = %v, want %q", err, test.wantText)
			}
			if len(result.Issues) != 0 || events != test.wantEvents {
				t.Fatalf("failed stream returned candidate result or wrong event count: result=%+v events=%d", result, events)
			}
		})
	}
}
