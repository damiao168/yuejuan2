package auth

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
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func wechatTestHandler(t *testing.T) (*Handler, *MemoryStore) {
	t.Helper()
	passwordHash, err := HashPassword("WechatTestPassword123!")
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	store.AddUser(UserWithPassword{User: User{
		ID: "wechat-user", TenantID: "wechat-tenant", TenantCode: "demo", Username: "teacher",
		DisplayName: "微信教师", Status: "active", Roles: []string{"teacher"}, Permissions: []string{"review:work"}, DataScope: map[string]any{},
	}, PasswordHash: passwordHash, SecurityEpoch: 1})
	store.BindWechatIdentity("wx-app-id", "demo", "union-1", "open-1", "wechat-user")
	handler := NewHandler(store, time.Hour, HandlerOptions{
		WechatEnabled: true, WechatAppID: "wx-app-id", WechatAppSecret: "wx-app-secret-for-test",
		WechatRedirectURL: "https://edugrade.example/api/v1/auth/wechat/callback", WechatChallengeTTL: 5 * time.Minute,
	})
	return handler, store
}

func TestStartWechatLoginCreatesOpaqueQRChallenge(t *testing.T) {
	handler, _ := wechatTestHandler(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/wechat/challenges", strings.NewReader(`{"tenant_code":"demo","remember_device":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.StartWechatLogin(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body["qr_code_data_url"].(string), "data:image/png;base64,") {
		t.Fatal("expected PNG data URL")
	}
	if body["challenge_id"] == "" || body["poll_token"] == "" {
		t.Fatal("missing opaque challenge credentials")
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("challenge response must not be cached")
	}
}

func TestWechatCallbackAuthorizesAndPollCreatesOneBrowserSession(t *testing.T) {
	handler, store := wechatTestHandler(t)
	state, pollToken := "one-time-state", "one-time-poll-token"
	challengeID := "20ce648e-8a5b-48c6-8127-99ae814f39f4"
	if err := store.CreateWechatLoginChallenge(context.Background(), CreateWechatLoginChallengeInput{
		ID: challengeID, StateHash: HashToken(state), PollTokenHash: HashToken(pollToken), TenantCode: "demo",
		ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	handler.wechatHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("secret") != "wx-app-secret-for-test" || request.URL.Query().Get("code") != "valid-code" {
			t.Fatal("missing exchange credentials")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(`{"openid":"open-1","unionid":"union-1"}`)), Header: make(http.Header)}, nil
	})}
	callback := httptest.NewRequest(http.MethodGet, "/api/v1/auth/wechat/callback?code=valid-code&state="+state, nil)
	callbackResponse := httptest.NewRecorder()
	handler.WechatCallback(callbackResponse, callback)
	if callbackResponse.Code != http.StatusOK {
		t.Fatalf("callback expected 200, got %d: %s", callbackResponse.Code, callbackResponse.Body.String())
	}

	pollBody := `{"challenge_id":"` + challengeID + `","poll_token":"` + pollToken + `"}`
	poll := httptest.NewRequest(http.MethodPost, "/api/v1/auth/wechat/session", strings.NewReader(pollBody))
	poll.Header.Set("Content-Type", "application/json")
	pollResponse := httptest.NewRecorder()
	handler.PollWechatLogin(pollResponse, poll)
	if pollResponse.Code != http.StatusOK {
		t.Fatalf("poll expected 200, got %d: %s", pollResponse.Code, pollResponse.Body.String())
	}
	if !strings.Contains(pollResponse.Body.String(), `"status":"authenticated"`) {
		t.Fatalf("unexpected response: %s", pollResponse.Body.String())
	}
	if len(pollResponse.Result().Cookies()) == 0 || pollResponse.Result().Cookies()[0].HttpOnly != true {
		t.Fatal("expected HttpOnly browser session cookie")
	}

	replay := httptest.NewRequest(http.MethodPost, "/api/v1/auth/wechat/session", strings.NewReader(pollBody))
	replay.Header.Set("Content-Type", "application/json")
	replayResponse := httptest.NewRecorder()
	handler.PollWechatLogin(replayResponse, replay)
	if replayResponse.Code != http.StatusOK || !strings.Contains(replayResponse.Body.String(), `"status":"consumed"`) {
		t.Fatalf("replay must not mint another session: %d %s", replayResponse.Code, replayResponse.Body.String())
	}
}

func TestWechatCallbackRejectsUnboundIdentity(t *testing.T) {
	handler, store := wechatTestHandler(t)
	state := "unbound-state"
	if err := store.CreateWechatLoginChallenge(context.Background(), CreateWechatLoginChallengeInput{
		ID: "df371d9a-6aab-4aba-91f3-31e404c71662", StateHash: HashToken(state), PollTokenHash: HashToken("poll"),
		TenantCode: "demo", ExpiresAt: time.Now().UTC().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	handler.wechatHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(`{"openid":"not-bound"}`)), Header: make(http.Header)}, nil
	})}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/wechat/callback?code=valid&state="+state, nil)
	response := httptest.NewRecorder()
	handler.WechatCallback(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", response.Code)
	}
	challenge, err := store.FindWechatLoginChallenge(context.Background(), "df371d9a-6aab-4aba-91f3-31e404c71662", HashToken("poll"), time.Now().UTC())
	if err != nil || challenge.ErrorCode != "wechat_account_unbound" {
		t.Fatalf("expected safe unbound status, got %#v %v", challenge, err)
	}
}
