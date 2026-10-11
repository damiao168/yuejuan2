package auth

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

type LoginLimiter interface {
	IsBlocked(ctx context.Context, key string, now time.Time) (time.Duration, bool)
	RegisterFailure(ctx context.Context, key string, now time.Time) (int, time.Duration, bool)
	Clear(ctx context.Context, key string)
}

type LoginAttempt struct {
	TenantCode string
	Identifier string
	IPAddress  string
	// AccountID 由服务端解析，不接受登录 JSON 提供的值；它将用户名、手机号和工号别名归入同一个限流桶。
	AccountID string
}

type LoginLimit struct {
	Bucket     string
	RetryAfter time.Duration
}

type LoginAttemptGuard interface {
	Check(ctx context.Context, attempt LoginAttempt, now time.Time) (LoginLimit, bool)
	RegisterFailure(ctx context.Context, attempt LoginAttempt, now time.Time) (LoginLimit, bool)
	RegisterSuccess(ctx context.Context, attempt LoginAttempt)
}

type LoginLimiterStatus interface {
	Degraded() bool
}

type LoginLimiterProber interface {
	Probe(context.Context) error
}

type LayeredLoginAttemptGuard struct {
	account  LoginLimiter
	source   LoginLimiter
	pair     LoginLimiter
	degraded func() bool
	probe    func(context.Context) error
}

func NewLayeredLoginAttemptGuard(account, source, pair LoginLimiter) *LayeredLoginAttemptGuard {
	return &LayeredLoginAttemptGuard{account: account, source: source, pair: pair}
}

func (l *LayeredLoginAttemptGuard) Degraded() bool {
	return l != nil && l.degraded != nil && l.degraded()
}

func (l *LayeredLoginAttemptGuard) Probe(ctx context.Context) error {
	if l != nil && l.probe != nil {
		return l.probe(ctx)
	}
	return nil
}

func NewMemoryLoginAttemptGuard(limit int, window time.Duration) *LayeredLoginAttemptGuard {
	if limit <= 0 {
		limit = 5
	}
	sourceLimit := limit * 10
	if sourceLimit < 50 {
		sourceLimit = 50
	}
	return NewLayeredLoginAttemptGuard(
		NewLoginFailureLimiter(limit, window),
		NewLoginFailureLimiter(sourceLimit, window),
		NewLoginFailureLimiter(limit, window),
	)
}

func (l *LayeredLoginAttemptGuard) Check(ctx context.Context, attempt LoginAttempt, now time.Time) (LoginLimit, bool) {
	for _, bucket := range l.buckets(attempt) {
		if bucket.limiter == nil {
			continue
		}
		if retryAfter, blocked := bucket.limiter.IsBlocked(ctx, bucket.key, now); blocked {
			return LoginLimit{Bucket: bucket.name, RetryAfter: retryAfter}, true
		}
	}
	return LoginLimit{}, false
}

func (l *LayeredLoginAttemptGuard) RegisterFailure(ctx context.Context, attempt LoginAttempt, now time.Time) (LoginLimit, bool) {
	var result LoginLimit
	blocked := false
	for _, bucket := range l.buckets(attempt) {
		if bucket.limiter == nil {
			continue
		}
		_, retryAfter, currentBlocked := bucket.limiter.RegisterFailure(ctx, bucket.key, now)
		if currentBlocked && (!blocked || retryAfter > result.RetryAfter) {
			result = LoginLimit{Bucket: bucket.name, RetryAfter: retryAfter}
			blocked = true
		}
	}
	return result, blocked
}

func (l *LayeredLoginAttemptGuard) RegisterSuccess(ctx context.Context, attempt LoginAttempt) {
	// 有效凭据证明调用方控制该账户及账户与来源 IP 的组合；登录成功不能清除同一来源 IP 下其他账户的历史。
	if l.account != nil {
		l.account.Clear(ctx, loginAttemptAccountKey(attempt))
		if attempt.AccountID != "" {
			l.account.Clear(ctx, LoginAccountFailureKey(attempt.TenantCode, attempt.Identifier))
		}
	}
	if l.pair != nil {
		l.pair.Clear(ctx, LoginPairFailureKey(attempt.TenantCode, attempt.Identifier, attempt.IPAddress))
	}
}

type loginBucket struct {
	name    string
	key     string
	limiter LoginLimiter
}

func (l *LayeredLoginAttemptGuard) buckets(attempt LoginAttempt) []loginBucket {
	return []loginBucket{
		{name: "account", key: loginAttemptAccountKey(attempt), limiter: l.account},
		{name: "pair", key: LoginPairFailureKey(attempt.TenantCode, attempt.Identifier, attempt.IPAddress), limiter: l.pair},
		{name: "source", key: LoginSourceFailureKey(attempt.IPAddress), limiter: l.source},
	}
}

func loginAttemptAccountKey(attempt LoginAttempt) string {
	if attempt.AccountID != "" {
		return "account-id|" + normalizeLoginKeyPart(attempt.TenantCode) + "|" + attempt.AccountID
	}
	return LoginAccountFailureKey(attempt.TenantCode, attempt.Identifier)
}

type LoginFailureLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	entries map[string]loginFailureEntry
}

type loginFailureEntry struct {
	Count        int
	FirstFailure time.Time
	BlockedUntil time.Time
}

func NewLoginFailureLimiter(limit int, window time.Duration) *LoginFailureLimiter {
	if window <= 0 {
		window = 15 * time.Minute
	}
	return &LoginFailureLimiter{
		limit:   limit,
		window:  window,
		entries: map[string]loginFailureEntry{},
	}
}

func LoginFailureKey(tenantCode string, username string, ipAddress string) string {
	return LoginPairFailureKey(tenantCode, username, ipAddress)
}

func LoginAccountFailureKey(tenantCode string, identifier string) string {
	return "account|" + normalizeLoginKeyPart(tenantCode) + "|" + normalizeLoginIdentifier(identifier)
}

func LoginSourceFailureKey(ipAddress string) string {
	return "source|" + normalizeLoginSource(ipAddress)
}

func LoginPairFailureKey(tenantCode string, identifier string, ipAddress string) string {
	return "pair|" + normalizeLoginKeyPart(tenantCode) + "|" + normalizeLoginIdentifier(identifier) + "|" + normalizeLoginSource(ipAddress)
}

func normalizeLoginKeyPart(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeLoginIdentifier(value string) string {
	if phone, err := NormalizePhone(value); err == nil {
		return phone
	}
	return normalizeLoginKeyPart(value)
}

func normalizeLoginSource(value string) string {
	value = strings.TrimSpace(value)
	if ip := net.ParseIP(value); ip != nil {
		return ip.String()
	}
	return value
}

func (l *LoginFailureLimiter) IsBlocked(_ context.Context, key string, now time.Time) (time.Duration, bool) {
	if l == nil || l.limit <= 0 {
		return 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if !ok {
		return 0, false
	}
	if entry.BlockedUntil.After(now) {
		return entry.BlockedUntil.Sub(now), true
	}
	if !entry.BlockedUntil.IsZero() || now.Sub(entry.FirstFailure) > l.window {
		delete(l.entries, key)
	}
	return 0, false
}

func (l *LoginFailureLimiter) RegisterFailure(_ context.Context, key string, now time.Time) (int, time.Duration, bool) {
	if l == nil || l.limit <= 0 {
		return 0, 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[key]
	if entry.FirstFailure.IsZero() || now.Sub(entry.FirstFailure) > l.window {
		entry = loginFailureEntry{FirstFailure: now}
	}
	entry.Count++
	blocked := entry.Count >= l.limit
	var retryAfter time.Duration
	if blocked {
		entry.BlockedUntil = now.Add(l.window)
		retryAfter = l.window
	}
	l.entries[key] = entry
	return entry.Count, retryAfter, blocked
}

func (l *LoginFailureLimiter) Clear(_ context.Context, key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}
