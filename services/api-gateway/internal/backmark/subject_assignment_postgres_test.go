package backmark

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/workflowactors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresNewBackmarkAssignmentsUseCurrentTeacherQualification(t *testing.T) {
	f := newMaterialAssignmentFixture(t)
	mathGrader := f.actor("math-grader", f.school, "active", []string{"mathematics"}, "grader")
	englishGrader := f.actor("english-grader", f.school, "active", []string{"english"}, "grader")
	foreignGrader := f.actor("foreign-grader", uuid.NewString(), "active", []string{"mathematics"}, "grader")
	disabledGrader := f.actor("disabled-grader", f.school, "disabled", []string{"mathematics"}, "grader")
	teacherOnly := f.actor("teacher-only", f.school, "active", []string{"mathematics"}, "teacher")
	admin := f.actor("admin-grader", f.school, "active", []string{}, "grader", "school_admin")
	adminOnly := f.actor("admin-only", f.school, "active", []string{}, "school_admin")
	revokedRole := f.actor("revoked-role", f.school, "active", []string{"mathematics"}, "grader")
	f.exec(`UPDATE user_role SET deleted_at=now() WHERE user_id=$1`, revokedRole)
	for _, tc := range []struct{ name, actor string }{
		{"wrong subject", englishGrader}, {"foreign school", foreignGrader}, {"disabled account", disabledGrader},
		{"teacher without grader post", teacherOnly}, {"administrator without grader post", adminOnly}, {"revoked grader role", revokedRole},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.assertBothRejected(tc.actor)
		})
	}
	t.Run("revoked actual task permission", func(t *testing.T) {
		f.exec(`UPDATE role_permission SET deleted_at=now() WHERE role_id=$1`, f.roles["grader"])
		f.assertBothRejected(mathGrader)
		f.exec(`UPDATE role_permission SET deleted_at=NULL WHERE role_id=$1`, f.roles["grader"])
	})
	for _, actor := range []string{mathGrader, admin} {
		t.Run("qualified "+actor, func(t *testing.T) {
			batch, items, err := f.backmark(actor)
			if err != nil || batch.ID == "" || len(items) != 1 || items[0].ReassignedTo != actor {
				t.Fatalf("qualified backmark assignment batch=%s items=%d error=%v", batch.ID, len(items), err)
			}
		})
	}
	// 既有回评任务不因资料变化被重写；新的回评分派使用最新资格。
	f.exec(`UPDATE app_user SET teaching_subjects='{english}' WHERE id=$1`, mathGrader)
	_, _, err := f.backmark(mathGrader)
	if !errors.Is(err, workflowactors.ErrAssigneeIneligible) {
		t.Fatalf("stale backmark candidate accepted: %v", err)
	}
	f.assertCounts(2, 2)
}

type materialAssignmentFixture struct {
	t                              *testing.T
	db                             *sql.DB
	tenant, school, exam, question string
	manager                        string
	roles                          map[string]string
}

func newMaterialAssignmentFixture(t *testing.T) *materialAssignmentFixture {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is required")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	schema := "material_assignment_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	f := &materialAssignmentFixture{t: t, db: db, tenant: uuid.NewString(), school: uuid.NewString(), exam: uuid.NewString(), question: uuid.NewString(), manager: uuid.NewString(), roles: map[string]string{}}
	// 克隆正式列、默认值和约束；跨模块外键不复制，本测试只写随机 schema 内的分派事务。
	for _, table := range []string{"app_user", "exam", "role", "user_role", "permission", "role_permission", "backmark_batch", "backmark_item"} {
		f.exec("CREATE TABLE " + table + " (LIKE public." + table + " INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES)")
	}
	permission := uuid.NewString()
	f.exec(`INSERT INTO permission(id,tenant_id,code,name,resource,action) VALUES($1,$2,'review:work','Review','review','work')`, permission, f.tenant)
	for _, code := range []string{"grader", "teacher", "school_admin"} {
		role := uuid.NewString()
		f.roles[code] = role
		kind := "exam_task"
		if code == "school_admin" {
			kind = "school"
		}
		f.exec(`INSERT INTO role(id,tenant_id,code,name,scope_type) VALUES($1,$2,$3,$3,$4)`, role, f.tenant, code, kind)
		// 只有正确的阅卷岗位可接任务，不能用其他岗位误配出的同名权限代替。
		f.exec(`INSERT INTO role_permission(tenant_id,role_id,permission_id) VALUES($1,$2,$3)`, f.tenant, role, permission)
	}
	f.exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2,$3,'Synthetic materials','math','quiz',10,'grading','ai_assisted','manual_after_confirmation',$4)`, f.exam, f.tenant, f.school, f.manager)
	return f
}

func (f *materialAssignmentFixture) exec(statement string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.t.Context(), statement, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *materialAssignmentFixture) actor(username, school, status string, subjects []string, roles ...string) string {
	f.t.Helper()
	id := uuid.NewString()
	f.exec(`INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status,teaching_subjects) VALUES($1,$2,$3,$4,$4,'unused',$5,$6)`, id, f.tenant, school, username, status, subjects)
	for _, role := range roles {
		f.exec(`INSERT INTO user_role(tenant_id,user_id,role_id) VALUES($1,$2,$3)`, f.tenant, id, f.roles[role])
	}
	return id
}

func (f *materialAssignmentFixture) backmark(actor string) (Batch, []Item, error) {
	return NewPostgresStore(f.db).CreateBatch(f.t.Context(), f.tenant, f.exam, f.question, f.manager,
		CreateInput{SourceIncidentID: uuid.NewString(), ReassignedTo: actor, Policy: Policy{Disposition: DispositionConfirm}},
		[]SourceTask{{ReviewTaskID: uuid.NewString(), OriginalGradeID: uuid.NewString(), OriginalReviewer: uuid.NewString(), OriginalScore: 5, MaxScore: 10, GradedAt: time.Now().UTC()}})
}

func (f *materialAssignmentFixture) assertBothRejected(actor string) {
	f.t.Helper()
	if _, _, err := f.backmark(actor); !errors.Is(err, workflowactors.ErrAssigneeIneligible) || !errors.Is(err, ErrAssigneeForbidden) {
		f.t.Fatalf("ineligible new backmark assignment: %v", err)
	}
	f.assertCounts(0, 0)
}

func (f *materialAssignmentFixture) assertCounts(batches, items int) {
	f.t.Helper()
	var actualBatches, actualItems int
	if err := f.db.QueryRowContext(f.t.Context(), `SELECT (SELECT count(*) FROM backmark_batch),(SELECT count(*) FROM backmark_item)`).Scan(&actualBatches, &actualItems); err != nil || actualBatches != batches || actualItems != items {
		f.t.Fatalf("backmark writes batches=%d/%d items=%d/%d error=%v", actualBatches, batches, actualItems, items, err)
	}
}
