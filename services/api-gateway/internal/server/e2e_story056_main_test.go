package server

import (
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/score"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type story056AcceptanceFixture struct {
	TenantID            string
	AdminID             string
	SchoolID            string
	ClassID             string
	StudentIDs          []string
	ExamID              string
	PaperFileAssetID    string
	TemplateID          string
	TemplateContentHash string
	QuestionIDs         map[string]string
}

type story056WorkerLease struct {
	TaskID   string
	OMRRunID string
	Token    string
}

// 此验收套件使用 EDUGRADE_E2E_DATABASE_URL 指定的隔离数据库，验证持久化流程协调。
// 像素级 OMR 决策质量仍由 worker 的合成图像验收套件覆盖。
func TestStory056ObjectiveScoringRecoveryE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping STORY-056 PostgreSQL acceptance workflow")
	}

	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin", "grader"})
	router := e2ePostgresRouter(db)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	graderID := e2eLookupUserID(t, db, "demo", "grader")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, adminToken, suffix, story056AcceptanceAnswerCount)
	e2eBindStory056ReviewActor(t, db, fixture, graderID)
	graderToken := e2eLoginWithTenant(t, router, "demo", "grader", "ChangeMe123!")

	missingSegments := e2eStory062ScoringReadiness(t, router, adminToken, fixture.ExamID)
	e2eAssertStory062ScoringReadiness(t, missingSegments, false, 3, 0, 0, 0)
	e2eAssertStory062ReadinessCheck(t, missingSegments, "answer_segments_present", false, "blocker")
	blockedStart := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+fixture.ExamID+"/scoring-runs", adminToken, story056JSON(t, map[string]any{
		"idempotency_key": "story062-before-segments-" + suffix,
	}), http.StatusConflict)
	e2eAssertStory062ReadinessError(t, blockedStart, "answer_segments_present")

	e2eSeedStory056AcceptanceAnswers(t, db, fixture, suffix)
	ready := e2eStory062ScoringReadiness(t, router, adminToken, fixture.ExamID)
	e2eAssertStory062ScoringReadiness(t, ready, true, 3, story056AcceptanceAnswerCount, story056AcceptanceAnswerCount, 0)
	e2eAssertStory062ReadinessCheck(t, ready, "active_run_clear", true, "blocker")

	cancelledRun := e2eStartStory056Run(t, router, adminToken, fixture.ExamID, "story056-cancel-"+suffix)
	e2eAssertStory056Run(t, cancelledRun, story056RunExpectation{Status: "processing", Total: story056AcceptanceAnswerCount, Queued: story056AcceptanceAnswerCount, AutoConfirmed: 0, HumanConfirmed: 0, Review: 0, Failed: 0})
	cancelledID := e2eString(t, cancelledRun, "id")
	cancelResponse := e2ePostJSON(t, router, http.MethodPost, "/api/v1/scoring-runs/"+cancelledID+"/cancel", adminToken, `{}`, http.StatusOK)
	e2eAssertStory056Run(t, cancelResponse["scoring_run"].(map[string]any), story056RunExpectation{Status: "cancelled", Total: story056AcceptanceAnswerCount, Queued: 0, AutoConfirmed: 0, HumanConfirmed: 0, Review: 0, Failed: 0})
	e2eAssertStory056CancelledWork(t, db, fixture.TenantID, cancelledID, story056AcceptanceAnswerCount)
	readyAfterCancellation := e2eStory062ScoringReadiness(t, router, adminToken, fixture.ExamID)
	e2eAssertStory062ScoringReadiness(t, readyAfterCancellation, true, 3, story056AcceptanceAnswerCount, story056AcceptanceAnswerCount, 0)

	run := e2eStartStory056Run(t, router, adminToken, fixture.ExamID, "story056-acceptance-"+suffix)
	e2eAssertStory056Run(t, run, story056RunExpectation{Status: "processing", Total: story056AcceptanceAnswerCount, Queued: story056AcceptanceAnswerCount, AutoConfirmed: 0, HumanConfirmed: 0, Review: 0, Failed: 0})
	runID := e2eString(t, run, "id")
	e2eEnableStory056AutoConfirmation(t, db, fixture.TenantID, runID)
	activeRunReadiness := e2eStory062ScoringReadiness(t, router, adminToken, fixture.ExamID)
	e2eAssertStory062ScoringReadiness(t, activeRunReadiness, false, 3, story056AcceptanceAnswerCount, story056AcceptanceAnswerCount, 0)
	e2eAssertStory062ReadinessCheck(t, activeRunReadiness, "active_run_clear", false, "blocker")
	overlappingStart := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+fixture.ExamID+"/scoring-runs", adminToken, story056JSON(t, map[string]any{
		"idempotency_key": "story056-overlapping-run-" + suffix,
	}), http.StatusConflict)
	e2eAssertStory062ReadinessError(t, overlappingStart, "active_run_clear")

	detail := e2eGetJSON(t, router, "/api/v1/scoring-runs/"+runID, adminToken, http.StatusOK)
	items := e2eStory056RunItems(t, detail)
	if len(items) != story056AcceptanceAnswerCount {
		t.Fatalf("scoring run should expose %d scheduled answer regions, got %d", story056AcceptanceAnswerCount, len(items))
	}
	e2eAssertStory056AnonymousItems(t, items)

	leases := e2eClaimStory056OMRTasks(t, router, adminToken, story056AcceptanceAnswerCount, "story056-worker", "initial")
	if len(leases) != story056AcceptanceAnswerCount {
		t.Fatalf("expected %d claimed OMR tasks, got %d", story056AcceptanceAnswerCount, len(leases))
	}

	failedItem := items[0]
	failedOMRRunID := e2eString(t, failedItem, "omr_run_id")
	failedLease, ok := leases[failedOMRRunID]
	if !ok {
		t.Fatalf("missing worker lease for OMR run %s", failedOMRRunID)
	}
	e2ePostWorkerJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+failedOMRRunID+"/failure", adminToken, story056JSON(t, map[string]any{
		"task_id":      failedLease.TaskID,
		"lease_token":  failedLease.Token,
		"retryable":    false,
		"error_code":   "story056_synthetic_terminal_failure",
		"error_detail": map[string]any{"case": "recovery_acceptance"},
		"duration_ms":  2,
	}), http.StatusOK)

	retryResponse := e2ePostJSON(t, router, http.MethodPost, "/api/v1/scoring-runs/"+runID+"/retry-failed", adminToken, `{}`, http.StatusOK)
	if e2eFloat(t, retryResponse, "requeued") != 1 || e2eFloat(t, retryResponse, "skipped") != 0 {
		t.Fatalf("failed OMR run should be requeued once: %#v", retryResponse)
	}
	retryRun := retryResponse["scoring_run"].(map[string]any)
	if retryRun["status"] != "processing" {
		t.Fatalf("retry should restore a processing scoring run: %#v", retryRun)
	}

	retryLeases := e2eClaimStory056OMRTasks(t, router, adminToken, 1, "story056-worker", "retry")
	if len(retryLeases) != 1 {
		t.Fatalf("expected exactly one retried OMR task, got %#v", retryLeases)
	}
	leases[failedOMRRunID] = retryLeases[failedOMRRunID]

	started := time.Now()
	fileStore := files.NewPostgresStore(db)
	for index, item := range items {
		omrRunID := e2eString(t, item, "omr_run_id")
		lease, ok := leases[omrRunID]
		if !ok {
			t.Fatalf("missing completion lease for OMR run %s", omrRunID)
		}
		overlay := e2eCreateStory056Overlay(t, fileStore, fixture, omrRunID, index)
		selected := e2eStory056SelectedOptions(t, e2eString(t, item, "question_type"))
		e2ePostWorkerJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+omrRunID+"/result", adminToken, story056JSON(t, map[string]any{
			"task_id":               lease.TaskID,
			"lease_token":           lease.Token,
			"result_version":        "story056-acceptance-v1-" + omrRunID,
			"duration_ms":           1,
			"decision":              "selected",
			"selected":              selected,
			"confidence":            0.99,
			"needs_human_review":    false,
			"measurements":          []map[string]any{},
			"profile_version":       "opencv-fill-v1",
			"profile_hash":          e2eStory056ProfileHash(),
			"thresholds":            map[string]any{"marked_threshold": 0.18, "minimum_margin": 0.06},
			"overlay_file_asset_id": overlay.ID,
			"overlay_sha256":        overlay.HashSHA256,
		}), http.StatusOK)
	}
	t.Logf("completed %d durable OMR result transitions in %s", story056AcceptanceAnswerCount, time.Since(started).Round(time.Millisecond))

	reviewDetail := e2eGetJSON(t, router, "/api/v1/scoring-runs/"+runID, adminToken, http.StatusOK)
	reviewRun := reviewDetail["scoring_run"].(map[string]any)
	e2eAssertStory056Run(t, reviewRun, story056RunExpectation{Status: "needs_review", Total: story056AcceptanceAnswerCount, Queued: 0, AutoConfirmed: story056AcceptanceAutoConfirmedCount, HumanConfirmed: 0, Review: story056AcceptanceHumanReviewCount, Failed: 0})
	e2eAssertStory056ItemStates(t, e2eStory056RunItems(t, reviewDetail), story056AcceptanceAutoConfirmedCount, story056AcceptanceHumanReviewCount)
	e2eCompleteStory056HumanReviews(t, db, router, story056ReviewActors{AdminToken: adminToken, GraderToken: graderToken, GraderID: graderID}, fixture, story056AcceptanceHumanReviewCount)

	completedDetail := e2eGetJSON(t, router, "/api/v1/scoring-runs/"+runID, adminToken, http.StatusOK)
	completedRun := completedDetail["scoring_run"].(map[string]any)
	e2eAssertStory056Run(t, completedRun, story056RunExpectation{Status: "completed", Total: story056AcceptanceAnswerCount, Queued: 0, AutoConfirmed: story056AcceptanceAutoConfirmedCount, HumanConfirmed: story056AcceptanceHumanReviewCount, Review: 0, Failed: 0})
	completedItems := e2eStory056RunItems(t, completedDetail)
	e2eAssertStory056ItemStates(t, completedItems, story056AcceptanceAnswerCount, 0)
	e2eAssertStory056ConfirmedScores(t, db, fixture.TenantID, runID, story056AcceptanceAutoConfirmedCount, story056AcceptanceHumanReviewCount)
	e2eReprocessAndFinalizeStory056(t, db, router, adminToken, fixture, suffix, completedItems)
}

// 自动题在汇总前可以重做；已有终评后必须改走正式更正。
// 此负载夹具每人只回答一题，汇总能形成题分，但必须因正式三题未全覆盖而阻止确认发布。
func e2eReprocessAndFinalizeStory056(t *testing.T, db *sql.DB, router http.Handler, adminToken string, fixture story056AcceptanceFixture, suffix string, completedItems []map[string]any) {
	t.Helper()
	fileStore := files.NewPostgresStore(db)
	segmentID := ""
	for _, item := range completedItems {
		if e2eString(t, item, "question_type") == "single_choice" {
			segmentID = e2eString(t, item, "answer_segment_id")
			break
		}
	}
	if segmentID == "" {
		t.Fatal("no automatic single-choice answer for reprocess acceptance")
	}
	reprocessKey := "story056-reprocess-" + suffix
	reprocessResponse := e2ePostJSON(t, router, http.MethodPost, "/api/v1/answer-segments/"+segmentID+"/reprocess-score", adminToken, story056JSON(t, map[string]any{
		"idempotency_key": reprocessKey,
	}), http.StatusCreated)
	reprocessRun := reprocessResponse["scoring_run"].(map[string]any)
	reprocessID := e2eString(t, reprocessRun, "id")
	e2eAssertStory056Run(t, reprocessRun, story056RunExpectation{Status: "processing", Total: 1, Queued: 1, AutoConfirmed: 0, HumanConfirmed: 0, Review: 0, Failed: 0})
	e2eEnableStory056AutoConfirmation(t, db, fixture.TenantID, reprocessID)
	idempotentReprocess := e2ePostJSON(t, router, http.MethodPost, "/api/v1/answer-segments/"+segmentID+"/reprocess-score", adminToken, story056JSON(t, map[string]any{
		"idempotency_key": reprocessKey,
	}), http.StatusCreated)["scoring_run"].(map[string]any)
	if e2eString(t, idempotentReprocess, "id") != reprocessID {
		t.Fatalf("single-answer reprocess should be idempotent: first=%s second=%#v", reprocessID, idempotentReprocess)
	}

	reprocessDetail := e2eGetJSON(t, router, "/api/v1/scoring-runs/"+reprocessID, adminToken, http.StatusOK)
	reprocessItems := e2eStory056RunItems(t, reprocessDetail)
	if len(reprocessItems) != 1 || e2eString(t, reprocessItems[0], "answer_segment_id") != segmentID {
		t.Fatalf("single-answer reprocess detail must remain scoped to its answer region: %#v", reprocessItems)
	}
	reprocessOMRRunID := e2eString(t, reprocessItems[0], "omr_run_id")
	reprocessLeases := e2eClaimStory056OMRTasks(t, router, adminToken, 1, "story056-worker", "reprocess")
	reprocessLease, ok := reprocessLeases[reprocessOMRRunID]
	if !ok {
		t.Fatalf("reprocess task was not claimable: %#v", reprocessLeases)
	}
	overlay := e2eCreateStory056Overlay(t, fileStore, fixture, reprocessOMRRunID, story056AcceptanceAnswerCount+1)
	e2ePostWorkerJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+reprocessOMRRunID+"/result", adminToken, story056JSON(t, map[string]any{
		"task_id":               reprocessLease.TaskID,
		"lease_token":           reprocessLease.Token,
		"result_version":        "story056-reprocess-v1-" + reprocessOMRRunID,
		"duration_ms":           1,
		"decision":              "selected",
		"selected":              e2eStory056SelectedOptions(t, e2eString(t, reprocessItems[0], "question_type")),
		"confidence":            0.99,
		"needs_human_review":    false,
		"measurements":          []map[string]any{},
		"profile_version":       "opencv-fill-v1",
		"profile_hash":          e2eStory056ProfileHash(),
		"thresholds":            map[string]any{"marked_threshold": 0.18, "minimum_margin": 0.06},
		"overlay_file_asset_id": overlay.ID,
		"overlay_sha256":        overlay.HashSHA256,
	}), http.StatusOK)

	finishedReprocess := e2eGetJSON(t, router, "/api/v1/scoring-runs/"+reprocessID, adminToken, http.StatusOK)
	e2eAssertStory056Run(t, finishedReprocess["scoring_run"].(map[string]any), story056RunExpectation{Status: "completed", Total: 1, Queued: 0, AutoConfirmed: 1, HumanConfirmed: 0, Review: 0, Failed: 0})
	e2eAssertStory056ReprocessVersion(t, db, fixture.TenantID, segmentID, reprocessID)
	finals, err := score.NewPostgresStore(db).FinalizeExam(t.Context(), fixture.TenantID, fixture.ExamID, fixture.AdminID)
	if err != nil || finals.CreatedFinals != story056AcceptanceAnswerCount || finals.Quality.Passed {
		t.Fatalf("partial-question fixture must produce finals while blocking publication: created=%d quality=%+v err=%v", finals.CreatedFinals, finals.Quality, err)
	}
	coverageBlocked := false
	for _, issue := range finals.Quality.Issues {
		if issue.Code == "question_coverage_incomplete" && issue.Blocking && issue.Count == story056AcceptanceAnswerCount {
			coverageBlocked = true
		}
	}
	if !coverageBlocked {
		t.Fatalf("missing official-question coverage blocker: %+v", finals.Quality)
	}
	repeated, err := score.NewPostgresStore(db).FinalizeExam(t.Context(), fixture.TenantID, fixture.ExamID, fixture.AdminID)
	if err != nil || repeated.CreatedFinals != 0 || repeated.Quality.Passed {
		t.Fatalf("repeated partial finalization must not duplicate grades or become publishable: created=%d quality=%+v err=%v", repeated.CreatedFinals, repeated.Quality, err)
	}
	e2eExpectStatus(t, router, http.MethodPost, "/api/v1/answer-segments/"+segmentID+"/reprocess-score", adminToken, story056JSON(t, map[string]any{"idempotency_key": "after-final-" + suffix}), http.StatusConflict)
}
