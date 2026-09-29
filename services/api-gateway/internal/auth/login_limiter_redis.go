package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisLoginFailureLimiter struct {
	client   redis.UniversalClient
	limit    int
	window   time.Duration
	// 本机 fallback 仅维持退化期间的局部限流；是否拒绝登录由上层结合 Degraded 决定。
	fallback *LoginFailureLimiter
	health   *redisLoginLimiterHealth
}

type redisLoginLimiterHealth struct{ degraded atomic.Bool }

func NewRedisLoginFailureLimiter(client redis.UniversalClient, limit int, window time.Duration) *RedisLoginFailureLimiter {
	return newRedisLoginFailureLimiter(client, limit, window, &redisLoginLimiterHealth{})
}

func newRedisLoginFailureLimiter(client redis.UniversalClient, limit int, window time.Duration, health *redisLoginLimiterHealth) *RedisLoginFailureLimiter {
	return &RedisLoginFailureLimiter{client: client, limit: limit, window: window, fallback: NewLoginFailureLimiter(limit, window), health: health}
}

func NewRedisLoginAttemptGuard(client redis.UniversalClient, limit int, window time.Duration) *LayeredLoginAttemptGuard {
	if limit <= 0 {
		limit = 5
	}
	sourceLimit := limit * 10
	if sourceLimit < 50 {
		sourceLimit = 50
	}
	account := NewRedisLoginFailureLimiter(client, limit, window)
	source := NewRedisLoginFailureLimiter(client, sourceLimit, window)
	pair := NewRedisLoginFailureLimiter(client, limit, window)
	guard := NewLayeredLoginAttemptGuard(account, source, pair)
	guard.degraded = func() bool {
		return account.health.degraded.Load() || source.health.degraded.Load() || pair.health.degraded.Load()
	}
	guard.probe = func(ctx context.Context) error {
		err := probeRedisLoginLimiter(ctx, client)
		for _, limiter := range []*RedisLoginFailureLimiter{account, source, pair} {
			if err != nil {
				limiter.markDegraded()
			} else {
				limiter.markHealthy()
			}
		}
		return err
	}
	return guard
}

// Probe the read, script/write and delete permissions used by the limiter.
// An expiring non-user key also lets readiness recover after Redis returns,
// without waiting for traffic through an unready instance.
func probeRedisLoginLimiter(ctx context.Context, client redis.UniversalClient) error {
	if client == nil {
		return errors.New("distributed authentication rate limiter is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	key := "edugrade:auth:limiter-health"
	values, err := redisFailureScript.Run(ctx, client, []string{key}, 1000).Int64Slice()
	if err != nil || len(values) != 2 {
		return errors.New("distributed authentication rate limiter write probe failed")
	}
	if _, err := client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Get(ctx, key)
		pipe.PTTL(ctx, key)
		pipe.Del(ctx, key)
		return nil
	}); err != nil && !errors.Is(err, redis.Nil) {
		return errors.New("distributed authentication rate limiter read probe failed")
	}
	return nil
}

func (l *RedisLoginFailureLimiter) IsBlocked(ctx context.Context, key string, now time.Time) (time.Duration, bool) {
	if l == nil || l.client == nil || l.limit <= 0 {
		l.markDegraded()
		return 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	values, err := l.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Get(ctx, l.redisKey(key))
		pipe.PTTL(ctx, l.redisKey(key))
		return nil
	})
	if err != nil && err != redis.Nil {
		l.markDegraded()
		return l.fallback.IsBlocked(ctx, key, now)
	}
	if len(values) != 2 {
		l.markDegraded()
		return l.fallback.IsBlocked(ctx, key, now)
	}
	l.markHealthy()
	count, countErr := values[0].(*redis.StringCmd).Int()
	ttl, ttlErr := values[1].(*redis.DurationCmd).Result()
	if countErr != nil || ttlErr != nil || count < l.limit || ttl <= 0 {
		return 0, false
	}
	return ttl, true
}

func (l *RedisLoginFailureLimiter) RegisterFailure(ctx context.Context, key string, now time.Time) (int, time.Duration, bool) {
	if l == nil || l.client == nil || l.limit <= 0 {
		l.markDegraded()
		return 0, 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	result, err := redisFailureScript.Run(ctx, l.client, []string{l.redisKey(key)}, l.window.Milliseconds()).Int64Slice()
	if err != nil || len(result) != 2 {
		l.markDegraded()
		return l.fallback.RegisterFailure(ctx, key, now)
	}
	l.markHealthy()
	count := int(result[0])
	retry := time.Duration(result[1]) * time.Millisecond
	return count, retry, count >= l.limit
}

func (l *RedisLoginFailureLimiter) Clear(ctx context.Context, key string) {
	if l == nil || l.client == nil {
		return
	}
	// The fallback may contain failures recorded during an earlier Redis
	// outage. Clear it even when Redis has recovered so a later outage cannot
	// resurrect stale lockout state after a successful login.
	l.fallback.Clear(ctx, key)
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if err := l.client.Del(ctx, l.redisKey(key)).Err(); err != nil {
		l.markDegraded()
		return
	}
	l.markHealthy()
}

func (l *RedisLoginFailureLimiter) markDegraded() {
	if l != nil && l.health != nil {
		l.health.degraded.Store(true)
	}
}

func (l *RedisLoginFailureLimiter) markHealthy() {
	if l != nil && l.health != nil {
		l.health.degraded.Store(false)
	}
}

func (l *RedisLoginFailureLimiter) redisKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "edugrade:auth:login-fail:" + hex.EncodeToString(sum[:])
}

// 计数和首次设置 TTL 必须原子执行；后续失败不重置 Redis 窗口的起点。
var redisFailureScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
local ttl = redis.call('PTTL', KEYS[1])
return {count, ttl}
`)
