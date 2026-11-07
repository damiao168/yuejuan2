package auth

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestUnknownAccountSourceFloodDoesNotLockKnownAccount(t *testing.T) {
	store, service := newLoginServiceFixture(t)
	guard := NewMemoryLoginAttemptGuard(5, time.Hour)
	service = newLoginServiceForTest(store, store, guard, NewStoreRiskEvaluator(store, "shadow"), false)
	command := validLoginCommand()
	ctx, now := t.Context(), time.Now().UTC()
	for index := range 55 {
		guard.RegisterFailure(ctx, LoginAttempt{TenantCode: command.TenantCode, Identifier: fmt.Sprint("nonexistent-", index), IPAddress: command.IPAddress}, now)
	}
	if limit, blocked := guard.Check(ctx, LoginAttempt{TenantCode: command.TenantCode, Identifier: "another-nonexistent", IPAddress: command.IPAddress}, now); !blocked || limit.Bucket != "source" {
		t.Fatalf("unknown account scan not throttled: %#v %v", limit, blocked)
	}
	result, err := service.Login(ctx, command)
	if err != nil || result.Token == "" {
		t.Fatalf("same-source correct credentials were locked out: %v", err)
	}
	// 正常登录不能清空探测者的失败历史，已知账号的失败仍受账号桶限制。
	known := LoginAttempt{TenantCode: command.TenantCode, Identifier: command.Identifier, IPAddress: command.IPAddress, AccountID: "user-1"}
	for range 5 {
		guard.RegisterFailure(context.Background(), known, now)
	}
	if _, blocked := guard.Check(ctx, known, now); !blocked {
		t.Fatal("known account lost its password-guessing limit")
	}
}

func TestKnownAccountFailuresDoNotLockOtherAccountsOnSameNAT(t *testing.T) {
	store, service := newLoginServiceFixture(t)
	ctx := t.Context()
	base, err := store.FindUserByLogin(ctx, "demo", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	// 每个被攻击账号只试一次，累计超过旧来源桶阈值；未被攻击教师的正确密码仍须成功。
	for index := range 55 {
		user := base
		user.ID, user.Username = fmt.Sprint("attacked-", index), fmt.Sprint("attacked-", index)
		store.AddUser(user)
		command := validLoginCommand()
		command.Identifier, command.Password = user.Username, "synthetic-wrong-password"
		requireLoginFailure(t, serviceLoginError(service, ctx, command), LoginFailureInvalidCredentials)
	}
	if result, err := service.Login(ctx, validLoginCommand()); err != nil || result.Token == "" {
		t.Fatalf("other accounts sharing NAT locked out an untouched teacher: %v", err)
	}
}

func serviceLoginError(service *LoginService, ctx context.Context, command LoginCommand) error {
	_, err := service.Login(ctx, command)
	return err
}
