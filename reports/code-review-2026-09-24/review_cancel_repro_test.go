package server

import (
    "net/http"
    "os"
    "testing"
    "time"

    "edugrade-enterprise/services/api-gateway/internal/files"
)

// Diagnostic reproduction: passes only when the reviewed cancellation defect
// is observed. It is not a regression assertion of desired behavior.
func TestAuditCancelledScoringRunCanBeResurrected(t *testing.T) {
    dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
    if dsn == "" { t.Fatal("isolated review database required") }
    db := e2eOpenPostgresTestDB(t, dsn)
    e2eApplyPostgresMigrations(t, db)
    e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin", "grader"})
    router := e2ePostgresRouter(db)
    admin := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
    grader := e2eLoginWithTenant(t, router, "demo", "grader", "ChangeMe123!")
    graderID := e2eLookupUserID(t, db, "demo", "grader")
    suffix := time.Now().UTC().Format("20060102150405.000000000")
    fixture := e2eCreateStory056AcceptanceFixture(t, db, router, admin, suffix)
    e2eSeedStory056AcceptanceAnswersWithQuestionTypes(t, db, fixture, suffix, 1, []string{"single_choice"})
    run := e2eStartStory056Run(t, router, admin, fixture.ExamID, "audit-cancel-"+suffix)
    runID := e2eString(t, run, "id")
    leases := e2eClaimStory056OMRTasks(t, router, admin, 1, "audit-worker", "audit")
    if len(leases) != 1 { t.Fatalf("expected one lease, got %d", len(leases)) }
    for omrID, lease := range leases {
        overlay := e2eCreateStory056Overlay(t, files.NewPostgresStore(db), fixture, omrID, 1)
        e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+omrID+"/result", admin,
            story056JSON(t, map[string]any{
                "task_id": lease.TaskID, "lease_token": lease.Token,
                "result_version": "audit-cancel-"+omrID, "duration_ms": 1,
                "decision": "selected", "selected": []string{"A"}, "confidence": 0.99,
                "needs_human_review": true, "measurements": []map[string]any{},
                "profile_version": "opencv-fill-v1", "profile_hash": e2eStory056ProfileHash(),
                "thresholds": map[string]any{"marked_threshold": 0.18, "minimum_margin": 0.06},
                "overlay_file_asset_id": overlay.ID, "overlay_sha256": overlay.HashSHA256,
            }), http.StatusOK)
    }
    cancelled := e2ePostJSON(t, router, http.MethodPost, "/api/v1/scoring-runs/"+runID+"/cancel", admin, `{}`, http.StatusOK)
    if cancelled["scoring_run"].(map[string]any)["status"] != "cancelled" { t.Fatal("cancellation failed") }
    var taskID, taskStatus string
    var revision int
    if err := db.QueryRow(`SELECT id::text,status,revision FROM review_task WHERE scoring_run_id=$1::uuid AND deleted_at IS NULL`, runID).Scan(&taskID, &taskStatus, &revision); err != nil { t.Fatal(err) }
    if taskStatus != "cancelled" { t.Fatalf("task must be cancelled, got %s", taskStatus) }
    assigned := e2ePostJSON(t, router, http.MethodPost, "/api/v1/review-tasks/"+taskID+"/assign", admin,
        story056JSON(t, map[string]any{"assigned_to": graderID, "expected_revision": revision}), http.StatusOK)
    task := assigned["task"].(map[string]any)
    e2ePostJSON(t, router, http.MethodPost, "/api/v1/review-tasks/"+taskID+"/submit", grader,
        story056JSON(t, map[string]any{"expected_revision": task["revision"], "score": 1,
            "rubric_selections": []any{}, "comments": "Synthetic audit reproduction", "reason": "verify cancellation invariant"}), http.StatusCreated)
    var runStatus string
    var gradeCount int
    if err := db.QueryRow(`SELECT status,(SELECT count(*) FROM question_grade WHERE scoring_run_id=$1::uuid AND is_current AND status='confirmed') FROM scoring_run WHERE id=$1::uuid`, runID).Scan(&runStatus, &gradeCount); err != nil { t.Fatal(err) }
    if runStatus != "completed" || gradeCount != 1 { t.Fatalf("defect not observed: run=%s current_grades=%d", runStatus, gradeCount) }
    t.Logf("DEFECT REPRODUCED: cancelled task reassigned via HTTP 200, submit HTTP 201, run cancelled->%s, current confirmed grades=%d", runStatus, gradeCount)
}
