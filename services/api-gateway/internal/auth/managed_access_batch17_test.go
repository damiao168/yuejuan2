package auth

import (
	"context"
	"testing"
	"time"
)

func TestManagedAccessNoOpKeepsEpochAndCredentials(t *testing.T) {
	store, actor, scope := managedAccessFixture(t)
	ctx, now := context.Background(), time.Now()
	store.AddUser(UserWithPassword{User: User{ID: "teacher", TenantID: actor.TenantID, TenantCode: "demo", Username: "teacher", Status: "active", Roles: []string{"teacher"}}, SchoolID: "school-1", ManagedClassIDs: []string{"class-1"}})
	user, err := store.FindUserByLogin(ctx, "demo", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSession(ctx, CreateSessionInput{TenantID: actor.TenantID, UserID: user.ID, TokenHash: "session", ExpiresAt: now.Add(time.Hour), SecurityEpoch: user.SecurityEpoch}); err != nil {
		t.Fatal(err)
	}
	store.devices["device"] = memoryDeviceBinding{TenantID: actor.TenantID, UserID: user.ID, ExpiresAt: now.Add(time.Hour)}
	// 重复职责和班级归一化后仍是同一授权，不能让管理员重试撤销现有凭据。
	_, err = store.UpdateManagedUserAccess(ctx, actor, scope, user.ID, UpdateManagedUserAccessInput{
		RoleCodes: []string{"teacher", "teacher"}, ClassIDs: []string{"class-1", "class-1"},
		ExpectedRoles: []string{"teacher"}, ExpectedClassIDs: []string{"class-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := store.FindUserByLogin(ctx, "demo", "teacher")
	if err != nil || unchanged.SecurityEpoch != user.SecurityEpoch {
		t.Fatalf("no-op changed security epoch: before=%d after=%d err=%v", user.SecurityEpoch, unchanged.SecurityEpoch, err)
	}
	if _, err := store.FindUserBySession(ctx, "session", now); err != nil {
		t.Fatalf("no-op revoked session: %v", err)
	}
	if _, ok := store.devices["device"]; !ok {
		t.Fatal("no-op revoked trusted device")
	}
}
