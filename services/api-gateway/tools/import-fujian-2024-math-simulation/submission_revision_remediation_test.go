package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPrepareSegmentsUsesQualityCheckedRevision(t *testing.T) {
	for _, test := range []struct {
		name, submission, wantError string
		status                      int
	}{
		{name: "current_revision", submission: `{"quality_status":"passed","revision":7}`, status: http.StatusOK},
		{name: "changed_after_quality", submission: `{"quality_status":"passed","revision":7}`, status: http.StatusConflict, wantError: "mark C001 ready for OCR"},
		{name: "missing_revision", submission: `{"quality_status":"passed"}`, wantError: "valid revision"},
		{name: "fractional_revision", submission: `{"quality_status":"passed","revision":7.5}`, wantError: "valid revision"},
		{name: "requires_review", submission: `{"quality_status":"review","revision":7}`, wantError: "requires operator action"},
	} {
		t.Run(test.name, func(t *testing.T) {
			statusCalls, segmentCalls := 0, 0
			client := batch9Client(func(request *http.Request) (*http.Response, error) {
				switch request.URL.Path {
				case "/api/v1/submissions/submission/quality-check":
					return batch9Response(request, http.StatusOK, `{"result":{"valid":true}}`), nil
				case "/api/v1/submissions/submission/run-quality-check":
					return batch9Response(request, http.StatusAccepted, `{}`), nil
				case "/api/v1/submissions/submission":
					return batch9Response(request, http.StatusOK, `{"submission":`+test.submission+`}`), nil
				case "/api/v1/submissions/submission/status":
					statusCalls++
					var body struct {
						Status   string `json:"status"`
						Revision int    `json:"expected_revision"`
					}
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Status != "ready_for_ocr" || body.Revision != 7 {
						t.Fatalf("transition body=%+v, err=%v", body, err)
					}
					return batch9Response(request, test.status, `{}`), nil
				case "/api/v1/submissions/submission/segment-answers":
					segmentCalls++
					return batch9Response(request, http.StatusOK, `{"result":{"segments":[{"id":"segment","question_no":"Q1"}]}}`), nil
				default:
					t.Fatalf("unexpected request: %s", request.URL.Path)
					return nil, nil
				}
			})
			run := candidateImport{candidate: candidateDef{CandidateNo: "C001", Synthetic: true}, client: client, submissionID: "submission", data: dataset{Questions: []questionDef{{QuestionNo: "Q1"}}}}
			result := importResult{}
			err := run.prepareSegments(context.Background(), &result)
			if test.wantError == "" {
				if err != nil || statusCalls != 1 || segmentCalls != 1 || result.AnswerSegmentCount != 1 {
					t.Fatalf("success: err=%v status=%d segment=%d result=%+v", err, statusCalls, segmentCalls, result)
				}
			} else {
				// 状态冲突是新事实的信号；导入必须停住，不能换修订号重试后继续分段。
				if err == nil || !strings.Contains(err.Error(), test.wantError) || segmentCalls != 0 || statusCalls > 1 {
					t.Fatalf("rejected: err=%v status=%d segment=%d", err, statusCalls, segmentCalls)
				}
			}
		})
	}
}
