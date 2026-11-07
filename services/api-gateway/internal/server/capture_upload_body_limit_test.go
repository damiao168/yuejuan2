package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/captureupload"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

func TestCaptureUploadNegotiatedChunksPassCompleteRouter(t *testing.T) {
	ctx := context.Background()
	stores := NewMemoryApplicationStores()
	identity := auth.NewMemoryStore()
	password, err := auth.HashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	identity.AddUser(auth.UserWithPassword{User: auth.User{ID: "user-1", TenantID: "tenant-1", TenantCode: "demo", Username: "teacher", Status: "active", Roles: []string{"tenant_admin"}, Permissions: []string{"capture:manage"}, DataScope: map[string]any{"scope": "tenant", "synthetic": true}}, PasswordHash: password})
	stores.Identity.Auth = identity
	batch, err := stores.Capture.Capture.CreateBatch(ctx, "tenant-1", "exam-1", "user-1", capture.CreateBatchInput{Name: "large scans", SourceType: "desktop_sync"})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouterWithApplicationStores(testConfig(), logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), stores)
	token := serverLogin(t, router)
	send := func(method, path string, payload []byte, offset int, unknownLength bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+token)
		if method == http.MethodPut {
			hash := sha256.Sum256(payload)
			req.Header.Set("X-Chunk-SHA256", hex.EncodeToString(hash[:]))
			req.Header.Set("Upload-Offset", strconv.Itoa(offset))
			if unknownLength {
				req.ContentLength = -1
			}
		} else {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	// 真正经过完整中间件、认证和上传服务，覆盖超过普通 JSON 上限的扫描原件。
	payload := make([]byte, captureupload.DefaultChunkSize+8)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))
	hash := sha256.Sum256(payload)
	input, _ := json.Marshal(captureupload.InitInput{SHA256: hex.EncodeToString(hash[:]), Size: int64(len(payload)), MIME: "image/png", ExamID: "exam-1", BatchID: batch.ID, IdempotencyKey: "large-scan", Filename: "scan.png"})
	rec := send(http.MethodPost, "/api/v1/capture/uploads:init", input, 0, false)
	var initialized captureupload.InitResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &initialized) != nil {
		t.Fatalf("init: %d %s", rec.Code, rec.Body.String())
	}
	path := "/api/v1/capture/uploads/" + initialized.RemoteUploadID + "/chunks"
	for _, unknownLength := range []bool{false, true} {
		rec = send(http.MethodPut, path, payload[:captureupload.DefaultChunkSize], 0, unknownLength)
		if rec.Code != http.StatusOK {
			t.Fatalf("4 MiB chunk unknownLength=%v: %d %s", unknownLength, rec.Code, rec.Body.String())
		}
	}
	for _, unknownLength := range []bool{false, true} {
		rec = send(http.MethodPut, path, payload[:captureupload.DefaultChunkSize+1], 0, unknownLength)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversized chunk unknownLength=%v: %d %s", unknownLength, rec.Code, rec.Body.String())
		}
	}
	rec = send(http.MethodPut, path, payload[captureupload.DefaultChunkSize:], int(captureupload.DefaultChunkSize), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("last chunk: %d %s", rec.Code, rec.Body.String())
	}
	rec = send(http.MethodPost, "/api/v1/capture/uploads/"+initialized.RemoteUploadID+"/complete", []byte(`{}`), 0, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", rec.Code, rec.Body.String())
	}
	registered, err := stores.Capture.Capture.ListFiles(ctx, "tenant-1", batch.ID)
	if err != nil || len(registered) != 1 {
		t.Fatalf("registered files: %d %v", len(registered), err)
	}
	for _, item := range []struct{ method, path string }{{http.MethodPost, path}, {http.MethodPut, path + "/extra"}, {http.MethodPut, "/api/v1/exams/exam-1"}} {
		if skipGlobalBodyLimit(httptest.NewRequest(item.method, item.path, nil)) {
			t.Fatalf("neighbor route exempted: %+v", item)
		}
	}
	rec = send(http.MethodPut, "/api/v1/exams/exam-1", payload[:3<<20], 0, false)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("ordinary request limit: %d %s", rec.Code, rec.Body.String())
	}
}
