package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type RedisLoginFailureLimiter struct {
	client redis.UniversalClient
	limit  int
	window time.Duration
	// 本机 fallback 仅维持退化期间的局部限流；是否拒绝登录由上层结合 Degraded 决定。
	fallback *LoginFailureLimiter
	health   *redisLoginLimiterHealth
}

type redisLoginLimiterHealth struct{ degraded atomic.Bool }

func NewRedisLoginFailureLimiter(client redis.UniversalClient, limit int, window time.Duration) *RedisLoginFailureLimiter {
	return newRedisLoginFailureLimiter(client, limit, window, &redisLoginLimiterHealth{})
}

func newRedisLoginFailureLimiter(client redis.UniversalClient, limit int, window time.Duration, health *redisLoginLimiterHealth) *RedisLoginFailureLimiter {
	if window <= 0 {
		window = 15 * time.Minute
	}
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

// 探测限流器需要的读取、脚本执行及写入、删除权限。使用有过期时间的非用户键，使 Redis 恢复后就绪状态可自行恢复，无需等待流量经过未就绪实例。
func probeRedisLoginLimiter(ctx context.Context, client redis.UniversalClient) error {
	if client == nil {
		return errors.New("distributed authentication rate limiter is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	key := "edugrade:auth:limiter-health"
	values, err := redisFailureScript.Run(ctx, client, []string{key}, 1000, uuid.NewString()).Int64Slice()
	if err != nil || len(values) != 2 {
		return errors.New("distributed authentication rate limiter write probe failed")
	}
	if _, err := redisReadScript.Run(ctx, client, []string{key}).Int64Slice(); err != nil {
		return errors.New("distributed authentication rate limiter read probe failed")
	}
	if err := client.Del(ctx, key).Err(); err != nil {
		return errors.New("distributed authentication rate limiter cleanup probe failed")
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
	values, err := redisReadScript.Run(ctx, l.client, []string{l.redisKey(key)}).Int64Slice()
	if err != nil || len(values) != 2 {
		l.markDegraded()
		return l.fallback.IsBlocked(ctx, key, now)
	}
	l.markHealthy()
	count, ttl := values[0], time.Duration(values[1])*time.Millisecond
	if count < int64(l.limit) || ttl <= 0 {
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
	result, err := redisFailureScript.Run(ctx, l.client, []string{l.redisKey(key)}, l.window.Milliseconds(), uuid.NewString()).Int64Slice()
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
	// 备用存储可能包含先前 Redis 故障期间记录的失败。即使 Redis 已恢复也要清除它，避免后续故障在成功登录后重新带出过时锁定状态。
	l.fallback.Clear(ctx, key)
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if err := l.client.Del(ctx, l.redisKey(key)).Err(); err != nil {
		l.markDegraded()
		return
	}
	l.markHealthy()
}

func (l *RedisLoginFailureLimiter) Reserve(ctx context.Context, key string, now time.Time) (loginReservationReceipt, time.Duration, bool) {
	if l == nil || l.client == nil || l.limit <= 0 {
		l.markDegraded()
		return loginReservationReceipt{}, 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	values, err := redisReserveScript.Run(ctx, l.client, []string{l.redisKey(key)}, l.limit, l.window.Milliseconds(), uuid.NewString()).Slice()
	var allowed, ttl int64
	var generation string
	valid := err == nil && len(values) == 3
	if valid {
		var allowedOK, ttlOK, generationOK bool
		allowed, allowedOK = values[0].(int64)
		ttl, ttlOK = values[1].(int64)
		generation, generationOK = values[2].(string)
		valid = allowedOK && ttlOK && generationOK && generation != "" && (allowed == 0 || allowed == 1)
	}
	if !valid {
		l.markDegraded()
		receipt, retry, blocked := l.fallback.Reserve(ctx, key, now)
		receipt.fallback = true
		return receipt, retry, blocked
	}
	l.markHealthy()
	if allowed == 0 {
		return loginReservationReceipt{}, time.Duration(max(ttl, 1)) * time.Millisecond, true
	}
	return loginReservationReceipt{generation: generation}, 0, false
}

func (l *RedisLoginFailureLimiter) Release(ctx context.Context, key string, receipt loginReservationReceipt) {
	if l == nil || receipt.generation == "" {
		return
	}
	// 预占可能来自退化存储；健康 Redis 的退款不能扣掉 fallback 中其他失败历史，反之亦然。
	if receipt.fallback {
		l.fallback.Release(ctx, key, receipt)
		return
	}
	if l.client == nil {
		l.markDegraded()
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if err := redisReleaseScript.Run(ctx, l.client, []string{l.redisKey(key)}, receipt.generation).Err(); err != nil {
		l.markDegraded()
	}
}

// 旧版字符串计数在同一键上转为带代次的窗口；保留既有计数与剩余 TTL，兼容记录不等于清空历史。
// 所有脚本只操作一个键，可用于 Redis Cluster；同一部署须一并升级写入限流键的网关实例。
const redisLoginWindowHelpers = `
local function read_count(key)
  local kind = redis.call('TYPE', key).ok
  if kind == 'none' then return 0, kind end
  local raw
  if kind == 'string' then raw = redis.call('GET', key)
  elseif kind == 'hash' then raw = redis.call('HGET', key, 'count')
  else error('unsupported login limiter key type') end
  local count = tonumber(raw or '0')
  if not count then error('invalid login limiter count') end
  return count, kind
end
local function ensure_window(key, window_ms, proposed_generation)
  local count, kind = read_count(key)
  local ttl = redis.call('PTTL', key)
  local generation = kind == 'hash' and redis.call('HGET', key, 'generation') or false
  if not generation then
    if kind == 'string' then redis.call('DEL', key) end
    generation = proposed_generation
    redis.call('HSET', key, 'count', count, 'generation', generation)
    redis.call('PEXPIRE', key, ttl > 0 and ttl or window_ms)
  elseif ttl <= 0 then redis.call('PEXPIRE', key, window_ms) end
  return count, generation
end
`

// 预占和上限判断在 Redis 内一次完成；被拒请求不加计数，也不延长已有窗口。
var redisReserveScript = redis.NewScript(redisLoginWindowHelpers + `
local count, generation = ensure_window(KEYS[1], ARGV[2], ARGV[3])
if count >= tonumber(ARGV[1]) then return {0, redis.call('PTTL', KEYS[1]), generation} end
redis.call('HINCRBY', KEYS[1], 'count', 1)
return {1, redis.call('PTTL', KEYS[1]), generation}
`)

var redisReleaseScript = redis.NewScript(`
if redis.call('TYPE', KEYS[1]).ok ~= 'hash' then return 0 end
if redis.call('HGET', KEYS[1], 'generation') ~= ARGV[1] then return 0 end
local count = tonumber(redis.call('HGET', KEYS[1], 'count') or '0')
if count <= 1 then return redis.call('DEL', KEYS[1]) end
return redis.call('HINCRBY', KEYS[1], 'count', -1)
`)

var redisReadScript = redis.NewScript(redisLoginWindowHelpers + `
local count = read_count(KEYS[1])
return {count, redis.call('PTTL', KEYS[1])}
`)

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
var redisFailureScript = redis.NewScript(redisLoginWindowHelpers + `
ensure_window(KEYS[1], ARGV[1], ARGV[2])
local count = redis.call('HINCRBY', KEYS[1], 'count', 1)
local ttl = redis.call('PTTL', KEYS[1])
return {count, ttl}
`)
