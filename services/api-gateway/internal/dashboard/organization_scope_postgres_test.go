package dashboard

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestOrganizationTeacherCountsKeepUnassignedSchoolMembers(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL test DSN required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := "organization_scope_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg.RuntimeParams["search_path"] = schema + ",public"
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for _, table := range []string{"app_user", "role", "user_role", "school_class", "student", "grade"} {
		if _, err = db.ExecContext(ctx, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	tenant, schoolA, schoolB, gradeA, gradeB, classA, classB := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, row := range [][3]string{{classA, schoolA, gradeA}, {classB, schoolB, gradeB}} {
		if _, err = db.ExecContext(ctx, `INSERT INTO school_class(id,tenant_id,school_id,grade_id,name,code,status,academic_year_id,grade_cohort_id) VALUES($1::uuid,$2,$3,$4,'synthetic',$1::text,'active',$5,$6)`, row[0], tenant, row[1], row[2], uuid.NewString(), uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	roles := map[string]string{"teacher": uuid.NewString(), "grader": uuid.NewString()}
	for code, id := range roles {
		if _, err = db.ExecContext(ctx, `INSERT INTO role(id,tenant_id,code,name,scope_type) VALUES($1,$2,$3,$3,'exam_task')`, id, tenant, code); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct{ school, class, role, status string }{
		{schoolA, "", "teacher", "active"},
		{schoolA, classA, "teacher", "active"},
		{schoolB, classB, "teacher", "active"},
		{schoolA, "", "teacher", "disabled"},
		{schoolA, classA, "grader", "active"},
	} {
		user := uuid.NewString()
		if _, err = db.ExecContext(ctx, `INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status) VALUES($1::uuid,$2,$3,$1::text,'synthetic','synthetic',$4)`, user, tenant, row.school, row.status); err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO user_role(tenant_id,user_id,role_id) VALUES($1,$2,$3)`, tenant, user, roles[row.role]); err != nil {
			t.Fatal(err)
		}
		if row.class != "" {
			if _, err = db.ExecContext(ctx, `UPDATE app_user SET teaching_subjects='{mathematics}' WHERE id=$1`, user); err != nil {
				t.Fatal(err)
			}
		}
	}
	schoolScope := func(school string) auth.AccessScope {
		scope, err := auth.ResolveDeclaredAccessScope(auth.User{ID: uuid.NewString(), TenantID: tenant, Roles: []string{"school_admin"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": school}}})
		if err != nil {
			t.Fatal(err)
		}
		return scope
	}
	expanded := schoolScope(schoolA)
	expanded.GradeIDs, expanded.ClassIDs = []string{gradeA}, []string{classA}
	// 管理员按学校统计教师；普通班级或年级投影不再授予人员统计权限。
	for _, test := range []struct {
		name              string
		scope             auth.AccessScope
		teachers, unbound int
		graders           int
	}{
		{"expanded_school", expanded, 2, 1, 1},
		{"school_without_projection", schoolScope(schoolA), 2, 1, 1},
		{"other_school", schoolScope(schoolB), 1, 0, 0},
		{"tenant_with_projection", auth.AccessScope{TenantID: tenant, TenantWide: true, SchoolIDs: []string{schoolA}, ClassIDs: []string{classA}}, 3, 1, 1},
		{"class", auth.AccessScope{TenantID: tenant, SchoolIDs: []string{schoolA}, ClassIDs: []string{classA}}, 0, 0, 0},
		{"grade", auth.AccessScope{TenantID: tenant, SchoolIDs: []string{schoolA}, GradeIDs: []string{gradeA}}, 0, 0, 0},
		{"mismatched_class", auth.AccessScope{TenantID: tenant, SchoolIDs: []string{schoolA}, ClassIDs: []string{classB}}, 0, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := NewPostgresOrganizationSummaryStore(db).DashboardOrganizationSummary(ctx, tenant, test.scope)
			if err != nil || result.TeacherCount != test.teachers || result.UnassignedTeacherCount != test.unbound || result.GraderCount != test.graders {
				t.Fatalf("summary=%+v err=%v, want teacher=%d unbound=%d grader=%d", result, err, test.teachers, test.unbound, test.graders)
			}
		})
	}
}
