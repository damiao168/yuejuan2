package auth_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
)

func TestMFAHTTPDoesNotCacheSecretsOrReplayOneTimeCommands(t *testing.T) {
	store := newTestStore(t)
	cfg := config.Config{Service: config.ServiceConfig{Environment: "test"}, Auth: config.AuthConfig{SessionTTL: time.Hour, MFAEnabled: true, MFAMasterKey: base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))}}
	router := newTestRouterWithConfig(store, cfg)
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", strings.NewReader(`{"tenant_code":"demo","username":"teacher","password":"ChangeMe123!"}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, login)
	var loggedIn struct {
		Token string `json:"access_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &loggedIn); err != nil || loggedIn.Token == "" {
		t.Fatal("login failed")
	}
	// An omitted cookie-name config must still protect the default cookie.
	for _, authorization := range []string{"", "Basic unrelated"} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/totp/enroll", strings.NewReader(`{"password":"ChangeMe123!"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", authorization)
		req.AddCookie(&http.Cookie{Name: auth.DefaultSessionCookieName, Value: loggedIn.Token})
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		if result.Code != http.StatusForbidden {
			t.Fatal("default cookie or malformed Authorization bypassed CSRF")
		}
	}
	send := func(path, body string, want int) map[string]any {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+loggedIn.Token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "mfa-do-not-cache-secret")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, request)
		if result.Code != want {
			t.Fatalf("%s status=%d want=%d", path, result.Code, want)
		}
		if result.Header().Get("Cache-Control") != "no-store" || result.Header().Get("X-Idempotent-Replay") == "true" {
			t.Fatal("MFA secret response must not be cached/replayed")
		}
		var out map[string]any
		if err := json.Unmarshal(result.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	setup := send("/api/v1/auth/mfa/totp/enroll", `{"password":"ChangeMe123!"}`, http.StatusOK)
	// 注册用允许偏差内的上一时间步，给后续命令保留当前步，避免被防重放规则拒绝。
	code, err := totp.GenerateCode(setup["secret"].(string), time.Now().UTC().Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	confirm := `{"enrollment_id":"` + setup["enrollment_id"].(string) + `","code":"` + code + `"}`
	send("/api/v1/auth/mfa/totp/confirm", confirm, http.StatusOK)
	send("/api/v1/auth/mfa/totp/confirm", confirm, http.StatusUnauthorized)
	challenge := send("/api/v1/auth/step-up/start", `{"operation":"mfa.recovery.rotate","password":"ChangeMe123!"}`, http.StatusOK)["challenge_id"].(string)
	code, err = totp.GenerateCode(setup["secret"].(string), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	verify := `{"challenge_id":"` + challenge + `","method":"totp","code":"` + code + `"}`
	send("/api/v1/auth/step-up/verify", verify, http.StatusOK)
	send("/api/v1/auth/step-up/verify", verify, http.StatusUnauthorized)
	command := `{"challenge_id":"` + challenge + `"}`
	send("/api/v1/auth/mfa/recovery-codes/rotate", command, http.StatusOK)
	send("/api/v1/auth/mfa/recovery-codes/rotate", command, http.StatusUnauthorized)
}
