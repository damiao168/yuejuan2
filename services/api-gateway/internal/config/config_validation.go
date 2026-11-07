package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// 所有环境先按固定顺序返回首个基础错误；生产类环境再汇总问题，最后才检查模型主密钥。
func validateProductionConfig(cfg Config) error {
	if err := validateInitialConfig(cfg); err != nil {
		return err
	}
	environment := strings.ToLower(strings.TrimSpace(cfg.Service.Environment))
	if environment == "demo" {
		if cfg.AIService.Enabled && strings.TrimSpace(cfg.AIService.URL) == "" && !cfg.AIService.AllowMock {
			return fmt.Errorf("AI grading is enabled in demo but neither EDUGRADE_AI_SERVICE_URL nor EDUGRADE_ALLOW_MOCK_AI=true is configured")
		}
		return validateModelSecrets(cfg)
	}
	if !isProductionLike(cfg.Service.Environment) {
		return validateModelSecrets(cfg)
	}
	problems := productionServiceProblems(cfg.Service, cfg.Auth, cfg.AIService)
	problems = append(problems, productionStorageProblems(cfg.Postgres, cfg.Redis, cfg.MinIO, cfg.Barcode)...)
	for _, origin := range cfg.Security.CORSAllowedOrigins {
		if strings.Contains(origin, "localhost") || strings.Contains(origin, "127.0.0.1") {
			problems = append(problems, "EDUGRADE_CORS_ALLOWED_ORIGINS must not contain local development origins")
			break
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("unsafe production configuration: %s", strings.Join(problems, "; "))
	}
	return validateModelSecrets(cfg)
}

func validateModelSecrets(cfg Config) error {
	// 本地持久化同样存储真实模型凭据，不能因环境名放行公开默认密钥。
	if len(cfg.ModelSecrets.MasterKey) < 32 || cfg.ModelSecrets.MasterKey == localModelCredentialMasterKey || strings.Contains(cfg.ModelSecrets.MasterKey, "change_me") || strings.Contains(cfg.ModelSecrets.MasterKey, "replace_with") {
		return fmt.Errorf("EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY must be a unique secret with at least 32 characters")
	}
	if len(cfg.ModelSecrets.ReviewEvidenceKey) < 32 || cfg.ModelSecrets.ReviewEvidenceKey == cfg.ModelSecrets.MasterKey || strings.Contains(cfg.ModelSecrets.ReviewEvidenceKey, "change_me") || strings.Contains(cfg.ModelSecrets.ReviewEvidenceKey, "replace_with") {
		return fmt.Errorf("EDUGRADE_REVIEW_EVIDENCE_HMAC_KEY must be an independent secret with at least 32 characters")
	}
	return nil
}

func validateInitialConfig(cfg Config) error {
	if err := validateSchoolAssistantSearchURL(cfg.SchoolAssistantSearchURL); err != nil {
		return err
	}
	if err := validateWechatConfig(cfg.Auth); err != nil {
		return err
	}
	if err := validateAuthPolicyConfig(cfg.Auth); err != nil {
		return err
	}
	if err := validatePostgresCapacity(cfg.Postgres); err != nil {
		return err
	}
	if err := validateListenerProxyConfig(cfg.Service, cfg.Security); err != nil {
		return err
	}
	return validateAIServiceConfig(cfg.AIService, cfg.Service)
}

func validateWechatConfig(auth AuthConfig) error {
	if auth.WechatEnabled {
		if len(auth.WechatAppID) < 6 || len(auth.WechatAppID) > 128 || len(auth.WechatAppSecret) < 16 {
			return fmt.Errorf("EDUGRADE_WECHAT_APP_ID and EDUGRADE_WECHAT_APP_SECRET are required when WeChat login is enabled")
		}
		redirect, err := url.Parse(auth.WechatRedirectURL)
		if err != nil || redirect.Scheme != "https" || redirect.Host == "" {
			return fmt.Errorf("EDUGRADE_WECHAT_REDIRECT_URL must be an absolute HTTPS URL when WeChat login is enabled")
		}
		if auth.WechatChallengeTTL < time.Minute || auth.WechatChallengeTTL > 10*time.Minute {
			return fmt.Errorf("EDUGRADE_WECHAT_CHALLENGE_TTL must be between 1m and 10m")
		}
	}
	return nil
}

func validateAuthPolicyConfig(auth AuthConfig) error {
	if auth.RiskMode != "off" && auth.RiskMode != "shadow" {
		return fmt.Errorf("EDUGRADE_AUTH_RISK_MODE must be off or shadow until business step-up and recovery policies are complete")
	}
	if auth.DeviceBindingTTL < 24*time.Hour || auth.DeviceBindingTTL > 365*24*time.Hour {
		return fmt.Errorf("EDUGRADE_DEVICE_BINDING_TTL must be between 24h and 8760h")
	}
	return nil
}

func validateListenerProxyConfig(service ServiceConfig, security SecurityConfig) error {
	internalTLSConfigured := service.InternalTLSPort > 0 || service.InternalTLSCertFile != "" || service.InternalTLSKeyFile != ""
	if internalTLSConfigured && (service.InternalTLSPort <= 0 || service.InternalTLSCertFile == "" || service.InternalTLSKeyFile == "") {
		return fmt.Errorf("EDUGRADE_INTERNAL_TLS_PORT, EDUGRADE_INTERNAL_TLS_CERT_FILE, and EDUGRADE_INTERNAL_TLS_KEY_FILE must be configured together")
	}
	if service.InternalTLSPort < 0 || service.InternalTLSPort > 65535 || service.InternalTLSPort == service.Port {
		return fmt.Errorf("EDUGRADE_INTERNAL_TLS_PORT must be 0 or a distinct port between 1 and 65535")
	}
	for _, cidr := range security.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid EDUGRADE_TRUSTED_PROXY_CIDRS entry %q", cidr)
		}
	}
	return nil
}

func validateAIServiceConfig(ai AIServiceConfig, service ServiceConfig) error {
	if ai.MathGradingV2 && !ai.Enabled {
		return fmt.Errorf("EDUGRADE_MATH_GRADING_V2_ENABLED requires EDUGRADE_AI_GRADING_ENABLED")
	}
	if ai.Enabled && strings.TrimSpace(ai.URL) != "" {
		if len(ai.Token) < 32 {
			return fmt.Errorf("unsafe AI service configuration: EDUGRADE_GRADING_AGENT_TOKEN must contain at least 32 characters")
		}
		if ai.Timeout <= 0 || ai.MaxRetries < 0 || ai.MaxRetries > 1 || ai.MinConfidence < 0 || ai.MinConfidence > 1 {
			return fmt.Errorf("invalid AI service timeout, retry, or confidence configuration")
		}
		if service.WriteTimeout <= ai.Timeout {
			return fmt.Errorf("EDUGRADE_HTTP_WRITE_TIMEOUT must exceed EDUGRADE_AI_SERVICE_TIMEOUT")
		}
		return validateAIServiceIdentity(ai)
	}
	return nil
}

func validateAIServiceIdentity(ai AIServiceConfig) error {
	identity := map[string]string{
		"EDUGRADE_AI_PROVIDER_KEY":       ai.ProviderKey,
		"EDUGRADE_AI_DEPLOYMENT_KEY":     ai.DeploymentKey,
		"EDUGRADE_AI_ADAPTER_TYPE":       ai.AdapterType,
		"EDUGRADE_AI_DEPLOYMENT_REGION":  ai.DeploymentRegion,
		"EDUGRADE_AI_CAPABILITY_PROFILE": ai.CapabilityProfile,
	}
	for name, value := range identity {
		if strings.TrimSpace(value) == "" || len(value) > 128 || strings.ContainsAny(value, " \t\r\n") {
			return fmt.Errorf("invalid AI service identity: %s must be a non-empty bounded identifier", name)
		}
	}
	return nil
}

func productionServiceProblems(service ServiceConfig, auth AuthConfig, ai AIServiceConfig) []string {
	var problems []string
	if ai.AllowMock {
		problems = append(problems, "EDUGRADE_ALLOW_MOCK_AI must be false in production-like environments")
	}
	if !auth.LoginLimiterFailClosed {
		problems = append(problems, "EDUGRADE_AUTH_LIMITER_FAIL_CLOSED must be true")
	}
	if ai.Enabled && strings.TrimSpace(ai.URL) == "" {
		problems = append(problems, "EDUGRADE_AI_SERVICE_URL must be configured when AI grading is enabled")
	}
	if service.InternalTLSPort <= 0 {
		problems = append(problems, "internal API TLS listener must be configured")
	}
	if ai.Enabled {
		if err := validateServiceURL("EDUGRADE_AI_SERVICE_URL", ai.URL, service.Environment); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if !auth.SessionCookieSecure {
		problems = append(problems, "EDUGRADE_SESSION_COOKIE_SECURE must be true")
	}
	return problems
}

func productionStorageProblems(postgres PostgresConfig, redis RedisConfig, minio MinIOConfig, barcode BarcodeConfig) []string {
	var problems []string
	if strings.Contains(postgres.DSN, "edugrade_dev") || strings.Contains(postgres.DSN, "sslmode=disable") {
		problems = append(problems, "EDUGRADE_POSTGRES_DSN must not use development credentials or disabled TLS")
	}
	if !postgres.TenantRLSEnabled {
		problems = append(problems, "EDUGRADE_POSTGRES_TENANT_RLS must be true")
	}
	if strings.TrimSpace(redis.Username) == "" {
		problems = append(problems, "EDUGRADE_REDIS_USERNAME must be configured")
	}
	if strings.TrimSpace(redis.Password) == "" {
		problems = append(problems, "EDUGRADE_REDIS_PASSWORD must be configured")
	}
	if !redis.TLSEnabled {
		problems = append(problems, "EDUGRADE_REDIS_TLS_ENABLED must be true")
	}
	if minio.AccessKey == "edugrade-app" || minio.SecretKey == "edugrade_app_dev_secret" {
		problems = append(problems, "MinIO application development credentials must be replaced")
	}
	if !minio.UseSSL {
		problems = append(problems, "EDUGRADE_MINIO_USE_SSL must be true")
	}
	if len(barcode.HMACKeys[barcode.ActiveKeyID]) < 32 {
		problems = append(problems, "EDUGRADE_BARCODE_HMAC_KEYS must contain the active key with at least 32 characters")
	}
	return problems
}

func validatePostgresCapacity(cfg PostgresConfig) error {
	if cfg.MaxOpenConns < 1 || cfg.MaxOpenConns > 500 {
		return fmt.Errorf("EDUGRADE_POSTGRES_MAX_OPEN_CONNS must be between 1 and 500")
	}
	if cfg.MaxIdleConns < 0 || cfg.MaxIdleConns > cfg.MaxOpenConns {
		return fmt.Errorf("EDUGRADE_POSTGRES_MAX_IDLE_CONNS must be between 0 and EDUGRADE_POSTGRES_MAX_OPEN_CONNS")
	}
	if cfg.ConnMaxLifetime < time.Minute || cfg.ConnMaxLifetime > 24*time.Hour {
		return fmt.Errorf("EDUGRADE_POSTGRES_CONN_MAX_LIFETIME must be between 1m and 24h")
	}
	if cfg.ConnMaxIdleTime < 10*time.Second || cfg.ConnMaxIdleTime > cfg.ConnMaxLifetime {
		return fmt.Errorf("EDUGRADE_POSTGRES_CONN_MAX_IDLE_TIME must be between 10s and EDUGRADE_POSTGRES_CONN_MAX_LIFETIME")
	}
	if cfg.StatementTimeout < time.Second || cfg.StatementTimeout > 15*time.Minute {
		return fmt.Errorf("EDUGRADE_POSTGRES_STATEMENT_TIMEOUT must be between 1s and 15m")
	}
	if cfg.LockTimeout < 100*time.Millisecond || cfg.LockTimeout > cfg.StatementTimeout {
		return fmt.Errorf("EDUGRADE_POSTGRES_LOCK_TIMEOUT must be between 100ms and EDUGRADE_POSTGRES_STATEMENT_TIMEOUT")
	}
	return nil
}

func isProductionLike(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "", "development", "dev", "test", "local":
		return false
	default:
		return true
	}
}

func validateServiceURL(name, value, environment string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be a valid HTTP(S) URL", name)
	}
	if !isProductionLike(environment) || parsed.Scheme == "https" {
		return nil
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%s must use https outside loopback in production-like environments", name)
}
