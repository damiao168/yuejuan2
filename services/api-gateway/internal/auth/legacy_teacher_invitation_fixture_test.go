package auth_test

import (
	"context"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// 只构造升级前已经发出的邀请；新HTTP创建教师必须设置初始密码，历史邀请仍须可激活和重发。
func legacyTeacherInvitation(t *testing.T, store *auth.MemoryStore, username, phone, employee string) (auth.ManagedUser, string) {
	t.Helper()
	actor, err := store.FindUserByLogin(context.Background(), "demo", "admin")
	if err != nil {
		t.Fatal(err)
	}
	phone, err = auth.NormalizePhone(phone)
	if err != nil {
		t.Fatal(err)
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateManagedUser(context.Background(), actor.User, auth.AccessScope{TenantID: actor.TenantID, ActorID: actor.ID, TenantWide: true}, auth.CreateManagedUserInput{
		Username: username, Phone: phone, EmployeeNo: employee, DisplayName: "张老师", RoleCode: "teacher", SchoolID: "school-1", TeachingSubjects: []string{"mathematics"}, ActivationTokenHash: hash, ActivationExpiresAt: time.Now().Add(48 * time.Hour),
	}, "!activation-required")
	if err != nil {
		t.Fatal(err)
	}
	return user, token
}
