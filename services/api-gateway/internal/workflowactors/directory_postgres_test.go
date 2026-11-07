package workflowactors

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 真正执行候选查询和分派校验；随机 schema 不读取或改写现有账号。
func TestPostgresReviewActorSubjectEligibility(t *testing.T) {
	db := openActorTestDB(t)
	ctx := t.Context()
	tenant, school, otherSchool := uuid.NewString(), uuid.NewString(), uuid.NewString()
	mathExam, politicsExam := uuid.NewString(), uuid.NewString()
	actorTestExec(t, db, `INSERT INTO exam(id,tenant_id,school_id,subject) VALUES ($1,$2,$3,'math'),($4,$2,$3,'politics')`, mathExam, tenant, school, politicsExam)
	graderRole, arbitratorRole, adminRole, processingRole, teacherRole := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	actorTestExec(t, db, `INSERT INTO role(id,tenant_id,code) VALUES ($1,$2,'grader'),($3,$2,'arbitrator'),($4,$2,'school_admin'),($5,$2,'capture_operator'),($6,$2,'teacher')`, graderRole, tenant, arbitratorRole, adminRole, processingRole, teacherRole)
	reviewPermission, arbitrationPermission, processingPermission := uuid.NewString(), uuid.NewString(), uuid.NewString()
	actorTestExec(t, db, `INSERT INTO permission(id,tenant_id,code) VALUES ($1,$2,'review:work'),($3,$2,'arbitration:work'),($4,$2,'submission:capture')`, reviewPermission, tenant, arbitrationPermission, processingPermission)
	actorTestExec(t, db, `INSERT INTO role_permission(tenant_id,role_id,permission_id) VALUES ($1,$2,$3),($1,$4,$5),($1,$6,$7),($1,$8,$3)`, tenant, graderRole, reviewPermission, arbitratorRole, arbitrationPermission, processingRole, processingPermission, teacherRole)

	add := func(username, actorSchool, status string, subjects []string, roles ...string) string {
		t.Helper()
		id := uuid.NewString()
		actorTestExec(t, db, `INSERT INTO app_user(id,tenant_id,school_id,username,status,teaching_subjects) VALUES ($1,$2,$3,$4,$5,$6)`, id, tenant, actorSchool, username, status, subjects)
		for _, role := range roles {
			actorTestExec(t, db, `INSERT INTO user_role(tenant_id,user_id,role_id) VALUES ($1,$2,$3)`, tenant, id, role)
		}
		return id
	}
	matched := add("a-math", school, "active", []string{"mathematics"}, graderRole)
	alias := add("b-alias", school, "active", []string{"数学", "physics"}, graderRole)
	politics := add("c-politics", school, "active", []string{"ethics_politics"}, graderRole)
	admin := add("d-admin", school, "active", []string{}, graderRole, adminRole)
	empty := add("empty", school, "active", []string{}, graderRole)
	wrongSchool := add("foreign-school", otherSchool, "active", []string{"mathematics"}, graderRole)
	disabled := add("inactive", school, "disabled", []string{"mathematics"}, graderRole)
	wrongRole := add("permission-without-grader-role", school, "active", []string{"mathematics"}, teacherRole)
	processing := add("processing", school, "active", []string{}, processingRole)
	arbitrator := add("qualified-arbitrator", school, "active", []string{"math"}, arbitratorRole)
	revoked := add("revoked", school, "active", []string{"mathematics"}, graderRole)
	actorTestExec(t, db, `UPDATE user_role SET deleted_at=now() WHERE user_id=$1`, revoked)
	deleted := add("soft-deleted", school, "active", []string{"mathematics"}, graderRole)
	actorTestExec(t, db, `UPDATE app_user SET deleted_at=now() WHERE id=$1`, deleted)

	for _, tc := range []struct {
		name, examID, permission string
		want                     []string
	}{
		{"math candidates", mathExam, "review:work", []string{matched, alias, admin}},
		{"politics candidates", politicsExam, "review:work", []string{politics, admin}},
		{"arbitration candidates", mathExam, "arbitration:work", []string{arbitrator}},
		{"capture is independent of teaching subjects", mathExam, "submission:capture", []string{processing}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actors, err := List(ctx, db, tenant, tc.examID, tc.permission)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, actor := range actors {
				got = append(got, actor.ID)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("candidates=%v, want %v", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name, userID, examID, permission string
		allowed                          bool
	}{
		{"canonical subject", matched, mathExam, "review:work", true},
		{"exam and profile aliases", alias, mathExam, "review:work", true},
		{"politics alias", politics, politicsExam, "review:work", true},
		{"administrator subject exemption", admin, politicsExam, "review:work", true},
		{"different subject", politics, mathExam, "review:work", false},
		{"profile missing subjects", empty, mathExam, "review:work", false},
		{"different school", wrongSchool, mathExam, "review:work", false},
		{"inactive account", disabled, mathExam, "review:work", false},
		{"missing grader role", wrongRole, mathExam, "review:work", false},
		{"revoked role", revoked, mathExam, "review:work", false},
		{"deleted account", deleted, mathExam, "review:work", false},
		{"arbitration role", arbitrator, mathExam, "arbitration:work", true},
		{"grader cannot arbitrate", matched, mathExam, "arbitration:work", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			err = ValidateReviewAssignee(ctx, tx, tenant, tc.examID, tc.userID, tc.permission)
			if tc.allowed && err != nil || !tc.allowed && !errors.Is(err, ErrAssigneeIneligible) {
				t.Fatalf("eligibility error=%v, allowed=%v", err, tc.allowed)
			}
		})
	}
	actorTestExec(t, db, `UPDATE role_permission SET deleted_at=now() WHERE role_id=$1`, graderRole)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := ValidateReviewAssignee(ctx, tx, tenant, mathExam, matched, "review:work"); !errors.Is(err, ErrAssigneeIneligible) {
		t.Fatalf("revoked task permission error=%v", err)
	}
}

func openActorTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is not configured")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	schema := "review_actor_subjects_" + uuid.NewString()[:8]
	actorTestExec(t, admin, `CREATE SCHEMA `+schema)
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
	actorTestExec(t, db, `CREATE TABLE app_user(id uuid,tenant_id uuid,school_id uuid,username text,display_name text,status text,teaching_subjects text[],deleted_at timestamptz);
CREATE TABLE exam(id uuid,tenant_id uuid,school_id uuid,subject text,deleted_at timestamptz);
CREATE TABLE role(id uuid,tenant_id uuid,code text,deleted_at timestamptz);
CREATE TABLE user_role(tenant_id uuid,user_id uuid,role_id uuid,deleted_at timestamptz);
CREATE TABLE permission(id uuid,tenant_id uuid,code text,deleted_at timestamptz);
CREATE TABLE role_permission(tenant_id uuid,role_id uuid,permission_id uuid,deleted_at timestamptz);`)
	return db
}

func actorTestExec(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
}
