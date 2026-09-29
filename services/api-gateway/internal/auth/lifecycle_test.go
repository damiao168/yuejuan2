package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func lifecycleFixture(t *testing.T) (*MemoryStore, User, AccessScope) {
	t.Helper()
	store := NewMemoryStore()
	actor := User{ID: "admin", TenantID: "tenant", TenantCode: "demo", Username: "admin", Status: "active", Roles: []string{"school_admin"}}
	store.AddUser(UserWithPassword{User: actor, SchoolID: "school-1", PasswordHash: "admin-hash"})
	store.AddRole(actor.TenantID, AssignableRole{Code: "teacher", ScopeType: "class"})
	return store, actor, AccessScope{TenantID: actor.TenantID, ActorID: actor.ID, SchoolIDs: []string{"school-1"}}
}

func TestInFlightReauthenticationCannotUndoNewerPublicComputerLock(t *testing.T) {
	store, actor, _ := lifecycleFixture(t)
	ctx := context.Background()
	startedAt := time.Now().UTC()
	tokenHash := HashToken("public-lock-race")
	if _, err := store.CreateSession(ctx, CreateSessionInput{
		TenantID: actor.TenantID, UserID: actor.ID, TokenHash: tokenHash, SessionType: SessionTypePublicDevice, ExpiresAt: startedAt.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	lockedAt := startedAt.Add(time.Millisecond)
	if locked, err := store.LockSession(ctx, actor.TenantID, actor.ID, tokenHash, lockedAt); err != nil || !locked {
		t.Fatalf("lock failed: locked=%v err=%v", locked, err)
	}
	if updated, err := store.MarkSessionReauthenticated(ctx, actor.TenantID, actor.ID, tokenHash, startedAt, lockedAt.Add(time.Millisecond)); err != nil || updated {
		t.Fatalf("in-flight verification must not undo a newer lock: updated=%v err=%v", updated, err)
	}
	if _, err := store.FindUserBySession(ctx, tokenHash, lockedAt.Add(time.Millisecond)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("newer lock must remain effective: %v", err)
	}
	if updated, err := store.MarkSessionReauthenticated(ctx, actor.TenantID, actor.ID, tokenHash, lockedAt.Add(2*time.Millisecond), lockedAt.Add(3*time.Millisecond)); err != nil || !updated {
		t.Fatalf("verification started after the lock must be able to unlock: updated=%v err=%v", updated, err)
	}
}

func TestActivationTokenExpiresAndConcurrentConsumptionHasOneWinner(t *testing.T) {
	store, actor, scope := lifecycleFixture(t)
	now := time.Now().UTC()
	input := CreateManagedUserInput{Username: "teacher", DisplayName: "Teacher", Phone: "+8613800138000", RoleCode: "teacher", SchoolID: "school-1", ActivationTokenHash: HashToken("expired"), ActivationExpiresAt: now.Add(-time.Minute)}
	user, err := store.CreateManagedUser(context.Background(), actor, scope, input, "!activation-required")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindActivation(context.Background(), input.ActivationTokenHash, now); !errors.Is(err, ErrActivationInvalid) {
		t.Fatalf("expired activation must fail: %v", err)
	}
	for _, status := range []string{"active", "disabled"} {
		if _, _, err := store.UpdateManagedUserStatus(context.Background(), actor, scope, user.ID, status); !errors.Is(err, ErrUserStatusForbidden) {
			t.Fatalf("invited account cannot bypass activation via status %s: %v", status, err)
		}
	}
	hash := HashToken("fresh")
	preview, err := store.CreateActivation(context.Background(), actor, scope, user.ID, hash, now.Add(time.Hour))
	if err != nil || preview.SchoolID != "school-1" {
		t.Fatalf("school admin must be able to reissue its teacher's invitation: preview=%#v err=%v", preview, err)
	}
	results := make(chan error, 8)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			_, err := store.ActivateUser(context.Background(), hash, "new-hash", now)
			results <- err
		})
	}
	group.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrActivationInvalid) {
			t.Fatalf("unexpected activation error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("single-use activation must have one concurrent winner, got %d", winners)
	}
}

func TestRecoveryTokenExpiresRevokesSessionsAndIsSingleUse(t *testing.T) {
	store, actor, scope := lifecycleFixture(t)
	now := time.Now().UTC()
	user, err := store.CreateManagedUser(context.Background(), actor, scope, CreateManagedUserInput{
		Username: "teacher", DisplayName: "Teacher", RoleCode: "teacher", SchoolID: "school-1",
	}, "old-hash")
	if err != nil {
		t.Fatal(err)
	}
	oldUser, err := store.FindUserByLogin(context.Background(), "demo", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"session-a", "session-b"} {
		if _, err := store.CreateSession(context.Background(), CreateSessionInput{
			TenantID: actor.TenantID, UserID: user.ID, TokenHash: HashToken(token), SessionType: SessionTypeStandard, ExpiresAt: now.Add(time.Hour), SecurityEpoch: oldUser.SecurityEpoch,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateRecovery(context.Background(), actor, scope, user.ID, HashToken("expired"), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindRecovery(context.Background(), HashToken("expired"), now); !errors.Is(err, ErrRecoveryInvalid) {
		t.Fatalf("expired recovery must fail: %v", err)
	}
	hash := HashToken("fresh")
	if _, err := store.CreateRecovery(context.Background(), actor, scope, user.ID, hash, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteRecovery(context.Background(), hash, "new-hash", now); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"session-a", "session-b"} {
		if _, err := store.FindUserBySession(context.Background(), HashToken(token), now); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("recovery must revoke all sessions: %v", err)
		}
	}
	if _, err := store.CompleteRecovery(context.Background(), hash, "another-hash", now); !errors.Is(err, ErrRecoveryInvalid) {
		t.Fatalf("recovery token replay must fail: %v", err)
	}
	if _, err := store.CreateSession(context.Background(), CreateSessionInput{
		TenantID: actor.TenantID, UserID: user.ID, TokenHash: HashToken("in-flight"), ExpiresAt: now.Add(time.Hour), SecurityEpoch: oldUser.SecurityEpoch,
	}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("stale in-flight login epoch must not create a new session: %v", err)
	}
	if err := store.RecordSuccessfulLogin(context.Background(), actor.TenantID, user.ID, "old-hash", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("stale verified password must not record successful login: %v", err)
	}
}

func TestRecoveryCannotCrossSchoolOrRecoverPrivilegedAccount(t *testing.T) {
	store, actor, scope := lifecycleFixture(t)
	store.AddUser(UserWithPassword{User: User{ID: "other-teacher", TenantID: actor.TenantID, TenantCode: "demo", Username: "other", Status: "active", Roles: []string{"teacher"}}, SchoolID: "school-2"})
	store.AddUser(UserWithPassword{User: User{ID: "tenant-admin", TenantID: actor.TenantID, TenantCode: "demo", Username: "privileged", Status: "active", Roles: []string{"tenant_admin"}}})
	for userID, expected := range map[string]error{"other-teacher": ErrOrganizationScope, "tenant-admin": ErrUserStatusForbidden, actor.ID: ErrUserStatusForbidden} {
		if _, err := store.CreateRecovery(context.Background(), actor, scope, userID, HashToken(userID), time.Now().Add(time.Hour)); !errors.Is(err, expected) {
			t.Fatalf("target %s expected %v, got %v", userID, expected, err)
		}
	}
}

func TestRecoveryGrantIsInvalidAfterPasswordChangeOrDisable(t *testing.T) {
	for _, mutation := range []string{"password-change", "disable-and-enable"} {
		t.Run(mutation, func(t *testing.T) {
			store, actor, scope := lifecycleFixture(t)
			user, err := store.CreateManagedUser(context.Background(), actor, scope, CreateManagedUserInput{
				Username: "teacher", DisplayName: "Teacher", RoleCode: "teacher", SchoolID: "school-1",
			}, "old-hash")
			if err != nil {
				t.Fatal(err)
			}
			hash := HashToken("pending-recovery")
			if _, err := store.CreateRecovery(context.Background(), actor, scope, user.ID, hash, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if mutation == "password-change" {
				if updated, _, err := store.UpdatePasswordAndRevokeSessions(context.Background(), actor.TenantID, user.ID, "old-hash", "changed-hash"); err != nil || !updated {
					t.Fatalf("password change failed: updated=%v err=%v", updated, err)
				}
			} else {
				for _, status := range []string{"disabled", "active"} {
					if _, _, err := store.UpdateManagedUserStatus(context.Background(), actor, scope, user.ID, status); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := store.FindRecovery(context.Background(), hash, time.Now()); !errors.Is(err, ErrRecoveryInvalid) {
				t.Fatalf("pending grant must expire after %s: %v", mutation, err)
			}
			if _, err := store.CompleteRecovery(context.Background(), hash, "unauthorized-hash", time.Now()); !errors.Is(err, ErrRecoveryInvalid) {
				t.Fatalf("pending grant must not replace credentials after %s: %v", mutation, err)
			}
		})
	}
}

type epochChangingStore struct {
	Store
	memory *MemoryStore
}

func (s epochChangingStore) CreateSession(ctx context.Context, input CreateSessionInput) (DeviceSession, error) {
	// 精确在密码已通过、会话尚未落库之间改安全版本，复现旧凭据在途登录的竞争窗口。
	s.memory.mu.Lock()
	for key, user := range s.memory.users {
		if user.ID == input.UserID && user.TenantID == input.TenantID {
			user.SecurityEpoch++
			s.memory.users[key] = user
		}
	}
	s.memory.mu.Unlock()
	return s.Store.CreateSession(ctx, input)
}

func TestInFlightLoginCannotIssueSessionAfterSecurityEpochChanges(t *testing.T) {
	memory := NewMemoryStore()
	hash, err := HashPassword("TeacherPrivatePassphrase")
	if err != nil {
		t.Fatal(err)
	}
	memory.AddUser(UserWithPassword{User: User{ID: "teacher", TenantID: "tenant", TenantCode: "demo", Username: "teacher", Status: "active", Roles: []string{"teacher"}, DataScope: map[string]any{"scope": "class", "class_ids": []any{"class-1"}}}, PasswordHash: hash})
	handler := NewHandler(epochChangingStore{Store: memory, memory: memory}, time.Hour)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"tenant_code":"demo","identifier":"teacher","password":"TeacherPrivatePassphrase"}`))
	rec := httptest.NewRecorder()
	handler.Login(rec, req)
	if rec.Code != http.StatusUnauthorized || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("stale login must not issue a cookie: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUnassignedTeacherCanManageOwnAccountButCannotAccessBusinessData(t *testing.T) {
	memory := NewMemoryStore()
	user := User{ID: "teacher", TenantID: "tenant", TenantCode: "demo", Username: "teacher", Status: "active", Roles: []string{"teacher"}, DataScope: map[string]any{"teacher": map[string]any{"scope": "class"}}}
	memory.AddUser(UserWithPassword{User: user, PasswordHash: "test-hash"})
	token := "self-account-session"
	if _, err := memory.CreateSession(context.Background(), CreateSessionInput{
		TenantID: user.TenantID, UserID: user.ID, TokenHash: HashToken(token), SessionType: SessionTypeStandard, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope, ok := AccessScopeFromContext(r.Context())
		if !ok || scope.HasDataAccess() || scope.TenantWide {
			t.Errorf("self-account route must retain an explicit empty business boundary: %#v", scope)
		}
		w.WriteHeader(http.StatusOK)
	})
	for _, test := range []struct {
		name       string
		middleware func(http.Handler) http.Handler
		want       int
	}{
		{"self-account", AccountAuthMiddleware(memory), http.StatusOK},
		{"business", AuthMiddleware(memory), http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			test.middleware(next).ServeHTTP(rec, req)
			if rec.Code != test.want {
				t.Fatalf("expected %d, got %d: %s", test.want, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestLoginCannotBypassAccountLimitBySwitchingIdentifierAliases(t *testing.T) {
	memory := NewMemoryStore()
	hash, err := HashPassword("TeacherPrivatePassphrase")
	if err != nil {
		t.Fatal(err)
	}
	memory.AddUser(UserWithPassword{
		User:         User{ID: "teacher", TenantID: "tenant", TenantCode: "demo", Username: "teacher", Status: "active", Roles: []string{"teacher"}},
		PasswordHash: hash, PhoneNormalized: "+8613800138000", EmployeeNo: "EMP-001",
	})
	handler := NewHandler(memory, time.Hour, HandlerOptions{LoginFailureLimit: 2, LoginFailureWindow: time.Hour})
	for index, identifier := range []string{"teacher", "13800138000", "EMP-001"} {
		password := "incorrect"
		want := http.StatusUnauthorized
		if index >= 1 {
			want = http.StatusTooManyRequests
		}
		if index == 2 {
			password = "TeacherPrivatePassphrase"
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"tenant_code":"demo","identifier":"`+identifier+`","password":"`+password+`"}`))
		req.RemoteAddr = []string{"203.0.113.1:8000", "203.0.113.2:8000", "203.0.113.3:8000"}[index]
		rec := httptest.NewRecorder()
		handler.Login(rec, req)
		if rec.Code != want || len(rec.Result().Cookies()) != 0 {
			t.Fatalf("alias %s must not bypass the account guard: status=%d body=%s", identifier, rec.Code, rec.Body.String())
		}
	}
}
