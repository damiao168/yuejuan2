package auth_test

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOrganizationUserManagementRequiresPermission(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	token := login(t, router, "demo", "teacher", "ChangeMe123!")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("user list without org:manage expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLoginSetsHttpOnlySessionCookieAndMeAcceptsCookie(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)

	loginRec := loginResponse(t, router, "demo", "teacher", "ChangeMe123!")
	cookie := sessionCookie(t, loginRec)
	if !cookie.HttpOnly {
		t.Fatalf("session cookie must be HttpOnly: %#v", cookie)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie must use SameSite=Lax, got %v", cookie.SameSite)
	}
	if cookie.Value == "" {
		t.Fatal("session cookie must contain token value")
	}
	if cookie.Path != "/api/v1" {
		t.Fatalf("session cookie must use the minimum API path, got %q", cookie.Path)
	}
	var browserResponse map[string]any
	if err := json.Unmarshal(loginRec.Body.Bytes(), &browserResponse); err != nil {
		t.Fatal(err)
	}
	if _, leaked := browserResponse["access_token"]; leaked {
		t.Fatalf("browser login response exposed an access token: %s", loginRec.Body.String())
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.AddCookie(cookie)
	meRec := httptest.NewRecorder()
	router.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("me with cookie expected 200, got %d: %s", meRec.Code, meRec.Body.String())
	}
}

func TestPublicComputerLoginUsesShortNonPersistentSession(t *testing.T) {
	store := newTestStore(t)
	publicTTL := 45 * time.Minute
	handler := auth.NewHandler(store, 8*time.Hour, auth.HandlerOptions{PublicSessionTTL: publicTTL})
	before := time.Now().UTC()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(
		`{"tenant_code":"demo","identifier":"teacher","password":"ChangeMe123!","public_device":true}`,
	))
	rec := httptest.NewRecorder()
	handler.Login(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("public computer login expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cookie := sessionCookie(t, rec)
	if cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Fatalf("public computer login must use a non-persistent cookie, got %#v", cookie)
	}
	var response struct {
		ExpiresAt time.Time `json:"expires_at"`
		User      auth.User `json:"user"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.User.CurrentSessionType != auth.SessionTypePublicDevice {
		t.Fatalf("current session type=%q want %q", response.User.CurrentSessionType, auth.SessionTypePublicDevice)
	}
	if response.ExpiresAt.Before(before.Add(publicTTL-time.Second)) || response.ExpiresAt.After(time.Now().UTC().Add(publicTTL+time.Second)) {
		t.Fatalf("public session expiry %s does not use configured lifetime %s", response.ExpiresAt, publicTTL)
	}
	user, err := store.FindUserBySession(context.Background(), auth.HashToken(cookie.Value), time.Now().UTC())
	if err != nil || user.CurrentSessionType != auth.SessionTypePublicDevice {
		t.Fatalf("stored public session not recoverable with its type: user=%#v err=%v", user, err)
	}
	sessions, err := store.ListSessions(context.Background(), "t-1", "u-1", auth.HashToken(cookie.Value), time.Now().UTC())
	if err != nil || len(sessions) != 1 || sessions[0].SessionType != auth.SessionTypePublicDevice {
		t.Fatalf("public session not projected by session management: sessions=%#v err=%v", sessions, err)
	}
}

func TestPublicComputerModeRejectsRememberedAndTokenSessions(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "remembered browser",
			path: "/api/v1/auth/login",
			body: `{"tenant_code":"demo","identifier":"teacher","password":"ChangeMe123!","remember_device":true,"public_device":true}`,
		},
		{
			name: "bearer token",
			path: "/api/v1/auth/token",
			body: `{"tenant_code":"demo","identifier":"teacher","password":"ChangeMe123!","client_type":"desktop","public_device":true}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			handler := auth.NewHandler(store, time.Hour)
			req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			rec := httptest.NewRecorder()
			if test.path == "/api/v1/auth/token" {
				handler.TokenLogin(rec, req)
			} else {
				handler.Login(rec, req)
			}
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_device_mode"`) {
				t.Fatalf("expected 400 invalid_device_mode, got %d: %s", rec.Code, rec.Body.String())
			}
			sessions, err := store.ListSessions(context.Background(), "t-1", "u-1", "", time.Now().UTC())
			if err != nil || len(sessions) != 0 {
				t.Fatalf("invalid public mode must not create a session: sessions=%#v err=%v", sessions, err)
			}
		})
	}
}

func TestPublicComputerSessionCanReauthenticateWithoutLosingWorkspace(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(
		`{"tenant_code":"demo","identifier":"teacher","password":"ChangeMe123!","public_device":true}`,
	))
	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("public computer login expected 200, got %d: %s", loginRec.Code, loginRec.Body.String())
	}
	cookie := sessionCookie(t, loginRec)
	lockReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/lock", nil)
	lockReq.AddCookie(cookie)
	lockReq.Header.Set("X-EduGrade-CSRF", "1")
	lockRec := httptest.NewRecorder()
	router.ServeHTTP(lockRec, lockReq)
	if lockRec.Code != http.StatusOK || !strings.Contains(lockRec.Body.String(), `"status":"locked"`) {
		t.Fatalf("public session lock expected 200, got %d: %s", lockRec.Code, lockRec.Body.String())
	}
	lockedMeReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	lockedMeReq.AddCookie(cookie)
	lockedMeRec := httptest.NewRecorder()
	router.ServeHTTP(lockedMeRec, lockedMeReq)
	if lockedMeRec.Code != http.StatusUnauthorized {
		t.Fatalf("locked session must fail normal authentication, got %d: %s", lockedMeRec.Code, lockedMeRec.Body.String())
	}
	reauthenticate := func(password string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reauthenticate", strings.NewReader(`{"password":"`+password+`"}`))
		req.AddCookie(cookie)
		req.Header.Set("X-EduGrade-CSRF", "1")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	wrong := reauthenticate("incorrect password")
	if wrong.Code != http.StatusUnauthorized || !strings.Contains(wrong.Body.String(), `"code":"reauthentication_failed"`) {
		t.Fatalf("wrong reauthentication expected generic 401, got %d: %s", wrong.Code, wrong.Body.String())
	}
	success := reauthenticate("ChangeMe123!")
	if success.Code != http.StatusOK || !strings.Contains(success.Body.String(), `"status":"reauthenticated"`) {
		t.Fatalf("correct reauthentication expected 200, got %d: %s", success.Code, success.Body.String())
	}
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.AddCookie(cookie)
	meRec := httptest.NewRecorder()
	router.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK || !strings.Contains(meRec.Body.String(), `"current_session_type":"public_device"`) {
		t.Fatalf("reauthentication must preserve the public session: %d %s", meRec.Code, meRec.Body.String())
	}
	assertPublicSessionReauthenticationAudits(t, store)
}

func TestStandardBrowserSessionCannotUsePublicComputerLock(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	cookie := sessionCookie(t, loginResponse(t, router, "demo", "teacher", "ChangeMe123!"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/lock", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-EduGrade-CSRF", "1")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"session_lock_forbidden"`) {
		t.Fatalf("standard session lock expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestServiceAccountCannotCreateBrowserSession(t *testing.T) {
	store := newTestStore(t)
	hash, err := auth.HashPassword("WorkerStart123!")
	if err != nil {
		t.Fatal(err)
	}
	store.AddUser(auth.UserWithPassword{
		User: auth.User{
			ID: "worker-1", TenantID: "t-1", TenantCode: "demo", Username: "page_worker",
			DisplayName: "Page Worker", Status: "active", Roles: []string{"page_processing_worker"},
			Permissions: []string{"ocr:manage"}, DataScope: map[string]any{"scope": "tenant"},
		},
		PasswordHash: hash,
	})
	router := newTestRouter(store)

	browserReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(
		`{"tenant_code":"demo","username":"page_worker","password":"WorkerStart123!"}`,
	))
	browserRec := httptest.NewRecorder()
	router.ServeHTTP(browserRec, browserReq)
	if browserRec.Code != http.StatusForbidden || !strings.Contains(browserRec.Body.String(), `"code":"client_type_forbidden"`) {
		t.Fatalf("service browser login expected 403 client_type_forbidden, got %d: %s", browserRec.Code, browserRec.Body.String())
	}

	serviceReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", strings.NewReader(
		`{"tenant_code":"demo","username":"page_worker","password":"WorkerStart123!","client_type":"service"}`,
	))
	serviceRec := httptest.NewRecorder()
	router.ServeHTTP(serviceRec, serviceReq)
	if serviceRec.Code != http.StatusOK || !strings.Contains(serviceRec.Body.String(), `"access_token"`) {
		t.Fatalf("service token login expected 200 with token, got %d: %s", serviceRec.Code, serviceRec.Body.String())
	}
}

func TestLogoutClearsSessionCookieAndRevokesCookieSession(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)

	loginRec := loginResponse(t, router, "demo", "teacher", "ChangeMe123!")
	cookie := sessionCookie(t, loginRec)

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutReq.AddCookie(cookie)
	deniedRec := httptest.NewRecorder()
	router.ServeHTTP(deniedRec, logoutReq)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("cookie logout without CSRF protection expected 403, got %d", deniedRec.Code)
	}
	logoutReq.Header.Set("X-EduGrade-CSRF", "1")
	logoutRec := httptest.NewRecorder()
	router.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout expected 200, got %d: %s", logoutRec.Code, logoutRec.Body.String())
	}
	cleared := sessionCookie(t, logoutRec)
	if cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Fatalf("logout must clear session cookie, got %#v", cleared)
	}

	afterLogout := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	afterLogout.AddCookie(cookie)
	afterLogoutRec := httptest.NewRecorder()
	router.ServeHTTP(afterLogoutRec, afterLogout)
	if afterLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("me after cookie logout expected 401, got %d", afterLogoutRec.Code)
	}
}
