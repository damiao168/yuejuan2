package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoginReservationBoundsConcurrentAccountAndSourceAttempts(t *testing.T) {
	for _, sourceAttack := range []bool{false, true} {
		t.Run(fmt.Sprint(sourceAttack), func(t *testing.T) {
			guard := NewLayeredLoginAttemptGuard(NewLoginFailureLimiter(5, time.Minute), NewLoginFailureLimiter(7, time.Minute), NewLoginFailureLimiter(5, time.Minute))
			start := make(chan struct{})
			var accepted atomic.Int32
			var group sync.WaitGroup
			for index := range 64 {
				group.Go(func() {
					<-start
					attempt := LoginAttempt{TenantCode: "synthetic", Identifier: "teacher", AccountID: "user", IPAddress: "203.0.113.8"}
					if sourceAttack {
						// 来源桶限制不存在账号的扫描；已解析账号不能因同一 NAT 下的其他账号失败互锁。
						attempt.Identifier, attempt.AccountID = fmt.Sprint(index), ""
					}
					if _, blocked := guard.Reserve(context.Background(), &attempt, time.Now()); !blocked {
						accepted.Add(1)
					}
				})
			}
			close(start)
			group.Wait()
			want := int32(5)
			if sourceAttack {
				want = 7
			}
			if accepted.Load() != want {
				t.Fatalf("concurrent password checks=%d want=%d", accepted.Load(), want)
			}
		})
	}
}

func TestLoginReservationRefundIgnoresReplacedWindows(t *testing.T) {
	for _, clear := range []bool{false, true} {
		t.Run(fmt.Sprint(clear), func(t *testing.T) {
			limiter := NewLoginFailureLimiter(5, time.Minute)
			ctx, now := context.Background(), time.Now()
			old, _, blocked := limiter.Reserve(ctx, "synthetic-window", now)
			if blocked {
				t.Fatal("initial reservation blocked")
			}
			nextTime := now.Add(time.Minute)
			if clear {
				limiter.Clear(ctx, "synthetic-window")
				nextTime = now // 即使清除后在同一时刻重建窗口，代次也必须不同。
			}
			current, _, blocked := limiter.Reserve(ctx, "synthetic-window", nextTime)
			if blocked {
				t.Fatal("new window blocked")
			}
			limiter.Release(ctx, "synthetic-window", old)
			if got := limiter.entries["synthetic-window"].Count; got != 1 {
				t.Fatalf("late refund consumed another window: %d", got)
			}
			limiter.Release(ctx, "synthetic-window", current)
			if len(limiter.entries) != 0 {
				t.Fatal("current reservation was not refunded")
			}
		})
	}
}

// 只暴露旧接口，模拟通过 HandlerOptions.LoginLimiter 传入的兼容实现。
type legacyReservationTestLimiter struct{ LoginLimiter }

func TestLoginReservationStillCountsLegacyBuckets(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	account, source, pair := NewLoginFailureLimiter(5, time.Minute), NewLoginFailureLimiter(5, time.Minute), NewLoginFailureLimiter(5, time.Minute)
	guard := NewLayeredLoginAttemptGuard(account, source, legacyReservationTestLimiter{pair})
	attempt := LoginAttempt{TenantCode: "synthetic", Identifier: "teacher", AccountID: "user", IPAddress: "203.0.113.17"}
	if _, blocked := guard.Reserve(ctx, &attempt, now); blocked {
		t.Fatal("initial reservation blocked")
	}
	guard.RegisterFailure(ctx, attempt, now)
	for _, bucket := range guard.buckets(attempt) {
		limiter := map[string]*LoginFailureLimiter{"account": account, "source": source, "pair": pair}[bucket.name]
		if got := limiter.entries[bucket.key].Count; got != 1 {
			t.Errorf("%s counted %d times, want exactly one", bucket.name, got)
		}
	}
}

type contextCheckingReservationLimiter struct {
	*LoginFailureLimiter
	releaseError error
}

func (l *contextCheckingReservationLimiter) Release(ctx context.Context, key string, receipt loginReservationReceipt) {
	l.releaseError = ctx.Err()
	l.LoginFailureLimiter.Release(ctx, key, receipt)
}

func TestLoginReservationRefundIsOnceAndSurvivesCallerCancellation(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	source := &contextCheckingReservationLimiter{LoginFailureLimiter: NewLoginFailureLimiter(5, time.Minute)}
	guard := NewLayeredLoginAttemptGuard(NewLoginFailureLimiter(5, time.Minute), source, NewLoginFailureLimiter(5, time.Minute))
	attempt := LoginAttempt{TenantCode: "synthetic", Identifier: "teacher", IPAddress: "203.0.113.19"}
	source.RegisterFailure(ctx, LoginSourceFailureKey(attempt.IPAddress), now)
	if _, blocked := guard.Reserve(ctx, &attempt, now); blocked {
		t.Fatal("initial reservation blocked")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	guard.ReleaseReservation(cancelled, attempt)
	guard.ReleaseReservation(cancelled, attempt)
	if source.releaseError != nil {
		t.Fatalf("refund reused cancelled context: %v", source.releaseError)
	}
	if got := source.entries[LoginSourceFailureKey(attempt.IPAddress)].Count; got != 1 {
		t.Fatalf("refund erased other failure or ran twice: %d", got)
	}
}

func TestLoginReservationDeniedBucketRollsBackOnlyOwnSlots(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	account, source, pair := NewLoginFailureLimiter(5, time.Minute), NewLoginFailureLimiter(1, time.Minute), NewLoginFailureLimiter(5, time.Minute)
	guard := NewLayeredLoginAttemptGuard(account, source, pair)
	attempt := LoginAttempt{TenantCode: "synthetic", Identifier: "teacher", IPAddress: "203.0.113.21"}
	source.RegisterFailure(ctx, LoginSourceFailureKey(attempt.IPAddress), now)
	if limit, blocked := guard.Reserve(ctx, &attempt, now); !blocked || limit.Bucket != "source" {
		t.Fatalf("expected source rejection, got %#v/%v", limit, blocked)
	}
	if len(account.entries) != 0 || len(pair.entries) != 0 {
		t.Fatal("rejection retained partial reservations")
	}
	if got := source.entries[LoginSourceFailureKey(attempt.IPAddress)].Count; got != 1 {
		t.Fatalf("rejection changed source history: %d", got)
	}
}

func TestLoginReservationCorrectPasswordDoesNotConsumeQuotaOnSessionFailure(t *testing.T) {
	store, _ := newLoginServiceFixture(t)
	guard := NewMemoryLoginAttemptGuard(2, time.Minute)
	command := validLoginCommand()
	user, err := store.FindUserByLogin(context.Background(), command.TenantCode, command.Identifier)
	if err != nil {
		t.Fatal(err)
	}
	history := LoginAttempt{TenantCode: command.TenantCode, Identifier: command.Identifier, AccountID: "user-1",
		CredentialVersion: HashToken(user.PasswordHash), IPAddress: command.IPAddress}
	guard.RegisterFailure(context.Background(), history, time.Now())
	sessions := failingSessionRepository{SessionRepository: store, err: errors.New("synthetic session outage")}
	service := newLoginServiceForTest(store, sessions, guard, NewStoreRiskEvaluator(store, "shadow"), false)
	for range 5 {
		_, err := service.Login(context.Background(), command)
		requireLoginFailure(t, err, LoginFailureSessionCreate)
	}
	for _, bucket := range guard.buckets(history) {
		if got := bucket.limiter.(*LoginFailureLimiter).entries[bucket.key].Count; got != 1 {
			t.Errorf("%s retained a correct-password reservation or erased history: %d", bucket.name, got)
		}
	}
}

type degradeAfterReservationGuard struct {
	*LayeredLoginAttemptGuard
	degraded bool
}

func (g *degradeAfterReservationGuard) Reserve(ctx context.Context, attempt *LoginAttempt, now time.Time) (LoginLimit, bool) {
	limit, blocked := g.LayeredLoginAttemptGuard.Reserve(ctx, attempt, now)
	g.degraded = true
	return limit, blocked
}
func (g *degradeAfterReservationGuard) Degraded() bool { return g.degraded }

func TestLoginReservationDegradedDependencyReturnsKnownSlots(t *testing.T) {
	store, _ := newLoginServiceFixture(t)
	guard := &degradeAfterReservationGuard{LayeredLoginAttemptGuard: NewMemoryLoginAttemptGuard(2, time.Minute)}
	service := newLoginServiceForTest(store, store, guard, NewStoreRiskEvaluator(store, "shadow"), true)
	_, err := service.Login(context.Background(), validLoginCommand())
	requireLoginFailure(t, err, LoginFailureLimiterUnavailable)
	for _, bucket := range guard.buckets(LoginAttempt{}) {
		if len(bucket.limiter.(*LoginFailureLimiter).entries) != 0 {
			t.Errorf("%s retained a reservation after dependency rejection", bucket.name)
		}
	}
}

func TestLoginReservationSuccessRefundsOnlyOwnSourceSlot(t *testing.T) {
	guard := NewMemoryLoginAttemptGuard(5, time.Minute)
	ctx, now := context.Background(), time.Now()
	failed := LoginAttempt{TenantCode: "test", Identifier: "failed", IPAddress: "203.0.113.8"}
	guard.RegisterFailure(ctx, failed, now)
	for index := range 70 {
		attempt := LoginAttempt{TenantCode: "test", Identifier: fmt.Sprint(index), IPAddress: failed.IPAddress}
		if _, blocked := guard.Reserve(ctx, &attempt, now); blocked {
			t.Fatal("successful logins exhausted source failure quota")
		}
		guard.ReleaseReservation(ctx, attempt)
		guard.RegisterSuccess(ctx, attempt)
	}
	source := guard.source.(*LoginFailureLimiter)
	if got := source.entries[LoginSourceFailureKey(failed.IPAddress)].Count; got != 1 {
		t.Fatalf("success erased other failure or retained own slot: %d", got)
	}
}
