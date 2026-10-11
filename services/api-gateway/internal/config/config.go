package config

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Service       ServiceConfig
	Auth          AuthConfig
	Security      SecurityConfig
	ModelSecrets  ModelSecretConfig
	Postgres      PostgresConfig
	Redis         RedisConfig
	MinIO         MinIOConfig
	Qdrant        QdrantConfig
	AIService     AIServiceConfig
	Files         FileConfig
	Observability ObservabilityConfig
	Barcode       BarcodeConfig
}

type ServiceConfig struct {
	Name                string
	Environment         string
	Host                string
	Port                int
	LogLevel            string
	ReadinessTimeout    time.Duration
	ShutdownTimeout     time.Duration
	ReadHeaderTimeout   time.Duration
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	InternalTLSPort     int
	InternalTLSCertFile string
	InternalTLSKeyFile  string
}

func (c ServiceConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func (c ServiceConfig) InternalTLSAddr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.InternalTLSPort)
}

type PostgresConfig struct {
	DSN              string
	TenantRLSEnabled bool
	MaxOpenConns     int
	MaxIdleConns     int
	ConnMaxLifetime  time.Duration
	ConnMaxIdleTime  time.Duration
	StatementTimeout time.Duration
	LockTimeout      time.Duration
}

type AuthConfig struct {
	SessionTTL             time.Duration
	RememberedSessionTTL   time.Duration
	PublicSessionTTL       time.Duration
	RecentAuthTTL          time.Duration
	LoginFailureLimit      int
	LoginFailureWindow     time.Duration
	LoginLimiterFailClosed bool
	SessionCookieName      string
	DeviceCookieName       string
	SessionCookieSecure    bool
	RiskMode               string
	DeviceBindingTTL       time.Duration
	MFAEnabled             bool
	MFAMasterKey           string
	WechatEnabled          bool
	WechatAppID            string
	WechatAppSecret        string
	WechatRedirectURL      string
	WechatChallengeTTL     time.Duration
}

type SecurityConfig struct {
	MaxHeaderBytes      int
	MaxRequestBodyBytes int64
	CORSAllowedOrigins  []string
	CORSAllowedMethods  []string
	CORSAllowedHeaders  []string
	TrustedProxyCIDRs   []string
}

type ModelSecretConfig struct {
	MasterKey string
}

const localModelCredentialMasterKey = "edugrade-local-model-credential-key-change-before-production"

const defaultAllowedFileExtensions = ".pdf,.png,.jpg,.jpeg,.tif,.tiff,.csv,.docx,.txt,.md,.markdown"

type RedisConfig struct {
	Addr          string
	Username      string
	Password      string
	DB            int
	TLSEnabled    bool
	TLSCAFile     string
	TLSServerName string
}

type MinIOConfig struct {
	Endpoint      string
	AccessKey     string
	SecretKey     string
	UseSSL        bool
	TLSCAFile     string
	TLSServerName string
}

type QdrantConfig struct {
	URL    string
	APIKey string
}

type AIServiceConfig struct {
	Enabled           bool
	MathGradingV2     bool
	AllowMock         bool
	URL               string
	Token             string
	Timeout           time.Duration
	MaxRetries        int
	ModelVersion      string
	PromptVersion     string
	MinConfidence     float64
	ProviderKey       string
	DeploymentKey     string
	AdapterType       string
	DeploymentRegion  string
	CapabilityProfile string
}

type FileConfig struct {
	Bucket                    string
	MaxUploadBytes            int64
	AllowedExtensions         []string
	ReconciliationInterval    time.Duration
	ReconciliationStaleAfter  time.Duration
	ReconciliationBatchSize   int
	ReconciliationObjectLimit int
}

type ObservabilityConfig struct {
	SlowRequestThreshold      time.Duration
	WorkerHeartbeatStaleAfter time.Duration
}

type BarcodeConfig struct {
	ActiveKeyID string
	HMACKeys    map[string][]byte
}

// Load 按环境读取默认值，先返回解析或基础校验错误，再执行对应环境的启动限制。
func Load(envFile string) (Config, error) {
	if envFile != "" {
		if err := loadDotEnv(envFile); err != nil {
			return Config{}, err
		}
	}

	environment := getEnv("EDUGRADE_ENV", "development")
	parser := envParser{strict: isProductionLike(environment)}
	secretDefaults := map[string]string{
		// 开发环境只提供无凭据的连接地址；实际数据库凭据必须通过环境变量或 *_FILE 注入，避免把可复制的口令提交到源码。
		"EDUGRADE_POSTGRES_DSN":                "postgres://edugrade@127.0.0.1:5432/edugrade?sslmode=disable",
		"EDUGRADE_REDIS_PASSWORD":              "",
		"EDUGRADE_MINIO_APP_ACCESS_KEY":        "edugrade-app",
		"EDUGRADE_MINIO_APP_SECRET_KEY":        "edugrade_app_dev_secret",
		"EDUGRADE_QDRANT_API_KEY":              "",
		"EDUGRADE_GRADING_AGENT_TOKEN":         "",
		"EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY": localModelCredentialMasterKey,
		"EDUGRADE_BARCODE_HMAC_KEYS":           "",
		"EDUGRADE_MFA_MASTER_KEY":              "",
		"EDUGRADE_WECHAT_APP_SECRET":           "",
	}
	secretValues := make(map[string]string, len(secretDefaults))
	for key, fallback := range secretDefaults {
		value, err := getEnvOrFile(key, fallback)
		if err != nil {
			return Config{}, err
		}
		secretValues[key] = value
	}
	sessionCookieSecure := parser.Bool("EDUGRADE_SESSION_COOKIE_SECURE", defaultSessionCookieSecure(environment))

	cfg := Config{
		Service: ServiceConfig{
			Name:                getEnv("EDUGRADE_SERVICE_NAME", "api-gateway"),
			Environment:         environment,
			Host:                getEnv("EDUGRADE_HTTP_HOST", "127.0.0.1"),
			Port:                parser.Int("EDUGRADE_HTTP_PORT", 8080),
			LogLevel:            getEnv("EDUGRADE_LOG_LEVEL", "info"),
			ReadinessTimeout:    parser.Duration("EDUGRADE_READINESS_TIMEOUT", 2*time.Second),
			ShutdownTimeout:     parser.Duration("EDUGRADE_SHUTDOWN_TIMEOUT", 10*time.Second),
			ReadHeaderTimeout:   parser.Duration("EDUGRADE_HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
			ReadTimeout:         parser.Duration("EDUGRADE_HTTP_READ_TIMEOUT", 15*time.Second),
			WriteTimeout:        parser.Duration("EDUGRADE_HTTP_WRITE_TIMEOUT", 780*time.Second),
			IdleTimeout:         parser.Duration("EDUGRADE_HTTP_IDLE_TIMEOUT", 60*time.Second),
			InternalTLSPort:     parser.Int("EDUGRADE_INTERNAL_TLS_PORT", 0),
			InternalTLSCertFile: strings.TrimSpace(os.Getenv("EDUGRADE_INTERNAL_TLS_CERT_FILE")),
			InternalTLSKeyFile:  strings.TrimSpace(os.Getenv("EDUGRADE_INTERNAL_TLS_KEY_FILE")),
		},
		Auth: AuthConfig{
			SessionTTL:             parser.Duration("EDUGRADE_SESSION_TTL", 8*time.Hour),
			RememberedSessionTTL:   parser.Duration("EDUGRADE_REMEMBERED_SESSION_TTL", 30*24*time.Hour),
			PublicSessionTTL:       parser.Duration("EDUGRADE_PUBLIC_SESSION_TTL", 4*time.Hour),
			RecentAuthTTL:          parser.Duration("EDUGRADE_RECENT_AUTH_TTL", time.Hour),
			LoginFailureLimit:      parser.Int("EDUGRADE_LOGIN_FAILURE_LIMIT", 5),
			LoginFailureWindow:     parser.Duration("EDUGRADE_LOGIN_FAILURE_WINDOW", 15*time.Minute),
			LoginLimiterFailClosed: parser.Bool("EDUGRADE_AUTH_LIMITER_FAIL_CLOSED", isProductionLike(environment)),
			SessionCookieName:      getEnv("EDUGRADE_SESSION_COOKIE_NAME", "edugrade_session"),
			DeviceCookieName:       getEnv("EDUGRADE_DEVICE_COOKIE_NAME", "edugrade_device"),
			SessionCookieSecure:    sessionCookieSecure,
			RiskMode:               strings.ToLower(strings.TrimSpace(getEnv("EDUGRADE_AUTH_RISK_MODE", "shadow"))),
			DeviceBindingTTL:       parser.Duration("EDUGRADE_DEVICE_BINDING_TTL", 180*24*time.Hour),
			MFAEnabled:             parser.Bool("EDUGRADE_MFA_ENABLED", false),
			MFAMasterKey:           secretValues["EDUGRADE_MFA_MASTER_KEY"],
			WechatEnabled:          parser.Bool("EDUGRADE_WECHAT_LOGIN_ENABLED", false),
			WechatAppID:            strings.TrimSpace(os.Getenv("EDUGRADE_WECHAT_APP_ID")),
			WechatAppSecret:        secretValues["EDUGRADE_WECHAT_APP_SECRET"],
			WechatRedirectURL:      strings.TrimSpace(os.Getenv("EDUGRADE_WECHAT_REDIRECT_URL")),
			WechatChallengeTTL:     parser.Duration("EDUGRADE_WECHAT_CHALLENGE_TTL", 5*time.Minute),
		},
		Security: SecurityConfig{
			MaxHeaderBytes:      parser.Int("EDUGRADE_HTTP_MAX_HEADER_BYTES", 1<<20),
			MaxRequestBodyBytes: int64(parser.Int("EDUGRADE_MAX_REQUEST_BODY_BYTES", 2*1024*1024)),
			CORSAllowedOrigins:  splitCSV(getEnv("EDUGRADE_CORS_ALLOWED_ORIGINS", "http://127.0.0.1:5173,http://127.0.0.1:5174,http://127.0.0.1:5180,http://localhost:5173,http://localhost:5174,http://localhost:5180")),
			CORSAllowedMethods:  splitCSV(getEnv("EDUGRADE_CORS_ALLOWED_METHODS", "GET,POST,PUT,PATCH,DELETE,OPTIONS")),
			CORSAllowedHeaders:  splitCSV(getEnv("EDUGRADE_CORS_ALLOWED_HEADERS", "Authorization,Content-Type,X-Request-ID,X-Trace-ID,Idempotency-Key,X-EduGrade-CSRF")),
			TrustedProxyCIDRs:   splitCSV(getEnv("EDUGRADE_TRUSTED_PROXY_CIDRS", "")),
		},
		ModelSecrets: ModelSecretConfig{
			MasterKey: secretValues["EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY"],
		},
		Postgres: PostgresConfig{
			DSN:              secretValues["EDUGRADE_POSTGRES_DSN"],
			TenantRLSEnabled: parser.Bool("EDUGRADE_POSTGRES_TENANT_RLS", isProductionLike(environment)),
			MaxOpenConns:     parser.Int("EDUGRADE_POSTGRES_MAX_OPEN_CONNS", 10),
			MaxIdleConns:     parser.Int("EDUGRADE_POSTGRES_MAX_IDLE_CONNS", 5),
			ConnMaxLifetime:  parser.Duration("EDUGRADE_POSTGRES_CONN_MAX_LIFETIME", 30*time.Minute),
			ConnMaxIdleTime:  parser.Duration("EDUGRADE_POSTGRES_CONN_MAX_IDLE_TIME", 5*time.Minute),
			StatementTimeout: parser.Duration("EDUGRADE_POSTGRES_STATEMENT_TIMEOUT", 60*time.Second),
			LockTimeout:      parser.Duration("EDUGRADE_POSTGRES_LOCK_TIMEOUT", 5*time.Second),
		},
		Redis: RedisConfig{
			Addr:          getEnv("EDUGRADE_REDIS_ADDR", "127.0.0.1:6379"),
			Username:      strings.TrimSpace(os.Getenv("EDUGRADE_REDIS_USERNAME")),
			Password:      secretValues["EDUGRADE_REDIS_PASSWORD"],
			DB:            parser.Int("EDUGRADE_REDIS_DB", 0),
			TLSEnabled:    parser.Bool("EDUGRADE_REDIS_TLS_ENABLED", false),
			TLSCAFile:     strings.TrimSpace(os.Getenv("EDUGRADE_REDIS_TLS_CA_FILE")),
			TLSServerName: strings.TrimSpace(os.Getenv("EDUGRADE_REDIS_TLS_SERVER_NAME")),
		},
		MinIO: MinIOConfig{
			Endpoint:      getEnv("EDUGRADE_MINIO_ENDPOINT", "127.0.0.1:9000"),
			AccessKey:     secretValues["EDUGRADE_MINIO_APP_ACCESS_KEY"],
			SecretKey:     secretValues["EDUGRADE_MINIO_APP_SECRET_KEY"],
			UseSSL:        parser.Bool("EDUGRADE_MINIO_USE_SSL", false),
			TLSCAFile:     getEnv("EDUGRADE_MINIO_TLS_CA_FILE", ""),
			TLSServerName: getEnv("EDUGRADE_MINIO_TLS_SERVER_NAME", ""),
		},
		Qdrant: QdrantConfig{
			URL:    getEnv("EDUGRADE_QDRANT_URL", ""),
			APIKey: secretValues["EDUGRADE_QDRANT_API_KEY"],
		},
		AIService: AIServiceConfig{
			Enabled:           parser.Bool("EDUGRADE_AI_GRADING_ENABLED", false),
			MathGradingV2:     parser.Bool("EDUGRADE_MATH_GRADING_V2_ENABLED", false),
			AllowMock:         parser.Bool("EDUGRADE_ALLOW_MOCK_AI", false),
			URL:               getEnv("EDUGRADE_AI_SERVICE_URL", ""),
			Token:             secretValues["EDUGRADE_GRADING_AGENT_TOKEN"],
			Timeout:           parser.Duration("EDUGRADE_AI_SERVICE_TIMEOUT", 750*time.Second),
			MaxRetries:        parser.Int("EDUGRADE_AI_SERVICE_MAX_RETRIES", 0),
			ModelVersion:      getEnv("EDUGRADE_AI_MODEL_VERSION", "Qwen/Qwen3-4B-GGUF:Q4_K_M"),
			PromptVersion:     getEnv("EDUGRADE_AI_PROMPT_VERSION", "subjective-governed-cn-subject-routing-v6"),
			MinConfidence:     parser.Float("EDUGRADE_AI_MIN_CONFIDENCE", 0.8),
			ProviderKey:       getEnv("EDUGRADE_AI_PROVIDER_KEY", "local"),
			DeploymentKey:     getEnv("EDUGRADE_AI_DEPLOYMENT_KEY", "local-qwen3-4b-q4-k-m"),
			AdapterType:       getEnv("EDUGRADE_AI_ADAPTER_TYPE", "local_llama_cpp"),
			DeploymentRegion:  getEnv("EDUGRADE_AI_DEPLOYMENT_REGION", "on_premise"),
			CapabilityProfile: getEnv("EDUGRADE_AI_CAPABILITY_PROFILE", "local-pilot-v1"),
		},
		Files: FileConfig{
			Bucket:                    getEnv("EDUGRADE_FILE_BUCKET", "edugrade-files"),
			MaxUploadBytes:            int64(parser.Int("EDUGRADE_FILE_MAX_UPLOAD_BYTES", 104857600)),
			AllowedExtensions:         splitCSV(getEnv("EDUGRADE_FILE_ALLOWED_EXTENSIONS", defaultAllowedFileExtensions)),
			ReconciliationInterval:    parser.Duration("EDUGRADE_FILE_RECONCILIATION_INTERVAL", 24*time.Hour),
			ReconciliationStaleAfter:  parser.Duration("EDUGRADE_FILE_RECONCILIATION_STALE_AFTER", time.Hour),
			ReconciliationBatchSize:   parser.Int("EDUGRADE_FILE_RECONCILIATION_BATCH_SIZE", 200),
			ReconciliationObjectLimit: parser.Int("EDUGRADE_FILE_RECONCILIATION_OBJECT_LIMIT", 1000),
		},
		Observability: ObservabilityConfig{
			SlowRequestThreshold:      parser.Duration("EDUGRADE_SLOW_REQUEST_THRESHOLD", 2*time.Second),
			WorkerHeartbeatStaleAfter: parser.Duration("EDUGRADE_WORKER_HEARTBEAT_STALE_AFTER", 30*time.Second),
		},
		Barcode: BarcodeConfig{
			ActiveKeyID: getEnv("EDUGRADE_BARCODE_ACTIVE_KEY_ID", "local-v1"),
			HMACKeys:    parseBarcodeKeys(secretValues["EDUGRADE_BARCODE_HMAC_KEYS"]),
		},
	}
	if err := parser.Err(); err != nil {
		return Config{}, err
	}
	if err := validateProductionConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func parseBarcodeKeys(raw string) map[string][]byte {
	keys := map[string][]byte{}
	for _, item := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), ":", 2)
		if len(parts) == 2 && parts[0] != "" && len(parts[1]) >= 32 {
			keys[parts[0]] = []byte(parts[1])
		}
	}
	return keys
}

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
		return nil
	}
	if !isProductionLike(cfg.Service.Environment) {
		return nil
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
	if len(cfg.ModelSecrets.MasterKey) < 32 || cfg.ModelSecrets.MasterKey == localModelCredentialMasterKey {
		return fmt.Errorf("unsafe production configuration: EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY must be a unique secret with at least 32 characters")
	}
	return nil
}

func validateInitialConfig(cfg Config) error {
	if err := validateMFAConfig(cfg.Auth, cfg.ModelSecrets); err != nil {
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

func validateMFAConfig(auth AuthConfig, modelSecrets ModelSecretConfig) error {
	if auth.MFAEnabled {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth.MFAMasterKey))
		if err != nil || len(key) != 32 {
			return fmt.Errorf("EDUGRADE_MFA_MASTER_KEY must be a base64-encoded 32-byte independent key when MFA is enabled")
		}
		if strings.TrimSpace(auth.MFAMasterKey) == strings.TrimSpace(modelSecrets.MasterKey) {
			return fmt.Errorf("EDUGRADE_MFA_MASTER_KEY must not reuse EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY")
		}
	}
	return nil
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

func defaultSessionCookieSecure(environment string) bool {
	return isProductionLike(environment)
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("invalid env line: %q", line)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getEnvOrFile(key, fallback string) (string, error) {
	if value, ok := os.LookupEnv(key); ok {
		if filePath := strings.TrimSpace(os.Getenv(key + "_FILE")); filePath != "" {
			if strings.TrimSpace(value) != "" {
				return "", fmt.Errorf("%s and %s_FILE cannot both be set", key, key)
			}
			content, err := os.ReadFile(filePath)
			if err != nil {
				return "", fmt.Errorf("read %s_FILE: %w", key, err)
			}
			return strings.TrimRight(string(content), "\r\n"), nil
		}
		return value, nil
	}
	filePath := strings.TrimSpace(os.Getenv(key + "_FILE"))
	if filePath == "" {
		return fallback, nil
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read %s_FILE: %w", key, err)
	}
	return strings.TrimRight(string(content), "\r\n"), nil
}

type envParser struct {
	strict   bool
	problems []string
}

func (p *envParser) raw(key string) (string, bool) {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func (p *envParser) invalid(key, expected string) {
	if p.strict {
		p.problems = append(p.problems, fmt.Sprintf("%s must be a valid %s", key, expected))
		return
	}
	// 不要记录原始值：配置可能来自共享密钥文件。
	slog.Warn("invalid configuration value; using development default", "key", key, "expected", expected)
}

func (p *envParser) Int(key string, fallback int) int {
	value, ok := p.raw(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		p.invalid(key, "integer")
		return fallback
	}
	return parsed
}

func (p *envParser) Bool(key string, fallback bool) bool {
	value, ok := p.raw(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		p.invalid(key, "boolean")
		return fallback
	}
	return parsed
}

func (p *envParser) Duration(key string, fallback time.Duration) time.Duration {
	value, ok := p.raw(key)
	if !ok {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		p.invalid(key, "duration")
		return fallback
	}
	return parsed
}

func (p *envParser) Float(key string, fallback float64) float64 {
	value, ok := p.raw(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		p.invalid(key, "finite number")
		return fallback
	}
	return parsed
}

func (p *envParser) Err() error {
	if len(p.problems) == 0 {
		return nil
	}
	return fmt.Errorf("invalid production configuration: %s", strings.Join(p.problems, "; "))
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
