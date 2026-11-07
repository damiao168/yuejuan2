package auth

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresManagedTeacherPersistsInitialPasswordAndSchoolLogin(t *testing.T) {
	ctx, db := managedSchoolPostgresFixture(t)
	for _, table := range []string{"tenant", "school"} {
		execAuthScopeFixture(t, ctx, db, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)")
	}
	tenant, school, actorID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	execAuthScopeFixture(t, ctx, db, `INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'Synthetic institution','teacher-tenant','active')`, tenant)
	execAuthScopeFixture(t, ctx, db, `INSERT INTO school(id,tenant_id,name,code,status) VALUES($1,$2,'Synthetic school','T053','active')`, school, tenant)
	for _, role := range []string{"teacher", "grader", "arbitrator"} {
		execAuthScopeFixture(t, ctx, db, `INSERT INTO role(tenant_id,code,name,scope_type) VALUES($1,$2,$2,'exam_task')`, tenant, role)
	}
	store := NewPostgresStore(db)
	actor := User{ID: actorID, TenantID: tenant, TenantCode: "teacher-tenant", Roles: []string{"tenant_admin"}}
	scope := AccessScope{TenantID: tenant, ActorID: actorID, TenantWide: true}
	hash, err := HashPassword("SyntheticInitial123!")
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateManagedUser(ctx, actor, scope, CreateManagedUserInput{
		Username: "synthetic-teacher", DisplayName: "Synthetic teacher", RoleCodes: []string{"teacher", "grader", "arbitrator"},
		SchoolID: school, TeachingSubjects: []string{"mathematics"}, MustChangePassword: true,
	}, hash)
	if err != nil || !created.MustChangePassword || created.Status != "active" {
		t.Fatalf("teacher account flag was not committed: %+v %v", created, err)
	}
	for _, code := range []string{"T053", "teacher-tenant"} {
		user, err := store.FindUserByLogin(ctx, code, "synthetic-teacher")
		if err != nil || user.ID != created.ID || !user.MustChangePassword || !CheckPassword(user.PasswordHash, "SyntheticInitial123!") {
			t.Fatalf("teacher school/legacy login %s: %+v %v", code, user.User, err)
		}
	}
	users, err := store.ListManagedUsers(ctx, tenant, ManagedUserFilter{})
	if err != nil || len(users) != 1 || !users[0].MustChangePassword {
		t.Fatalf("teacher list lost initial flag: %+v %v", users, err)
	}
	// 资料编辑不会解除首次改密要求，不能靠一次姓名修改绕过初始凭据限制。
	user, _, err := store.UpdateManagedUserProfile(ctx, actor, scope, created.ID, UpdateManagedUserProfileInput{DisplayName: "Corrected synthetic teacher", ExpectedDisplayName: created.DisplayName})
	if err != nil || !user.MustChangePassword {
		t.Fatalf("profile edit cleared initial flag: %+v %v", user, err)
	}
	// 学校代码不能占用平台保留入口，避免它与真实平台管理员候选竞争。
	execAuthScopeFixture(t, ctx, db, `UPDATE school SET code='platform' WHERE tenant_id=$1 AND id=$2`, tenant, school)
	if _, err := store.FindUserByLogin(ctx, "platform", "synthetic-teacher"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("school claimed reserved platform login: %v", err)
	}

}
