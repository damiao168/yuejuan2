package config

import (
	"os"
	"strings"
	"time"
)

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
		"EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY": "",
		"EDUGRADE_REVIEW_EVIDENCE_HMAC_KEY":    "",
		"EDUGRADE_BARCODE_HMAC_KEYS":           "",
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
			MasterKey:         secretValues["EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY"],
			ReviewEvidenceKey: secretValues["EDUGRADE_REVIEW_EVIDENCE_HMAC_KEY"],
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
		SchoolAssistantSearchURL: strings.TrimSpace(os.Getenv("EDUGRADE_SCHOOL_ASSISTANT_SEARCH_URL")),
		AIService: AIServiceConfig{
			Enabled:           parser.Bool("EDUGRADE_AI_GRADING_ENABLED", false),
			MathGradingV2:     parser.Bool("EDUGRADE_MATH_GRADING_V2_ENABLED", false),
			ImagePrefixCache:  parser.Bool("EDUGRADE_AI_IMAGE_PREFIX_CACHE_ENABLED", false),
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

func defaultSessionCookieSecure(environment string) bool {
	return isProductionLike(environment)
}
