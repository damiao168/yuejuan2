package auth

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresReviewTeacherMigrationPreservesAdministratorsAndRevokesLegacyClassAccess(t *testing.T) {
	ctx, db := authScopePostgresFixture(t)
	// 隔离 schema 从正式表结构复制，再退回迁移前的单列状态，验证迁移本身对存量关系的处理。
	// 不重跑其他迁移，也不修改 public 中的业务资料。
	execAuthScopeFixture(t, ctx, db, `ALTER TABLE app_user DROP COLUMN teaching_subjects`)
	tenant, school, teacherID, adminID, teacherRole, adminRole := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, role := range []struct{ id, code, kind string }{{teacherRole, "teacher", "class"}, {adminRole, "school_admin", "school"}} {
		execAuthScopeFixture(t, ctx, db, `INSERT INTO role(id,tenant_id,code,name,scope_type) VALUES($1,$2,$3,$3,$4)`, role.id, tenant, role.code, role.kind)
	}
	for _, userID := range []string{teacherID, adminID} {
		execAuthScopeFixture(t, ctx, db, `INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status,security_epoch) VALUES($1::uuid,$2,$3,$1::text,'Synthetic teacher','unused','active',5)`, userID, tenant, school)
		execAuthScopeFixture(t, ctx, db, `INSERT INTO user_role(tenant_id,user_id,role_id,data_scope) VALUES($1,$2,$3,'{"scope":"class"}')`, tenant, userID, teacherRole)
		execAuthScopeFixture(t, ctx, db, `INSERT INTO teacher_class(tenant_id,teacher_id,class_id) VALUES($1,$2,$3)`, tenant, userID, uuid.NewString())
		execAuthScopeFixture(t, ctx, db, `INSERT INTO auth_session(tenant_id,user_id,token_hash,device_id,expires_at) VALUES($1,$2::uuid,$2::text,'synthetic',now()+interval '1 hour')`, tenant, userID)
		execAuthScopeFixture(t, ctx, db, `INSERT INTO auth_trusted_device(tenant_id,user_id,token_hash,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')`, tenant, userID, HashToken(userID))
	}
	execAuthScopeFixture(t, ctx, db, `INSERT INTO user_role(tenant_id,user_id,role_id,data_scope) VALUES($1,$2,$3,'{"scope":"school"}')`, tenant, adminID, adminRole)
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000222_review_teacher_subjects.sql"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, string(raw)); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id                                string
		wantBindings, wantSessions, epoch int
	}{{teacherID, 0, 0, 6}, {adminID, 1, 1, 5}} {
		var bindings, sessions, devices, epoch, subjectCount int
		err := db.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM teacher_class WHERE tenant_id=$1 AND teacher_id=$2 AND deleted_at IS NULL),
 (SELECT count(*) FROM auth_session WHERE tenant_id=$1 AND user_id=$2 AND revoked_at IS NULL),
 (SELECT count(*) FROM auth_trusted_device WHERE tenant_id=$1 AND user_id=$2 AND revoked_at IS NULL),
 security_epoch,cardinality(teaching_subjects) FROM app_user WHERE tenant_id=$1 AND id=$2`, tenant, tc.id).Scan(&bindings, &sessions, &devices, &epoch, &subjectCount)
		if err != nil || bindings != tc.wantBindings || sessions != tc.wantSessions || devices != tc.wantSessions || epoch != tc.epoch || subjectCount != 0 {
			t.Fatalf("migrated user %s: bindings=%d sessions=%d devices=%d epoch=%d subjects=%d error=%v", tc.id, bindings, sessions, devices, epoch, subjectCount, err)
		}
		var roleScope, dataScope string
		if err := db.QueryRowContext(ctx, `SELECT r.scope_type,ur.data_scope->>'scope' FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id WHERE ur.tenant_id=$1 AND ur.user_id=$2 AND r.code='teacher'`, tenant, tc.id).Scan(&roleScope, &dataScope); err != nil || roleScope != "exam_task" || dataScope != "exam_task" {
			t.Fatalf("teacher role scope=%s declaration=%s error=%v", roleScope, dataScope, err)
		}
	}
	store := NewPostgresStore(db)
	for _, tc := range []struct {
		id    string
		roles []string
	}{{teacherID, []string{"teacher"}}, {adminID, []string{"school_admin", "teacher"}}} {
		declarations := managedRoleScopes(tc.roles, school)
		scope, err := store.ResolveAccessScope(ctx, User{ID: tc.id, TenantID: tenant, Roles: tc.roles, DataScope: declarations})
		if err != nil || (tc.id == teacherID && (!scope.AssignedOnly || len(scope.ClassIDs)+len(scope.SchoolIDs)+len(scope.ExamIDs) != 0)) || (tc.id == adminID && !scope.managesSchool(school)) {
			t.Fatalf("migrated scope user=%s scope=%+v error=%v", tc.id, scope, err)
		}
	}
}

func TestPostgresManagedSubjectProfileConcurrentEditsKeepOneSnapshotAndRevokeCredentials(t *testing.T) {
	ctx, db := authScopePostgresFixture(t)
	for _, table := range []string{"auth_activation", "auth_recovery"} {
		execAuthScopeFixture(t, ctx, db, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES)")
	}
	tenant, teacherID, teacherRole, school := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	execAuthScopeFixture(t, ctx, db, `INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status,teaching_subjects) VALUES($1::uuid,$2,$3,$1::text,'Synthetic teacher','unused','active','{mathematics}')`, teacherID, tenant, school)
	execAuthScopeFixture(t, ctx, db, `INSERT INTO role(id,tenant_id,code,name,scope_type) VALUES($1,$2,'teacher','Teacher','exam_task')`, teacherRole, tenant)
	execAuthScopeFixture(t, ctx, db, `INSERT INTO user_role(tenant_id,user_id,role_id) VALUES($1,$2,$3)`, tenant, teacherID, teacherRole)
	execAuthScopeFixture(t, ctx, db, `INSERT INTO auth_session(tenant_id,user_id,token_hash,device_id,expires_at) VALUES($1,$2::uuid,$2::text,'synthetic',now()+interval '1 hour')`, tenant, teacherID)
	execAuthScopeFixture(t, ctx, db, `INSERT INTO auth_trusted_device(tenant_id,user_id,token_hash,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')`, tenant, teacherID, HashToken(teacherID))
	store := NewPostgresStore(db)
	actor := User{ID: uuid.NewString(), TenantID: tenant, Roles: []string{"tenant_admin"}}
	scope := AccessScope{TenantID: tenant, ActorID: actor.ID, TenantWide: true}
	results := make(chan error, 2)
	for _, subject := range []string{"physics", "chemistry"} {
		go func(subject string) {
			user, identityChanged, err := store.UpdateManagedUserProfile(ctx, actor, scope, teacherID, UpdateManagedUserProfileInput{
				DisplayName: "Synthetic teacher", ExpectedDisplayName: "Synthetic teacher", TeachingSubjects: []string{subject}, ExpectedTeachingSubjects: []string{"math"},
			})
			if err == nil && (identityChanged || !slices.Equal(user.TeachingSubjects, []string{subject})) {
				err = errors.New("subject-only update returned incorrect profile")
			}
			results <- err
		}(subject)
	}
	succeeded, conflicts := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrManagedUserProfileConflict):
			conflicts++
		default:
			t.Fatalf("concurrent subject profile update: %v", err)
		}
	}
	var epoch, sessions, devices int
	if err := db.QueryRowContext(ctx, `SELECT security_epoch,
 (SELECT count(*) FROM auth_session WHERE tenant_id=$1 AND user_id=$2 AND revoked_at IS NULL),
 (SELECT count(*) FROM auth_trusted_device WHERE tenant_id=$1 AND user_id=$2 AND revoked_at IS NULL)
 FROM app_user WHERE tenant_id=$1 AND id=$2`, tenant, teacherID).Scan(&epoch, &sessions, &devices); err != nil || succeeded != 1 || conflicts != 1 || epoch != 2 || sessions != 0 || devices != 0 {
		t.Fatalf("concurrent subject result success=%d conflicts=%d epoch=%d sessions=%d devices=%d error=%v", succeeded, conflicts, epoch, sessions, devices, err)
	}
}
