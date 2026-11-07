package org

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

func newStudentAccountsPostgresFixture(t *testing.T) cohortPostgresFixture {
	t.Helper()
	f := newCohortPostgresFixture(t)
	if _, err := f.db.Exec(`UPDATE tenant SET code='synthetic-tenant' WHERE id=$1`, f.tenant); err != nil {
		t.Fatal(err)
	}
	// 沿用随机 schema，只复制正式表结构；账号 SQL、登录查询及外键都在隔离数据上执行。
	for _, table := range []string{"permission", "role_permission", "auth_session", "auth_trusted_device", "wechat_identity",
		"exam_candidate_snapshot", "exam_student_attendance", "submission", "submission_grade", "score_release_item", "answer_sheet_print_sheet", "capture_page", "appeal", "question_appeal"} {
		if _, err := f.db.Exec(`CREATE TABLE ` + table + ` (LIKE public.` + table + ` INCLUDING ALL)`); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`ALTER TABLE app_user ADD FOREIGN KEY(tenant_id,school_id,student_id) REFERENCES student(tenant_id,school_id,id)`,
		`ALTER TABLE user_role ADD FOREIGN KEY(tenant_id,user_id) REFERENCES app_user(tenant_id,id)`,
		`ALTER TABLE user_role ADD FOREIGN KEY(tenant_id,role_id) REFERENCES role(tenant_id,id)`,
		`ALTER TABLE auth_session ADD FOREIGN KEY(tenant_id,user_id) REFERENCES app_user(tenant_id,id)`,
	} {
		if _, err := f.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	seedStudentAccountRole(t, f)
	return f
}

func seedStudentAccountRole(t *testing.T, f cohortPostgresFixture) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO role(tenant_id,code,name,scope_type) VALUES($1,'student','Synthetic student','self')`, f.tenant); err != nil {
		t.Fatal(err)
	}
}

func provisionSyntheticStudent(t *testing.T, f cohortPostgresFixture, no string) Student {
	t.Helper()
	g := f.grade(t, "2026-2027", 2026, 10)
	c := f.class(t, g, "student-account-class-"+no)
	hash, err := auth.HashPassword("Synthetic123!")
	if err != nil {
		t.Fatal(err)
	}
	student, err := f.store.CreateStudentWithAccount(t.Context(), f.tenant, StudentAccountProvision{Student: Student{SchoolID: f.school, ClassID: c.ID, StudentNo: no, Name: "Synthetic " + no}, PasswordHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	return student
}

func createSyntheticStudentSession(t *testing.T, store *auth.PostgresStore, user auth.UserWithPassword, label string) string {
	t.Helper()
	hash := auth.HashToken(label + uuid.NewString())
	_, err := store.CreateSession(t.Context(), auth.CreateSessionInput{TenantID: user.TenantID, UserID: user.ID, TokenHash: hash,
		SessionType: auth.SessionTypeStandard, DeviceID: uuid.NewString(), DeviceName: "Synthetic test", SecurityEpoch: user.SecurityEpoch, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestStudentAccountsPostgresLoginProjectionAndFirstPasswordChange(t *testing.T) {
	f := newStudentAccountsPostgresFixture(t)
	student := provisionSyntheticStudent(t, f, "00123")
	if student.AccountStatus != "active" || student.LoginAccount != "00123" || student.LoginSchoolCode != "synthetic" || !student.MustChangePassword {
		t.Fatalf("created account projection=%+v", student)
	}
	store := auth.NewPostgresStore(f.db)
	user, err := store.FindUserByLogin(t.Context(), "synthetic", "00123")
	studentID, bound := auth.ScopedStudentID(user.User)
	if err != nil || !auth.CheckPassword(user.PasswordHash, "Synthetic123!") || !user.MustChangePassword || !bound || studentID != student.ID {
		t.Fatalf("school code/student number did not resolve the exact account: %+v %v", user.User, err)
	}
	if _, err = store.FindUserByLogin(t.Context(), "synthetic", user.Username); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("internal username bypassed student alias: %v", err)
	}
	if _, err = store.FindUserByLogin(t.Context(), "synthetic-tenant", user.Username); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("tenant/internal username bypassed student alias: %v", err)
	}
	byID, err := store.FindUserByID(t.Context(), f.tenant, user.ID)
	if err != nil || byID.ID != user.ID || !byID.MustChangePassword {
		t.Fatalf("private primary key lookup failed: %+v %v", byID.User, err)
	}
	if _, err = f.db.Exec(`INSERT INTO wechat_identity(tenant_id,user_id,app_id,open_id,union_id) VALUES($1,$2,'synthetic-app','synthetic-open','synthetic-union')`, f.tenant, user.ID); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"synthetic", "synthetic-tenant"} {
		wechatUser, err := store.FindUserByWechatIdentity(t.Context(), code, "synthetic-app", "synthetic-union", "synthetic-open")
		if err != nil || wechatUser.ID != user.ID || !wechatUser.MustChangePassword {
			t.Fatalf("WeChat primary key lookup lost student or mandatory change: code=%s user=%+v err=%v", code, wechatUser.User, err)
		}
	}
	sessionHash := createSyntheticStudentSession(t, store, user, "first-login")
	sessionUser, err := store.FindUserBySession(t.Context(), sessionHash, time.Now())
	if err != nil || !sessionUser.MustChangePassword {
		t.Fatalf("session lost mandatory password change: %+v %v", sessionUser, err)
	}
	reauthUser, err := store.FindUserBySessionForReauthentication(t.Context(), sessionHash, time.Now())
	if err != nil || !reauthUser.MustChangePassword {
		t.Fatalf("reauthentication session lost mandatory change: %+v %v", reauthUser, err)
	}
	newHash, err := auth.HashPassword("Replacement123!")
	if err != nil {
		t.Fatal(err)
	}
	updated, revoked, err := store.UpdatePasswordAndRevokeSessions(t.Context(), f.tenant, user.ID, user.PasswordHash, newHash)
	if err != nil || !updated || revoked != 1 {
		t.Fatalf("first password change=%v revoked=%d err=%v", updated, revoked, err)
	}
	if _, err = store.FindUserBySession(t.Context(), sessionHash, time.Now()); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("old session survived password change: %v", err)
	}
	user, err = store.FindUserByLogin(t.Context(), "synthetic", "00123")
	if err != nil || user.MustChangePassword || !auth.CheckPassword(user.PasswordHash, "Replacement123!") {
		t.Fatalf("changed password projection=%+v %v", user.User, err)
	}
	students, err := f.store.ListStudents(t.Context(), f.tenant, StudentListFilter{})
	if err != nil || len(students) != 1 || students[0].MustChangePassword || students[0].ID != student.ID {
		t.Fatalf("roster failed to refresh changed account: %+v %v", students, err)
	}
}

func TestStudentAccountsPostgresSchoolAliasesAndTenantAmbiguity(t *testing.T) {
	f := newStudentAccountsPostgresFixture(t)
	first := provisionSyntheticStudent(t, f, "00123")
	otherSchool, err := f.store.CreateSchool(t.Context(), f.tenant, School{Name: "Synthetic other school", Code: "synthetic-other"})
	if err != nil {
		t.Fatal(err)
	}
	other := f
	other.school = otherSchool.ID
	second := provisionSyntheticStudent(t, other, "00123")
	if _, err = f.db.Exec(`INSERT INTO app_user(tenant_id,school_id,student_id,username,display_name,password_hash,status)
VALUES($1,$2,$3,'synthetic-cross-school','Synthetic invalid','unused','active')`, f.tenant, other.school, first.ID); err == nil {
		t.Fatal("database accepted a student account bound to another school")
	}
	store := auth.NewPostgresStore(f.db)
	for _, tc := range []struct{ code, studentID string }{{"synthetic", first.ID}, {"synthetic-other", second.ID}} {
		user, err := store.FindUserByLogin(t.Context(), tc.code, "00123")
		id, _ := auth.ScopedStudentID(user.User)
		if err != nil || id != tc.studentID {
			t.Fatalf("school alias selected another student's account: code=%s id=%s err=%v", tc.code, id, err)
		}
	}
	third := f
	third.tenant = uuid.NewString()
	if _, err = f.db.Exec(`INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'Synthetic third tenant','synthetic-third','active')`, third.tenant); err != nil {
		t.Fatal(err)
	}
	seedStudentAccountRole(t, third)
	thirdSchool, err := f.store.CreateSchool(t.Context(), third.tenant, School{Name: "Synthetic ambiguous school", Code: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	third.school = thirdSchool.ID
	provisionSyntheticStudent(t, third, "00123")
	if _, err = store.FindUserByLogin(t.Context(), "synthetic", "00123"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("ambiguous cross-tenant school alias selected one account: %v", err)
	}
}

func TestStudentAccountsPostgresLegacyAndMixedRolesRequireManualReview(t *testing.T) {
	f := newStudentAccountsPostgresFixture(t)
	g := f.grade(t, "2026-2027", 2026, 10)
	c := f.class(t, g, "legacy")
	student, err := f.store.CreateStudent(t.Context(), f.tenant, Student{SchoolID: f.school, ClassID: c.ID, StudentNo: "legacy", Name: "Synthetic legacy"})
	if err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		userID := uuid.NewString()
		if _, err = f.db.Exec(`INSERT INTO app_user(id,tenant_id,username,display_name,password_hash,status) VALUES($1,$2,$3,'Synthetic legacy','unused','active')`, userID, f.tenant, fmt.Sprintf("legacy-%d", index)); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec(`INSERT INTO user_role(tenant_id,user_id,role_id,data_scope)
SELECT $1,$2,id,jsonb_build_object('scope','self','student_id',$3::text) FROM role WHERE tenant_id=$1 AND code='student'`, f.tenant, userID, student.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.store.OpenStudentAccount(t.Context(), f.tenant, student.ID, "synthetic-hash"); !errors.Is(err, ErrStudentAccountLegacyConflict) {
		t.Fatalf("unbound duplicate legacy accounts were ignored: %v", err)
	}
	if _, err = f.store.UpdateStudentStatus(t.Context(), f.tenant, student.ID, "inactive"); !errors.Is(err, ErrStudentAccountLegacyConflict) {
		t.Fatalf("legacy account status change should require review: %v", err)
	}
	// 直接构造升级前已停用的历史名册，单独验证删除保护；公开停用入口在上面已经明确拒绝。
	if _, err = f.db.Exec(`UPDATE student SET status='inactive' WHERE tenant_id=$1 AND id=$2`, f.tenant, student.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.store.DeleteStudent(t.Context(), f.tenant, student.ID); !errors.Is(err, ErrStudentInUse) {
		t.Fatalf("unbound legacy account was silently retired: %v", err)
	}
	mixed, err := f.store.CreateStudentWithAccount(t.Context(), f.tenant, StudentAccountProvision{Student: Student{SchoolID: f.school, ClassID: c.ID, StudentNo: "mixed", Name: "Synthetic mixed"}, PasswordHash: "synthetic-hash"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO role(tenant_id,code,name,scope_type) VALUES($1,'grader','Synthetic mixed role','exam_task')`, f.tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO user_role(tenant_id,user_id,role_id,data_scope)
SELECT $1,u.id,r.id,'{"scope":"exam_task"}'::jsonb FROM app_user u CROSS JOIN role r
WHERE u.tenant_id=$1 AND u.student_id=$2 AND r.tenant_id=$1 AND r.code='grader'`, f.tenant, mixed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.UpdateStudentStatus(t.Context(), f.tenant, mixed.ID, "inactive"); !errors.Is(err, ErrStudentAccountLegacyConflict) {
		t.Fatalf("mixed staff account status change should require review: %v", err)
	}
	var rosterStatus, accountStatus string
	if err = f.db.QueryRow(`SELECT st.status,u.status FROM student st JOIN app_user u ON u.tenant_id=st.tenant_id AND u.student_id=st.id WHERE st.tenant_id=$1 AND st.id=$2`, f.tenant, mixed.ID).Scan(&rosterStatus, &accountStatus); err != nil || rosterStatus != "active" || accountStatus != "active" {
		t.Fatalf("rejected student change disabled mixed staff identity: student=%s account=%s err=%v", rosterStatus, accountStatus, err)
	}
	// 历史夹具把名册和账号都预置为停用，避免删除测试只因账号尚在启用而提前返回。
	if _, err = f.db.Exec(`WITH historical AS (UPDATE student SET status='inactive' WHERE tenant_id=$1 AND id=$2 RETURNING id)
UPDATE app_user SET status='disabled' FROM historical WHERE app_user.tenant_id=$1 AND app_user.student_id=historical.id`, f.tenant, mixed.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.store.DeleteStudent(t.Context(), f.tenant, mixed.ID); !errors.Is(err, ErrStudentInUse) {
		t.Fatalf("mixed role account was silently retired: %v", err)
	}
}

func TestStudentAccountsPostgresLegacyPatchKeepsRosterUnchanged(t *testing.T) {
	f := newStudentAccountsPostgresFixture(t)
	g := f.grade(t, "2026-2027", 2026, 10)
	c := f.class(t, g, "legacy-patch")
	student, err := f.store.CreateStudent(t.Context(), f.tenant, Student{SchoolID: f.school, ClassID: c.ID, StudentNo: "legacy-before", Name: "Synthetic before"})
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.NewString()
	if _, err = f.db.Exec(`INSERT INTO app_user(id,tenant_id,username,display_name,password_hash,status) VALUES($1,$2,'unresolved-legacy','Synthetic before','unused','active')`, userID, f.tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO user_role(tenant_id,user_id,role_id,data_scope)
SELECT $1,$2,id,jsonb_build_object('scope','self','student_id',$3::text) FROM role WHERE tenant_id=$1 AND code='student'`, f.tenant, userID, student.ID); err != nil {
		t.Fatal(err)
	}
	newNo, newName, inactive := "legacy-after", "Synthetic after", "inactive"
	for _, patch := range []StudentPatch{{StudentNo: &newNo}, {Name: &newName}, {Status: &inactive}, {StudentNo: &newNo, Name: &newName, Status: &inactive}} {
		if _, err = f.store.UpdateStudent(t.Context(), f.tenant, student.ID, patch); !errors.Is(err, ErrStudentAccountLegacyConflict) {
			t.Fatalf("legacy patch should require account review: %v", err)
		}
	}
	var no, name, status, accountName, accountStatus string
	if err = f.db.QueryRow(`SELECT st.student_no,st.name,st.status,u.display_name,u.status FROM student st CROSS JOIN app_user u WHERE st.id=$1 AND u.id=$2`, student.ID, userID).Scan(&no, &name, &status, &accountName, &accountStatus); err != nil || no != student.StudentNo || name != student.Name || status != "active" || accountName != "Synthetic before" || accountStatus != "active" {
		t.Fatalf("rejected legacy patch partially changed roster/account: student=(%s,%s,%s) account=(%s,%s) err=%v", no, name, status, accountName, accountStatus, err)
	}
}

func TestStudentAccountsPostgresBusinessReferenceAndLateForeignKeyPreserveAccount(t *testing.T) {
	f := newStudentAccountsPostgresFixture(t)
	student := provisionSyntheticStudent(t, f, "referenced")
	if _, err := f.db.Exec(`INSERT INTO exam_candidate_snapshot(tenant_id,exam_id,student_id,student_no_snapshot,student_name_snapshot,grade_id_snapshot,grade_name_snapshot,class_id_snapshot,class_name_snapshot)
VALUES($1,$2,$3,'referenced','Synthetic',$4,'Synthetic',$5,'Synthetic')`, f.tenant, uuid.NewString(), student.ID, uuid.NewString(), student.ClassID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpdateStudentStatus(t.Context(), f.tenant, student.ID, "inactive"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteStudent(t.Context(), f.tenant, student.ID); !errors.Is(err, ErrStudentInUse) {
		t.Fatalf("exam candidate student was deleted: %v", err)
	}
	var bound, retired bool
	if err := f.db.QueryRow(`SELECT student_id IS NOT NULL,deleted_at IS NOT NULL FROM app_user WHERE tenant_id=$1 AND student_id=$2`, f.tenant, student.ID).Scan(&bound, &retired); err != nil || !bound || retired {
		t.Fatalf("business reference retired an account: bound=%v retired=%v err=%v", bound, retired, err)
	}
	// 新增业务外键可能不在显式检查清单中；删名册最后一步失败时，前面的账号撤权与解绑必须全部回滚。
	if _, err := f.db.Exec(`DELETE FROM exam_candidate_snapshot WHERE tenant_id=$1 AND student_id=$2`, f.tenant, student.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`CREATE TABLE synthetic_student_guard(student_id uuid REFERENCES student(id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO synthetic_student_guard(student_id) VALUES($1)`, student.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteStudent(t.Context(), f.tenant, student.ID); !errors.Is(err, ErrStudentInUse) {
		t.Fatalf("unlisted business foreign key was ignored: %v", err)
	}
	if err := f.db.QueryRow(`SELECT student_id IS NOT NULL,deleted_at IS NOT NULL FROM app_user WHERE tenant_id=$1 AND student_id=$2`, f.tenant, student.ID).Scan(&bound, &retired); err != nil || !bound || retired {
		t.Fatalf("late delete failure left partially retired account: bound=%v retired=%v err=%v", bound, retired, err)
	}
}

func TestStudentAccountsPostgresFailureRollsBackRosterAndEnrollment(t *testing.T) {
	f := newStudentAccountsPostgresFixture(t)
	g := f.grade(t, "2026-2027", 2026, 10)
	c := f.class(t, g, "rollback")
	if _, err := f.db.Exec(`DELETE FROM role WHERE tenant_id=$1 AND code='student'`, f.tenant); err != nil {
		t.Fatal(err)
	}
	_, err := f.store.CreateStudentWithAccount(t.Context(), f.tenant, StudentAccountProvision{Student: Student{SchoolID: f.school, ClassID: c.ID, StudentNo: "rollback", Name: "Synthetic rollback"}, PasswordHash: "synthetic-hash"})
	if err == nil {
		t.Fatal("missing role unexpectedly created account")
	}
	for _, table := range []string{"student", "student_enrollment", "app_user", "user_role"} {
		var count int
		if err = f.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE tenant_id=$1`, f.tenant).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed aggregate left %s rows=%d err=%v", table, count, err)
		}
	}
}

func TestStudentAccountsPostgresOpeningIsUniqueAndRejectsInactive(t *testing.T) {
	f := newStudentAccountsPostgresFixture(t)
	g := f.grade(t, "2026-2027", 2026, 10)
	c := f.class(t, g, "opening")
	student, err := f.store.CreateStudent(t.Context(), f.tenant, Student{SchoolID: f.school, ClassID: c.ID, StudentNo: "opening", Name: "Synthetic opening"})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("Synthetic123!")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 6)
	var workers sync.WaitGroup
	for range 6 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := f.store.OpenStudentAccount(t.Context(), f.tenant, student.ID, hash)
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	created, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrStudentAccountExists):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	var accounts int
	if err = f.db.QueryRow(`SELECT count(*) FROM app_user WHERE tenant_id=$1 AND student_id=$2`, f.tenant, student.ID).Scan(&accounts); err != nil || accounts != 1 || created != 1 || conflicts != 5 {
		t.Fatalf("concurrent opening created=%d conflict=%d accounts=%d err=%v", created, conflicts, accounts, err)
	}
	inactive, err := f.store.CreateStudent(t.Context(), f.tenant, Student{SchoolID: f.school, ClassID: c.ID, StudentNo: "inactive", Name: "Synthetic inactive", Status: "inactive"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.OpenStudentAccount(t.Context(), f.tenant, inactive.ID, hash); !errors.Is(err, ErrStudentAccountInactive) {
		t.Fatalf("inactive student opened account: %v", err)
	}
}

func TestStudentAccountsPostgresEditsAndLifecycleKeepOneIdentity(t *testing.T) {
	f := newStudentAccountsPostgresFixture(t)
	student := provisionSyntheticStudent(t, f, "before")
	store := auth.NewPostgresStore(f.db)
	user, err := store.FindUserByLogin(t.Context(), "synthetic", "before")
	if err != nil {
		t.Fatal(err)
	}
	sessionHash := createSyntheticStudentSession(t, store, user, "before-edit")
	newNo, newName := "after", "Synthetic corrected"
	updated, err := f.store.UpdateStudent(t.Context(), f.tenant, student.ID, StudentPatch{StudentNo: &newNo, Name: &newName})
	if err != nil || updated.ID != student.ID || updated.LoginAccount != newNo {
		t.Fatalf("student edit changed identity: %+v %v", updated, err)
	}
	if _, err = store.FindUserByLogin(t.Context(), "synthetic", "before"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("old student number still logs in: %v", err)
	}
	if _, err = store.FindUserBySession(t.Context(), sessionHash, time.Now()); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("old identity session survived number change: %v", err)
	}
	userAfter, err := store.FindUserByLogin(t.Context(), "synthetic", "after")
	if err != nil || userAfter.ID != user.ID || userAfter.DisplayName != newName {
		t.Fatalf("new student number changed account: %+v %v", userAfter.User, err)
	}
	sessionHash = createSyntheticStudentSession(t, store, userAfter, "before-disable")
	updated, err = f.store.UpdateStudentStatus(t.Context(), f.tenant, student.ID, "inactive")
	if err != nil || updated.AccountStatus != "disabled" {
		t.Fatalf("student/account disable not synchronized: %+v %v", updated, err)
	}
	if _, err = store.FindUserBySession(t.Context(), sessionHash, time.Now()); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("disabled student retained session: %v", err)
	}
	if _, err = store.FindUserByLogin(t.Context(), "synthetic", "after"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("disabled student still logs in: %v", err)
	}
	updated, err = f.store.UpdateStudentStatus(t.Context(), f.tenant, student.ID, "active")
	if err != nil || updated.AccountStatus != "active" {
		t.Fatalf("student/account restore not synchronized: %+v %v", updated, err)
	}
	restored, err := store.FindUserByLogin(t.Context(), "synthetic", "after")
	if err != nil || restored.ID != user.ID {
		t.Fatalf("restore replaced account: %+v %v", restored.User, err)
	}
	if _, err = f.store.CreateStudentWithAccount(t.Context(), f.tenant, StudentAccountProvision{Student: Student{SchoolID: f.school, ClassID: student.ClassID, StudentNo: newNo, Name: "Synthetic duplicate"}, PasswordHash: "synthetic-hash"}); !errors.Is(err, ErrStudentNoConflict) {
		t.Fatalf("duplicate school/student number was not rejected: %v", err)
	}
	var boundUsers int
	if err = f.db.QueryRow(`SELECT count(*) FROM app_user WHERE tenant_id=$1 AND student_id=$2`, f.tenant, student.ID).Scan(&boundUsers); err != nil || boundUsers != 1 {
		t.Fatalf("lifecycle created another account: count=%d err=%v", boundUsers, err)
	}
	sessionHash = createSyntheticStudentSession(t, store, restored, "before-delete")
	if _, err = f.store.UpdateStudentStatus(t.Context(), f.tenant, student.ID, "inactive"); err != nil {
		t.Fatal(err)
	}
	if err = f.store.DeleteStudent(t.Context(), f.tenant, student.ID); err != nil {
		t.Fatalf("unused student and generated account could not be deleted: %v", err)
	}
	if _, err = store.FindUserBySession(t.Context(), sessionHash, time.Now()); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("deleted student retained session: %v", err)
	}
	if _, err = store.FindUserByLogin(t.Context(), "synthetic", "after"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("deleted student still logs in: %v", err)
	}
	var cleared, deleted bool
	if err = f.db.QueryRow(`SELECT student_id IS NULL,deleted_at IS NOT NULL FROM app_user WHERE id=$1`, user.ID).Scan(&cleared, &deleted); err != nil || !cleared || !deleted {
		t.Fatalf("deleted student's account was not retained and unbound: cleared=%v deleted=%v err=%v", cleared, deleted, err)
	}
}
