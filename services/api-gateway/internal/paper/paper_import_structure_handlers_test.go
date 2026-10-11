package paper_test

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStructureRoutesRequirePermissionAndExplicitCompletePaper(t *testing.T) {
	store := paper.NewMemoryStore()
	ctx := context.Background()
	job, _ := store.CreatePaperImport(ctx, "tenant-paper", "exam-1", "u-paper", paper.CreatePaperImportInput{Subject: "math", Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: "asset", RoleHint: "question"}}})
	job, _ = store.CompletePaperImportCandidates(ctx, "tenant-paper", job.ID, paper.PaperImportParseResult{Documents: []paper.PaperImportDetectedDocument{{SourceID: job.Sources[0].ID, DetectedRole: "question"}}, QuestionCandidates: []paper.QuestionCandidate{{QuestionNoRaw: "1", QuestionType: "single_choice", Stem: "试卷题干"}}})
	router := testRouter(authStoreWithPermissions(t, []string{"exam:manage"}), store)
	token := login(t, router)
	previewPath := "/api/v1/paper-imports/" + job.ID + "/structure-preview"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, authedRequest(http.MethodGet, previewPath, nil, ""))
	if rec.Code != 401 {
		t.Fatalf("unauthorized preview=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, authedRequest(http.MethodGet, previewPath, nil, token))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var preview paper.PaperStructurePreview
	json.Unmarshal(rec.Body.Bytes(), &preview)
	for _, test := range []struct {
		generation int64
		complete   bool
		status     int
	}{{job.Generation, false, 400}, {job.Generation + 1, true, 409}, {job.Generation, true, 200}} {
		payload := fmt.Sprintf(`{"expected_generation":%d,"preview_hash":%q,"complete_paper_confirmed":%t}`, test.generation, preview.PreviewHash, test.complete)
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/paper-imports/"+job.ID+"/adopt-structure", bytes.NewBufferString(payload), token))
		if rec.Code != test.status {
			t.Fatalf("adopt status %d, want %d: %s", rec.Code, test.status, rec.Body.String())
		}
	}
	denied := testRouter(authStoreWithPermissions(t, nil), store)
	rec = httptest.NewRecorder()
	denied.ServeHTTP(rec, authedRequest(http.MethodGet, previewPath, nil, login(t, denied)))
	if rec.Code != 403 {
		t.Fatalf("missing permission=%d", rec.Code)
	}
}
