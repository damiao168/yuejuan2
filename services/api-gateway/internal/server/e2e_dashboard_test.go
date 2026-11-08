package server

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDashboardSummaryE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping PostgreSQL dashboard workflow")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	e2eActivatePostgresUsers(t, db, "platform", []string{"platform_admin"})
	router := e2ePostgresRouter(db)
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	platformToken := e2eLoginWithTenant(t, router, "platform", "platform_admin", "ChangeMe123!")
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	school := e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", adminToken,
		`{"name":"Dashboard School","code":"dashboard-`+suffix+`"}`, http.StatusCreated)["school"].(map[string]any)
	schoolID := e2eString(t, school, "id")
	grade := e2ePostJSON(t, router, http.MethodPost, "/api/v1/grades", adminToken,
		`{"school_id":"`+schoolID+`","name":"Dashboard Grade","level_no":11}`, http.StatusCreated)["grade"].(map[string]any)
	gradeID := e2eString(t, grade, "id")
	class := e2ePostJSON(t, router, http.MethodPost, "/api/v1/classes", adminToken,
		`{"school_id":"`+schoolID+`","grade_id":"`+gradeID+`","name":"Dashboard Class","code":"dashboard-class-`+suffix+`"}`, http.StatusCreated)["class"].(map[string]any)
	classID := e2eString(t, class, "id")
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/students", adminToken,
		`{"school_id":"`+schoolID+`","class_id":"`+classID+`","student_no":"dashboard-student-`+suffix+`","name":"Dashboard Student","password":"SyntheticStudent123!","password_confirm":"SyntheticStudent123!"}`, http.StatusCreated)

	for index := 0; index < 11; index++ {
		e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams", adminToken,
			`{"school_id":"`+schoolID+`","name":"Dashboard Exam `+strconv.Itoa(index+1)+`","subject":"math","exam_type":"midterm","total_score":100,"grading_mode":"human_review_required","appeal_enabled":true,"publish_policy":"manual_after_confirmation","class_ids":[]}`,
			http.StatusCreated)
	}

	summary := e2eGetJSON(t, router, "/api/v1/dashboard/summary", adminToken, http.StatusOK)
	statistics := summary["statistics"].(map[string]any)
	if int(statistics["active_exam_count"].(float64)) != 11 {
		t.Fatalf("dashboard must aggregate all 11 exams: %#v", statistics)
	}
	if len(summary["active_exams"].([]any)) != 11 {
		t.Fatalf("dashboard active exam rows were sampled: %#v", summary["active_exams"])
	}
	if summary["updated_at"] == "" {
		t.Fatal("dashboard summary must expose updated_at")
	}
	organizationStatistics := summary["organization_statistics"].(map[string]any)
	if int(organizationStatistics["active_student_count"].(float64)) < 1 || int(organizationStatistics["grade_count"].(float64)) < 1 || int(organizationStatistics["class_count"].(float64)) < 1 {
		t.Fatalf("dashboard must aggregate organization data in PostgreSQL: %#v", organizationStatistics)
	}

	e2eGetJSON(t, router, "/api/v1/dashboard/summary", platformToken, http.StatusForbidden)
}
