package auth

import (
	"context"
	"net"
	"strconv"
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
	// CredentialVersion 是服务端密码哈希的指纹；改密后使用新桶，旧密码的失败不能继续锁住新凭据。
	CredentialVersion string
	reservation       *loginAttemptReservation
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

type loginReservationLimiter interface {
	Reserve(context.Context, string, time.Time) (loginReservationReceipt, time.Duration, bool)
	Release(context.Context, string, loginReservationReceipt)
}

type loginReservationGuard interface {
	Reserve(context.Context, *LoginAttempt, time.Time) (LoginLimit, bool)
	ReleaseReservation(context.Context, LoginAttempt)
}

type loginReservationReceipt struct {
	generation string
	fallback   bool
}

type reservedLoginBucket struct {
	loginBucket
	receipt loginReservationReceipt
}

type loginAttemptReservation struct {
	buckets []reservedLoginBucket
	release sync.Once
}

func (r *loginAttemptReservation) contains(bucket loginBucket) bool {
	if r != nil {
		for _, reserved := range r.buckets {
			if reserved.name == bucket.name && reserved.key == bucket.key {
				return true
			}
		}
	}
	return false
}

// Reserve 在校验密码前占用名额。每个请求使用独立 attempt，收据记录窗口及实际存储后端；
// 拒绝时只撤销本次已取得的其他桶名额，不能扣除窗口更替后其他请求的计数。
func (l *LayeredLoginAttemptGuard) Reserve(ctx context.Context, attempt *LoginAttempt, now time.Time) (LoginLimit, bool) {
	attempt.reservation = nil
	reservation := &loginAttemptReservation{}
	for _, bucket := range l.buckets(*attempt) {
		if bucket.limiter == nil {
			continue
		}
		limiter, ok := bucket.limiter.(loginReservationLimiter)
		if !ok {
			continue
		}
		receipt, retry, blocked := limiter.Reserve(ctx, bucket.key, now)
		if blocked {
			l.releaseReservation(ctx, reservation)
			return LoginLimit{Bucket: bucket.name, RetryAfter: retry}, true
		}
		reservation.buckets = append(reservation.buckets, reservedLoginBucket{loginBucket: bucket, receipt: receipt})
	}
	attempt.reservation = reservation
	return LoginLimit{}, false
}

// ReleaseReservation 只退还本次名额；正确密码或依赖故障不应消耗失败额度。
// 完整登录成功后的历史清理由 RegisterSuccess 处理，来源桶始终保留别人的失败。
func (l *LayeredLoginAttemptGuard) ReleaseReservation(ctx context.Context, attempt LoginAttempt) {
	l.releaseReservation(ctx, attempt.reservation)
}

func (l *LayeredLoginAttemptGuard) releaseReservation(ctx context.Context, reservation *loginAttemptReservation) {
	if reservation == nil {
		return
	}
	reservation.release.Do(func() {
		// 客户端取消后仍尝试归还已知预占；清理仍有短时限，不占用无限后台请求。
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		for _, bucket := range reservation.buckets {
			bucket.limiter.(loginReservationLimiter).Release(cleanup, bucket.key, bucket.receipt)
		}
	})
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
		var retryAfter time.Duration
		var currentBlocked bool
		if attempt.reservation.contains(bucket) {
			// 已预占的桶把本次名额保留为失败；旧自定义桶仍须在此登记，不能因混用而漏计。
			retryAfter, currentBlocked = bucket.limiter.IsBlocked(ctx, bucket.key, now)
		} else {
			_, retryAfter, currentBlocked = bucket.limiter.RegisterFailure(ctx, bucket.key, now)
		}
		if currentBlocked && (!blocked || retryAfter > result.RetryAfter) {
			result = LoginLimit{Bucket: bucket.name, RetryAfter: retryAfter}
			blocked = true
		}
	}
	return result, blocked
}

func (l *LayeredLoginAttemptGuard) RegisterSuccess(ctx context.Context, attempt LoginAttempt) {
	l.ReleaseReservation(ctx, attempt)
	// 有效凭据证明调用方控制该账户及账户与来源 IP 的组合；登录成功不能清除同一来源 IP 下其他账户的历史。
	if l.account != nil {
		l.account.Clear(ctx, loginAttemptAccountKey(attempt))
		if attempt.AccountID != "" {
			l.account.Clear(ctx, LoginAccountFailureKey(attempt.TenantCode, attempt.Identifier))
		}
	}
	if l.pair != nil {
		l.pair.Clear(ctx, loginAttemptPairKey(attempt))
	}
}

type loginBucket struct {
	name    string
	key     string
	limiter LoginLimiter
}

func (l *LayeredLoginAttemptGuard) buckets(attempt LoginAttempt) []loginBucket {
	buckets := []loginBucket{
		{name: "account", key: loginAttemptAccountKey(attempt), limiter: l.account},
		{name: "pair", key: loginAttemptPairKey(attempt), limiter: l.pair},
	}
	// NAT 或 Docker 转发可能让全校共享来源。已解析账号靠账号及组合桶防猜解，
	// 只有不存在账号的扫描共用来源桶，避免攻击者把其他人的正确登录一起锁住。
	if attempt.AccountID == "" {
		buckets = append(buckets, loginBucket{name: "source", key: LoginSourceFailureKey(attempt.IPAddress), limiter: l.source})
	}
	return buckets
}

func loginAttemptAccountKey(attempt LoginAttempt) string {
	if attempt.AccountID != "" {
		return "account-id|" + normalizeLoginKeyPart(attempt.TenantCode) + "|" + attempt.AccountID + "|credential|" + attempt.CredentialVersion
	}
	return LoginAccountFailureKey(attempt.TenantCode, attempt.Identifier)
}

func loginAttemptPairKey(attempt LoginAttempt) string {
	if attempt.AccountID != "" {
		// 已开通账号不沿用开通前按学号累计的未知账号失败；登录别名也共用稳定账号与来源的组合。
		return "pair-account|" + loginAttemptAccountKey(attempt) + "|" + normalizeLoginSource(attempt.IPAddress)
	}
	return LoginPairFailureKey(attempt.TenantCode, attempt.Identifier, attempt.IPAddress)
}

type LoginFailureLimiter struct {
	mu             sync.Mutex
	limit          int
	window         time.Duration
	entries        map[string]loginFailureEntry
	nextGeneration uint64
}

type loginFailureEntry struct {
	Count        int
	FirstFailure time.Time
	BlockedUntil time.Time
	Generation   string
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
		entry = l.newEntry(now)
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

func (l *LoginFailureLimiter) Reserve(_ context.Context, key string, now time.Time) (loginReservationReceipt, time.Duration, bool) {
	if l == nil || l.limit <= 0 {
		return loginReservationReceipt{}, 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[key]
	if entry.FirstFailure.IsZero() || !now.Before(entry.FirstFailure.Add(l.window)) {
		entry = l.newEntry(now)
	}
	if entry.Count >= l.limit || entry.BlockedUntil.After(now) {
		return loginReservationReceipt{}, entry.FirstFailure.Add(l.window).Sub(now), true
	}
	entry.Count++
	if entry.Count >= l.limit {
		entry.BlockedUntil = entry.FirstFailure.Add(l.window)
	}
	l.entries[key] = entry
	return loginReservationReceipt{generation: entry.Generation}, 0, false
}

func (l *LoginFailureLimiter) newEntry(now time.Time) loginFailureEntry {
	l.nextGeneration++
	return loginFailureEntry{FirstFailure: now, Generation: strconv.FormatUint(l.nextGeneration, 10)}
}

func (l *LoginFailureLimiter) Release(_ context.Context, key string, receipt loginReservationReceipt) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if !ok || receipt.generation == "" || entry.Generation != receipt.generation {
		return
	}
	entry.Count--
	if entry.Count <= 0 {
		delete(l.entries, key)
		return
	}
	if entry.Count < l.limit {
		entry.BlockedUntil = time.Time{}
	}
	l.entries[key] = entry
}
