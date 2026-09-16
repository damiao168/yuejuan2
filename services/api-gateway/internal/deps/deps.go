package deps

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/minioadapter"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/minio/minio-go/v7"
	"github.com/redis/go-redis/v9"
)

type Checker interface {
	Name() string
	Check(ctx context.Context) error
}

type CheckResult struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Required   bool   `json:"required"`
	Error      string `json:"error,omitempty"`
	Detail     string `json:"detail,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	CheckedAt  string `json:"checked_at"`
}

var ErrNotConfigured = errors.New("dependency not configured")

type PostgresChecker struct {
	db *sql.DB
}

func NewPostgresChecker(db *sql.DB) *PostgresChecker {
	return &PostgresChecker{db: db}
}

func (c *PostgresChecker) Name() string {
	return "postgres"
}

func (c *PostgresChecker) Check(ctx context.Context) error {
	return c.db.PingContext(ctx)
}

type RedisChecker struct {
	client *redis.Client
}

type AuthRateLimiterChecker struct {
	degraded func() bool
	probe    func(context.Context) error
}

func NewAuthRateLimiterChecker(degraded func() bool) AuthRateLimiterChecker {
	return AuthRateLimiterChecker{degraded: degraded}
}

func (c AuthRateLimiterChecker) Name() string { return "auth_rate_limiter" }

func (c AuthRateLimiterChecker) WithProbe(probe func(context.Context) error) AuthRateLimiterChecker {
	c.probe = probe
	return c
}

func (c AuthRateLimiterChecker) Check(ctx context.Context) error {
	if c.probe != nil {
		if err := c.probe(ctx); err != nil {
			return err
		}
	}
	if c.degraded != nil && c.degraded() {
		return errors.New("distributed authentication rate limiter is degraded")
	}
	return nil
}

func NewRedisChecker(cfg config.RedisConfig) (*RedisChecker, func() error, error) {
	options := &redis.Options{
		Addr:     cfg.Addr,
		Username: cfg.Username,
		Password: cfg.Password,
		DB:       cfg.DB,
	}
	if cfg.TLSEnabled {
		tlsConfig, err := newRedisTLSConfig(cfg)
		if err != nil {
			return nil, nil, err
		}
		options.TLSConfig = tlsConfig
	}
	client := redis.NewClient(options)
	return &RedisChecker{client: client}, client.Close, nil
}

func newRedisTLSConfig(cfg config.RedisConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: strings.TrimSpace(cfg.TLSServerName),
	}
	caFile := strings.TrimSpace(cfg.TLSCAFile)
	if caFile == "" {
		return tlsConfig, nil
	}

	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read Redis TLS CA file: %w", err)
	}
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	if ok := rootCAs.AppendCertsFromPEM(caPEM); !ok {
		return nil, fmt.Errorf("failed to load Redis TLS CA file %q: file contains no valid certificates", caFile)
	}
	tlsConfig.RootCAs = rootCAs
	return tlsConfig, nil
}

func (c *RedisChecker) Name() string {
	return "redis"
}

func (c *RedisChecker) Check(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func (c *RedisChecker) Client() redis.UniversalClient {
	return c.client
}

type MinIOChecker struct {
	client *minio.Client
	bucket string
}

func NewMinIOChecker(cfg config.MinIOConfig, bucket string) (*MinIOChecker, error) {
	client, err := minioadapter.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &MinIOChecker{client: client, bucket: strings.TrimSpace(bucket)}, nil
}

func (c *MinIOChecker) Name() string {
	return "minio"
}

func (c *MinIOChecker) Check(ctx context.Context) error {
	if c.bucket == "" {
		return errors.New("MinIO bucket is not configured")
	}
	exists, err := c.client.BucketExists(ctx, c.bucket)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("MinIO bucket %q is missing; restore it before accepting traffic", c.bucket)
	}
	return nil
}

type HTTPChecker struct {
	checkerName string
	url         string
	headers     map[string]string
	detail      string
	required    bool
}

func NewQdrantChecker(cfg config.QdrantConfig) Checker {
	url := strings.TrimSpace(cfg.URL)
	if url == "" {
		return NotConfiguredChecker{CheckerName: "qdrant", DetailText: "EDUGRADE_QDRANT_URL 未配置/待接入"}
	}
	headers := map[string]string{}
	if cfg.APIKey != "" {
		headers["api-key"] = cfg.APIKey
	}
	return HTTPChecker{
		checkerName: "qdrant",
		url:         strings.TrimRight(url, "/") + "/healthz",
		headers:     headers,
		detail:      "Qdrant /healthz",
	}
}

func NewAIServiceChecker(cfg config.AIServiceConfig, environments ...string) Checker {
	environment := ""
	if len(environments) > 0 {
		environment = strings.ToLower(strings.TrimSpace(environments[0]))
	}
	url := strings.TrimSpace(cfg.URL)
	if url == "" && (environment == "" || environment == "development" || environment == "dev" || environment == "test" || environment == "local") {
		return StatusChecker{CheckerName: "ai_service", StatusValue: "mock", DetailText: "开发/测试 Mock，仅用于流程验证，不可作为正式评分结论"}
	}
	if url == "" && environment == "demo" && cfg.Enabled && cfg.AllowMock {
		return StatusChecker{CheckerName: "ai_service", StatusValue: "mock", DetailText: "Demo Mock 已显式启用，仅用于演示，不可作为正式评分结论"}
	}
	if !cfg.Enabled && (environment == "production" || environment == "staging" || environment == "demo") {
		return StatusChecker{CheckerName: "ai_service", StatusValue: "disabled", DetailText: "AI 阅卷已关闭；客观题规则评分和人工阅卷可继续使用"}
	}
	if url == "" {
		return NotConfiguredChecker{CheckerName: "ai_service", DetailText: "EDUGRADE_AI_SERVICE_URL 未配置/待接入"}
	}
	return HTTPChecker{
		checkerName: "ai_service",
		url:         strings.TrimRight(url, "/") + "/ready",
		detail:      "grading-agent model readiness /ready",
	}
}

func (c HTTPChecker) Name() string {
	return c.checkerName
}

func (c HTTPChecker) Detail() string {
	return c.detail
}

func (c HTTPChecker) RequiredForReadiness() bool {
	return c.required
}

func (c HTTPChecker) Check(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s health endpoint returned %d", c.checkerName, resp.StatusCode)
	}
	return nil
}

type DetailedChecker interface {
	Detail() string
}

type StatusProvider interface {
	Status() string
}

// ReadinessProvider marks optional capabilities which may be unavailable
// without taking the API process out of the load balancer. Core storage and
// coordination checkers intentionally do not implement it and remain required.
type ReadinessProvider interface {
	RequiredForReadiness() bool
}

func requiredForReadiness(checker Checker) bool {
	if provider, ok := checker.(ReadinessProvider); ok {
		return provider.RequiredForReadiness()
	}
	return true
}

func CheckAll(ctx context.Context, timeout time.Duration, checkers []Checker) ([]CheckResult, bool) {
	results := make([]CheckResult, 0, len(checkers))
	allHealthy := true
	for _, checker := range checkers {
		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		start := time.Now()
		err := checker.Check(checkCtx)
		duration := time.Since(start)
		cancel()
		result := CheckResult{
			Name:       checker.Name(),
			Status:     "ok",
			Required:   requiredForReadiness(checker),
			DurationMS: duration.Milliseconds(),
			CheckedAt:  time.Now().UTC().Format(time.RFC3339),
		}
		if detailed, ok := checker.(DetailedChecker); ok {
			result.Detail = detailed.Detail()
		}
		if provider, ok := checker.(StatusProvider); ok {
			result.Status = provider.Status()
		}
		if err != nil {
			if errors.Is(err, ErrNotConfigured) {
				result.Status = "not_configured"
				if result.Detail == "" {
					result.Detail = "未配置/待接入"
				}
			} else {
				result.Status = "error"
				result.Error = err.Error()
			}
			if result.Required {
				allHealthy = false
			}
		}
		results = append(results, result)
	}
	return results, allHealthy
}

type StatusChecker struct {
	CheckerName string
	StatusValue string
	DetailText  string
	Required    bool
}

func (c StatusChecker) Name() string {
	return c.CheckerName
}

func (c StatusChecker) Status() string {
	return c.StatusValue
}

func (c StatusChecker) Detail() string {
	return c.DetailText
}

func (c StatusChecker) Check(context.Context) error {
	return nil
}

func (c StatusChecker) RequiredForReadiness() bool {
	return c.Required
}

type StaticChecker struct {
	CheckerName string
	Err         error
}

func (c StaticChecker) Name() string {
	return c.CheckerName
}

func (c StaticChecker) Check(context.Context) error {
	if c.Err != nil {
		return fmt.Errorf("%s: %w", c.CheckerName, c.Err)
	}
	return nil
}

type NotConfiguredChecker struct {
	CheckerName string
	DetailText  string
	Required    bool
}

func (c NotConfiguredChecker) Name() string {
	return c.CheckerName
}

func (c NotConfiguredChecker) Detail() string {
	if c.DetailText == "" {
		return "未配置/待接入"
	}
	return c.DetailText
}

func (c NotConfiguredChecker) Check(context.Context) error {
	return fmt.Errorf("%s: %w", c.CheckerName, ErrNotConfigured)
}

func (c NotConfiguredChecker) RequiredForReadiness() bool {
	return c.Required
}
