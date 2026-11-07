package auth

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type schoolLoginAliasCredentials struct {
	*MemoryStore
}

func (s schoolLoginAliasCredentials) FindUserByLogin(ctx context.Context, code, identifier string) (UserWithPassword, error) {
	if code == "S053" {
		code = "demo"
	}
	return s.MemoryStore.FindUserByLogin(ctx, code, identifier)
}

// 学校代码与租户代码解析到同一真实账号；切换入口不能获得第二份猜密额度。
// 每次使用不同来源，避免账号/来源组合桶碰巧拦截而掩盖账号桶分裂。
func TestLoginSchoolAndTenantCodesShareCanonicalAccountLimit(t *testing.T) {
	for _, firstCode := range []string{"S053", "demo"} {
		t.Run("first_"+firstCode, func(t *testing.T) {
			store, base := newLoginServiceFixture(t)
			credentials := schoolLoginAliasCredentials{MemoryStore: store}
			for _, code := range []string{"S053", "demo"} {
				user, err := credentials.FindUserByLogin(t.Context(), code, "teacher")
				if err != nil || user.ID != "user-1" || user.TenantCode != "demo" {
					t.Fatalf("alias %q did not resolve the canonical account: user=%q tenant=%q err=%v", code, user.ID, user.TenantCode, err)
				}
			}
			guard := NewMemoryLoginAttemptGuard(2, time.Hour)
			service := NewLoginService(credentials, store, store, store, guard, NewStoreRiskEvaluator(store, "shadow"), base.options)
			codes := []string{firstCode, "demo"}
			if firstCode == "demo" {
				codes[1] = "S053"
			}
			for attempt := range 4 {
				command := validLoginCommand()
				command.TenantCode = codes[attempt%len(codes)]
				command.Password = "synthetic-wrong-password"
				command.IPAddress = fmt.Sprintf("203.0.113.%d", attempt+10)
				_, err := service.Login(t.Context(), command)
				want := LoginFailureRateLimited
				if attempt == 0 {
					want = LoginFailureInvalidCredentials
				}
				requireLoginFailure(t, err, want)
			}
		})
	}
}
