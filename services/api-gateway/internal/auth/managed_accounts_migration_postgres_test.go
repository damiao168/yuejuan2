package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// 在随机 schema 的克隆表恢复 pre228 列，再执行正式迁移；不改 public 或传入库的业务记录。
// 回填只能修复唯一且可信的学生归属，不能替历史账户生成密码或要求重新激活。
func TestPostgresManagedAccountsMigrationBackfillsOnlyUnambiguousStudents(t *testing.T) {
	ctx, db := managedSchoolPostgresFixture(t)
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(schema, "auth_scope_fix_") {
		t.Fatalf("migration fixture requires its isolated schema, got %q", schema)
	}
	table := func(name string) string { return pgx.Identifier{schema, name}.Sanitize() }
	for _, name := range []string{"tenant", "school", "student"} {
		execAuthScopeFixture(t, ctx, db, "CREATE TABLE "+table(name)+" (LIKE public."+pgx.Identifier{name}.Sanitize()+" INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES)")
	}
	// 模板已应用 228；只撤销克隆表的新列及同名约束，让正式迁移重新创建它们。
	// LIKE 的同列唯一索引可能采用自动名称，不按模糊名称批量删除。
	execAuthScopeFixture(t, ctx, db, "ALTER TABLE "+table("app_user")+" DROP COLUMN student_id CASCADE, DROP COLUMN must_change_password")
	execAuthScopeFixture(t, ctx, db, "ALTER TABLE "+table("student")+" DROP CONSTRAINT IF EXISTS uq_student_tenant_school_id")

	tenant, schoolA, schoolB := uuid.NewString(), uuid.NewString(), uuid.NewString()
	execAuthScopeFixture(t, ctx, db, `INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'Synthetic migration institution','migration-228','active')`, tenant)
	for _, school := range []string{schoolA, schoolB} {
		execAuthScopeFixture(t, ctx, db, `INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'Synthetic migration school',$1::uuid::text,'active')`, school, tenant)
	}
	roles := map[string]string{"student": uuid.NewString(), "grader": uuid.NewString()}
	for code, id := range roles {
		execAuthScopeFixture(t, ctx, db, `INSERT INTO role(id,tenant_id,code,name,scope_type) VALUES($1,$2,$3,$3,$4)`, id, tenant, code, RolePolicy(code).CanonicalScope)
	}
	newStudent := func(school string) string {
		t.Helper()
		id := uuid.NewString()
		execAuthScopeFixture(t, ctx, db, `INSERT INTO student(id,tenant_id,school_id,class_id,student_no,name,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$1::uuid::text,'Synthetic migration student','active')`, id, tenant, school, uuid.NewString())
		return id
	}
	type expectedAccount struct {
		name, school, student, passwordHash string
	}
	expected := make(map[string]expectedAccount)
	newUser := func(name, school string) string {
		t.Helper()
		id := uuid.NewString()
		// 不可登录的合成哈希只用于核对迁移没有改写历史凭据，不在夹具中保存明文密码。
		hash := HashToken("synthetic-migration-password|" + id)
		execAuthScopeFixture(t, ctx, db, `INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status) VALUES($1::uuid,$2::uuid,NULLIF($3,'')::uuid,$1::uuid::text,'Synthetic migration account',$4,'active')`, id, tenant, school, hash)
		expected[id] = expectedAccount{name: name, school: school, passwordHash: hash}
		return id
	}
	bind := func(userID, role, studentID string) {
		t.Helper()
		scope := canonicalRoleDataScope(role, "")
		if role == "student" {
			scope["student_id"] = studentID
		}
		raw, err := json.Marshal(scope)
		if err != nil {
			t.Fatal(err)
		}
		execAuthScopeFixture(t, ctx, db, `INSERT INTO user_role(tenant_id,user_id,role_id,data_scope) VALUES($1,$2,$3,$4::jsonb)`, tenant, userID, roles[role], string(raw))
	}
	wantBinding := func(userID, school, studentID string) {
		want := expected[userID]
		want.school, want.student = school, studentID
		expected[userID] = want
	}

	sameSchoolStudent := newStudent(schoolA)
	sameSchoolUser := newUser("single same-school self binding", schoolA)
	bind(sameSchoolUser, "student", sameSchoolStudent)
	wantBinding(sameSchoolUser, schoolA, sameSchoolStudent)

	inferredSchoolStudent := newStudent(schoolA)
	inferredSchoolUser := newUser("single self binding infers missing school", "")
	bind(inferredSchoolUser, "student", inferredSchoolStudent)
	wantBinding(inferredSchoolUser, schoolA, inferredSchoolStudent)

	crossSchoolUser := newUser("cross-school self binding is untouched", schoolA)
	bind(crossSchoolUser, "student", newStudent(schoolB))

	sharedStudent := newStudent(schoolA)
	for _, name := range []string{"first account sharing one student", "second account sharing one student"} {
		bind(newUser(name, schoolA), "student", sharedStudent)
	}

	mixedRoleUser := newUser("student and grader roles are untouched", schoolA)
	bind(mixedRoleUser, "student", newStudent(schoolA))
	bind(mixedRoleUser, "grader", "")

	// 混合职责和错误学校的声明也参与唯一性判断；先过滤掉它们会把另一个账户误判为唯一。
	sharedWithMixedStudent := newStudent(schoolA)
	pureWithMixedUser := newUser("pure student sharing with mixed-role account is untouched", schoolA)
	bind(pureWithMixedUser, "student", sharedWithMixedStudent)
	mixedOverlapUser := newUser("mixed-role account sharing with pure student is untouched", schoolA)
	bind(mixedOverlapUser, "student", sharedWithMixedStudent)
	bind(mixedOverlapUser, "grader", "")

	sharedWithForeignStudent := newStudent(schoolA)
	localOverlapUser := newUser("same-school student sharing with foreign-school claim is untouched", schoolA)
	bind(localOverlapUser, "student", sharedWithForeignStudent)
	foreignOverlapUser := newUser("foreign-school account sharing the same student is untouched", schoolB)
	bind(foreignOverlapUser, "student", sharedWithForeignStudent)

	// 正常 user_role 唯一约束不允许同一学生职责重复绑定；仅在克隆表中构造历史异常，
	// 验证迁移的 user_count>1 分支不会按任意一条学生记录猜测归属。
	var bindingConstraint string
	if err := db.QueryRowContext(ctx, `SELECT conname FROM pg_constraint
WHERE conrelid=$1::regclass AND contype='u'
  AND pg_get_constraintdef(oid)='UNIQUE (tenant_id, user_id, role_id)'`, table("user_role")).Scan(&bindingConstraint); err != nil {
		t.Fatalf("find the isolated binding uniqueness constraint: %v", err)
	}
	execAuthScopeFixture(t, ctx, db, "ALTER TABLE "+table("user_role")+" DROP CONSTRAINT "+pgx.Identifier{bindingConstraint}.Sanitize())
	multipleStudentsUser := newUser("one account with multiple self students is untouched", schoolA)
	bind(multipleStudentsUser, "student", newStudent(schoolA))
	bind(multipleStudentsUser, "student", newStudent(schoolA))

	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000228_managed_student_accounts.sql"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, string(raw)); err != nil {
		t.Fatalf("apply formal managed student account migration: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for id, want := range expected {
		t.Run(want.name, func(t *testing.T) {
			var school, student, hash string
			var mustChange bool
			if err := db.QueryRowContext(ctx, `SELECT COALESCE(school_id::text,''),COALESCE(student_id::text,''),password_hash,must_change_password FROM app_user WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&school, &student, &hash, &mustChange); err != nil {
				t.Fatal(err)
			}
			if school != want.school || student != want.student {
				t.Fatalf("unexpected migrated identity: school=%q student=%q, want school=%q student=%q", school, student, want.school, want.student)
			}
			if hash != want.passwordHash || mustChange {
				t.Fatal("migration changed historical password or required a new password")
			}
		})
	}
}
