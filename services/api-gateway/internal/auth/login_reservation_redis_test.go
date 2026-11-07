package auth

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func loginReservationRedis(t *testing.T) (*redis.Client, string) {
	t.Helper()
	address := strings.TrimSpace(os.Getenv("EDUGRADE_REDIS_TEST_ADDRESS"))
	if address == "" {
		t.Skip("EDUGRADE_REDIS_TEST_ADDRESS is required for real Redis reservation tests")
	}
	client := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("configured Redis test dependency is unavailable: %v", err)
	}
	return client, "synthetic-login-reservation-" + uuid.NewString()
}

func cleanupLoginReservationRedisKey(t *testing.T, client *redis.Client, raw string) {
	t.Helper()
	key := (&RedisLoginFailureLimiter{}).redisKey(raw)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := client.Del(ctx, key).Err(); err != nil {
			t.Errorf("cleanup isolated Redis test key: %v", err)
		}
	})
}

func requireRedisReservationCount(t *testing.T, client *redis.Client, raw string, want int64) {
	t.Helper()
	values, err := redisReadScript.Run(context.Background(), client, []string{(&RedisLoginFailureLimiter{}).redisKey(raw)}).Int64Slice()
	if err != nil || len(values) != 2 || values[0] != want {
		t.Fatalf("Redis reservation count=%v want=%d err=%v", values, want, err)
	}
}

func TestRedisLoginReservationBoundsTwoIndependentGuards(t *testing.T) {
	for _, sourceAttack := range []bool{false, true} {
		t.Run(fmt.Sprint(sourceAttack), func(t *testing.T) {
			client, tenant := loginReservationRedis(t)
			guards := []*LayeredLoginAttemptGuard{
				NewRedisLoginAttemptGuard(client, 5, time.Minute),
				NewRedisLoginAttemptGuard(client, 5, time.Minute),
			}
			// 文档保留网段加随机后缀隔离来源桶，不与其他测试或本机真实来源共享失败记录。
			hex := strings.ReplaceAll(uuid.NewString(), "-", "")
			ip := fmt.Sprintf("2001:db8:%s:%s:%s:%s:%s:%s", hex[:4], hex[4:8], hex[8:12], hex[12:16], hex[16:20], hex[20:24])
			attempts := make([]LoginAttempt, 96)
			keys := map[string]bool{}
			for index := range attempts {
				attempts[index] = LoginAttempt{TenantCode: tenant, Identifier: "teacher", AccountID: "user", IPAddress: ip}
				if sourceAttack {
					attempts[index].Identifier, attempts[index].AccountID = fmt.Sprint(index), ""
				}
				for _, bucket := range guards[0].buckets(attempts[index]) {
					keys[bucket.key] = true
				}
			}
			for key := range keys {
				cleanupLoginReservationRedisKey(t, client, key)
			}
			start := make(chan struct{})
			var accepted atomic.Int32
			var group sync.WaitGroup
			for index := range attempts {
				group.Go(func() {
					<-start
					guard := guards[index%len(guards)]
					_, blocked := guard.Reserve(context.Background(), &attempts[index], time.Now())
					if guard.Degraded() {
						t.Error("real Redis reservation fell back to local memory")
					}
					if !blocked {
						accepted.Add(1)
						for _, reserved := range attempts[index].reservation.buckets {
							if reserved.receipt.fallback {
								t.Error("accepted reservation did not come from Redis")
							}
						}
					}
				})
			}
			close(start)
			group.Wait()
			want := int32(5)
			if sourceAttack {
				want = 50
			}
			if got := accepted.Load(); got != want {
				t.Fatalf("two-guard concurrent password quota=%d want=%d", got, want)
			}
		})
	}
}

func TestRedisLoginReservationLateRefundIgnoresReplacedWindow(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(fmt.Sprint(expire), func(t *testing.T) {
			client, key := loginReservationRedis(t)
			cleanupLoginReservationRedisKey(t, client, key)
			limiter := NewRedisLoginFailureLimiter(client, 5, time.Minute)
			ctx := context.Background()
			old, _, blocked := limiter.Reserve(ctx, key, time.Now())
			if blocked || old.fallback {
				t.Fatal("initial Redis reservation failed")
			}
			if expire {
				if err := client.PExpire(ctx, limiter.redisKey(key), 10*time.Millisecond).Err(); err != nil {
					t.Fatal(err)
				}
				time.Sleep(20 * time.Millisecond)
			} else {
				limiter.Clear(ctx, key)
			}
			current, _, blocked := limiter.Reserve(ctx, key, time.Now())
			if blocked || current.fallback || current.generation == old.generation {
				t.Fatal("replacement window did not get a distinct Redis generation")
			}
			limiter.Release(ctx, key, old)
			requireRedisReservationCount(t, client, key, 1)
			limiter.Release(ctx, key, current)
			requireRedisReservationCount(t, client, key, 0)
		})
	}
}

func TestRedisLoginReservationRetainsLegacyCountAndTTL(t *testing.T) {
	client, key := loginReservationRedis(t)
	cleanupLoginReservationRedisKey(t, client, key)
	limiter := NewRedisLoginFailureLimiter(client, 3, time.Minute)
	ctx := context.Background()
	if err := client.Set(ctx, limiter.redisKey(key), "2", 30*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	before, err := client.PTTL(ctx, limiter.redisKey(key)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if _, blocked := limiter.IsBlocked(ctx, key, time.Now()); blocked {
		t.Fatal("legacy count below limit unexpectedly blocked")
	}
	receipt, _, blocked := limiter.Reserve(ctx, key, time.Now())
	if blocked || receipt.fallback {
		t.Fatal("legacy final slot was not available")
	}
	requireRedisReservationCount(t, client, key, 3)
	after, err := client.PTTL(ctx, limiter.redisKey(key)).Result()
	if err != nil || after <= 0 || after > before {
		t.Fatalf("legacy TTL reset: before=%v after=%v err=%v", before, after, err)
	}
	if _, retry, blocked := limiter.Reserve(ctx, key, time.Now()); !blocked || retry <= 0 {
		t.Fatal("legacy history was erased during migration")
	}
	limiter.Release(ctx, key, receipt)
	requireRedisReservationCount(t, client, key, 2)
	if count, _, blocked := limiter.RegisterFailure(ctx, key, time.Now()); count != 3 || !blocked {
		t.Fatalf("hash failure count=%d blocked=%v", count, blocked)
	}
	if _, blocked := limiter.IsBlocked(ctx, key, time.Now()); !blocked {
		t.Fatal("hash window was not readable")
	}
}

func TestRedisLoginReservationRefundStaysOnItsOriginalBackend(t *testing.T) {
	client, key := loginReservationRedis(t)
	cleanupLoginReservationRedisKey(t, client, key)
	limiter := NewRedisLoginFailureLimiter(client, 5, time.Minute)
	ctx, now := context.Background(), time.Now()
	limiter.fallback.RegisterFailure(ctx, key, now)
	healthy, _, blocked := limiter.Reserve(ctx, key, now)
	if blocked || healthy.fallback {
		t.Fatal("healthy reservation did not use Redis")
	}
	limiter.Release(ctx, key, healthy)
	if got := limiter.fallback.entries[key].Count; got != 1 {
		t.Fatalf("Redis refund erased fallback history: %d", got)
	}

	// 关闭独立客户端来确定地触发退化；不依赖网络时序，也不关闭其他测试使用的健康连接。
	closedClient := redis.NewClient(&redis.Options{Addr: client.Options().Addr, MaxRetries: -1})
	_ = closedClient.Close()
	limiter.client = closedClient
	local, _, blocked := limiter.Reserve(ctx, key, now)
	if blocked || !local.fallback {
		t.Fatal("failed Redis call did not retain fallback receipt")
	}
	limiter.client = client
	if count, _, _ := limiter.RegisterFailure(ctx, key, now); count != 1 {
		t.Fatalf("new Redis history=%d", count)
	}
	limiter.Release(ctx, key, local)
	requireRedisReservationCount(t, client, key, 1)
	if got := limiter.fallback.entries[key].Count; got != 1 {
		t.Fatalf("fallback refund lost its own history: %d", got)
	}
}
