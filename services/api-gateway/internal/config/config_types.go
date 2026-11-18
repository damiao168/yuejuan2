package config

import (
	"fmt"
	"time"
)

type Config struct {
	Service                  ServiceConfig
	Auth                     AuthConfig
	Security                 SecurityConfig
	ModelSecrets             ModelSecretConfig
	Postgres                 PostgresConfig
	Redis                    RedisConfig
	MinIO                    MinIOConfig
	Qdrant                   QdrantConfig
	AIService                AIServiceConfig
	SchoolAssistantSearchURL string
	SchoolAssistant          SchoolAssistantConfig
	Files                    FileConfig
	Observability            ObservabilityConfig
	Barcode                  BarcodeConfig
}

type SchoolAssistantConfig struct {
	DirectAnswerEnabled    bool
	DisableMemory          bool
	DisableCacheCoalescing bool
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
	MasterKey         string
	ReviewEvidenceKey string
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
	ImagePrefixCache  bool
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
