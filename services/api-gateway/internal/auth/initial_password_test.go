package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInitialPasswordSessionOnlyAllowsPasswordCompletion(t *testing.T) {
	store := NewMemoryStore()
	hash, err := HashPassword("initial-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	user := User{ID: "new-teacher", TenantID: "tenant", TenantCode: "school", Username: "new-teacher", Status: "active", Roles: []string{"grader"}, MustChangePassword: true}
	store.AddUser(UserWithPassword{User: user, PasswordHash: hash})
	_, err = store.CreateSession(context.Background(), CreateSessionInput{TenantID: user.TenantID, UserID: user.ID, TokenHash: HashToken("initial-session"), SessionType: SessionTypeStandard, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	// 没有分配任务的教师也必须能完成首次改密；普通业务及其他自账户操作不能绕过门禁。
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/v1/auth/me", 204}, {"POST", "/api/v1/auth/password", 204}, {"POST", "/api/v1/auth/logout", 204},
		{"GET", "/api/v1/students", 403}, {"GET", "/api/v1/auth/profile", 403}, {"POST", "/api/v1/users", 403}, {"GET", "/api/v1/auth/password", 403},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set("Authorization", "Bearer initial-session")
		w := httptest.NewRecorder()
		AccountAuthMiddleware(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
		if w.Code != tc.want || (w.Code == 403 && !strings.Contains(w.Body.String(), "password_change_required")) {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}

	for _, guard := range []func(http.Handler) http.Handler{AuthMiddleware(store), AccountAuthMiddleware(store), ReauthenticationAuthMiddleware(store)} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/exams", nil)
		r.Header.Set("Authorization", "Bearer initial-session")
		w := httptest.NewRecorder()
		guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "password_change_required") {
			t.Fatalf("authentication guard bypassed initial password: %d %s", w.Code, w.Body.String())
		}
	}
	unchanged := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(`{"current_password":"initial-pass-123","new_password":"initial-pass-123"}`))
	unchanged.Header.Set("Authorization", "Bearer initial-session")
	unchangedResponse := httptest.NewRecorder()
	AccountAuthMiddleware(store)(http.HandlerFunc(NewHandler(store, time.Hour).ChangePassword)).ServeHTTP(unchangedResponse, unchanged)
	if unchangedResponse.Code != 400 || !strings.Contains(unchangedResponse.Body.String(), "password_unchanged") {
		t.Fatal("initial password accepted without being changed")
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(`{"current_password":"initial-pass-123","new_password":"personal-pass-456"}`))
	r.Header.Set("Authorization", "Bearer initial-session")
	w := httptest.NewRecorder()
	AccountAuthMiddleware(store)(http.HandlerFunc(NewHandler(store, time.Hour).ChangePassword)).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("complete initial password: %d %s", w.Code, w.Body.String())
	}
	updated, err := store.FindUserByLogin(context.Background(), "school", "new-teacher")
	if err != nil || updated.MustChangePassword || !CheckPassword(updated.PasswordHash, "personal-pass-456") {
		t.Fatalf("initial flag/password not updated: %v", err)
	}
	if _, err = store.FindUserBySession(context.Background(), HashToken("initial-session"), time.Now()); err != ErrUnauthenticated {
		t.Fatalf("initial session survived password change: %v", err)
	}
}
