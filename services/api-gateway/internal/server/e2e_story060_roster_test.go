package server

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/score"
)

func TestStory060RosterReconciliationAndAbsenceE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping STORY-060 roster PostgreSQL acceptance workflow")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	adminID := e2eLookupUserID(t, db, "demo", "tenant_admin")
	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")

	school := e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", adminToken, `{"name":"STORY-060 Roster School","code":"s060-`+suffix+`"}`, http.StatusCreated)["school"].(map[string]any)
	schoolID := e2eString(t, school, "id")
	grade := e2ePostJSON(t, router, http.MethodPost, "/api/v1/grades", adminToken, `{"school_id":"`+schoolID+`","name":"STORY-060 Grade","level_no":9,"academic_year":"2026"}`, http.StatusCreated)["grade"].(map[string]any)
	gradeID := e2eString(t, grade, "id")
	class := e2ePostJSON(t, router, http.MethodPost, "/api/v1/classes", adminToken, `{"school_id":"`+schoolID+`","grade_id":"`+gradeID+`","name":"STORY-060 Class","code":"c060-`+suffix+`"}`, http.StatusCreated)["class"].(map[string]any)
	classID := e2eString(t, class, "id")
	transferClass := e2ePostJSON(t, router, http.MethodPost, "/api/v1/classes", adminToken, `{"school_id":"`+schoolID+`","grade_id":"`+gradeID+`","name":"STORY-060 Transfer Class","code":"c060-transfer-`+suffix+`"}`, http.StatusCreated)["class"].(map[string]any)
	transferClassID := e2eString(t, transferClass, "id")

	studentIDs := make([]string, 0, 3)
	for index, name := range []string{"Roster Graded", "Roster Absent One", "Roster Absent Two"} {
		studentNo := fmt.Sprintf("S060-%s-%d", suffix, index+1)
		student := e2ePostJSON(t, router, http.MethodPost, "/api/v1/students", adminToken,
			`{"school_id":"`+schoolID+`","class_id":"`+classID+`","student_no":"`+studentNo+`","name":"`+name+`"}`,
			http.StatusCreated)["student"].(map[string]any)
		studentIDs = append(studentIDs, e2eString(t, student, "id"))
	}
	exam := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams", adminToken,
		`{"school_id":"`+schoolID+`","name":"STORY-060 Roster Exam","subject":"Math","exam_type":"mock","total_score":100,"grading_mode":"human_review_required","appeal_enabled":true,"publish_policy":"manual_after_confirmation","class_ids":["`+classID+`"]}`,
		http.StatusCreated)["exam"].(map[string]any)
	examID := e2eString(t, exam, "id")

	gradedSubmission := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/submissions", adminToken,
		`{"student_id":"`+studentIDs[0]+`","candidate_no":"S060-GRADED-`+suffix+`","source_type":"pdf_upload","expected_page_count":0}`,
		http.StatusCreated)["submission"].(map[string]any)
	gradedSubmissionID := e2eString(t, gradedSubmission, "id")
	unknownSubmission := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/submissions", adminToken,
		`{"candidate_no":"S060-UNKNOWN-`+suffix+`","source_type":"pdf_upload","expected_page_count":0}`,
		http.StatusCreated)["submission"].(map[string]any)
	unknownSubmissionID := e2eString(t, unknownSubmission, "id")

	var tenantID string
	if err := db.QueryRow(`SELECT id::text FROM tenant WHERE code='demo' AND deleted_at IS NULL`).Scan(&tenantID); err != nil {
		t.Fatalf("lookup tenant: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO submission_grade (
  tenant_id, exam_id, submission_id, student_id, anonymous_code,
  total_score, max_score, status, locked, confirmed_by, confirmed_at, created_by
)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, 88, 100, 'confirmed', false, $6::uuid, now(), $6::uuid)
`, tenantID, examID, gradedSubmissionID, studentIDs[0], "S060-GRADED-"+suffix, adminID); err != nil {
		t.Fatalf("seed confirmed grade: %v", err)
	}
	if _, err := db.Exec(`
UPDATE student
SET class_id=$3::uuid, updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid
`, tenantID, studentIDs[0], transferClassID); err != nil {
		t.Fatalf("transfer student after the exam roster was frozen: %v", err)
	}
	if _, err := db.Exec(`
UPDATE student_enrollment
SET class_id=$3::uuid, updated_at=now()
WHERE tenant_id=$1::uuid AND student_id=$2::uuid AND status='enrolled' AND deleted_at IS NULL
`, tenantID, studentIDs[0], transferClassID); err != nil {
		t.Fatalf("transfer active enrollment after the exam roster was frozen: %v", err)
	}

	roster := e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/roster", adminToken, http.StatusOK)["roster"].(map[string]any)
	summary := roster["summary"].(map[string]any)
	if e2eFloat(t, summary, "expected") != 3 || e2eFloat(t, summary, "received") != 2 || e2eFloat(t, summary, "unidentified") != 1 {
		t.Fatalf("initial roster summary mismatch: %#v", summary)
	}
	quality := e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/grades/quality?stage=publish", adminToken, http.StatusOK)
	e2eAssertStory060QualityIssue(t, quality, "missing_submission_unresolved", 2)
	e2eAssertStory060QualityIssue(t, quality, "unidentified_submission", 1)

	for _, studentID := range studentIDs[1:] {
		e2ePostJSON(t, router, http.MethodPut, "/api/v1/exams/"+examID+"/roster/"+studentID+"/attendance", adminToken,
			`{"status":"absent","reason":"STORY-060 verified absence"}`, http.StatusOK)
	}
	quality = e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/grades/quality?stage=publish", adminToken, http.StatusOK)
	if story060HasQualityIssue(quality, "missing_submission_unresolved") {
		t.Fatalf("explicit absence should resolve missing submissions: %#v", quality)
	}
	e2eAssertStory060QualityIssue(t, quality, "unidentified_submission", 1)
	e2eExpectStatus(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/publish", adminToken, `{"reason":"unknown sheet still unresolved"}`, http.StatusConflict)

	if _, err := db.Exec(`UPDATE submission SET deleted_at=now(), updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, unknownSubmissionID); err != nil {
		t.Fatalf("resolve unidentified submission: %v", err)
	}
	quality = e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/grades/quality?stage=publish", adminToken, http.StatusOK)
	if quality["can_publish"] != true {
		t.Fatalf("resolved roster should pass publish quality gate: %#v", quality)
	}
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/publish", adminToken, `{"reason":"roster fully reconciled"}`, http.StatusOK)
	e2eExpectStatus(t, router, http.MethodPut, "/api/v1/exams/"+examID+"/roster/"+studentIDs[1]+"/attendance", adminToken,
		`{"status":"expected","reason":"too late after publish"}`, http.StatusConflict)

	var attendanceAuditCount int
	if err := db.QueryRow(`
SELECT COUNT(*) FROM audit_log
WHERE tenant_id=$1::uuid AND action='score.roster_attendance_updated' AND target_id IN ($2, $3)
`, tenantID, studentIDs[1], studentIDs[2]).Scan(&attendanceAuditCount); err != nil {
		t.Fatalf("query attendance audit: %v", err)
	}
	if attendanceAuditCount != 2 {
		t.Fatalf("expected two attendance audit events, got %d", attendanceAuditCount)
	}
}

func TestExamRosterFreezeBlocksUpdateAndDirectMutationE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping exam roster freeze PostgreSQL acceptance workflow")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	fixture := e2eCreateStory056MutableTemplateFixture(t, db, router, adminToken, suffix, nil)

	var gradeID string
	if err := db.QueryRow(`SELECT grade_id::text FROM school_class WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, fixture.ClassID).Scan(&gradeID); err != nil {
		t.Fatalf("lookup fixture grade: %v", err)
	}
	class := e2ePostJSON(t, router, http.MethodPost, "/api/v1/classes", adminToken, fmt.Sprintf(`{"school_id":"%s","grade_id":"%s","name":"Freeze Target %s","code":"freeze-target-%s"}`, fixture.SchoolID, gradeID, suffix, suffix), http.StatusCreated)["class"].(map[string]any)
	newClassID := e2eString(t, class, "id")
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+fixture.ExamID+"/readiness/confirm", adminToken, `{}`, http.StatusOK)

	var revision int64
	if err := db.QueryRow(`SELECT revision FROM exam WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, fixture.ExamID).Scan(&revision); err != nil {
		t.Fatalf("lookup exam revision: %v", err)
	}
	// 准备确认后，API 和数据库触发器都拒绝改动 exam_class，原冻结快照必须保持不变。
	e2ePostJSON(t, router, http.MethodPatch, "/api/v1/exams/"+fixture.ExamID, adminToken, fmt.Sprintf(`{"class_ids":["%s"],"expected_revision":%d}`, newClassID, revision), http.StatusConflict)
	if _, err := db.Exec(`DELETE FROM exam_class WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND class_id=$3::uuid`, fixture.TenantID, fixture.ExamID, fixture.ClassID); err == nil {
		t.Fatal("direct exam_class deletion bypassed roster freeze")
	}

	e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+fixture.ExamID+"/start-collection", adminToken, `{}`, http.StatusOK)
	e2ePostJSON(t, router, http.MethodPatch, "/api/v1/exams/"+fixture.ExamID, adminToken, fmt.Sprintf(`{"class_ids":["%s"],"expected_revision":%d}`, newClassID, revision), http.StatusConflict)
	var classCount, snapshotCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM exam_class WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL`, fixture.TenantID, fixture.ExamID).Scan(&classCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM exam_candidate_snapshot WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, fixture.TenantID, fixture.ExamID).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if classCount != 1 || snapshotCount != 1 {
		t.Fatalf("frozen roster changed: classes=%d snapshots=%d", classCount, snapshotCount)
	}
}

func TestStory060FiveHundredStudentRosterScaleE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping STORY-060 roster scale acceptance workflow")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	adminID := e2eLookupUserID(t, db, "demo", "tenant_admin")
	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")

	school := e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", adminToken, `{"name":"STORY-060 Scale School","code":"s060-scale-`+suffix+`"}`, http.StatusCreated)["school"].(map[string]any)
	schoolID := e2eString(t, school, "id")
	grade := e2ePostJSON(t, router, http.MethodPost, "/api/v1/grades", adminToken, `{"school_id":"`+schoolID+`","name":"STORY-060 Scale Grade","level_no":9,"academic_year":"2026"}`, http.StatusCreated)["grade"].(map[string]any)
	gradeID := e2eString(t, grade, "id")
	class := e2ePostJSON(t, router, http.MethodPost, "/api/v1/classes", adminToken, `{"school_id":"`+schoolID+`","grade_id":"`+gradeID+`","name":"STORY-060 Scale Class","code":"c060-scale-`+suffix+`"}`, http.StatusCreated)["class"].(map[string]any)
	classID := e2eString(t, class, "id")
	var tenantID string
	if err := db.QueryRow(`SELECT id::text FROM tenant WHERE code='demo' AND deleted_at IS NULL`).Scan(&tenantID); err != nil {
		t.Fatalf("lookup tenant: %v", err)
	}

	if _, err := db.Exec(`
WITH inserted_students AS (
  INSERT INTO student (tenant_id, school_id, class_id, student_no, name, status)
  SELECT $1::uuid, $2::uuid, $3::uuid,
    'S060-SCALE-' || $4 || '-' || lpad(n::text, 3, '0'),
    'Scale Student ' || lpad(n::text, 3, '0'), 'active'
  FROM generate_series(1, 499) AS n
  RETURNING id, student_no
)
INSERT INTO student_enrollment (
  tenant_id, school_id, student_id, academic_year_id, grade_cohort_id, class_id, status, start_date
)
SELECT $1::uuid, $2::uuid, inserted_students.id, cls.academic_year_id, cls.grade_cohort_id, cls.id, 'enrolled', ay.starts_at
FROM inserted_students
JOIN school_class cls ON cls.tenant_id=$1::uuid AND cls.id=$3::uuid
JOIN academic_year ay ON ay.tenant_id=cls.tenant_id AND ay.id=cls.academic_year_id
`, tenantID, schoolID, classID, suffix); err != nil {
		t.Fatalf("seed initial 499-student enrollment roster: %v", err)
	}

	exam := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams", adminToken,
		`{"school_id":"`+schoolID+`","name":"STORY-060 500 Student Exam","subject":"Math","exam_type":"mock","total_score":100,"grading_mode":"human_review_required","appeal_enabled":true,"publish_policy":"manual_after_confirmation","class_ids":["`+classID+`"]}`,
		http.StatusCreated)["exam"].(map[string]any)
	examID := e2eString(t, exam, "id")

	if _, err := db.Exec(`
WITH inserted_student AS (
  INSERT INTO student (tenant_id, school_id, class_id, student_no, name, status)
  VALUES ($1::uuid, $2::uuid, $3::uuid, 'S060-SCALE-' || $4 || '-500', 'Scale Student 500', 'active')
  RETURNING id
)
INSERT INTO student_enrollment (
  tenant_id, school_id, student_id, academic_year_id, grade_cohort_id, class_id, status, start_date
)
SELECT $1::uuid, $2::uuid, inserted_student.id, cls.academic_year_id, cls.grade_cohort_id, cls.id, 'enrolled', ay.starts_at
FROM inserted_student
JOIN school_class cls ON cls.tenant_id=$1::uuid AND cls.id=$3::uuid
JOIN academic_year ay ON ay.tenant_id=cls.tenant_id AND ay.id=cls.academic_year_id
`, tenantID, schoolID, classID, suffix); err != nil {
		t.Fatalf("seed late 500th student enrollment: %v", err)
	}
	refresh := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/candidates/refresh", adminToken, ``, http.StatusOK)["candidate_refresh"].(map[string]any)
	if e2eFloat(t, refresh, "before_count") != 499 || e2eFloat(t, refresh, "after_count") != 500 ||
		e2eFloat(t, refresh, "added_count") != 1 || e2eFloat(t, refresh, "removed_count") != 0 {
		t.Fatalf("candidate refresh should add the late enrollment: %#v", refresh)
	}

	if _, err := db.Exec(`
WITH inserted_submissions AS (
  INSERT INTO submission (
    tenant_id, exam_id, student_id, candidate_no, source_type, status,
    expected_page_count, actual_page_count, quality_status, collected_by
  )
  SELECT $1::uuid, $4::uuid, id, student_no, 'pdf_upload', 'created', 0, 0, 'unchecked', $5::uuid
  FROM student
  WHERE tenant_id=$1::uuid AND class_id=$2::uuid AND student_no LIKE 'S060-SCALE-' || $3 || '-%'
    AND right(student_no, 3)::int <= 497
  RETURNING id, student_id, candidate_no
)
INSERT INTO submission_grade (
  tenant_id, exam_id, submission_id, student_id, anonymous_code,
  total_score, max_score, status, locked, confirmed_by, confirmed_at, created_by
)
SELECT $1::uuid, $4::uuid, id, student_id, candidate_no,
  80, 100, 'confirmed', false, $5::uuid, now(), $5::uuid
FROM inserted_submissions
`, tenantID, classID, suffix, examID, adminID); err != nil {
		t.Fatalf("seed 497 roster submissions: %v", err)
	}

	const rosterReadBudget = 5 * time.Second
	storeStarted := time.Now()
	directRoster, err := score.NewPostgresStore(db).ListRoster(t.Context(), tenantID, examID)
	if err != nil {
		t.Fatalf("directly reconcile 500-student roster: %v", err)
	}
	if directRoster.Summary.Expected != 500 {
		t.Fatalf("direct roster expected 500 students: %#v", directRoster.Summary)
	}
	storeElapsed := time.Since(storeStarted)
	t.Logf("store reconciled 500 roster entries in %s (budget %s)", storeElapsed.Round(time.Millisecond), rosterReadBudget)
	if storeElapsed > rosterReadBudget {
		t.Fatalf("500-student roster store read exceeded performance baseline: %s > %s", storeElapsed, rosterReadBudget)
	}
	started := time.Now()
	roster := e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/roster", adminToken, http.StatusOK)["roster"].(map[string]any)
	summary := roster["summary"].(map[string]any)
	if e2eFloat(t, summary, "expected") != 500 || e2eFloat(t, summary, "received") != 497 ||
		e2eFloat(t, summary, "graded") != 497 || e2eFloat(t, summary, "unresolved") != 3 {
		t.Fatalf("500-student roster summary mismatch: %#v", summary)
	}
	httpElapsed := time.Since(started)
	t.Logf("HTTP reconciled 500 roster entries in %s (budget %s)", httpElapsed.Round(time.Millisecond), rosterReadBudget)
	if httpElapsed > rosterReadBudget {
		t.Fatalf("500-student roster HTTP read exceeded performance baseline: %s > %s", httpElapsed, rosterReadBudget)
	}
	quality := e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/grades/quality?stage=publish", adminToken, http.StatusOK)
	e2eAssertStory060QualityIssue(t, quality, "missing_submission_unresolved", 3)

	rows, err := db.Query(`
SELECT id::text FROM student
WHERE tenant_id=$1::uuid AND class_id=$2::uuid AND student_no LIKE $3
ORDER BY student_no DESC LIMIT 3
`, tenantID, classID, "S060-SCALE-"+suffix+"-%")
	if err != nil {
		t.Fatalf("query missing students: %v", err)
	}
	missingStudentIDs := []string{}
	for rows.Next() {
		var studentID string
		if err := rows.Scan(&studentID); err != nil {
			t.Fatalf("scan missing student: %v", err)
		}
		missingStudentIDs = append(missingStudentIDs, studentID)
	}
	_ = rows.Close()
	if len(missingStudentIDs) != 3 {
		t.Fatalf("expected three missing students, got %#v", missingStudentIDs)
	}
	for _, studentID := range missingStudentIDs {
		e2ePostJSON(t, router, http.MethodPut, "/api/v1/exams/"+examID+"/roster/"+studentID+"/attendance", adminToken,
			`{"status":"absent","reason":"verified absent in 500-student acceptance"}`, http.StatusOK)
	}
	roster = e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/roster", adminToken, http.StatusOK)["roster"].(map[string]any)
	summary = roster["summary"].(map[string]any)
	if e2eFloat(t, summary, "absent") != 3 || e2eFloat(t, summary, "unresolved") != 0 {
		t.Fatalf("explicit absence should reconcile the three missing students: %#v", summary)
	}
	quality = e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/grades/quality?stage=publish", adminToken, http.StatusOK)
	if quality["can_publish"] != true {
		t.Fatalf("500-student roster should pass after three explicit absences: %#v", quality)
	}
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/publish", adminToken, `{"reason":"500-student roster reconciled"}`, http.StatusOK)
}

func e2eAssertStory060QualityIssue(t *testing.T, response map[string]any, code string, count float64) {
	t.Helper()
	quality := response["quality"].(map[string]any)
	for _, raw := range quality["issues"].([]any) {
		issue := raw.(map[string]any)
		if issue["code"] == code {
			if e2eFloat(t, issue, "count") != count {
				t.Fatalf("quality issue %s count mismatch: %#v", code, issue)
			}
			return
		}
	}
	t.Fatalf("quality issue %s missing: %#v", code, response)
}

func story060HasQualityIssue(response map[string]any, code string) bool {
	quality, _ := response["quality"].(map[string]any)
	issues, _ := quality["issues"].([]any)
	for _, raw := range issues {
		issue, _ := raw.(map[string]any)
		if issue["code"] == code {
			return true
		}
	}
	return false
}
