package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func TestListSessionsAndLogoutAllAffectOnlySignedInAccount(t *testing.T) {
	store, actor, _ := managedAccessFixture(t)
	other := User{ID: "another-user", TenantID: actor.TenantID, TenantCode: actor.TenantCode, Username: "another-user", Status: "active"}
	store.AddUser(UserWithPassword{User: other})
	now := time.Now().UTC()
	current := createSessionForHandlerTest(t, store, actor, "browser-session", now.Add(time.Hour))
	second := createSessionForHandlerTest(t, store, actor, "other-session", now.Add(time.Hour))
	createSessionForHandlerTest(t, store, actor, "expired-session", now.Add(-time.Second))
	createSessionForHandlerTest(t, store, other, "another-user-session", now.Add(time.Hour))
	handler := NewHandler(store, time.Hour)
	ctx := WithUser(t.Context(), actor)

	list := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/auth/sessions", nil)
	list.AddCookie(&http.Cookie{Name: DefaultSessionCookieName, Value: "browser-session"})
	listResponse := httptest.NewRecorder()
	handler.ListSessions(listResponse, list)
	if listResponse.Code != http.StatusOK || listResponse.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("list sessions returned %d, Cache-Control=%q", listResponse.Code, listResponse.Header().Get("Cache-Control"))
	}
	var listing struct {
		Sessions []DeviceSession `json:"sessions"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	sessionFlags := map[string]bool{}
	for _, session := range listing.Sessions {
		sessionFlags[session.ID] = session.Current
	}
	otherCurrent, otherListed := sessionFlags[second.ID]
	if len(listing.Sessions) != 2 || !sessionFlags[current.ID] || !otherListed || otherCurrent {
		t.Fatalf("expected only two active sessions, with the browser session marked current: %+v", listing.Sessions)
	}
	if strings.Contains(listResponse.Body.String(), HashToken("browser-session")) || strings.Contains(listResponse.Body.String(), "browser-session") {
		t.Fatal("session list exposed a login credential")
	}

	logout := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/auth/logout-all", nil)
	logout.AddCookie(&http.Cookie{Name: DefaultSessionCookieName, Value: "browser-session"})
	logoutResponse := httptest.NewRecorder()
	handler.LogoutAll(logoutResponse, logout)
	if logoutResponse.Code != http.StatusOK {
		t.Fatalf("logout all returned %d: %s", logoutResponse.Code, logoutResponse.Body)
	}
	var result struct {
		Status       string `json:"status"`
		RevokedCount int    `json:"revoked_count"`
	}
	if err := json.Unmarshal(logoutResponse.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "logged_out_all" || result.RevokedCount != 3 {
		t.Fatalf("logout all did not report revoking the signed-in account's three stored sessions: %+v", result)
	}
	cookies := logoutResponse.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != DefaultSessionCookieName || cookies[0].MaxAge >= 0 {
		t.Fatalf("expected logout to clear the browser cookie: %v", cookies)
	}
	for _, token := range []string{"browser-session", "other-session"} {
		if _, err := store.FindUserBySession(ctx, HashToken(token), now); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("signed-in account has an active session after logout: %v", err)
		}
	}
	if _, err := store.FindUserBySession(t.Context(), HashToken("another-user-session"), now); err != nil {
		t.Fatalf("logout affected another account in the same organization: %v", err)
	}
	events := store.Audits()
	if len(events) != 1 || events[0].Action != "auth.sessions_revoked_all" || events[0].ActorID != actor.ID || events[0].TargetID != actor.ID {
		t.Fatalf("expected one audit for the signed-in account: %v", events)
	}
}

func TestSessionManagementReportsStoreFailureWithoutClearingCookie(t *testing.T) {
	store, actor, _ := managedAccessFixture(t)
	handler := NewHandler(sessionManagementFailureStore{store}, time.Hour)
	ctx := WithUser(t.Context(), actor)
	for _, tc := range []struct {
		method, path, code string
		handler            http.HandlerFunc
	}{
		{http.MethodGet, "/api/v1/auth/sessions", "session_lookup_failed", handler.ListSessions},
		{http.MethodPost, "/api/v1/auth/logout-all", "session_revoke_failed", handler.LogoutAll},
	} {
		t.Run(tc.code, func(t *testing.T) {
			response := httptest.NewRecorder()
			tc.handler.ServeHTTP(response, httptest.NewRequestWithContext(ctx, tc.method, tc.path, nil))
			if response.Code != http.StatusInternalServerError || response.Header().Get(httpx.ErrorCodeHeader) != tc.code {
				t.Fatalf("expected storage failure %q, got %d %s", tc.code, response.Code, response.Body)
			}
			if len(response.Result().Cookies()) != 0 || len(store.Audits()) != 0 {
				t.Fatal("storage failure must not produce a logout cookie or success audit")
			}
		})
	}
}

type sessionManagementFailureStore struct{ *MemoryStore }

func (sessionManagementFailureStore) ListSessions(context.Context, string, string, string, time.Time) ([]DeviceSession, error) {
	return nil, errors.New("session store unavailable")
}

func (sessionManagementFailureStore) RevokeAllSessions(context.Context, string, string, string) (int, error) {
	return 0, errors.New("session store unavailable")
}

func createSessionForHandlerTest(t *testing.T, store *MemoryStore, user User, token string, expires time.Time) DeviceSession {
	t.Helper()
	session, err := store.CreateSession(t.Context(), CreateSessionInput{
		TenantID: user.TenantID, UserID: user.ID, TokenHash: HashToken(token),
		SessionType: SessionTypeStandard, ExpiresAt: expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}
