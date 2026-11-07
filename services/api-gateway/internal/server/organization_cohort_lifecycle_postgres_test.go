package server

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOrganizationCohortLifecycleE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin", "school_admin"})
	router := e2ePostgresRouter(db)
	admin := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := uuid.NewString()
	school := e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", admin, `{"name":"Synthetic cohort school","code":"cohort-`+suffix+`"}`, http.StatusCreated)["school"].(map[string]any)
	schoolID := e2eString(t, school, "id")
	tenantID := e2eString(t, school, "tenant_id")
	now := time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60))
	year := now.Year()
	if now.Month() < time.September {
		year--
	}
	configureYear := func(id string, start int) {
		if _, err := db.Exec(`UPDATE academic_year SET is_current=false WHERE school_id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO academic_year(tenant_id,school_id,name,start_year,end_year,starts_at,ends_at,is_current) VALUES($1,$2,($3::int)::text||'-'||($3::int+1)::text,$3::int,$3::int+1,make_date($3::int,9,1),make_date($3::int+1,8,31),true) ON CONFLICT(tenant_id,school_id,start_year) DO UPDATE SET is_current=true`, tenantID, id, start); err != nil {
			t.Fatal(err)
		}
	}
	configureYear(schoolID, year)
	e2eBindPostgresSchoolAdmin(t, db, "school_admin", schoolID)
	schoolAdmin := e2eLoginWithTenant(t, router, "demo", "school_admin", "ChangeMe123!")
	createGrade := func(start, entry, level int) map[string]any {
		grade := e2ePostJSON(t, router, http.MethodPost, "/api/v1/grades", schoolAdmin, fmt.Sprintf(`{"school_id":%q,"name":"Synthetic current grade","education_stage":"senior","academic_year":"%d-%d","entry_year":%d,"level_no":%d}`, schoolID, start, start+1, entry, level), http.StatusCreated)["grade"].(map[string]any)
		return grade
	}
	currentGrade := createGrade(year, year-1, 11)
	current := e2eString(t, currentGrade, "id")
	e2eExpectStatus(t, router, http.MethodPost, "/api/v1/grades", schoolAdmin, fmt.Sprintf(`{"school_id":%q,"name":"Duplicate","education_stage":"senior","entry_year":%d,"level_no":11}`, schoolID, year-1), http.StatusConflict)
	// 历史年度是存量夹具，不再通过公开新建接口给同届追加年度记录。
	var olderYearID, older string
	if err := db.QueryRow(`INSERT INTO academic_year(tenant_id,school_id,name,start_year,end_year,starts_at,ends_at,is_current) VALUES($1,$2,($3::int)::text||'-'||($3::int+1)::text,$3::int,$3::int+1,make_date($3::int,9,1),make_date($3::int+1,8,31),false) RETURNING id::text`, tenantID, schoolID, year-1).Scan(&olderYearID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO grade(tenant_id,school_id,name,level_no,academic_year,education_stage,academic_year_id,grade_cohort_id,status) VALUES($1,$2,'高一',10,$3,'senior',$4,$5,'active') RETURNING id::text`, tenantID, schoolID, fmt.Sprintf("%d-%d", year-1, year), olderYearID, e2eString(t, currentGrade, "grade_cohort_id")).Scan(&older); err != nil {
		t.Fatal(err)
	}
	createClass := func(gradeID, code string) string {
		class := e2ePostJSON(t, router, http.MethodPost, "/api/v1/classes", schoolAdmin, `{"school_id":"`+schoolID+`","grade_id":"`+gradeID+`","name":"Synthetic class","code":"`+code+`"}`, http.StatusCreated)["class"].(map[string]any)
		return e2eString(t, class, "id")
	}
	oldClass, emptyClass := createClass(older, "old"), createClass(current, "empty")
	student := e2ePostJSON(t, router, http.MethodPost, "/api/v1/students", schoolAdmin, `{"school_id":"`+schoolID+`","class_id":"`+oldClass+`","name":"Synthetic cohort student","student_no":"S-`+suffix+`","password":"SyntheticStudent123!","password_confirm":"SyntheticStudent123!"}`, http.StatusCreated)["student"].(map[string]any)
	studentID := e2eString(t, student, "id")
	studentToken := e2eLoginProvisionedStudent(t, router, e2eString(t, school, "code"), e2eString(t, student, "student_no"), "SyntheticStudent123!")
	e2ePostJSON(t, router, http.MethodPatch, "/api/v1/students/"+studentID, schoolAdmin, `{"status":"inactive"}`, http.StatusOK)
	// 停用名册必须同时撤销该学生刚完成首改的真实 HTTP 会话。
	e2eExpectStatus(t, router, http.MethodGet, "/api/v1/auth/me", studentToken, "", http.StatusUnauthorized)
	// UI按届别聚合；旧年度班级和停用学生都必须由后端阻止删除，不能只检查当前代表记录。
	e2eExpectStatus(t, router, http.MethodDelete, "/api/v1/grades/"+current, schoolAdmin, "", http.StatusConflict)
	e2eExpectStatus(t, router, http.MethodDelete, "/api/v1/classes/"+oldClass, schoolAdmin, "", http.StatusConflict)
	e2eExpectStatus(t, router, http.MethodDelete, "/api/v1/classes/"+emptyClass, schoolAdmin, "", http.StatusNoContent)
	e2eExpectStatus(t, router, http.MethodPatch, "/api/v1/grades/"+current, schoolAdmin, `{"level_no":12}`, http.StatusBadRequest)
	configureYear(schoolID, year+1)
	updated := e2ePostJSON(t, router, http.MethodPatch, "/api/v1/grades/"+current, schoolAdmin, `{"level_no":12}`, http.StatusOK)["grade"].(map[string]any)
	if updated["entry_year"] != float64(year-1) || updated["level_no"] != float64(12) {
		t.Fatalf("manual level change must preserve cohort entry year: %#v", updated)
	}
	e2eExpectStatus(t, router, http.MethodPatch, "/api/v1/grades/"+current, schoolAdmin, `{"entry_year":2025}`, http.StatusBadRequest)
	otherSchool := e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", admin, `{"name":"Other synthetic school","code":"other-`+suffix+`"}`, http.StatusCreated)["school"].(map[string]any)
	configureYear(e2eString(t, otherSchool, "id"), year+1)
	foreignGrade := e2ePostJSON(t, router, http.MethodPost, "/api/v1/grades", admin, fmt.Sprintf(`{"school_id":%q,"name":"高一","entry_year":%d,"education_stage":"senior","level_no":10}`, e2eString(t, otherSchool, "id"), year+1), http.StatusCreated)["grade"].(map[string]any)
	e2eExpectStatus(t, router, http.MethodDelete, "/api/v1/grades/"+e2eString(t, foreignGrade, "id"), schoolAdmin, "", http.StatusForbidden)
	targetGrade := createGrade(year+1, year, 11)
	targetClass := createClass(e2eString(t, targetGrade, "id"), "target")
	e2ePostJSON(t, router, http.MethodPut, "/api/v1/students/"+studentID+"/enrollment", schoolAdmin, fmt.Sprintf(`{"class_id":%q,"start_date":%q}`, targetClass, time.Now().UTC().Add(24*time.Hour).Format("2006-01-02")), http.StatusOK)
	e2eExpectStatus(t, router, http.MethodDelete, "/api/v1/classes/"+oldClass, schoolAdmin, "", http.StatusNoContent)
	e2eExpectStatus(t, router, http.MethodDelete, "/api/v1/grades/"+current, schoolAdmin, "", http.StatusNoContent)
	var remainingGrades, historicalEnrollments, historicalClasses int
	if err := db.QueryRow(`SELECT count(*) FROM grade WHERE id IN ($1::uuid,$2::uuid) AND deleted_at IS NULL`, older, current).Scan(&remainingGrades); err != nil || remainingGrades != 0 {
		t.Fatalf("empty cohort must disappear including old annual records: count=%d err=%v", remainingGrades, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM student_enrollment WHERE student_id=$1::uuid AND class_id=$2::uuid`, studentID, oldClass).Scan(&historicalEnrollments); err != nil || historicalEnrollments == 0 {
		t.Fatalf("transferred enrollment history must remain: count=%d err=%v", historicalEnrollments, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM school_class WHERE id=$1::uuid AND deleted_at IS NOT NULL`, oldClass).Scan(&historicalClasses); err != nil || historicalClasses != 1 {
		t.Fatalf("deleted class must retain historical identity: count=%d err=%v", historicalClasses, err)
	}
}
