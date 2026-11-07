package appeal

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/workflowactors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresAppealNewAssignmentsRequireExamSubjectAndTeacherRole(t *testing.T) {
	f := newAppealSubjectFixture(t)
	ctx := t.Context()
	mathTeacher := f.actor("a-math", f.school, "active", []string{"mathematics"}, "teacher")
	englishTeacher := f.actor("english", f.school, "active", []string{"english"}, "teacher")
	foreignTeacher := f.actor("foreign", uuid.NewString(), "active", []string{"mathematics"}, "teacher")
	disabledTeacher := f.actor("disabled", f.school, "disabled", []string{"mathematics"}, "teacher")
	grader := f.actor("grader-only", f.school, "active", []string{"mathematics"}, "grader")
	admin := f.actor("b-admin-teacher", f.school, "active", []string{}, "teacher", "school_admin")
	adminOnly := f.actor("admin-only", f.school, "active", []string{}, "school_admin")
	actors, err := workflowactors.List(ctx, f.db, f.tenant, f.exam, "appeal:work")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, actor := range actors {
		ids = append(ids, actor.ID)
	}
	if !slices.Equal(ids, []string{mathTeacher, admin}) {
		t.Fatalf("appeal subject candidates=%v", ids)
	}
	for _, kind := range []string{"legacy", "published-question"} {
		t.Run(kind, func(t *testing.T) {
			for _, assignee := range []string{englishTeacher, foreignTeacher, disabledTeacher, grader, adminOnly} {
				id := f.appeal(kind)
				if err := f.assign(kind, id, assignee); !errors.Is(err, ErrForbidden) || !errors.Is(err, workflowactors.ErrAssigneeIneligible) {
					t.Fatalf("unqualified new %s assignment: %v", kind, err)
				}
				var status string
				var revision int
				var unassigned bool
				table := "appeal"
				if kind != "legacy" {
					table = "question_appeal"
				}
				if err := f.db.QueryRowContext(ctx, `SELECT status,revision,assigned_to IS NULL FROM `+table+` WHERE id=$1`, id).Scan(&status, &revision, &unassigned); err != nil || status != "submitted" || revision != 1 || !unassigned {
					t.Fatalf("rejected assignment changed state: status=%s revision=%d unassigned=%t error=%v", status, revision, unassigned, err)
				}
			}
			for _, assignee := range []string{mathTeacher, admin} {
				if err := f.assign(kind, f.appeal(kind), assignee); err != nil {
					t.Fatalf("qualified %s assignment: %v", kind, err)
				}
			}
		})
	}
	// 候选已展示后管理员更改学科，两个实际写入口仍重验，不能依赖此前列表。
	f.exec(`UPDATE app_user SET teaching_subjects='{english}' WHERE id=$1`, mathTeacher)
	for _, kind := range []string{"legacy", "published-question"} {
		if err := f.assign(kind, f.appeal(kind), mathTeacher); !errors.Is(err, ErrForbidden) {
			t.Fatalf("changed qualification accepted a new %s assignment: %v", kind, err)
		}
	}
}

type appealSubjectFixture struct {
	t                        *testing.T
	db                       *sql.DB
	tenant, school, exam     string
	manager, submissionGrade string
	roles                    map[string]string
}

func newAppealSubjectFixture(t *testing.T) *appealSubjectFixture {
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
	schema := "appeal_subject_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	f := &appealSubjectFixture{t: t, db: db, tenant: uuid.NewString(), school: uuid.NewString(), exam: uuid.NewString(), manager: uuid.NewString(), submissionGrade: uuid.NewString(), roles: map[string]string{}}
	// 保留正式列、默认值、检查与唯一约束；不复制跨模块外键及业务触发器，专门验证这两个真实分派事务。
	for _, table := range []string{"app_user", "exam", "role", "user_role", "role_permission", "permission", "appeal", "question_appeal", "submission_grade", "score_adjustment", "final_grade", "human_grade", "score_release", "score_release_question", "question_appeal_event"} {
		f.exec("CREATE TABLE " + table + " (LIKE public." + table + " INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES)")
	}
	permissionID := uuid.NewString()
	f.exec(`INSERT INTO permission(id,tenant_id,code,name,resource,action) VALUES($1,$2,'appeal:work','Appeal review','appeal','work')`, permissionID, f.tenant)
	for _, code := range []string{"teacher", "grader", "school_admin"} {
		roleID := uuid.NewString()
		f.roles[code] = roleID
		f.exec(`INSERT INTO role(id,tenant_id,code,name,scope_type) VALUES($1,$2,$3,$3,$4)`, roleID, f.tenant, code, map[string]string{"teacher": "exam_task", "grader": "exam_task", "school_admin": "school"}[code])
		// 即使其他岗位误持有该权限，也不能代替复核教师岗位。
		f.exec(`INSERT INTO role_permission(tenant_id,role_id,permission_id) VALUES($1,$2,$3)`, f.tenant, roleID, permissionID)
	}
	f.exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2,$3,'Synthetic subject appeal','math','quiz',10,'grading','ai_assisted','manual_after_confirmation',$4)`, f.exam, f.tenant, f.school, f.manager)
	f.exec(`INSERT INTO submission_grade(id,tenant_id,exam_id,submission_id,student_id,anonymous_code,total_score,max_score,status,locked,created_by) VALUES($1,$2,$3,$4,$5,'synthetic',5,10,'published',true,$6)`, f.submissionGrade, f.tenant, f.exam, uuid.NewString(), uuid.NewString(), f.manager)
	return f
}

func (f *appealSubjectFixture) exec(statement string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.t.Context(), statement, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *appealSubjectFixture) actor(username, school, status string, subjects []string, roles ...string) string {
	f.t.Helper()
	id := uuid.NewString()
	f.exec(`INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status,teaching_subjects) VALUES($1,$2,$3,$4,$4,'unused',$5,$6)`, id, f.tenant, school, username, status, subjects)
	for _, role := range roles {
		f.exec(`INSERT INTO user_role(tenant_id,user_id,role_id) VALUES($1,$2,$3)`, f.tenant, id, f.roles[role])
	}
	return id
}

func (f *appealSubjectFixture) appeal(kind string) string {
	f.t.Helper()
	id := uuid.NewString()
	if kind == "legacy" {
		f.exec(`INSERT INTO appeal(id,tenant_id,exam_id,submission_id,submission_grade_id,student_id,target_type,reason,status,created_by) VALUES($1,$2,$3,$4,$5,$6,'exam','Synthetic reason','submitted',$7)`, id, f.tenant, f.exam, uuid.NewString(), f.submissionGrade, uuid.NewString(), f.manager)
	} else {
		f.exec(`INSERT INTO question_appeal(id,tenant_id,exam_id,student_id,submission_id,source_release_id,source_release_version,question_id,question_no,source_final_grade_id,source_score,source_max_score,reason_code,reason,status,created_by) VALUES($1,$2,$3,$4,$5,$6,1,$7,'1',$8,5,10,'missing_step_credit','Synthetic reason','submitted',$9)`, id, f.tenant, f.exam, uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), f.manager)
	}
	return id
}

func (f *appealSubjectFixture) assign(kind, id, assignee string) error {
	f.t.Helper()
	if kind == "legacy" {
		_, err := NewPostgresStore(f.db).AssignAppeal(f.t.Context(), f.tenant, id, f.manager, AssignAppealInput{AssignedTo: assignee, ExpectedRevision: 1})
		return err
	}
	_, err := NewPublishedQuestionAppealPostgresStore(f.db).StartPublishedQuestionAppealReview(f.t.Context(), f.tenant, id, f.manager, StartQuestionAppealReviewInput{AssignedTo: assignee, ExpectedRevision: 1})
	return err
}

func TestAppealAssigneeEligibilityHasConsistentSafeResponse(t *testing.T) {
	for _, writeError := range []func(http.ResponseWriter, *http.Request, error){writeStoreError, writePublishedQuestionAppealError} {
		rec := httptest.NewRecorder()
		writeError(rec, httptest.NewRequest(http.MethodPost, "/appeals/assign", nil), errors.Join(ErrForbidden, workflowactors.ErrAssigneeIneligible))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"appeal_assignee_ineligible"`) {
			t.Fatalf("assignment eligibility response=%d %s", rec.Code, rec.Body.String())
		}
	}
}
