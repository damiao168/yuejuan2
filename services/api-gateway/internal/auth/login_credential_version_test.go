package auth

import (
	"context"
	"testing"
	"time"
)

func TestLoginPasswordChangeDoesNotInheritPreviousCredentialLock(t *testing.T) {
	ctx := context.Background()
	store, service := newLoginServiceFixture(t)
	command := validLoginCommand()
	command.Password = "synthetic incorrect password"
	for index := range 5 {
		_, err := service.Login(ctx, command)
		if index < 4 {
			requireLoginFailure(t, err, LoginFailureInvalidCredentials)
		} else {
			requireLoginFailure(t, err, LoginFailureRateLimited)
		}
	}
	_, err := service.Login(ctx, validLoginCommand())
	requireLoginFailure(t, err, LoginFailureRateLimited)

	user, err := store.FindUserByLogin(ctx, "demo", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	const newPassword = "SyntheticReplacementPassword"
	newHash, err := HashPassword(newPassword)
	if err != nil {
		t.Fatal(err)
	}
	updated, _, err := store.UpdatePasswordAndRevokeSessions(ctx, user.TenantID, user.ID, user.PasswordHash, newHash)
	if err != nil || !updated {
		t.Fatalf("password change failed: updated=%v err=%v", updated, err)
	}
	command.Password = newPassword
	if _, err := service.Login(ctx, command); err != nil {
		t.Fatalf("new credentials inherited the previous password's cooldown: %v", err)
	}
}

func TestLoginNewAccountDoesNotInheritUnresolvedIdentifierLock(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	guard := NewMemoryLoginAttemptGuard(2, time.Hour)
	unresolved := LoginAttempt{TenantCode: "school", Identifier: "20260001", IPAddress: "203.0.113.9"}
	guard.RegisterFailure(ctx, unresolved, now)
	guard.RegisterFailure(ctx, unresolved, now)
	if _, blocked := guard.Check(ctx, unresolved, now); !blocked {
		t.Fatal("unknown identifier must retain its scan cooldown")
	}
	resolved := unresolved
	resolved.AccountID = "new-student"
	resolved.CredentialVersion = "synthetic-version"
	if _, blocked := guard.Check(ctx, resolved, now); blocked {
		t.Fatal("newly provisioned account inherited failures from before it existed")
	}
	if _, blocked := guard.Reserve(ctx, &resolved, now); blocked {
		t.Fatal("newly provisioned account could not reserve a login attempt")
	}
	guard.RegisterSuccess(ctx, resolved)
	if _, blocked := guard.source.IsBlocked(ctx, LoginSourceFailureKey(unresolved.IPAddress), now); blocked {
		t.Fatal("this fixture should not exhaust the shared source quota")
	}
}

func TestLoginResolvedPairUsesCredentialVersionAcrossAliases(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	guard := NewLayeredLoginAttemptGuard(NewLoginFailureLimiter(100, time.Hour), nil, NewLoginFailureLimiter(2, time.Hour))
	attempt := LoginAttempt{TenantCode: "demo", Identifier: "teacher", AccountID: "teacher-1",
		CredentialVersion: "old-credential", IPAddress: "203.0.113.9"}
	guard.RegisterFailure(ctx, attempt, now)
	attempt.Identifier = "EMP-001"
	guard.RegisterFailure(ctx, attempt, now)
	if limit, blocked := guard.Check(ctx, attempt, now); !blocked || limit.Bucket != "pair" {
		t.Fatalf("aliases must share account/source failures: %#v blocked=%v", limit, blocked)
	}
	attempt.CredentialVersion = "new-credential"
	if _, blocked := guard.Check(ctx, attempt, now); blocked {
		t.Fatal("new credential version inherited an old account/source lock")
	}
	attempt.CredentialVersion = "old-credential"
	attempt.AccountID = "teacher-2"
	if _, blocked := guard.Check(ctx, attempt, now); blocked {
		t.Fatal("another account inherited the same identifier/source lock")
	}
}
