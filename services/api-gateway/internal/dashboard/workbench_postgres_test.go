package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// openWorkbenchTestSchema 在隔离 schema 中只建本测试需要的列，测试结束后整体删除；未设置 DSN 时跳过。
func openWorkbenchTestSchema(t *testing.T, prefix, ddl string) (*sql.DB, func(string, ...any)) {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL test DSN required")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	schema := prefix + uuid.NewString()[:8]
	if _, err := admin.ExecContext(t.Context(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		_ = db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
		}
		_ = admin.Close()
	})
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(ddl)
	return db, exec
}

func TestPostgresExamRosterCountsUseFrozenRosterOnly(t *testing.T) {
	db, exec := openWorkbenchTestSchema(t, "dashboard_roster_", `CREATE TABLE exam_candidate_snapshot(tenant_id uuid,exam_id uuid,student_id uuid);
CREATE TABLE submission(id uuid,tenant_id uuid,exam_id uuid,student_id uuid,deleted_at timestamptz);`)
	tenant, otherTenant := uuid.NewString(), uuid.NewString()
	withRoster, withoutRoster, notRequested := uuid.NewString(), uuid.NewString(), uuid.NewString()
	roster := func(tenantID, examID string) string {
		t.Helper()
		student := uuid.NewString()
		exec(`INSERT INTO exam_candidate_snapshot(tenant_id,exam_id,student_id) VALUES($1,$2,$3)`, tenantID, examID, student)
		return student
	}
	answer := func(tenantID, examID string, student, deletedAt any) {
		t.Helper()
		exec(`INSERT INTO submission(id,tenant_id,exam_id,student_id,deleted_at) VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), tenantID, examID, student, deletedAt)
	}
	// 名册四人：一人一份、一人重复两份、一人只有已删除答卷、一人缺考；只有前两人算已收。
	answer(tenant, withRoster, roster(tenant, withRoster), nil)
	twice := roster(tenant, withRoster)
	answer(tenant, withRoster, twice, nil)
	answer(tenant, withRoster, twice, nil)
	answer(tenant, withRoster, roster(tenant, withRoster), time.Now())
	roster(tenant, withRoster)
	// 未匹配学生和名册外学生的答卷不计入已收；没有名册的考试不返回记录，由服务层按 0 处理。
	answer(tenant, withRoster, nil, nil)
	answer(tenant, withRoster, uuid.NewString(), nil)
	answer(tenant, withoutRoster, uuid.NewString(), nil)
	roster(tenant, notRequested)
	// 其他租户复用同一考试 ID 的名册与答卷不能混入。
	answer(otherTenant, withRoster, roster(otherTenant, withRoster), nil)

	store := NewPostgresOrganizationSummaryStore(db)
	counts, err := store.DashboardExamRosterCounts(t.Context(), tenant, []string{withRoster, withoutRoster})
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 1 || counts[withRoster] != (ExamRosterCount{RosterCount: 4, SubmittedCount: 2}) {
		t.Fatalf("roster counts=%#v", counts)
	}
	if counts, err := store.DashboardExamRosterCounts(t.Context(), tenant, nil); err != nil || len(counts) != 0 {
		t.Fatalf("empty exam list must not count anything: counts=%#v err=%v", counts, err)
	}
}

func TestPostgresAppealStatisticsMatchStaffListScope(t *testing.T) {
	db, exec := openWorkbenchTestSchema(t, "dashboard_appeals_", `CREATE TABLE exam(id uuid,tenant_id uuid,school_id uuid,deleted_at timestamptz);
CREATE TABLE exam_class(tenant_id uuid,exam_id uuid,class_id uuid,deleted_at timestamptz);
CREATE TABLE question_appeal(id uuid,tenant_id uuid,exam_id uuid,status text);
CREATE TABLE question_appeal_event(tenant_id uuid,appeal_id uuid,event_type text);`)
	tenant, otherTenant, schoolA, schoolB := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	examA, examB, deletedExam := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO exam(id,tenant_id,school_id,deleted_at) VALUES($1,$4,$5,NULL),($2,$4,$6,NULL),($3,$4,$5,now())`, examA, examB, deletedExam, tenant, schoolA, schoolB)
	appeal := func(tenantID, examID, status string, events ...string) string {
		t.Helper()
		id := uuid.NewString()
		exec(`INSERT INTO question_appeal(id,tenant_id,exam_id,status) VALUES($1,$2,$3,$4)`, id, tenantID, examID, status)
		for _, event := range events {
			exec(`INSERT INTO question_appeal_event(tenant_id,appeal_id,event_type) VALUES($1,$2,$3)`, tenantID, id, event)
		}
		return id
	}
	// 本校：两条待分派；复核中只有提交过意见的一条待裁决；已结束的申诉即使有意见也不计。
	appeal(tenant, examA, "submitted", "submitted")
	appeal(tenant, examA, "submitted", "submitted")
	appeal(tenant, examA, "under_review", "submitted", "review_started", "review_opinion_submitted")
	appeal(tenant, examA, "under_review", "submitted", "review_started")
	foreignOpinion := appeal(tenant, examA, "under_review", "submitted", "review_started")
	exec(`INSERT INTO question_appeal_event(tenant_id,appeal_id,event_type) VALUES($1,$2,'review_opinion_submitted')`, otherTenant, foreignOpinion)
	appeal(tenant, examA, "rejected", "review_opinion_submitted", "decided")
	appeal(tenant, examA, "upheld_pending_regrade", "review_opinion_submitted", "decided")
	appeal(tenant, examA, "resolved", "review_opinion_submitted", "decided", "resolved")
	// 外校与已删除考试：学校管理员看不到，租户范围按申诉列表同样的谓词放行。
	appeal(tenant, examB, "submitted")
	appeal(tenant, examB, "under_review", "review_opinion_submitted")
	appeal(tenant, deletedExam, "submitted")
	appeal(otherTenant, examA, "submitted")

	schoolScope := func(schoolID string) auth.AccessScope {
		t.Helper()
		scope, err := auth.ResolveDeclaredAccessScope(auth.User{ID: uuid.NewString(), TenantID: tenant, Roles: []string{"school_admin"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": schoolID}}})
		if err != nil {
			t.Fatal(err)
		}
		return scope
	}
	store := NewPostgresOrganizationSummaryStore(db)
	for _, tc := range []struct {
		name  string
		scope auth.AccessScope
		want  AppealStatistics
	}{
		{"school_a_admin", schoolScope(schoolA), AppealStatistics{PendingAssignmentCount: 2, PendingDecisionCount: 1}},
		{"school_b_admin", schoolScope(schoolB), AppealStatistics{PendingAssignmentCount: 1, PendingDecisionCount: 1}},
		{"tenant_wide", auth.AccessScope{TenantID: tenant, ActorID: uuid.NewString(), TenantWide: true}, AppealStatistics{PendingAssignmentCount: 4, PendingDecisionCount: 2}},
		// 班级投影不是管理范围，与申诉管理列表一样看不到任何记录。
		{"class_projection", auth.AccessScope{TenantID: tenant, ActorID: uuid.NewString(), SchoolIDs: []string{schoolA}, ClassIDs: []string{uuid.NewString()}}, AppealStatistics{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.DashboardAppealStatistics(t.Context(), tenant, tc.scope)
			if err != nil || got != tc.want {
				t.Fatalf("appeal statistics=%#v err=%v, want %#v", got, err, tc.want)
			}
		})
	}
	for _, invalid := range []struct {
		tenantID string
		scope    auth.AccessScope
	}{
		{otherTenant, schoolScope(schoolA)},
		{tenant, auth.AccessScope{TenantID: tenant, TenantWide: true}},
	} {
		if _, err := store.DashboardAppealStatistics(t.Context(), invalid.tenantID, invalid.scope); !errors.Is(err, auth.ErrForbidden) {
			t.Fatalf("invalid appeal scope accepted: %v", err)
		}
	}
}
