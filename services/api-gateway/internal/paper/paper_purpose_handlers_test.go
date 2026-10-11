package paper_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestExamPapersAndAnswerSheetsHaveSeparateLists(t *testing.T) {
	store := paper.NewMemoryStore()
	router := testRouter(authStoreWithPermissions(t, []string{"exam:manage"}), store)
	token := login(t, router)
	questionPaper := createPaper(t, router, token, "exam-1")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/exams/exam-1/papers", bytes.NewBufferString(`{"file_asset_id":"sheet-file","purpose":"answer_sheet"}`), token))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload source registration: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Paper paper.Paper `json:"paper"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ query, expectedID string }{{"", questionPaper.ID}, {"?purpose=answer_sheet", created.Paper.ID}} {
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/exams/exam-1/papers"+test.query, nil, token))
		var listed struct {
			Papers []paper.Paper `json:"papers"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusOK || len(listed.Papers) != 1 || listed.Papers[0].ID != test.expectedID {
			t.Fatalf("mixed source lists: %d %s", rec.Code, rec.Body.String())
		}
	}
	// 独立答题卡仍能作为模板底图，不要求先登记考试试卷。
	payload, _ := json.Marshal(map[string]any{"exam_paper_id": created.Paper.ID, "name": "空白答题卡", "page_count": 1, "layout": map[string]any{"pages": []any{map[string]any{"page_no": 1, "width": 1000, "height": 1400, "registration_marks": []any{}, "identity_regions": []any{}, "question_regions": []any{}}}}})
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/exams/exam-1/answer-sheet-templates", bytes.NewBuffer(payload), token))
	if rec.Code != http.StatusCreated {
		t.Fatalf("answer sheet template creation: %d %s", rec.Code, rec.Body.String())
	}
}
