package calibration

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

func TestPostgresNewCalibrationAssignmentsUseCurrentTeacherQualification(t *testing.T) {
	f := newMaterialAssignmentFixture(t)
	mathGrader := f.actor("math-grader", f.school, "active", []string{"mathematics"}, "grader")
	englishGrader := f.actor("english-grader", f.school, "active", []string{"english"}, "grader")
	foreignGrader := f.actor("foreign-grader", uuid.NewString(), "active", []string{"mathematics"}, "grader")
	disabledGrader := f.actor("disabled-grader", f.school, "disabled", []string{"mathematics"}, "grader")
	teacherOnly := f.actor("teacher-only", f.school, "active", []string{"mathematics"}, "teacher")
	admin := f.actor("admin-grader", f.school, "active", []string{}, "grader", "school_admin")
	adminOnly := f.actor("admin-only", f.school, "active", []string{}, "school_admin")
	outsider := f.actor("auditor-with-review-permission", f.school, "active", []string{"mathematics"}, "auditor")
	missingSubject := f.actor("teacher-without-subject", f.school, "active", []string{}, "teacher")
	foreignAdmin := f.actor("foreign-admin", uuid.NewString(), "active", []string{}, "school_admin")
	revokedRole := f.actor("revoked-role", f.school, "active", []string{"mathematics"}, "grader")
	f.exec(`UPDATE user_role SET deleted_at=now() WHERE user_id=$1`, revokedRole)
	for _, tc := range []struct{ name, actor string }{
		{"wrong subject", englishGrader}, {"foreign school", foreignGrader}, {"disabled account", disabledGrader},
		{"unrelated role despite permission", outsider}, {"teacher without subject", missingSubject}, {"foreign admin", foreignAdmin}, {"revoked grader role", revokedRole},
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
	for _, actor := range []string{mathGrader, teacherOnly, admin, adminOnly} {
		t.Run("qualified "+actor, func(t *testing.T) {
			session, err := f.calibrate(actor)
			if err != nil || session.ID == "" || session.GraderID != actor {
				t.Fatalf("qualified calibration assignment=%+v error=%v", session, err)
			}
		})
	}
	// 既有会话不因资料变化被重写；只有新校准分派使用最新资格。
	f.exec(`UPDATE app_user SET teaching_subjects='{english}' WHERE id=$1`, mathGrader)
	if _, err := f.calibrate(mathGrader); !errors.Is(err, workflowactors.ErrAssigneeIneligible) {
		t.Fatalf("stale calibration candidate accepted: %v", err)
	}
	f.assertCounts(4)
	// 恢复学科后仍执行原有失败冷却，资格检查不能覆盖或跳过重考限制。
	f.exec(`UPDATE app_user SET teaching_subjects='{mathematics}' WHERE id=$1`, mathGrader)
	failedAt := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Microsecond)
	f.exec(`UPDATE grader_calibration_session SET status='failed',completed_at=$2,metrics_json='{}' WHERE grader_id=$1`, mathGrader, failedAt)
	_, cooldownErr := f.calibrate(mathGrader)
	if !errors.Is(cooldownErr, ErrRetakeCooldown) {
		t.Fatalf("new qualification guard bypassed retake cooldown: %v", cooldownErr)
	}
	var cooldown *RetakeCooldownError
	if !errors.As(cooldownErr, &cooldown) || !cooldown.AvailableAt.Equal(failedAt.Add(24*time.Hour)) {
		t.Fatalf("cooldown did not retain the stored completion deadline: %v", cooldownErr)
	}
	f.assertCounts(4)
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
	for _, table := range []string{"app_user", "exam", "role", "user_role", "permission", "role_permission", "grader_calibration_session"} {
		f.exec("CREATE TABLE " + table + " (LIKE public." + table + " INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES)")
	}
	permission := uuid.NewString()
	f.exec(`INSERT INTO permission(id,tenant_id,code,name,resource,action) VALUES($1,$2,'review:work','Review','review','work')`, permission, f.tenant)
	for _, code := range []string{"grader", "teacher", "school_admin", "auditor"} {
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

func (f *materialAssignmentFixture) calibrate(actor string) (Session, error) {
	return NewPostgresStore(f.db).CreateSession(f.t.Context(), f.tenant,
		Session{ID: uuid.NewString(), ExamID: f.exam, QuestionID: f.question, GraderID: actor, SnapshotID: uuid.NewString(), GoldSetHash: strings.Repeat("a", 64), Status: SessionInProgress, StartedAt: time.Now().UTC()},
		Policy{ExamID: f.exam, QuestionID: f.question, MaxScore: 10, MinimumSamples: 1},
		[]goldReference{{GoldSample: GoldSample{GoldPaperID: uuid.NewString(), GoldVersion: 1, MaxScore: 10}, SnapshotID: uuid.NewString(), ReferenceScore: 5}})
}

func (f *materialAssignmentFixture) assertBothRejected(actor string) {
	f.t.Helper()
	if _, err := f.calibrate(actor); !errors.Is(err, workflowactors.ErrAssigneeIneligible) {
		f.t.Fatalf("ineligible new calibration assignment: %v", err)
	}
	f.assertCounts(0)
}

func (f *materialAssignmentFixture) assertCounts(sessions int) {
	f.t.Helper()
	var actual int
	if err := f.db.QueryRowContext(f.t.Context(), `SELECT count(*) FROM grader_calibration_session`).Scan(&actual); err != nil || actual != sessions {
		f.t.Fatalf("calibration sessions=%d/%d error=%v", actual, sessions, err)
	}
}
