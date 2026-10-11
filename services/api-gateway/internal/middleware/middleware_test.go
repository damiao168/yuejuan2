package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/logger"
)

func TestCORSAllowsCredentialsForAllowedOrigin(t *testing.T) {
	handler := CORS(
		[]string{"http://127.0.0.1:5173"},
		[]string{"GET", "POST", "OPTIONS"},
		[]string{"Content-Type"},
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Origin", "http://127.0.0.1:5173")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("allowed CORS origin must allow credentials, headers=%v", rec.Header())
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, "+CSRFHeaderName {
		t.Fatalf("CORS must always advertise the browser CSRF header, got %q", got)
	}
}

func TestAccessLogKeepsCorrelationIDsWhenRecoverHandlesPanic(t *testing.T) {
	var logs bytes.Buffer
	logg := logger.New(&logs, "info")
	handler := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("test panic")
	}), RequestID(), AccessLog(logg), Recover(logg))
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("recovered panic should return 500, got %d", response.Code)
	}
	if !bytes.Contains(logs.Bytes(), []byte(`"event":"panic_recovered"`)) ||
		!bytes.Contains(logs.Bytes(), []byte(`"event":"api_request"`)) ||
		!bytes.Contains(logs.Bytes(), []byte(`"request_id":"`)) {
		t.Fatalf("panic and access logs must include the request context: %s", logs.String())
	}
}

func TestBrowserCSRFRejectsCookieMutationWithoutHeader(t *testing.T) {
	handler := BrowserCSRF("edugrade_session")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/exams", nil)
	req.AddCookie(&http.Cookie{Name: "edugrade_session", Value: "session-token"})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("cookie mutation without CSRF header expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBrowserCSRFDoesNotExemptCookieFallbackForInvalidAuthorization(t *testing.T) {
	for _, authorization := range []string{"Basic unrelated", "Bearer ", "not-bearer", " Bearer token"} {
		handler := BrowserCSRF("edugrade_session")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/totp/enroll", nil)
		req.AddCookie(&http.Cookie{Name: "edugrade_session", Value: "session"})
		req.Header.Set("Authorization", authorization)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusForbidden {
			t.Fatal("invalid Authorization bypassed cookie CSRF protection")
		}
	}
}

func TestBrowserCSRFAcceptsProtectedBrowserMutation(t *testing.T) {
	handler := BrowserCSRF("edugrade_session")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/sessions/other", nil)
	req.AddCookie(&http.Cookie{Name: "edugrade_session", Value: "session-token"})
	req.Header.Set(CSRFHeaderName, "1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("protected browser mutation expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBrowserCSRFRejectsCrossOriginLoginForm(t *testing.T) {
	handler := BrowserCSRF("edugrade_session")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.Header.Set("Origin", "https://attacker.example")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login form expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBrowserCSRFLeavesBearerAndReadRequestsCompatible(t *testing.T) {
	called := 0
	handler := BrowserCSRF("edugrade_session")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))

	bearerReq := httptest.NewRequest(http.MethodPost, "/api/v1/internal/worker/tasks/claim", nil)
	bearerReq.Header.Set("Authorization", "Bearer worker-token")
	bearerRec := httptest.NewRecorder()
	handler.ServeHTTP(bearerRec, bearerReq)

	readReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	readReq.AddCookie(&http.Cookie{Name: "edugrade_session", Value: "session-token"})
	readRec := httptest.NewRecorder()
	handler.ServeHTTP(readRec, readReq)

	if bearerRec.Code != http.StatusNoContent || readRec.Code != http.StatusNoContent || called != 2 {
		t.Fatalf("bearer and read requests must remain compatible: bearer=%d read=%d called=%d", bearerRec.Code, readRec.Code, called)
	}
}

func TestRequestIDReplacesInvalidCorrelationHeaders(t *testing.T) {
	handler := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	request.Header.Set("X-Request-ID", strings.Repeat("x", 4096))
	request.Header.Set("X-Trace-ID", "not-a-trace-id")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got := response.Header().Get("X-Request-ID"); !requestIDPattern.MatchString(got) || got == request.Header.Get("X-Request-ID") {
		t.Fatalf("invalid request id was not replaced: %q", got)
	}
	if got := response.Header().Get("X-Trace-ID"); !validTraceID(got) {
		t.Fatalf("invalid trace id was not replaced: %q", got)
	}
}

func TestRequestIDAcceptsBoundedRequestAndTraceIdentifiers(t *testing.T) {
	handler := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	request.Header.Set("X-Request-ID", "01ARZ3NDEKTSV4RRFFQ69G5FAV")
	request.Header.Set("X-Trace-ID", "4bf92f3577b34da6a3ce929d0e0e4736")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Header().Get("X-Request-ID") != "01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("valid request id was not propagated: %q", response.Header().Get("X-Request-ID"))
	}
	if response.Header().Get("X-Trace-ID") != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("valid trace id was not propagated: %q", response.Header().Get("X-Trace-ID"))
	}
}
