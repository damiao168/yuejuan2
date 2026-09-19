package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGetEnvOrFileReadsSecretAndRejectsAmbiguousSources(t *testing.T) {
	key := "EDUGRADE_TEST_SECRET_SOURCE"
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(key+"_FILE", path)
	value, err := getEnvOrFile(key, "fallback")
	if err != nil || value != "from-file" {
		t.Fatalf("unexpected file secret: %q %v", value, err)
	}
	t.Setenv(key, "from-env")
	if _, err := getEnvOrFile(key, "fallback"); err == nil {
		t.Fatal("direct and file secret sources must not be accepted together")
	}
}

func TestLoadUsesDefaults(t *testing.T) {
	t.Setenv("EDUGRADE_HTTP_PORT", "")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Service.Name != "api-gateway" {
		t.Fatalf("unexpected service name: %s", cfg.Service.Name)
	}
	if cfg.Service.Port == 0 {
		t.Fatal("expected non-zero default port")
	}
	if len(cfg.ModelSecrets.MasterKey) < 32 {
		t.Fatal("development must have an encryption key for managed model credentials")
	}
	if cfg.Auth.RecentAuthTTL != time.Hour {
		t.Fatalf("unexpected default recent authentication lifetime: %s", cfg.Auth.RecentAuthTTL)
	}
	if cfg.Auth.RiskMode != "shadow" || cfg.Auth.DeviceBindingTTL != 180*24*time.Hour || cfg.Auth.DeviceCookieName != "edugrade_device" {
		t.Fatalf("unexpected default adaptive authentication config: %#v", cfg.Auth)
	}
	for _, extension := range []string{".tif", ".tiff", ".txt", ".md", ".markdown"} {
		if !containsString(cfg.Files.AllowedExtensions, extension) {
			t.Fatalf("default file extensions must include %s: %v", extension, cfg.Files.AllowedExtensions)
		}
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestLoadReadsEnvironment(t *testing.T) {
	t.Setenv("EDUGRADE_SERVICE_NAME", "test-service")
	t.Setenv("EDUGRADE_HTTP_PORT", "18080")
	t.Setenv("EDUGRADE_INTERNAL_TLS_PORT", "18443")
	t.Setenv("EDUGRADE_INTERNAL_TLS_CERT_FILE", "/run/secrets/api-tls/server.crt")
	t.Setenv("EDUGRADE_INTERNAL_TLS_KEY_FILE", "/run/secrets/api-tls/server.key")
	t.Setenv("EDUGRADE_MINIO_USE_SSL", "true")
	t.Setenv("EDUGRADE_MINIO_TLS_CA_FILE", "/run/secrets/minio-ca/ca.crt")
	t.Setenv("EDUGRADE_MINIO_TLS_SERVER_NAME", "minio.internal")
	t.Setenv("EDUGRADE_REDIS_USERNAME", "edugrade-api")
	t.Setenv("EDUGRADE_REDIS_TLS_ENABLED", "true")
	t.Setenv("EDUGRADE_REDIS_TLS_CA_FILE", "/run/secrets/redis-ca/ca.crt")
	t.Setenv("EDUGRADE_REDIS_TLS_SERVER_NAME", "redis.internal")
	t.Setenv("EDUGRADE_WORKER_HEARTBEAT_STALE_AFTER", "45s")
	t.Setenv("EDUGRADE_POSTGRES_MAX_OPEN_CONNS", "24")
	t.Setenv("EDUGRADE_POSTGRES_MAX_IDLE_CONNS", "8")
	t.Setenv("EDUGRADE_POSTGRES_CONN_MAX_LIFETIME", "45m")
	t.Setenv("EDUGRADE_POSTGRES_CONN_MAX_IDLE_TIME", "4m")
	t.Setenv("EDUGRADE_POSTGRES_STATEMENT_TIMEOUT", "40s")
	t.Setenv("EDUGRADE_POSTGRES_LOCK_TIMEOUT", "3s")
	t.Setenv("EDUGRADE_PUBLIC_SESSION_TTL", "90m")
	t.Setenv("EDUGRADE_RECENT_AUTH_TTL", "20m")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Service.Name != "test-service" {
		t.Fatalf("unexpected service name: %s", cfg.Service.Name)
	}
	if cfg.Service.Port != 18080 {
		t.Fatalf("unexpected port: %d", cfg.Service.Port)
	}
	if cfg.Service.InternalTLSPort != 18443 || cfg.Service.InternalTLSCertFile == "" || cfg.Service.InternalTLSKeyFile == "" {
		t.Fatalf("unexpected internal TLS listener: %#v", cfg.Service)
	}
	if !cfg.MinIO.UseSSL {
		t.Fatal("expected MinIO SSL to be true")
	}
	if cfg.MinIO.TLSCAFile != "/run/secrets/minio-ca/ca.crt" || cfg.MinIO.TLSServerName != "minio.internal" {
		t.Fatalf("unexpected MinIO TLS settings: %#v", cfg.MinIO)
	}
	if cfg.Observability.WorkerHeartbeatStaleAfter != 45*time.Second {
		t.Fatalf("unexpected worker heartbeat stale threshold: %s", cfg.Observability.WorkerHeartbeatStaleAfter)
	}
	if cfg.Postgres.MaxOpenConns != 24 || cfg.Postgres.MaxIdleConns != 8 ||
		cfg.Postgres.ConnMaxLifetime != 45*time.Minute || cfg.Postgres.ConnMaxIdleTime != 4*time.Minute ||
		cfg.Postgres.StatementTimeout != 40*time.Second || cfg.Postgres.LockTimeout != 3*time.Second {
		t.Fatalf("unexpected PostgreSQL capacity configuration: %#v", cfg.Postgres)
	}
	if cfg.Postgres.TenantRLSEnabled {
		t.Fatal("development must not enable tenant RLS connection scoping unless explicitly requested")
	}
	if cfg.Redis.Username != "edugrade-api" || !cfg.Redis.TLSEnabled ||
		cfg.Redis.TLSCAFile != "/run/secrets/redis-ca/ca.crt" || cfg.Redis.TLSServerName != "redis.internal" {
		t.Fatalf("unexpected Redis TLS/ACL configuration: %#v", cfg.Redis)
	}
	if cfg.Auth.PublicSessionTTL != 90*time.Minute {
		t.Fatalf("unexpected public session lifetime: %s", cfg.Auth.PublicSessionTTL)
	}
	if cfg.Auth.RecentAuthTTL != 20*time.Minute {
		t.Fatalf("unexpected recent authentication lifetime: %s", cfg.Auth.RecentAuthTTL)
	}
}

func TestLoadRejectsPartialInternalTLSConfiguration(t *testing.T) {
	for _, test := range []struct{ port, cert, key string }{
		{"0", "server.crt", ""}, {"0", "", "server.key"}, {"8443", "server.crt", ""}, {"0", "server.crt", "server.key"},
	} {
		t.Run(test.port+"/"+test.cert+"/"+test.key, func(t *testing.T) {
			t.Setenv("EDUGRADE_ENV", "development")
			t.Setenv("EDUGRADE_INTERNAL_TLS_PORT", test.port)
			t.Setenv("EDUGRADE_INTERNAL_TLS_CERT_FILE", test.cert)
			t.Setenv("EDUGRADE_INTERNAL_TLS_KEY_FILE", test.key)
			if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "must be configured together") {
				t.Fatalf("partial internal TLS config accepted: %v", err)
			}
		})
	}
}

func TestLoadWarnsAndFallsBackForInvalidDevelopmentValues(t *testing.T) {
	t.Setenv("EDUGRADE_ENV", "development")
	t.Setenv("EDUGRADE_HTTP_PORT", "eighty-eighty")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("development invalid value should use a warned fallback: %v", err)
	}
	if cfg.Service.Port != 8080 {
		t.Fatalf("development fallback port=%d want 8080", cfg.Service.Port)
	}
}

func TestLoadRejectsRiskEnforcementBeforeStrongAuthenticatorExists(t *testing.T) {
	t.Setenv("EDUGRADE_ENV", "development")
	t.Setenv("EDUGRADE_AUTH_RISK_MODE", "enforce")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "must be off or shadow") {
		t.Fatalf("expected risk mode safety error, got %v", err)
	}
}

func TestLoadRejectsUnboundedDeviceHistory(t *testing.T) {
	t.Setenv("EDUGRADE_ENV", "development")
	t.Setenv("EDUGRADE_DEVICE_BINDING_TTL", "9000h")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "between 24h and 8760h") {
		t.Fatalf("expected device retention safety error, got %v", err)
	}
}

func TestLoadRejectsInvalidTypedValuesInProductionLikeEnvironments(t *testing.T) {
	for _, test := range []struct {
		key, value string
	}{
		{"EDUGRADE_HTTP_PORT", "eighty-eighty"},
		{"EDUGRADE_MINIO_USE_SSL", "ture"},
		{"EDUGRADE_HTTP_READ_TIMEOUT", "soon"},
		{"EDUGRADE_AI_MIN_CONFIDENCE", "NaN"},
	} {
		t.Run(test.key, func(t *testing.T) {
			t.Setenv("EDUGRADE_ENV", "production")
			setSecureProductionEnvironment(t)
			t.Setenv(test.key, test.value)
			_, err := Load("")
			if err == nil || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("invalid %s must fail closed without exposing its value: %v", test.key, err)
			}
		})
	}
}

func TestLoadRejectsUnsafePostgresCapacityConfiguration(t *testing.T) {
	t.Setenv("EDUGRADE_POSTGRES_MAX_OPEN_CONNS", "5")
	t.Setenv("EDUGRADE_POSTGRES_MAX_IDLE_CONNS", "6")
	if _, err := Load(""); err == nil {
		t.Fatal("idle connections must not exceed the open connection ceiling")
	}

	t.Setenv("EDUGRADE_POSTGRES_MAX_IDLE_CONNS", "2")
	t.Setenv("EDUGRADE_POSTGRES_LOCK_TIMEOUT", "2m")
	t.Setenv("EDUGRADE_POSTGRES_STATEMENT_TIMEOUT", "1m")
	if _, err := Load(""); err == nil {
		t.Fatal("lock timeout must not exceed the statement timeout")
	}
}

func TestLoadReadsGovernedAIServiceConfiguration(t *testing.T) {
	t.Setenv("EDUGRADE_AI_GRADING_ENABLED", "true")
	t.Setenv("EDUGRADE_MATH_GRADING_V2_ENABLED", "true")
	t.Setenv("EDUGRADE_AI_SERVICE_URL", "http://grading-agent:8100")
	t.Setenv("EDUGRADE_GRADING_AGENT_TOKEN", "test-service-token-with-at-least-32-characters")
	t.Setenv("EDUGRADE_AI_SERVICE_TIMEOUT", "240s")
	t.Setenv("EDUGRADE_AI_SERVICE_MAX_RETRIES", "0")
	t.Setenv("EDUGRADE_AI_MODEL_VERSION", "model-v1")
	t.Setenv("EDUGRADE_AI_PROMPT_VERSION", "prompt-v2")
	t.Setenv("EDUGRADE_AI_MIN_CONFIDENCE", "0.75")
	t.Setenv("EDUGRADE_AI_PROVIDER_KEY", "local")
	t.Setenv("EDUGRADE_AI_DEPLOYMENT_KEY", "local-test-v1")
	t.Setenv("EDUGRADE_AI_ADAPTER_TYPE", "local_llama_cpp")
	t.Setenv("EDUGRADE_AI_DEPLOYMENT_REGION", "on_premise")
	t.Setenv("EDUGRADE_AI_CAPABILITY_PROFILE", "local-pilot-v1")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AIService.MathGradingV2 ||
		cfg.AIService.URL != "http://grading-agent:8100" ||
		cfg.AIService.Timeout != 240*time.Second ||
		cfg.AIService.MaxRetries != 0 ||
		cfg.AIService.ModelVersion != "model-v1" ||
		cfg.AIService.PromptVersion != "prompt-v2" ||
		cfg.AIService.MinConfidence != 0.75 ||
		cfg.AIService.ProviderKey != "local" ||
		cfg.AIService.DeploymentKey != "local-test-v1" ||
		cfg.AIService.AdapterType != "local_llama_cpp" ||
		cfg.AIService.DeploymentRegion != "on_premise" ||
		cfg.AIService.CapabilityProfile != "local-pilot-v1" {
		t.Fatalf("unexpected AI service configuration: %#v", cfg.AIService)
	}
}

func TestLoadRejectsMathGradingV2WhenAIGradingIsDisabled(t *testing.T) {
	t.Setenv("EDUGRADE_AI_GRADING_ENABLED", "false")
	t.Setenv("EDUGRADE_MATH_GRADING_V2_ENABLED", "true")

	if _, err := Load(""); err == nil {
		t.Fatal("math grading v2 must not be enabled while AI grading is disabled")
	}
}

func TestLoadRejectsIncompleteAIServiceIdentity(t *testing.T) {
	t.Setenv("EDUGRADE_AI_GRADING_ENABLED", "true")
	t.Setenv("EDUGRADE_AI_SERVICE_URL", "http://grading-agent:8100")
	t.Setenv("EDUGRADE_GRADING_AGENT_TOKEN", "test-service-token-with-at-least-32-characters")
	t.Setenv("EDUGRADE_AI_PROVIDER_KEY", "contains whitespace")
	if _, err := Load(""); err == nil {
		t.Fatal("configured AI service must use bounded provider identity values")
	}
}

func TestLoadRejectsAIServiceWithoutStrongServiceToken(t *testing.T) {
	t.Setenv("EDUGRADE_AI_GRADING_ENABLED", "true")
	t.Setenv("EDUGRADE_AI_SERVICE_URL", "http://grading-agent:8100")
	t.Setenv("EDUGRADE_GRADING_AGENT_TOKEN", "short")
	if _, err := Load(""); err == nil {
		t.Fatal("configured AI service must require a strong service token")
	}
}

func TestLoadRejectsHTTPWriteTimeoutShorterThanAIRequest(t *testing.T) {
	t.Setenv("EDUGRADE_AI_GRADING_ENABLED", "true")
	t.Setenv("EDUGRADE_AI_SERVICE_URL", "http://grading-agent:8100")
	t.Setenv("EDUGRADE_GRADING_AGENT_TOKEN", "test-service-token-with-at-least-32-characters")
	t.Setenv("EDUGRADE_AI_SERVICE_TIMEOUT", "250s")
	t.Setenv("EDUGRADE_HTTP_WRITE_TIMEOUT", "15s")
	if _, err := Load(""); err == nil {
		t.Fatal("HTTP write timeout must outlive the synchronous grading request")
	}
}

func TestLoadParsesRotatableBarcodeKeys(t *testing.T) {
	t.Setenv("EDUGRADE_BARCODE_ACTIVE_KEY_ID", "v2")
	t.Setenv("EDUGRADE_BARCODE_HMAC_KEYS", "v1:11111111111111111111111111111111,v2:22222222222222222222222222222222")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Barcode.ActiveKeyID != "v2" || len(cfg.Barcode.HMACKeys) != 2 || len(cfg.Barcode.HMACKeys["v1"]) != 32 {
		t.Fatalf("unexpected barcode key configuration: active=%q keys=%d", cfg.Barcode.ActiveKeyID, len(cfg.Barcode.HMACKeys))
	}
}

func TestLoadDefaultsSessionCookieSecureForProduction(t *testing.T) {
	t.Setenv("EDUGRADE_ENV", "production")
	t.Setenv("EDUGRADE_SESSION_COOKIE_SECURE", "")
	setSecureProductionEnvironment(t)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.Auth.SessionCookieSecure {
		t.Fatal("production environment must default session cookie to Secure")
	}
}

func TestLoadRejectsInsecureProductionCookieOverride(t *testing.T) {
	t.Setenv("EDUGRADE_ENV", "production")
	t.Setenv("EDUGRADE_SESSION_COOKIE_SECURE", "false")
	setSecureProductionEnvironment(t)

	if _, err := Load(""); err == nil {
		t.Fatal("insecure production cookie override must be rejected")
	}
}

func TestLoadRejectsDevelopmentCredentialsInProduction(t *testing.T) {
	t.Setenv("EDUGRADE_ENV", "production")
	t.Setenv("EDUGRADE_SESSION_COOKIE_SECURE", "true")
	t.Setenv("EDUGRADE_POSTGRES_DSN", "postgres://edugrade:edugrade_dev@db.internal:5432/edugrade?sslmode=disable")
	t.Setenv("EDUGRADE_MINIO_APP_ACCESS_KEY", "edugrade-app")
	t.Setenv("EDUGRADE_MINIO_APP_SECRET_KEY", "edugrade_app_dev_secret")
	t.Setenv("EDUGRADE_CORS_ALLOWED_ORIGINS", "http://localhost:5173")
	if _, err := Load(""); err == nil {
		t.Fatal("development credentials must be rejected in production")
	}
}

func TestLoadRejectsPlaintextMinIOInProduction(t *testing.T) {
	setSecureProductionEnvironment(t)
	t.Setenv("EDUGRADE_ENV", "production")
	t.Setenv("EDUGRADE_MINIO_USE_SSL", "false")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "EDUGRADE_MINIO_USE_SSL must be true") {
		t.Fatalf("production must reject plaintext MinIO, got %v", err)
	}
}

func TestLoadRejectsPlaintextOrUnauthenticatedRedisInProduction(t *testing.T) {
	for _, test := range []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "plaintext", key: "EDUGRADE_REDIS_TLS_ENABLED", value: "false", want: "EDUGRADE_REDIS_TLS_ENABLED must be true"},
		{name: "missing username", key: "EDUGRADE_REDIS_USERNAME", value: "", want: "EDUGRADE_REDIS_USERNAME must be configured"},
		{name: "missing password", key: "EDUGRADE_REDIS_PASSWORD", value: "", want: "EDUGRADE_REDIS_PASSWORD must be configured"},
	} {
		t.Run(test.name, func(t *testing.T) {
			setSecureProductionEnvironment(t)
			t.Setenv("EDUGRADE_ENV", "production")
			t.Setenv(test.key, test.value)
			if _, err := Load(""); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("production must reject unsafe Redis configuration, got %v", err)
			}
		})
	}
}

func TestLoadRejectsFailOpenAuthenticationLimiterInProduction(t *testing.T) {
	setSecureProductionEnvironment(t)
	t.Setenv("EDUGRADE_ENV", "production")
	t.Setenv("EDUGRADE_AUTH_LIMITER_FAIL_CLOSED", "false")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "EDUGRADE_AUTH_LIMITER_FAIL_CLOSED must be true") {
		t.Fatalf("production must reject fail-open authentication limiting, got %v", err)
	}
}

func TestLoadRejectsMissingAIServiceInProduction(t *testing.T) {
	t.Setenv("EDUGRADE_ENV", "production")
	t.Setenv("EDUGRADE_AI_GRADING_ENABLED", "true")
	t.Setenv("EDUGRADE_SESSION_COOKIE_SECURE", "true")
	setSecureProductionEnvironment(t)
	t.Setenv("EDUGRADE_AI_SERVICE_URL", "")

	if _, err := Load(""); err == nil {
		t.Fatal("enabled production AI grading must require a real service")
	}
}

func TestLoadRejectsPlaintextRemoteAIServiceInProduction(t *testing.T) {
	t.Setenv("EDUGRADE_ENV", "production")
	setSecureProductionEnvironment(t)
	t.Setenv("EDUGRADE_AI_GRADING_ENABLED", "true")
	t.Setenv("EDUGRADE_AI_SERVICE_URL", "http://grading-agent:8100")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "must use https outside loopback") {
		t.Fatalf("production must reject plaintext grading-agent transport, got %v", err)
	}
}

func TestLoadAllowsProductionWithAIGradingDisabled(t *testing.T) {
	t.Setenv("EDUGRADE_ENV", "production")
	t.Setenv("EDUGRADE_AI_GRADING_ENABLED", "false")
	t.Setenv("EDUGRADE_ALLOW_MOCK_AI", "false")
	setSecureProductionEnvironment(t)
	t.Setenv("EDUGRADE_AI_SERVICE_URL", "")
	t.Setenv("EDUGRADE_GRADING_AGENT_TOKEN", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("disabled production AI grading should start safely: %v", err)
	}
	if cfg.AIService.Enabled {
		t.Fatal("AI grading must remain disabled")
	}
}

func TestLoadRejectsProductionMockAI(t *testing.T) {
	t.Setenv("EDUGRADE_ENV", "production")
	t.Setenv("EDUGRADE_AI_GRADING_ENABLED", "false")
	t.Setenv("EDUGRADE_ALLOW_MOCK_AI", "true")
	setSecureProductionEnvironment(t)

	if _, err := Load(""); err == nil {
		t.Fatal("production must reject mock AI even while grading is disabled")
	}
}

func TestLoadRequiresExplicitDemoMock(t *testing.T) {
	t.Setenv("EDUGRADE_ENV", "demo")
	t.Setenv("EDUGRADE_AI_GRADING_ENABLED", "true")
	t.Setenv("EDUGRADE_ALLOW_MOCK_AI", "false")
	t.Setenv("EDUGRADE_AI_SERVICE_URL", "")

	if _, err := Load(""); err == nil {
		t.Fatal("demo mock AI must be explicitly enabled")
	}
}

func setSecureProductionEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("EDUGRADE_AI_SERVICE_URL", "https://grading-agent.internal")
	t.Setenv("EDUGRADE_INTERNAL_TLS_PORT", "8443")
	t.Setenv("EDUGRADE_INTERNAL_TLS_CERT_FILE", "/run/secrets/api-tls/server.crt")
	t.Setenv("EDUGRADE_INTERNAL_TLS_KEY_FILE", "/run/secrets/api-tls/server.key")
	t.Setenv("EDUGRADE_GRADING_AGENT_TOKEN", "production-service-token-with-at-least-32-characters")
	t.Setenv("EDUGRADE_POSTGRES_DSN", "postgres://edugrade:strong-password@db.internal:5432/edugrade?sslmode=require")
	t.Setenv("EDUGRADE_POSTGRES_TENANT_RLS", "true")
	t.Setenv("EDUGRADE_REDIS_PASSWORD", "production-redis-password-with-at-least-32-characters")
	t.Setenv("EDUGRADE_REDIS_USERNAME", "edugrade-api")
	t.Setenv("EDUGRADE_REDIS_TLS_ENABLED", "true")
	t.Setenv("EDUGRADE_MINIO_APP_ACCESS_KEY", "production-app-access")
	t.Setenv("EDUGRADE_MINIO_APP_SECRET_KEY", "production-app-secret")
	t.Setenv("EDUGRADE_MINIO_USE_SSL", "true")
	t.Setenv("EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY", "production-model-credential-key-with-at-least-32-characters")
	t.Setenv("EDUGRADE_CORS_ALLOWED_ORIGINS", "https://grading.example.edu")
	t.Setenv("EDUGRADE_BARCODE_ACTIVE_KEY_ID", "production-v1")
	t.Setenv("EDUGRADE_BARCODE_HMAC_KEYS", "production-v1:0123456789abcdef0123456789abcdef")
}
