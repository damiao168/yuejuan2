package config

import "testing"

func TestLoadReturnsOrderedProductionProblemsBeforeModelKeyError(t *testing.T) {
	t.Setenv("EDUGRADE_ENV", "production")
	setSecureProductionEnvironment(t)
	t.Setenv("EDUGRADE_ALLOW_MOCK_AI", "true")
	t.Setenv("EDUGRADE_SESSION_COOKIE_SECURE", "false")
	t.Setenv("EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY", "short")

	// 同时触发两项生产问题和末尾密钥问题，保护有序汇总先于最终密钥校验的边界。
	_, err := Load("")
	want := "unsafe production configuration: EDUGRADE_ALLOW_MOCK_AI must be false in production-like environments; EDUGRADE_SESSION_COOKIE_SECURE must be true"
	if err == nil || err.Error() != want {
		t.Fatalf("production validation error=%v want %q", err, want)
	}
}
