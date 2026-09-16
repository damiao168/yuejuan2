package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/score"
)

const (
	story056AcceptanceAnswerCount        = 100
	story056AcceptanceAutoConfirmedCount = 67
	story056AcceptanceHumanReviewCount   = 33
)

type story056AcceptanceFixture struct {
	TenantID            string
	AdminID             string
	SchoolID            string
	ClassID             string
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

// This acceptance suite intentionally uses an isolated database named by
// EDUGRADE_E2E_DATABASE_URL. It verifies durable workflow coordination; the
// pixel-level OMR decision quality remains covered by the worker's synthetic
// image acceptance suite.
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
	graderToken := e2eLoginWithTenant(t, router, "demo", "grader", "ChangeMe123!")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, adminToken, suffix)

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
	e2eAssertStory056Run(t, cancelledRun, "processing", story056AcceptanceAnswerCount, story056AcceptanceAnswerCount, 0, 0, 0, 0)
	cancelledID := e2eString(t, cancelledRun, "id")
	cancelResponse := e2ePostJSON(t, router, http.MethodPost, "/api/v1/scoring-runs/"+cancelledID+"/cancel", adminToken, `{}`, http.StatusOK)
	e2eAssertStory056Run(t, cancelResponse["scoring_run"].(map[string]any), "cancelled", story056AcceptanceAnswerCount, 0, 0, 0, 0, 0)
	e2eAssertStory056CancelledWork(t, db, fixture.TenantID, cancelledID, story056AcceptanceAnswerCount)
	readyAfterCancellation := e2eStory062ScoringReadiness(t, router, adminToken, fixture.ExamID)
	e2eAssertStory062ScoringReadiness(t, readyAfterCancellation, true, 3, story056AcceptanceAnswerCount, story056AcceptanceAnswerCount, 0)

	run := e2eStartStory056Run(t, router, adminToken, fixture.ExamID, "story056-acceptance-"+suffix)
	e2eAssertStory056Run(t, run, "processing", story056AcceptanceAnswerCount, story056AcceptanceAnswerCount, 0, 0, 0, 0)
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
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+failedOMRRunID+"/failure", adminToken, story056JSON(t, map[string]any{
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
		e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+omrRunID+"/result", adminToken, story056JSON(t, map[string]any{
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
	e2eAssertStory056Run(t, reviewRun, "needs_review", story056AcceptanceAnswerCount, 0, story056AcceptanceAutoConfirmedCount, 0, story056AcceptanceHumanReviewCount, 0)
	e2eAssertStory056ItemStates(t, e2eStory056RunItems(t, reviewDetail), story056AcceptanceAutoConfirmedCount, story056AcceptanceHumanReviewCount)
	e2eCompleteStory056HumanReviews(t, db, router, adminToken, graderToken, graderID, fixture, story056AcceptanceHumanReviewCount)

	completedDetail := e2eGetJSON(t, router, "/api/v1/scoring-runs/"+runID, adminToken, http.StatusOK)
	completedRun := completedDetail["scoring_run"].(map[string]any)
	e2eAssertStory056Run(t, completedRun, "completed", story056AcceptanceAnswerCount, 0, story056AcceptanceAutoConfirmedCount, story056AcceptanceHumanReviewCount, 0, 0)
	completedItems := e2eStory056RunItems(t, completedDetail)
	e2eAssertStory056ItemStates(t, completedItems, story056AcceptanceAnswerCount, 0)
	e2eAssertStory056ConfirmedScores(t, db, fixture.TenantID, runID, story056AcceptanceAutoConfirmedCount, story056AcceptanceHumanReviewCount)
	finals, err := score.NewPostgresStore(db).FinalizeExam(t.Context(), fixture.TenantID, fixture.ExamID, fixture.AdminID)
	if err != nil || finals.CreatedFinals != story056AcceptanceAnswerCount || !finals.Quality.Passed {
		t.Fatalf("completed scoring must finalize every answer: %+v err=%v", finals, err)
	}
	repeated, err := score.NewPostgresStore(db).FinalizeExam(t.Context(), fixture.TenantID, fixture.ExamID, fixture.AdminID)
	if err != nil || repeated.CreatedFinals != 0 || !repeated.Quality.Passed {
		t.Fatalf("repeated finalization must not duplicate grades: %+v err=%v", repeated, err)
	}

	segmentID := e2eString(t, completedItems[0], "answer_segment_id")
	reprocessKey := "story056-reprocess-" + suffix
	reprocessResponse := e2ePostJSON(t, router, http.MethodPost, "/api/v1/answer-segments/"+segmentID+"/reprocess-score", adminToken, story056JSON(t, map[string]any{
		"idempotency_key": reprocessKey,
	}), http.StatusCreated)
	reprocessRun := reprocessResponse["scoring_run"].(map[string]any)
	reprocessID := e2eString(t, reprocessRun, "id")
	e2eAssertStory056Run(t, reprocessRun, "processing", 1, 1, 0, 0, 0, 0)
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
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+reprocessOMRRunID+"/result", adminToken, story056JSON(t, map[string]any{
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
	e2eAssertStory056Run(t, finishedReprocess["scoring_run"].(map[string]any), "completed", 1, 0, 1, 0, 0, 0)
	e2eAssertStory056ReprocessVersion(t, db, fixture.TenantID, segmentID, reprocessID)
}

func e2eCreateStory056AcceptanceFixture(t *testing.T, db *sql.DB, router http.Handler, adminToken string, suffix string) story056AcceptanceFixture {
	return e2eCreateStory056AcceptanceFixtureWithOMRProfile(t, db, router, adminToken, suffix, nil)
}

func e2eCreateStory056AcceptanceFixtureWithOMRProfile(t *testing.T, db *sql.DB, router http.Handler, adminToken string, suffix string, omrProfile map[string]any) story056AcceptanceFixture {
	return e2eCreateStory056AcceptanceFixtureWithLifecycle(t, db, router, adminToken, suffix, omrProfile, true)
}

func e2eCreateStory056MutableTemplateFixture(t *testing.T, db *sql.DB, router http.Handler, adminToken string, suffix string, omrProfile map[string]any) story056AcceptanceFixture {
	return e2eCreateStory056AcceptanceFixtureWithLifecycle(t, db, router, adminToken, suffix, omrProfile, false)
}

func e2eCreateStory056AcceptanceFixtureWithLifecycle(t *testing.T, db *sql.DB, router http.Handler, adminToken string, suffix string, omrProfile map[string]any, activateExam bool) story056AcceptanceFixture {
	t.Helper()
	adminID := e2eLookupUserID(t, db, "demo", "tenant_admin")
	var tenantID string
	if err := db.QueryRow(`SELECT id::text FROM tenant WHERE code='demo' AND deleted_at IS NULL`).Scan(&tenantID); err != nil {
		t.Fatalf("look up demo tenant: %v", err)
	}

	school := e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", adminToken, story056JSON(t, map[string]any{
		"name": "STORY-056 验收学校", "code": "story056-" + suffix,
	}), http.StatusCreated)["school"].(map[string]any)
	schoolID := e2eString(t, school, "id")
	grade := e2ePostJSON(t, router, http.MethodPost, "/api/v1/grades", adminToken, story056JSON(t, map[string]any{
		"school_id": schoolID, "name": "高一", "level_no": 10, "academic_year": "2026",
	}), http.StatusCreated)["grade"].(map[string]any)
	class := e2ePostJSON(t, router, http.MethodPost, "/api/v1/classes", adminToken, story056JSON(t, map[string]any{
		"school_id": schoolID, "grade_id": e2eString(t, grade, "id"), "name": "高一(1)班", "code": "story056-" + suffix,
	}), http.StatusCreated)["class"].(map[string]any)
	classID := e2eString(t, class, "id")
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/students", adminToken, story056JSON(t, map[string]any{
		"school_id": schoolID, "class_id": classID, "student_no": "S056-READY-" + suffix, "name": "验收学生",
	}), http.StatusCreated)

	exam := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams", adminToken, story056JSON(t, map[string]any{
		"school_id": schoolID, "name": "高一物理能力测试", "subject": "physics", "exam_type": "unit_test", "total_score": 3,
		"grading_mode": "auto_objective_only", "appeal_enabled": true, "publish_policy": "manual_after_confirmation", "class_ids": []string{classID},
	}), http.StatusCreated)["exam"].(map[string]any)
	examID := e2eString(t, exam, "id")
	paperFileID := e2eUploadSyntheticPDF(t, router, adminToken, "story056-paper-"+suffix+".pdf", "%PDF-1.4\n% story056 acceptance paper "+suffix+"\n")
	paper := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/papers", adminToken, story056JSON(t, map[string]any{
		"file_asset_id": paperFileID,
	}), http.StatusCreated)["paper"].(map[string]any)
	paperID := e2eString(t, paper, "id")

	questionIDs := map[string]string{}
	questionIDs["single_choice"] = e2eCreateStory056Question(t, router, adminToken, examID, paperID, "Q1", "single_choice", "A", 1)
	questionIDs["true_false"] = e2eCreateStory056Question(t, router, adminToken, examID, paperID, "Q2", "true_false", true, 2)
	questionIDs["multiple_choice"] = e2eCreateStory056Question(t, router, adminToken, examID, paperID, "Q3", "multiple_choice", []string{"A", "C"}, 3)

	for questionType, questionID := range questionIDs {
		config := map[string]any{}
		if questionType == "multiple_choice" {
			config["allow_partial"] = true
		}
		rule := e2ePostJSON(t, router, http.MethodPost, "/api/v1/questions/"+questionID+"/scoring-rules", adminToken, story056JSON(t, map[string]any{
			"rule_type": questionType, "config": config,
		}), http.StatusCreated)["scoring_rule"].(map[string]any)
		e2ePostJSON(t, router, http.MethodPost, "/api/v1/scoring-rules/"+e2eString(t, rule, "id")+"/publish", adminToken, `{}`, http.StatusOK)
	}

	layout := map[string]any{"pages": []any{map[string]any{
		"page_no": 1, "width": 1000, "height": 1400,
		"registration_marks": []any{}, "identity_regions": []any{},
		"question_regions": []any{
			e2eStory056QuestionRegion(questionIDs["single_choice"], 0.10, e2eStory056Options("A", "B", "C", "D")),
			e2eStory056QuestionRegion(questionIDs["true_false"], 0.35, e2eStory056Options("true", "false")),
			e2eStory056QuestionRegion(questionIDs["multiple_choice"], 0.60, e2eStory056Options("A", "B", "C", "D")),
		},
	}}}
	if omrProfile != nil {
		layout["omr_profile"] = omrProfile
	}
	template := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/answer-sheet-templates", adminToken, story056JSON(t, map[string]any{
		"exam_paper_id": paperID, "name": "高一综合能力测试答题卡", "page_count": 1, "layout": layout,
	}), http.StatusCreated)["template"].(map[string]any)
	templateID := e2eString(t, template, "id")
	locked := e2ePostJSON(t, router, http.MethodPost, "/api/v1/answer-sheet-templates/"+templateID+"/lock", adminToken, `{}`, http.StatusOK)["template"].(map[string]any)
	if activateExam {
		e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/readiness/confirm", adminToken, `{}`, http.StatusOK)
		e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/start-collection", adminToken, `{}`, http.StatusOK)
	}

	return story056AcceptanceFixture{
		TenantID: tenantID, AdminID: adminID, SchoolID: schoolID, ClassID: classID, ExamID: examID,
		PaperFileAssetID: paperFileID, TemplateID: templateID, TemplateContentHash: e2eString(t, locked, "content_hash"), QuestionIDs: questionIDs,
	}
}

func e2eCreateStory056Question(t *testing.T, router http.Handler, adminToken, examID, paperID, questionNo, questionType string, standardAnswer any, sortOrder int) string {
	t.Helper()
	question := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/questions", adminToken, story056JSON(t, map[string]any{
		"exam_paper_id": paperID, "question_no": questionNo, "question_type": questionType, "score": 1,
		"stem": "STORY-056 " + questionNo, "knowledge_points": []string{"story056"}, "answer_area": map[string]any{}, "sort_order": sortOrder,
		"answer_key": map[string]any{"standard_answer": standardAnswer, "equivalent_answers": []any{}, "tolerance": map[string]any{}},
	}), http.StatusCreated)["question"].(map[string]any)
	return e2eString(t, question, "id")
}

func e2eStory056QuestionRegion(questionID string, y float64, options []any) map[string]any {
	return map[string]any{"question_id": questionID, "x": 0.10, "y": y, "width": 0.70, "height": 0.16, "option_regions": options}
}

func e2eStory056Options(labels ...string) []any {
	options := make([]any, 0, len(labels))
	for index, label := range labels {
		options = append(options, map[string]any{
			"label": label, "x": 0.12 + float64(index)*0.14, "y": 0.02, "width": 0.08, "height": 0.08,
		})
	}
	return options
}

func e2eSeedStory056AcceptanceAnswers(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, suffix string) {
	e2eSeedStory056AcceptanceAnswersCount(t, db, fixture, suffix, story056AcceptanceAnswerCount)
}

// e2eSeedStory056AcceptanceAnswersCount keeps the PostgreSQL workflow tests
// focused: broad recovery coverage can seed the full acceptance volume, while
// transactional edge cases can make an unambiguous one-answer assertion.
func e2eSeedStory056AcceptanceAnswersCount(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, suffix string, count int) {
	e2eSeedStory056AcceptanceAnswersWithQuestionTypes(t, db, fixture, suffix, count, []string{"single_choice", "true_false", "multiple_choice"})
}

func e2eSeedStory056AcceptanceAnswersWithQuestionTypes(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, suffix string, count int, questionTypes []string) {
	t.Helper()
	if count < 1 || len(questionTypes) == 0 {
		t.Fatalf("STORY-056 acceptance answer count must be positive, got %d", count)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fileStore := files.NewPostgresStore(db)
	for index := 0; index < count; index++ {
		questionType := questionTypes[index%len(questionTypes)]
		if _, ok := fixture.QuestionIDs[questionType]; !ok {
			t.Fatalf("STORY-056 fixture has no %q question", questionType)
		}
		var studentID string
		if err := db.QueryRowContext(ctx, `
INSERT INTO student (tenant_id, school_id, class_id, student_no, name, status)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, 'active')
RETURNING id::text
`, fixture.TenantID, fixture.SchoolID, fixture.ClassID, fmt.Sprintf("S056-%s-%03d", strings.ReplaceAll(suffix, ".", ""), index+1), fmt.Sprintf("验收学生%03d", index+1)).Scan(&studentID); err != nil {
			t.Fatalf("seed acceptance student %d: %v", index+1, err)
		}
		var submissionID string
		if err := db.QueryRowContext(ctx, `
INSERT INTO submission (
  tenant_id, exam_id, student_id, candidate_no, source_type, status,
  expected_page_count, actual_page_count, quality_status, quality_issues, collected_by,
  identity_status, identity_evidence
)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'scanner_upload', 'ready_for_ocr', 1, 1, 'passed', '[]'::jsonb, $5::uuid, 'matched', '{}'::jsonb)
RETURNING id::text
`, fixture.TenantID, fixture.ExamID, studentID, fmt.Sprintf("C056-%03d", index+1), fixture.AdminID).Scan(&submissionID); err != nil {
			t.Fatalf("seed acceptance submission %d: %v", index+1, err)
		}
		assetToken := fmt.Sprintf("%s-%03d", strings.ReplaceAll(suffix, ".", ""), index+1)
		cropHash := e2eStory056AssetHash("crop", fixture.ExamID, suffix, fmt.Sprintf("%d", index+1))
		crop, err := fileStore.Create(ctx, files.CreateAssetInput{
			TenantID: fixture.TenantID, ExamID: fixture.ExamID, SubmissionID: submissionID, OwnerType: "answer_segment_crop",
			OriginalName: fmt.Sprintf("story056-crop-%s.png", assetToken), ContentType: "image/png", SizeBytes: 1,
			HashSHA256: cropHash, StorageBucket: "edugrade-story056-e2e", StorageKey: fmt.Sprintf("crops/%s.png", assetToken),
			Visibility: "private", UploadedBy: fixture.AdminID,
		})
		if err != nil {
			t.Fatalf("seed acceptance crop %d: %v", index+1, err)
		}
		var pageID string
		if err := db.QueryRowContext(ctx, `
INSERT INTO submission_page (tenant_id, submission_id, file_asset_id, page_no, status, quality_status)
VALUES ($1::uuid, $2::uuid, $3::uuid, 1, 'accepted', 'passed')
RETURNING id::text
`, fixture.TenantID, submissionID, crop.ID).Scan(&pageID); err != nil {
			t.Fatalf("seed acceptance page %d: %v", index+1, err)
		}
		if _, err := db.ExecContext(ctx, `
INSERT INTO answer_segment (
  tenant_id, submission_id, submission_page_id, question_id, question_no, bbox, source, status,
  template_id, template_content_hash, normalized_bbox, pixel_bbox, crop_file_asset_id, crop_sha256,
  question_version, processing_status, confidence
)
VALUES (
  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, '{"x":0.1,"y":0.1,"width":0.7,"height":0.16}'::jsonb, 'configured_answer_area', 'accepted',
  $6::uuid, $7, '{"x":0.1,"y":0.1,"width":0.7,"height":0.16}'::jsonb, '{"x":100,"y":100,"width":700,"height":224}'::jsonb, $8::uuid, $9,
  1, 'completed', 0.99
)
`, fixture.TenantID, submissionID, pageID, fixture.QuestionIDs[questionType], map[string]string{"single_choice": "Q1", "true_false": "Q2", "multiple_choice": "Q3"}[questionType], fixture.TemplateID, fixture.TemplateContentHash, crop.ID, crop.HashSHA256); err != nil {
			t.Fatalf("seed acceptance answer segment %d: %v", index+1, err)
		}
	}
}

func e2eStartStory056Run(t *testing.T, router http.Handler, adminToken, examID, idempotencyKey string) map[string]any {
	t.Helper()
	return e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/scoring-runs", adminToken, story056JSON(t, map[string]any{
		"idempotency_key": idempotencyKey,
	}), http.StatusCreated)["scoring_run"].(map[string]any)
}

func e2eStory062ScoringReadiness(t *testing.T, router http.Handler, adminToken, examID string) map[string]any {
	t.Helper()
	response := e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/scoring-readiness", adminToken, http.StatusOK)
	readiness, ok := response["scoring_readiness"].(map[string]any)
	if !ok {
		t.Fatalf("scoring readiness response has unexpected shape: %#v", response)
	}
	return readiness
}

func e2eAssertStory062ScoringReadiness(
	t *testing.T,
	readiness map[string]any,
	wantReady bool,
	wantQuestions, wantSegments, wantAutomatic, wantManual int,
) {
	t.Helper()
	if got, ok := readiness["ready"].(bool); !ok || got != wantReady {
		t.Fatalf("scoring readiness ready=%v, want %v: %#v", readiness["ready"], wantReady, readiness)
	}
	counts := map[string]int{
		"total_questions":          wantQuestions,
		"total_segments":           wantSegments,
		"automatic_candidates":     wantAutomatic,
		"manual_review_candidates": wantManual,
	}
	for field, want := range counts {
		if got, ok := readiness[field].(float64); !ok || int(got) != want {
			t.Fatalf("scoring readiness %s=%v, want %d: %#v", field, readiness[field], want, readiness)
		}
	}
}

func e2eAssertStory062ReadinessCheck(t *testing.T, readiness map[string]any, code string, wantPassed bool, wantSeverity string) {
	t.Helper()
	checks, ok := readiness["checks"].([]any)
	if !ok {
		t.Fatalf("scoring readiness checks have unexpected shape: %#v", readiness)
	}
	for _, raw := range checks {
		check, ok := raw.(map[string]any)
		if !ok || check["code"] != code {
			continue
		}
		if got, ok := check["passed"].(bool); !ok || got != wantPassed {
			t.Fatalf("scoring readiness check %s passed=%v, want %v: %#v", code, check["passed"], wantPassed, check)
		}
		if check["severity"] != wantSeverity {
			t.Fatalf("scoring readiness check %s severity=%v, want %s: %#v", code, check["severity"], wantSeverity, check)
		}
		return
	}
	t.Fatalf("scoring readiness check %s is missing: %#v", code, readiness)
}

func e2eAssertStory062ReadinessError(t *testing.T, response map[string]any, blockerCode string) {
	t.Helper()
	apiError, ok := response["error"].(map[string]any)
	if !ok || apiError["code"] != "scoring_not_ready" {
		t.Fatalf("blocked scoring start must return scoring_not_ready: %#v", response)
	}
	readiness, ok := response["scoring_readiness"].(map[string]any)
	if !ok {
		t.Fatalf("blocked scoring start must include readiness details: %#v", response)
	}
	if ready, ok := readiness["ready"].(bool); !ok || ready {
		t.Fatalf("blocked scoring start must report ready=false: %#v", response)
	}
	e2eAssertStory062ReadinessCheck(t, readiness, blockerCode, false, "blocker")
}

func e2eClaimStory056OMRTasks(t *testing.T, router http.Handler, adminToken string, limit int, service, instance string) map[string]story056WorkerLease {
	t.Helper()
	response := e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/worker/tasks/claim", adminToken, story056JSON(t, map[string]any{
		"queue_name": "page-processing", "worker_service": service, "worker_instance_id": instance, "limit": limit, "lease_seconds": 300,
	}), http.StatusOK)
	items, ok := response["tasks"].([]any)
	if !ok {
		t.Fatalf("claim tasks response has unexpected shape: %#v", response)
	}
	leases := make(map[string]story056WorkerLease, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("claimed task has unexpected shape: %#v", raw)
		}
		if e2eString(t, item, "task_type") != "omr_extract" || e2eString(t, item, "source_type") != "omr_run" {
			t.Fatalf("acceptance run claimed a non-OMR task: %#v", item)
		}
		lease := story056WorkerLease{TaskID: e2eString(t, item, "id"), OMRRunID: e2eString(t, item, "source_id"), Token: e2eString(t, item, "lease_token")}
		leases[lease.OMRRunID] = lease
	}
	return leases
}

func e2eCreateStory056Overlay(t *testing.T, fileStore files.Store, fixture story056AcceptanceFixture, omrRunID string, sequence int) files.FileAsset {
	t.Helper()
	assetToken := fmt.Sprintf("%s-%03d", omrRunID, sequence)
	asset, err := fileStore.Create(context.Background(), files.CreateAssetInput{
		TenantID: fixture.TenantID, ExamID: fixture.ExamID, OwnerType: "omr_evidence", OwnerID: omrRunID,
		OriginalName: fmt.Sprintf("story056-overlay-%s.png", assetToken), ContentType: "image/png", SizeBytes: 1,
		HashSHA256: e2eStory056AssetHash("overlay", fixture.ExamID, omrRunID, fmt.Sprintf("%d", sequence)), StorageBucket: "edugrade-story056-e2e", StorageKey: fmt.Sprintf("overlays/%s.png", assetToken),
		Visibility: "private", UploadedBy: fixture.AdminID,
	})
	if err != nil {
		t.Fatalf("create OMR overlay for %s: %v", omrRunID, err)
	}
	return asset
}

// e2eStory056AssetHash deliberately derives a valid SHA-256 value from the
// fixture identity. File assets have tenant-scoped deduplication, so fixed
// synthetic hashes turn otherwise independent PostgreSQL E2E cases into
// accidental duplicates when they share the demo tenant.
func e2eStory056AssetHash(parts ...string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(parts, "\x00"))))
}

func e2eStory056RunItems(t *testing.T, response map[string]any) []map[string]any {
	t.Helper()
	rawItems, ok := response["items"].([]any)
	if !ok {
		t.Fatalf("scoring run detail has unexpected items: %#v", response)
	}
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("scoring run item has unexpected shape: %#v", raw)
		}
		items = append(items, item)
	}
	return items
}

func e2eStory056SelectedOptions(t *testing.T, questionType string) []string {
	t.Helper()
	switch questionType {
	case "single_choice":
		return []string{"A"}
	case "true_false":
		return []string{"true"}
	case "multiple_choice":
		return []string{"A", "C"}
	default:
		t.Fatalf("unexpected question type in OMR acceptance data: %s", questionType)
		return nil
	}
}

func e2eAssertStory056Run(t *testing.T, run map[string]any, status string, total, queued, autoConfirmed, humanConfirmed, review, failed int) {
	t.Helper()
	if run["status"] != status ||
		int(e2eFloat(t, run, "total_count")) != total ||
		int(e2eFloat(t, run, "queued_count")) != queued ||
		int(e2eFloat(t, run, "auto_confirmed_count")) != autoConfirmed ||
		int(e2eFloat(t, run, "human_confirmed_count")) != humanConfirmed ||
		int(e2eFloat(t, run, "review_count")) != review ||
		int(e2eFloat(t, run, "failed_count")) != failed {
		t.Fatalf("unexpected scoring run summary: %#v", run)
	}
}

func e2eAssertStory056ItemStates(t *testing.T, items []map[string]any, wantConfirmed, wantReview int) {
	t.Helper()
	confirmed := 0
	review := 0
	for _, item := range items {
		switch item["state"] {
		case "confirmed":
			confirmed++
		case "review":
			review++
			if e2eString(t, item, "question_type") != "multiple_choice" {
				t.Fatalf("only multi-select OMR answers should require human review: %#v", item)
			}
		default:
			t.Fatalf("acceptance item must be confirmed or awaiting review: %#v", item)
		}
	}
	if confirmed != wantConfirmed || review != wantReview {
		t.Fatalf("unexpected scoring item states: confirmed=%d review=%d want_confirmed=%d want_review=%d", confirmed, review, wantConfirmed, wantReview)
	}
}

func e2eCompleteStory056HumanReviews(t *testing.T, db *sql.DB, router http.Handler, adminToken, graderToken, graderID string, fixture story056AcceptanceFixture, count int) {
	t.Helper()
	taskResponse := e2eGetJSON(t, router, "/api/v1/review-tasks?exam_id="+fixture.ExamID, adminToken, http.StatusOK)
	tasks := taskResponse["tasks"].([]any)
	pendingTasks := make([]map[string]any, 0, count)
	for _, rawTask := range tasks {
		task := rawTask.(map[string]any)
		if task["status"] == "pending" && task["source"] == "omr_ambiguous" && task["question_id"] == fixture.QuestionIDs["multiple_choice"] {
			pendingTasks = append(pendingTasks, task)
		}
	}
	if len(pendingTasks) != count {
		t.Fatalf("expected %d pending OMR review tasks for explicit assignment, got %d", count, len(pendingTasks))
	}
	for _, task := range pendingTasks {
		taskID := e2eString(t, task, "id")
		e2ePostJSON(t, router, http.MethodPost, "/api/v1/review-tasks/"+taskID+"/assign", adminToken, story056JSON(t, map[string]any{
			"assigned_to": graderID, "expected_revision": task["revision"],
		}), http.StatusOK)
	}
	for index := 0; index < count; index++ {
		claim := e2ePostJSON(t, router, http.MethodPost, "/api/v1/review-tasks/next", graderToken, story056JSON(t, map[string]any{
			"exam_id": fixture.ExamID, "question_id": fixture.QuestionIDs["multiple_choice"],
		}), http.StatusOK)
		task := claim["task"].(map[string]any)
		if task["source"] != "omr_ambiguous" || task["status"] != "assigned" {
			t.Fatalf("human review claim has unexpected state: %#v", task)
		}
		taskID := e2eString(t, task, "id")
		submitted := e2ePostJSON(t, router, http.MethodPost, "/api/v1/review-tasks/"+taskID+"/submit", graderToken, story056JSON(t, map[string]any{
			"expected_revision": task["revision"], "score": 1, "rubric_selections": []any{}, "comments": "STORY-056 multi-select verification", "reason": "confirmed against answer key",
		}), http.StatusCreated)
		if submitted["question_grade_id"] == nil || submitted["task"].(map[string]any)["status"] != "submitted" {
			t.Fatalf("human review must create a durable question grade: %#v", submitted)
		}

		type submittedState struct {
			Status              string
			CurrentGradeID      string
			HumanGradeCount     int
			ReviewCount         int
			HumanConfirmedCount int
			FailedCount         int
		}
		readState := func() submittedState {
			var state submittedState
			if err := db.QueryRow(`
SELECT rt.status, COALESCE(rt.current_grade_id::text, ''), count(hg.id)::int,
  sr.review_count, sr.human_confirmed_count, sr.failed_count
FROM review_task rt
JOIN scoring_run sr ON sr.tenant_id=rt.tenant_id AND sr.id=rt.scoring_run_id
LEFT JOIN human_grade hg ON hg.tenant_id=rt.tenant_id AND hg.review_task_id=rt.id AND hg.deleted_at IS NULL
WHERE rt.tenant_id=$1::uuid AND rt.id=$2::uuid AND rt.deleted_at IS NULL
GROUP BY rt.status, rt.current_grade_id, sr.review_count, sr.human_confirmed_count, sr.failed_count
`, fixture.TenantID, taskID).Scan(
				&state.Status,
				&state.CurrentGradeID,
				&state.HumanGradeCount,
				&state.ReviewCount,
				&state.HumanConfirmedCount,
				&state.FailedCount,
			); err != nil {
				t.Fatalf("read submitted review state: %v", err)
			}
			return state
		}
		beforeReturn := readState()
		if beforeReturn.Status != "submitted" || beforeReturn.CurrentGradeID == "" || beforeReturn.HumanGradeCount != 1 {
			t.Fatalf("submitted review state is incomplete: %#v", beforeReturn)
		}
		e2eExpectStatus(t, router, http.MethodPost, "/api/v1/review-tasks/"+taskID+"/return", adminToken, story056JSON(t, map[string]any{
			"reason": "must not invalidate a submitted grade", "expected_revision": submitted["task"].(map[string]any)["revision"],
		}), http.StatusConflict)
		if afterReturn := readState(); afterReturn != beforeReturn {
			t.Fatalf("rejected return mutated task, grade, or scoring progress: before=%#v after=%#v", beforeReturn, afterReturn)
		}
	}
}

func e2eAssertStory056CancelledWork(t *testing.T, db *sql.DB, tenantID, runID string, want int) {
	t.Helper()
	var cancelledTasks, invalidatedOMR int
	if err := db.QueryRow(`SELECT count(*) FROM agent_worker_task wt JOIN omr_run o ON o.tenant_id=wt.tenant_id AND o.runtime_task_id=wt.id WHERE wt.tenant_id=$1::uuid AND o.scoring_run_id=$2::uuid AND wt.status='cancelled'`, tenantID, runID).Scan(&cancelledTasks); err != nil {
		t.Fatalf("count cancelled worker tasks: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM omr_run WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND status='invalidated'`, tenantID, runID).Scan(&invalidatedOMR); err != nil {
		t.Fatalf("count invalidated OMR runs: %v", err)
	}
	if cancelledTasks != want || invalidatedOMR != want {
		t.Fatalf("cancel must finish all active work: cancelled=%d invalidated=%d want=%d", cancelledTasks, invalidatedOMR, want)
	}
}

func e2eAssertStory056AnonymousItems(t *testing.T, items []map[string]any) {
	t.Helper()
	for _, item := range items {
		for _, prohibited := range []string{"student_id", "student_name", "candidate_no"} {
			if _, found := item[prohibited]; found {
				t.Fatalf("scoring recovery detail must remain anonymous; found %s in %#v", prohibited, item)
			}
		}
	}
}

func e2eAssertStory056ConfirmedScores(t *testing.T, db *sql.DB, tenantID, runID string, wantAuto, wantHuman int) {
	t.Helper()
	var total, fullScore, autoConfirmed, humanConfirmed int
	if err := db.QueryRow(`
SELECT count(*), count(*) FILTER (WHERE score=1 AND max_score=1),
       count(*) FILTER (WHERE source='rule_confirmed'),
       count(*) FILTER (WHERE source='human')
FROM question_grade
WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND is_current AND status='confirmed' AND deleted_at IS NULL
`, tenantID, runID).Scan(&total, &fullScore, &autoConfirmed, &humanConfirmed); err != nil {
		t.Fatalf("count confirmed scores: %v", err)
	}
	wantTotal := wantAuto + wantHuman
	if total != wantTotal || fullScore != wantTotal || autoConfirmed != wantAuto || humanConfirmed != wantHuman {
		t.Fatalf("clear synthetic OMR acceptance answers must preserve score and provenance: total=%d full_score=%d auto=%d human=%d want_auto=%d want_human=%d", total, fullScore, autoConfirmed, humanConfirmed, wantAuto, wantHuman)
	}
}

func e2eAssertStory056ReprocessVersion(t *testing.T, db *sql.DB, tenantID, segmentID, runID string) {
	t.Helper()
	var currentCount, currentVersion int
	if err := db.QueryRow(`
SELECT count(*), COALESCE(max(version), 0)
FROM question_grade
WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid
  AND is_current AND status='confirmed' AND deleted_at IS NULL
`, tenantID, segmentID, runID).Scan(&currentCount, &currentVersion); err != nil {
		t.Fatalf("inspect reprocessed score: %v", err)
	}
	if currentCount != 1 || currentVersion < 2 {
		t.Fatalf("reprocess must leave one new current score version: count=%d version=%d", currentCount, currentVersion)
	}
}

func story056JSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal STORY-056 acceptance JSON: %v", err)
	}
	return string(raw)
}
