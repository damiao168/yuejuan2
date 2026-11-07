package auth_test

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPasswordChangeRevokesEveryExistingSession(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	firstToken := login(t, router, "demo", "teacher", "ChangeMe123!")
	secondToken := login(t, router, "demo", "teacher", "ChangeMe123!")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(`{"current_password":"ChangeMe123!","new_password":"NewPassword456!"}`))
	req.Header.Set("Authorization", "Bearer "+firstToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("password change expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	for _, token := range []string{firstToken, secondToken} {
		me := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		me.Header.Set("Authorization", "Bearer "+token)
		meRec := httptest.NewRecorder()
		router.ServeHTTP(meRec, me)
		if meRec.Code != http.StatusUnauthorized {
			t.Fatalf("password change must revoke existing token, got %d %s", meRec.Code, meRec.Body.String())
		}
	}
	if old := loginResponseRaw(router, "demo", "teacher", "ChangeMe123!"); old.Code != http.StatusUnauthorized {
		t.Fatalf("old password must fail, got %d %s", old.Code, old.Body.String())
	}
	_ = login(t, router, "demo", "teacher", "NewPassword456!")
}

func TestMeRequiresAuthentication(t *testing.T) {
	router := newTestRouter(newTestStore(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestPermissionMiddlewareRejectsMissingPermission(t *testing.T) {
	store := newTestStore(t)
	token, tokenHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, err := store.CreateSession(context.Background(), auth.CreateSessionInput{
		TenantID: "t-1", UserID: "u-1", TokenHash: tokenHash,
		SessionType: auth.SessionTypeStandard, DeviceID: "test-device",
		DeviceName: "test", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	handler := auth.AuthMiddleware(store)(auth.RequirePermission("audit:read")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func login(t *testing.T, router http.Handler, tenant string, username string, password string) string {
	t.Helper()
	payload := map[string]string{"tenant_code": tenant, "username": username, "password": password, "client_type": "desktop"}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("token login expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		t.Fatal("expected access token")
	}
	return response.AccessToken
}

func loginResponse(t *testing.T, router http.Handler, tenant string, username string, password string) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]string{"tenant_code": tenant, "username": username, "password": password}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	return rec
}

func loginResponseRaw(router http.Handler, tenant, username, password string) *httptest.ResponseRecorder {
	payload := map[string]string{"tenant_code": tenant, "username": username, "password": password, "client_type": "desktop"}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == auth.DefaultSessionCookieName {
			return cookie
		}
	}
	t.Fatalf("expected %s cookie, got %#v", auth.DefaultSessionCookieName, rec.Result().Cookies())
	return nil
}

func addAuditUser(t *testing.T, store *auth.MemoryStore) {
	t.Helper()
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	store.AddUser(auth.UserWithPassword{
		User: auth.User{
			ID:          "u-2",
			TenantID:    "t-1",
			TenantCode:  "demo",
			Username:    "auditor",
			DisplayName: "Auditor",
			Status:      "active",
			Roles:       []string{"auditor"},
			Permissions: []string{"audit:read", "audit:export"},
			DataScope:   map[string]any{"scope": "tenant"},
		},
		PasswordHash: hash,
	})
}

// 先验证同一会话恢复可用，再核对锁定、失败和成功三类安全审计事实。
func assertPublicSessionReauthenticationAudits(t *testing.T, store *auth.MemoryStore) {
	t.Helper()
	audits := store.Audits()
	foundLocked, foundFailure, foundSuccess := false, false, false
	for _, event := range audits {
		foundLocked = foundLocked || event.Action == "auth.session_locked"
		foundFailure = foundFailure || event.Action == "auth.reauthentication_failed"
		foundSuccess = foundSuccess || event.Action == "auth.session_reauthenticated"
	}
	if !foundLocked || !foundFailure || !foundSuccess {
		t.Fatalf("reauthentication attempts must be audited: %#v", audits)
	}
}
