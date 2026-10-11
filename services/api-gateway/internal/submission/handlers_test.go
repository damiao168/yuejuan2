package submission_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/server"
	"edugrade-enterprise/services/api-gateway/internal/submission"
)

const tenantID = "00000000-0000-0000-0000-000000000002"
const userID = "00000000-0000-0000-0000-000000000301"

func TestSubmissionQualityGateFindsMissingPages(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"submission:manage"})
	fileStore := files.NewMemoryStore()
	fileAsset := createFileAsset(t, fileStore)
	router := testRouter(authStore, fileStore, submission.NewMemoryStore())
	token := login(t, router)

	item := createSubmission(t, router, token, "exam-1", 2)
	addPage(t, router, token, item.ID, fileAsset.ID, 1)

	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/quality-check", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "page_count_mismatch") || !strings.Contains(rec.Body.String(), "missing_page") {
		t.Fatalf("expected quality issues, got %d %s", rec.Code, rec.Body.String())
	}

	req = authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/status", bytes.NewBufferString(`{"status":"ready_for_ocr","expected_revision":1}`), token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected ready_for_ocr transition conflict, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestSubmissionCanBecomeReadyForOCRAfterQualityPass(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"submission:manage"})
	fileStore := files.NewMemoryStore()
	fileAsset := createFileAsset(t, fileStore)
	submissionStore := submission.NewMemoryStore()
	router := testRouter(authStore, fileStore, submissionStore)
	token := login(t, router)

	item := createSubmission(t, router, token, "exam-1", 1)
	page := addPage(t, router, token, item.ID, fileAsset.ID, 1)
	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/quality-check", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"valid":true`) {
		t.Fatalf("expected collection integrity pass, got %d %s", rec.Code, rec.Body.String())
	}

	req = authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/status", bytes.NewBufferString(`{"status":"ready_for_ocr","expected_revision":1}`), token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("collection integrity alone must not enter OCR, got %d %s", rec.Code, rec.Body.String())
	}

	if _, err := submissionStore.ApplyPageQualityResult(context.Background(), tenantID, submission.ApplyPageQualityInput{
		SubmissionID:          item.ID,
		PageID:                page.ID,
		LatestQualityRunID:    "quality-run-1",
		NormalizedFileAssetID: "normalized-file-1",
		QualityStatus:         "passed",
	}); err != nil {
		t.Fatalf("apply page image quality result: %v", err)
	}
	req = authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/status", bytes.NewBufferString(`{"status":"ready_for_ocr","expected_revision":1}`), token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ready_for_ocr") {
		t.Fatalf("expected ready_for_ocr, got %d %s", rec.Code, rec.Body.String())
	}

	assertAuditAction(t, authStore, "submission.created")
	assertAuditAction(t, authStore, "submission.page_added")
	assertAuditAction(t, authStore, "submission.quality_checked")
	assertAuditAction(t, authStore, "submission.status_changed")
}

func TestCannotManuallyMarkQualityChecked(t *testing.T) {
	fileStore := files.NewMemoryStore()
	fileAsset := createFileAsset(t, fileStore)
	router := testRouter(authStoreWithPermissions(t, []string{"submission:manage"}), fileStore, submission.NewMemoryStore())
	token := login(t, router)
	item := createSubmission(t, router, token, "exam-1", 1)
	addPage(t, router, token, item.ID, fileAsset.ID, 1)

	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/status", bytes.NewBufferString(`{"status":"quality_checked","expected_revision":1}`), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("manual quality_checked transition expected 409, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestAddingPageAfterQualityPassInvalidatesGate(t *testing.T) {
	fileStore := files.NewMemoryStore()
	fileAsset := createFileAsset(t, fileStore)
	secondFile := createSecondFileAsset(t, fileStore)
	submissionStore := submission.NewMemoryStore()
	router := testRouter(authStoreWithPermissions(t, []string{"submission:manage"}), fileStore, submissionStore)
	token := login(t, router)
	item := createSubmission(t, router, token, "exam-1", 2)
	page1 := addPage(t, router, token, item.ID, fileAsset.ID, 1)
	page2 := addPage(t, router, token, item.ID, secondFile.ID, 2)
	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/quality-check", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"valid":true`) {
		t.Fatalf("collection integrity pass expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	for index, page := range []submission.SubmissionPage{page1, page2} {
		if _, err := submissionStore.ApplyPageQualityResult(context.Background(), tenantID, submission.ApplyPageQualityInput{
			SubmissionID:          item.ID,
			PageID:                page.ID,
			LatestQualityRunID:    "quality-run-" + intString(index+1),
			NormalizedFileAssetID: "normalized-file-" + intString(index+1),
			QualityStatus:         "passed",
		}); err != nil {
			t.Fatalf("apply page image quality result: %v", err)
		}
	}

	thirdFile := createThirdFileAsset(t, fileStore)
	addPage(t, router, token, item.ID, thirdFile.ID, 3)
	req = authedRequest(http.MethodGet, "/api/v1/submissions/"+item.ID, nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"pages_uploaded"`) || !strings.Contains(rec.Body.String(), `"quality_status":"unchecked"`) {
		t.Fatalf("adding a page must invalidate quality gate, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestReplacePageResetsQualityGateAndKeepsPageCount(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"submission:manage"})
	fileStore := files.NewMemoryStore()
	fileAsset := createFileAsset(t, fileStore)
	replacementFile := createSecondFileAsset(t, fileStore)
	submissionStore := submission.NewMemoryStore()
	router := testRouter(authStore, fileStore, submissionStore)
	token := login(t, router)
	item := createSubmission(t, router, token, "exam-1", 1)
	page := addPage(t, router, token, item.ID, fileAsset.ID, 1)

	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/quality-check", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"valid":true`) {
		t.Fatalf("collection integrity pass expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	if _, err := submissionStore.ApplyPageQualityResult(context.Background(), tenantID, submission.ApplyPageQualityInput{
		SubmissionID:          item.ID,
		PageID:                page.ID,
		LatestQualityRunID:    "quality-run-1",
		NormalizedFileAssetID: "normalized-file-1",
		QualityStatus:         "passed",
	}); err != nil {
		t.Fatalf("apply page image quality result: %v", err)
	}

	req = authedRequest(http.MethodPut, "/api/v1/submissions/"+item.ID+"/pages/1", bytes.NewBufferString(`{"file_asset_id":"`+replacementFile.ID+`"}`), token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), replacementFile.ID) {
		t.Fatalf("replace page expected 200 with replacement file, got %d %s", rec.Code, rec.Body.String())
	}

	req = authedRequest(http.MethodGet, "/api/v1/submissions/"+item.ID, nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"actual_page_count":1`) ||
		!strings.Contains(rec.Body.String(), `"status":"pages_uploaded"`) ||
		!strings.Contains(rec.Body.String(), `"quality_status":"unchecked"`) ||
		!strings.Contains(rec.Body.String(), replacementFile.ID) {
		t.Fatalf("replace page must reset quality gate without increasing page count, got %d %s", rec.Code, rec.Body.String())
	}

	req = authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/pages", bytes.NewBufferString(`{"file_asset_id":"`+replacementFile.ID+`","page_no":1}`), token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "duplicate_page") {
		t.Fatalf("post page must still reject duplicates after replace, got %d %s", rec.Code, rec.Body.String())
	}

	assertAuditAction(t, authStore, "submission.page_replaced")
}

func TestDuplicatePageRejected(t *testing.T) {
	fileStore := files.NewMemoryStore()
	fileAsset := createFileAsset(t, fileStore)
	router := testRouter(authStoreWithPermissions(t, []string{"submission:manage"}), fileStore, submission.NewMemoryStore())
	token := login(t, router)
	item := createSubmission(t, router, token, "exam-1", 1)
	addPage(t, router, token, item.ID, fileAsset.ID, 1)

	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/pages", bytes.NewBufferString(`{"file_asset_id":"`+fileAsset.ID+`","page_no":1}`), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "duplicate_page") {
		t.Fatalf("expected duplicate page conflict, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestPageRejectsFileOwnedByAnotherSubmission(t *testing.T) {
	fileStore := files.NewMemoryStore()
	submissionStore := submission.NewMemoryStore()
	router := testRouter(authStoreWithPermissions(t, []string{"submission:manage"}), fileStore, submissionStore)
	token := login(t, router)
	item := createSubmission(t, router, token, "exam-1", 1)
	asset, err := fileStore.Create(context.Background(), files.CreateAssetInput{
		TenantID: tenantID, SubmissionID: "another-submission", ExamID: "exam-1", OwnerType: "submission", OwnerID: "another-submission",
		OriginalName: "other.pdf", ContentType: "application/pdf", SizeBytes: 128, HashSHA256: "hash-other",
		StorageBucket: "test", StorageKey: "tenant/demo/other.pdf", Visibility: "private", UploadedBy: userID,
	})
	if err != nil {
		t.Fatalf("create mismatched file: %v", err)
	}

	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/pages", bytes.NewBufferString(`{"file_asset_id":"`+asset.ID+`","page_no":1}`), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "file_asset_scope_mismatch") {
		t.Fatalf("mismatched submission file expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestSubmissionPermissionDenied(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"system:read"}), files.NewMemoryStore(), submission.NewMemoryStore())
	token := login(t, router)
	req := authedRequest(http.MethodPost, "/api/v1/exams/exam-1/submissions", bytes.NewBufferString(validSubmissionJSON(1)), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", rec.Code, rec.Body.String())
	}
}

func testRouter(authStore *auth.MemoryStore, fileStore files.Store, submissionStore submission.Store) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
	}
	return server.NewRouterFull(cfg, logger.New(io.Discard, "error"), nil, server.IdentityStores{Auth: authStore, Org: org.NewMemoryStore()}, server.ExamPreparationStores{Exam: exam.NewMemoryStore(), Paper: paper.NewMemoryStore(), Files: fileStore, Submissions: submissionStore}, files.NewMemoryObjectStorage())
}

func authStoreWithPermissions(t *testing.T, permissions []string) *auth.MemoryStore {
	t.Helper()
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store := auth.NewMemoryStore()
	store.AddUser(auth.UserWithPassword{
		User: auth.User{
			ID:          userID,
			TenantID:    tenantID,
			TenantCode:  "demo",
			Username:    "submission_admin",
			DisplayName: "Submission Admin",
			Status:      "active",
			Roles:       []string{"teacher"},
			Permissions: permissions,
			DataScope:   map[string]any{"scope": "school", "synthetic": true},
		},
		PasswordHash: hash,
	})
	return store
}

func login(t *testing.T, router http.Handler) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"tenant_code": "demo", "username": "submission_admin", "password": "ChangeMe123!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.AccessToken
}

func createSubmission(t *testing.T, router http.Handler, token string, examID string, expectedPages int) submission.Submission {
	t.Helper()
	req := authedRequest(http.MethodPost, "/api/v1/exams/"+examID+"/submissions", bytes.NewBufferString(validSubmissionJSON(expectedPages)), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create submission expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Submission submission.Submission `json:"submission"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.Submission
}

func addPage(t *testing.T, router http.Handler, token string, submissionID string, fileAssetID string, pageNo int) submission.SubmissionPage {
	t.Helper()
	body := bytes.NewBufferString(`{"file_asset_id":"` + fileAssetID + `","page_no":` + intString(pageNo) + `}`)
	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+submissionID+"/pages", body, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add page expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Page submission.SubmissionPage `json:"page"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.Page
}

func createFileAsset(t *testing.T, store files.Store) files.FileAsset {
	t.Helper()
	asset, err := store.Create(context.Background(), files.CreateAssetInput{
		TenantID:      tenantID,
		OwnerType:     "submission",
		OriginalName:  "page-1.pdf",
		ContentType:   "application/pdf",
		SizeBytes:     128,
		HashSHA256:    "hash-page-1",
		StorageBucket: "test",
		StorageKey:    "tenant/demo/page-1.pdf",
		Visibility:    "private",
		UploadedBy:    userID,
	})
	if err != nil {
		t.Fatalf("create file asset: %v", err)
	}
	return asset
}

func createSecondFileAsset(t *testing.T, store files.Store) files.FileAsset {
	t.Helper()
	return createNamedFileAsset(t, store, "page-2.pdf", "hash-page-2")
}

func createThirdFileAsset(t *testing.T, store files.Store) files.FileAsset {
	t.Helper()
	return createNamedFileAsset(t, store, "page-3.pdf", "hash-page-3")
}

func createNamedFileAsset(t *testing.T, store files.Store, name string, hash string) files.FileAsset {
	t.Helper()
	asset, err := store.Create(context.Background(), files.CreateAssetInput{
		TenantID:      tenantID,
		OwnerType:     "submission",
		OriginalName:  name,
		ContentType:   "application/pdf",
		SizeBytes:     128,
		HashSHA256:    hash,
		StorageBucket: "test",
		StorageKey:    "tenant/demo/" + name,
		Visibility:    "private",
		UploadedBy:    userID,
	})
	if err != nil {
		t.Fatalf("create file asset: %v", err)
	}
	return asset
}

func validSubmissionJSON(expectedPages int) string {
	return `{"candidate_no":"S20260703001","source_type":"scanner_upload","expected_page_count":` + intString(expectedPages) + `}`
}

func authedRequest(method string, path string, body *bytes.Buffer, token string) *http.Request {
	var reader io.Reader
	if body != nil {
		reader = body
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func intString(value int) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func assertAuditAction(t *testing.T, store *auth.MemoryStore, action string) {
	t.Helper()
	for _, audit := range store.Audits() {
		if audit.Action == action {
			return
		}
	}
	t.Fatalf("missing audit action %s in %#v", action, store.Audits())
}
