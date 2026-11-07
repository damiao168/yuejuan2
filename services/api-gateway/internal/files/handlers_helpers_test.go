package files_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/server"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"
	"time"
)

func testRouter(authStore *auth.MemoryStore, fileStore files.Store, objectStore files.ObjectStorage) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
		Files: config.FileConfig{
			Bucket:            "test-files",
			MaxUploadBytes:    1024 * 1024,
			AllowedExtensions: []string{".pdf", ".png", ".jpg", ".jpeg", ".csv", ".docx"},
		},
	}
	return server.NewRouterWithFiles(cfg, logger.New(io.Discard, "error"), nil, server.IdentityStores{Auth: authStore, Org: org.NewMemoryStore()}, server.ExamPreparationStores{Exam: exam.NewMemoryStore(), Paper: paper.NewMemoryStore(), Files: fileStore}, objectStore)
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
			ID:          "00000000-0000-0000-0000-000000000201",
			TenantID:    "00000000-0000-0000-0000-000000000002",
			TenantCode:  "demo",
			Username:    "file_admin",
			DisplayName: "File Admin",
			Status:      "active",
			Roles:       []string{"teacher"},
			Permissions: permissions,
			DataScope:   map[string]any{"scope": "tenant"},
		},
		PasswordHash: hash,
	})
	return store
}

func login(t *testing.T, router http.Handler) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"tenant_code": "demo", "username": "file_admin", "password": "ChangeMe123!"})
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

func uploadFile(t *testing.T, router http.Handler, token string, filename string, fileContentType string, content []byte, fields map[string]string) files.FileResponse {
	t.Helper()
	body, contentType := multipartUploadBody(t, filename, fileContentType, content, fields)
	req := authedRequest(http.MethodPost, "/api/v1/files", body, token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		File files.FileResponse `json:"file"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.File
}

func multipartUploadBody(t *testing.T, filename string, fileContentType string, content []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field: %v", err)
		}
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	header.Set("Content-Type", fileContentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return body, writer.FormDataContentType()
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

func assertAuditAction(t *testing.T, store *auth.MemoryStore, action string) {
	t.Helper()
	for _, audit := range store.Audits() {
		if audit.Action == action {
			return
		}
	}
	t.Fatalf("missing audit action %s in %#v", action, store.Audits())
}

type faultStorage struct {
	*files.MemoryObjectStorage
	failPut    bool
	failRemove bool
	putKeys    []string
}

func (s *faultStorage) Put(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	s.putKeys = append(s.putKeys, key)
	if s.failPut {
		return errors.New("injected put failure")
	}
	return s.MemoryObjectStorage.Put(ctx, bucket, key, body, size, contentType)
}

type activationFailureStore struct {
	*files.MemoryStore
	failOnce bool
}

func (s *activationFailureStore) Activate(ctx context.Context, tenantID, id string, revision int64) (files.FileAsset, error) {
	if s.failOnce {
		s.failOnce = false
		return files.FileAsset{}, errors.New("injected activation failure")
	}
	return s.MemoryStore.Activate(ctx, tenantID, id, revision)
}

func (s *faultStorage) Remove(ctx context.Context, bucket, key string) error {
	if s.failRemove {
		return errors.New("injected remove failure")
	}
	return s.MemoryObjectStorage.Remove(ctx, bucket, key)
}

func filesHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
