package dashboard

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresReviewTeacherProgressKeepsSchoolAndSessionBoundaries(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL test DSN required")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	schema := "dashboard_review_teachers_" + uuid.NewString()[:8]
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
	exec(`CREATE TABLE app_user(id uuid,tenant_id uuid,school_id uuid,username text,display_name text,teaching_subjects text[],security_epoch bigint,status text,deleted_at timestamptz);
CREATE TABLE role(id uuid,tenant_id uuid,code text,deleted_at timestamptz);
CREATE TABLE user_role(tenant_id uuid,user_id uuid,role_id uuid,deleted_at timestamptz);
CREATE TABLE exam(id uuid,tenant_id uuid,school_id uuid,status text,deleted_at timestamptz);
CREATE TABLE review_task(id uuid,tenant_id uuid,exam_id uuid,assigned_to uuid,status text,deleted_at timestamptz);
CREATE TABLE score_release(id uuid,tenant_id uuid,exam_id uuid,status text,published_at timestamptz);
CREATE TABLE score_release_current(tenant_id uuid,exam_id uuid,release_id uuid);
CREATE TABLE auth_session(tenant_id uuid,user_id uuid,security_epoch bigint,last_seen_at timestamptz,expires_at timestamptz,revoked_at timestamptz,locked_at timestamptz,created_at timestamptz);`)
	tenant, school, otherSchool := uuid.NewString(), uuid.NewString(), uuid.NewString()
	roles := map[string]string{}
	for _, code := range []string{"teacher", "grader", "arbitrator", "school_admin"} {
		roles[code] = uuid.NewString()
		exec(`INSERT INTO role(id,tenant_id,code) VALUES($1,$2,$3)`, roles[code], tenant, code)
	}
	actor := func(name, schoolID, status string, actorRoles ...string) string {
		t.Helper()
		id := uuid.NewString()
		exec(`INSERT INTO app_user(id,tenant_id,school_id,username,display_name,teaching_subjects,security_epoch,status) VALUES($1,$2,$3,$4,$4,'{mathematics}',3,$5)`, id, tenant, schoolID, name, status)
		for _, code := range actorRoles {
			exec(`INSERT INTO user_role(tenant_id,user_id,role_id) VALUES($1,$2,$3)`, tenant, id, roles[code])
		}
		return id
	}
	online := actor("online", school, "active", "teacher", "grader")
	offline := actor("offline", school, "active", "grader")
	loggedOut := actor("logged-out", school, "active", "grader")
	expired := actor("expired", school, "active", "arbitrator")
	locked := actor("locked", school, "active", "grader")
	staleEpoch := actor("stale-epoch", school, "active", "grader")
	future := actor("future-session", school, "active", "grader")
	actor("school-admin", school, "active", "school_admin", "teacher", "grader")
	actor("foreign-school", otherSchool, "active", "grader")
	actor("disabled", school, "disabled", "grader")
	deleted := actor("deleted", school, "active", "grader")
	exec(`UPDATE app_user SET deleted_at=now() WHERE id=$1`, deleted)
	revoked := actor("revoked-role", school, "active", "grader")
	exec(`UPDATE user_role SET deleted_at=now() WHERE user_id=$1`, revoked)

	now := time.Now().UTC().Truncate(time.Second)
	session := func(userID string, seen time.Time, epoch int64, expires time.Time, revokedAt, lockedAt any) {
		t.Helper()
		exec(`INSERT INTO auth_session(tenant_id,user_id,security_epoch,last_seen_at,expires_at,revoked_at,locked_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, tenant, userID, epoch, seen, expires, revokedAt, lockedAt, now.Add(-time.Hour))
	}
	session(online, now.Add(-2*time.Minute), 3, now.Add(time.Hour), nil, nil)
	session(online, now.Add(-3*time.Minute), 3, now.Add(time.Hour), nil, nil)
	session(offline, now.Add(-10*time.Minute), 3, now.Add(time.Hour), nil, nil)
	session(loggedOut, now.Add(-time.Minute), 3, now.Add(time.Hour), now, nil)
	session(expired, now.Add(-time.Minute), 3, now, nil, nil)
	session(locked, now.Add(-time.Minute), 3, now.Add(time.Hour), nil, now)
	session(staleEpoch, now.Add(-time.Minute), 2, now.Add(time.Hour), nil, nil)
	session(future, now.Add(time.Minute), 3, now.Add(time.Hour), nil, nil)

	activeExam, publishedExam, archivedExam, foreignExam := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO exam(id,tenant_id,school_id,status) VALUES($1,$2,$3,'grading'),($4,$2,$3,'published'),($5,$2,$3,'archived'),($6,$2,$7,'grading')`, activeExam, tenant, school, publishedExam, archivedExam, foreignExam, otherSchool)
	// 学生端发布不改 exam.status：当前版本已正式发布的 finalized 考试不再计入；只有草稿版本的考试仍算未发布。
	releasedExam, draftOnlyExam, releaseID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO exam(id,tenant_id,school_id,status) VALUES($1,$2,$3,'finalized'),($4,$2,$3,'finalized')`, releasedExam, tenant, school, draftOnlyExam)
	exec(`INSERT INTO score_release(id,tenant_id,exam_id,status,published_at) VALUES($1,$2,$3,'published',$4),($5,$2,$6,'draft',NULL)`, releaseID, tenant, releasedExam, now, uuid.NewString(), draftOnlyExam)
	exec(`INSERT INTO score_release_current(tenant_id,exam_id,release_id) VALUES($1,$2,$3)`, tenant, releasedExam, releaseID)
	exec(`INSERT INTO review_task(id,tenant_id,exam_id,assigned_to,status) VALUES($1,$2,$3,$4,'assigned')`, uuid.NewString(), tenant, draftOnlyExam, offline)
	for _, status := range []string{"pending", "assigned", "in_progress", "returned", "submitted", "completed", "cancelled"} {
		exec(`INSERT INTO review_task(id,tenant_id,exam_id,assigned_to,status) VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), tenant, activeExam, online, status)
	}
	for _, examID := range []string{publishedExam, archivedExam, foreignExam, releasedExam} {
		exec(`INSERT INTO review_task(id,tenant_id,exam_id,assigned_to,status) VALUES($1,$2,$3,$4,'assigned')`, uuid.NewString(), tenant, examID, online)
	}
	exec(`INSERT INTO review_task(id,tenant_id,exam_id,assigned_to,status,deleted_at) VALUES($1,$2,$3,$4,'submitted',$5)`, uuid.NewString(), tenant, activeExam, online, now)
	scope, err := auth.ResolveDeclaredAccessScope(auth.User{ID: uuid.NewString(), TenantID: tenant, Roles: []string{"school_admin"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": school}}})
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresOrganizationSummaryStore(db)
	teachers, err := store.DashboardReviewTeacherProgress(t.Context(), tenant, scope, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(teachers) != 7 {
		t.Fatalf("unexpected teacher candidates=%#v", teachers)
	}
	byID := map[string]ReviewTeacherProgress{}
	for _, teacher := range teachers {
		byID[teacher.UserID] = teacher
	}
	actual := byID[online]
	if actual.AssignedCount != 2 || actual.InProgressCount != 1 || actual.ReturnedCount != 1 || actual.SubmittedCount != 2 || actual.RemainingCount != 4 || !actual.IsOnline || actual.LastActiveAt == nil || !actual.LastActiveAt.Equal(now.Add(-2*time.Minute)) {
		t.Fatalf("online teacher progress=%#v", actual)
	}
	if actual := byID[offline]; actual.IsOnline || actual.LastActiveAt == nil {
		t.Fatalf("inactive valid session=%#v", actual)
	}
	if actual := byID[offline]; actual.AssignedCount != 1 || actual.RemainingCount != 1 {
		t.Fatalf("exam with only a draft release lost its in-progress task: %#v", actual)
	}
	for _, userID := range []string{loggedOut, expired, locked, staleEpoch, future} {
		if actual := byID[userID]; actual.IsOnline || actual.LastActiveAt != nil {
			t.Fatalf("invalid session counted as online activity: %#v", actual)
		}
	}
	for _, invalid := range []struct {
		tenantID string
		scope    auth.AccessScope
	}{
		{uuid.NewString(), scope},
		{tenant, auth.AccessScope{TenantID: tenant, SchoolIDs: []string{school}, AssignedOnly: true}},
		{tenant, auth.AccessScope{TenantID: tenant, TenantWide: true}},
	} {
		if _, err := store.DashboardReviewTeacherProgress(t.Context(), invalid.tenantID, invalid.scope, now); !errors.Is(err, auth.ErrForbidden) {
			t.Fatalf("invalid management scope accepted: %v", err)
		}
	}
}
