package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func e2eLogin(t *testing.T, router http.Handler, username string) string {
	t.Helper()
	return e2eLoginWithTenant(t, router, e2eTenantCode, username, e2ePassword)
}

func e2eLoginWithTenant(t *testing.T, router http.Handler, tenantCode string, username string, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"tenant_code": tenantCode, "username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s expected 200, got %d %s", username, rec.Code, rec.Body.String())
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if response.AccessToken == "" {
		t.Fatal("login response missing access token")
	}
	return response.AccessToken
}

func e2ePostJSON(t *testing.T, router http.Handler, method string, path string, token string, body string, want int, requestHeaders ...map[string]string) map[string]any {
	t.Helper()
	rec := e2eExpectStatus(t, router, method, path, token, body, want, requestHeaders...)
	if rec.Body.Len() == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s %s response: %v; raw=%s", method, path, err, rec.Body.String())
	}
	return out
}

func e2eGetJSON(t *testing.T, router http.Handler, path string, token string, want int) map[string]any {
	t.Helper()
	return e2ePostJSON(t, router, http.MethodGet, path, token, "", want)
}

func e2eExpectStatus(t *testing.T, router http.Handler, method string, path string, token string, body string, want int, requestHeaders ...map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for _, headers := range requestHeaders {
		for key, value := range headers {
			req.Header.Set(key, value)
		}
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s expected %d, got %d %s", method, path, want, rec.Code, rec.Body.String())
	}
	return rec
}

func e2eUploadSyntheticPDF(t *testing.T, router http.Handler, token string, filename string, content string) string {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := io.Copy(part, strings.NewReader(content)); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.WriteField("owner_type", "generic"); err != nil {
		t.Fatalf("write owner type: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload synthetic file expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	return e2eString(t, response["file"].(map[string]any), "id")
}

func e2eString(t *testing.T, item map[string]any, key string) string {
	t.Helper()
	value, ok := item[key].(string)
	if !ok || value == "" {
		t.Fatalf("missing string field %s in %#v", key, item)
	}
	return value
}

func e2eFloat(t *testing.T, item map[string]any, key string) float64 {
	t.Helper()
	value, ok := item[key].(float64)
	if !ok {
		t.Fatalf("missing numeric field %s in %#v", key, item)
	}
	return value
}

func e2eAssertAuditActions(t *testing.T, audits []any, actions []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, item := range audits {
		record, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("audit record has unexpected shape: %#v", item)
		}
		if action, ok := record["action"].(string); ok {
			seen[action] = true
		}
	}
	for _, action := range actions {
		if !seen[action] {
			t.Fatalf("missing audit action %s in %#v", action, seen)
		}
	}
}
