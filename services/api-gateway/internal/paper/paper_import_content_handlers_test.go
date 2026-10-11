package paper_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestFillImportContentRoutePermissionsAndGeneration(t *testing.T) {
	store := paper.NewMemoryStore()
	ctx := context.Background()
	if _, err := store.CreateQuestion(ctx, "tenant-paper", "exam-1", "u-paper", paper.CreateQuestionInput{QuestionNo: "1", QuestionType: "single_choice", Score: 5}); err != nil {
		t.Fatal(err)
	}
	job, err := store.CreatePaperImport(ctx, "tenant-paper", "exam-1", "u-paper", paper.CreatePaperImportInput{Subject: "mathematics", Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: "asset", RoleHint: "auto"}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.CompletePaperImportCandidates(ctx, "tenant-paper", job.ID, paper.PaperImportParseResult{QuestionCandidates: []paper.QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "single_choice", Stem: "题干"}}, AnswerCandidates: []paper.AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "B"}}})
	if err != nil {
		t.Fatal(err)
	}
	router := testRouter(authStoreWithPermissions(t, []string{"exam:manage"}), store)
	token := login(t, router)
	path := "/api/v1/paper-imports/" + job.ID + "/fill-content"
	for _, test := range []struct {
		body, token string
		status      int
	}{
		{fmt.Sprintf(`{"expected_generation":%d}`, job.Generation), "", http.StatusUnauthorized},
		{`{}`, token, http.StatusBadRequest},
		{fmt.Sprintf(`{"expected_generation":%d}`, job.Generation+1), token, http.StatusConflict},
		{fmt.Sprintf(`{"expected_generation":%d}`, job.Generation), token, http.StatusOK},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, authedRequest(http.MethodPost, path, bytes.NewBufferString(test.body), test.token))
		if rec.Code != test.status {
			t.Fatalf("route status %d, want %d: %s", rec.Code, test.status, rec.Body.String())
		}
		if rec.Code == http.StatusOK {
			var result paper.PaperImportContentResult
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || len(result.FilledQuestions) != 1 {
				t.Fatalf("unexpected fill result: %s", rec.Body.String())
			}
		}
	}
	noPermissionRouter := testRouter(authStoreWithPermissions(t, nil), store)
	noPermissionToken := login(t, noPermissionRouter)
	rec := httptest.NewRecorder()
	noPermissionRouter.ServeHTTP(rec, authedRequest(http.MethodPost, path, bytes.NewBufferString(fmt.Sprintf(`{"expected_generation":%d}`, job.Generation)), noPermissionToken))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing exam permission status = %d", rec.Code)
	}
}
