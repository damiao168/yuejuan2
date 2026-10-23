package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/imagequality"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestStory060CaptureRecoveryE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping STORY-060 capture recovery workflow")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, adminToken, suffix)
	e2eAssertStory060SessionCommandIdentity(t, db, fixture, suffix)

	sourceAssetID := e2eUploadSyntheticPDF(
		t,
		router,
		adminToken,
		"story060-recovery-"+suffix+".pdf",
		"%PDF-1.4\n% STORY-060 capture recovery "+suffix+"\n",
	)
	batch := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+fixture.ExamID+"/capture-batches", adminToken, story056JSON(t, map[string]any{
		"name":            "STORY-060 采集自救验收",
		"source_type":     "web_upload",
		"idempotency_key": "story060-recovery-batch-" + suffix,
	}), http.StatusCreated)["batch"].(map[string]any)
	batchID := e2eString(t, batch, "id")
	first := e2ePostJSON(t, router, http.MethodPost, "/api/v1/capture-batches/"+batchID+"/files", adminToken, story056JSON(t, map[string]any{
		"file_asset_id":   sourceAssetID,
		"idempotency_key": "story060-recovery-first-" + suffix,
	}), http.StatusCreated)["file"].(map[string]any)
	firstID := e2eString(t, first, "id")
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/capture-batches/"+batchID+"/process", adminToken, `{}`, http.StatusOK)

	if _, err := db.Exec(`
UPDATE agent_worker_task
SET status='failed',attempt_count=max_attempts,completed_at=now(),error_code='pdf_decode_failed',updated_at=now()
WHERE tenant_id=$1::uuid AND source_type='capture_file' AND source_id=$2::uuid
`, fixture.TenantID, firstID); err != nil {
		t.Fatalf("seed terminal worker failure: %v", err)
	}
	if _, err := db.Exec(`
UPDATE capture_file
SET status='failed',error_code='pdf_decode_failed',updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid
`, fixture.TenantID, firstID); err != nil {
		t.Fatalf("seed terminal capture file failure: %v", err)
	}
	if _, err := db.Exec(`
UPDATE capture_batch SET status='needs_review',failed_count=1,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid
`, fixture.TenantID, batchID); err != nil {
		t.Fatalf("seed capture batch review state: %v", err)
	}

	reuploaded := e2ePostJSON(t, router, http.MethodPost, "/api/v1/capture-batches/"+batchID+"/files", adminToken, story056JSON(t, map[string]any{
		"file_asset_id":   sourceAssetID,
		"idempotency_key": "story060-recovery-second-" + suffix,
	}), http.StatusCreated)["file"].(map[string]any)
	if reuploaded["status"] != "uploaded" {
		t.Fatalf("same hash must be accepted after the prior source failed: %#v", reuploaded)
	}
	reuploadedID := e2eString(t, reuploaded, "id")
	duplicate := e2ePostJSON(t, router, http.MethodPost, "/api/v1/capture-batches/"+batchID+"/files", adminToken, story056JSON(t, map[string]any{
		"file_asset_id":   sourceAssetID,
		"idempotency_key": "story060-recovery-duplicate-" + suffix,
	}), http.StatusCreated)["file"].(map[string]any)
	if duplicate["status"] != "duplicate" {
		t.Fatalf("an active source must continue to protect against duplicates: %#v", duplicate)
	}

	e2ePostJSON(t, router, http.MethodPost, "/api/v1/capture-batches/"+batchID+"/process", adminToken, `{}`, http.StatusOK)
	var firstStatus, firstError, secondStatus, taskStatus string
	var maxAttempts, attemptCount int
	if err := db.QueryRow(`
SELECT f1.status,COALESCE(f1.error_code,''),f2.status,t.status,t.max_attempts,t.attempt_count
FROM capture_file f1
JOIN capture_file f2 ON f2.tenant_id=f1.tenant_id AND f2.id=$3::uuid
JOIN agent_worker_task t ON t.tenant_id=f1.tenant_id AND t.source_type='capture_file' AND t.source_id=f1.id
WHERE f1.tenant_id=$1::uuid AND f1.id=$2::uuid
`, fixture.TenantID, firstID, reuploadedID).Scan(&firstStatus, &firstError, &secondStatus, &taskStatus, &maxAttempts, &attemptCount); err != nil {
		t.Fatalf("query recovered capture state: %v", err)
	}
	if firstStatus != "queued" || firstError != "" || secondStatus != "queued" || taskStatus != "queued" || maxAttempts <= attemptCount {
		t.Fatalf("failed file and runtime task were not safely requeued: first=%s error=%q second=%s task=%s attempts=%d/%d",
			firstStatus, firstError, secondStatus, taskStatus, attemptCount, maxAttempts)
	}

	quality := e2eMaterializeStory060RecoveredQuality(t, db, fixture, suffix, story060RecoveredCaptureSource{AssetID: sourceAssetID, FileID: reuploadedID})
	e2eAssertStory060QualityAtomicity(t, db, fixture, quality)
	e2eAssertStory060ManualQualityRecovery(t, db, router, adminToken, fixture, quality.PageID)
}

// 丢失创建响应后沿用同一命令身份，场次及其唯一学科考试都不能重复创建。
func e2eAssertStory060SessionCommandIdentity(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, suffix string) {
	t.Helper()
	var gradeID string
	if err := db.QueryRow(`SELECT grade_id::text FROM school_class WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, fixture.ClassID).Scan(&gradeID); err != nil {
		t.Fatalf("lookup command recovery grade: %v", err)
	}
	commandID := "exam-session-recovery-" + suffix
	sessionInput := exam.CreateSessionInput{
		SchoolID: fixture.SchoolID, GradeID: gradeID, Name: "稳定操作身份验收", ExamType: "unit_test",
		GradingMode: "ai_assisted", PublishPolicy: "manual_after_confirmation", ClassIDs: []string{fixture.ClassID}, CommandID: commandID,
		Subjects: []exam.SessionSubjectInput{{Subject: "math", TotalScore: 10, DurationMinutes: 30, Sections: []exam.BlueprintSectionInput{{Title: "全卷", QuestionType: "short_answer", QuestionCount: 1, ScorePerQuestion: 10}}}},
	}
	sessionStore := exam.NewPostgresStore(db)
	firstSession, err := sessionStore.CreateExamSession(context.Background(), auth.AccessScope{TenantID: fixture.TenantID, TenantWide: true}, fixture.AdminID, sessionInput)
	if err != nil {
		t.Fatalf("create stable command session: %v", err)
	}
	secondSession, err := sessionStore.CreateExamSession(context.Background(), auth.AccessScope{TenantID: fixture.TenantID, TenantWide: true}, fixture.AdminID, sessionInput)
	if err != nil || secondSession.ID != firstSession.ID || len(secondSession.Exams) != 1 || secondSession.Exams[0].ID != firstSession.Exams[0].ID {
		t.Fatalf("lost-response retry duplicated exam session: first=%#v second=%#v err=%v", firstSession, secondSession, err)
	}

}

type story060RecoveredCaptureSource struct {
	AssetID, FileID string
}

type story060QualityRecovery struct {
	PageID, RunID string
	Store         *imagequality.PostgresStore
	Task          workerruntime.Task
	Input         imagequality.ResultInput
}

// 恢复采集页后再建立质量任务；Runtime 租约和质量源租约必须来自同一次领取。
func e2eMaterializeStory060RecoveredQuality(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, suffix string, source story060RecoveredCaptureSource) story060QualityRecovery {
	t.Helper()
	sourceAssetID, reuploadedID := source.AssetID, source.FileID
	var sourceHash string
	if err := db.QueryRow(`SELECT hash_sha256 FROM file_asset WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, sourceAssetID).Scan(&sourceHash); err != nil {
		t.Fatalf("lookup source hash: %v", err)
	}
	captureStore := capture.NewPostgresStoreWithBarcodeKeyring(db, e2eBarcodeKeyring())
	if _, err := captureStore.ApplyFileResult(context.Background(), fixture.TenantID, reuploadedID, []capture.DecodedPageInput{{
		SourceIndex: 1, FileAssetID: sourceAssetID, SHA256: sourceHash, Width: 1000, Height: 1400,
	}}); err != nil {
		t.Fatalf("materialize recovered capture page: %v", err)
	}
	var capturePageID, submissionPageID, submissionID string
	if err := db.QueryRow(`
SELECT id::text,submission_page_id::text,submission_id::text
FROM capture_page
WHERE tenant_id=$1::uuid AND capture_file_id=$2::uuid AND deleted_at IS NULL
`, fixture.TenantID, reuploadedID).Scan(&capturePageID, &submissionPageID, &submissionID); err != nil {
		t.Fatalf("lookup recovered page: %v", err)
	}
	normalized, err := files.NewPostgresStore(db).Create(context.Background(), files.CreateAssetInput{
		TenantID: fixture.TenantID, ExamID: fixture.ExamID, SubmissionID: submissionID,
		OwnerType: "submission_page_normalized", OwnerID: submissionPageID,
		OriginalName: "story060-normalized-" + suffix + ".png", ContentType: "image/png", SizeBytes: 1,
		HashSHA256: "sha256:story060-normalized-" + suffix, StorageBucket: "edugrade-story060-e2e",
		StorageKey: "normalized/" + suffix + ".png", Visibility: "private", UploadedBy: fixture.AdminID,
	})
	if err != nil {
		t.Fatalf("create normalized quality evidence: %v", err)
	}
	qualityStore := imagequality.NewPostgresStore(db)
	qualityRuns, err := qualityStore.CreateRunsWithTasks(context.Background(), fixture.TenantID, fixture.AdminID, imagequality.CreateRunsInput{
		SubmissionID: submissionID, Profile: imagequality.DefaultProfile(), Pages: []imagequality.PageSource{{
			SubmissionPageID: submissionPageID, PageNo: 1, SourceFileAssetID: sourceAssetID,
			SourceSHA256: sourceHash, DownloadURL: "/api/v1/files/" + sourceAssetID + "/download",
		}},
	})
	if err != nil || len(qualityRuns) != 1 {
		t.Fatalf("create quality run and task atomically: %#v %v", qualityRuns, err)
	}
	runtimeStore := workerruntime.NewPostgresStore(db)
	qualityTasks, err := runtimeStore.Claim(context.Background(), fixture.TenantID, workerruntime.ClaimInput{
		QueueName: "image-quality", WorkerService: "image-quality-worker", WorkerInstanceID: "atomicity-test", Limit: 100, LeaseSeconds: 300,
	})
	var qualityTask workerruntime.Task
	for _, task := range qualityTasks {
		if task.SourceID == qualityRuns[0].ID {
			qualityTask = task
			break
		}
	}
	if err != nil || qualityTask.ID == "" || qualityTask.LeaseExpiresAt == nil {
		t.Fatalf("claim quality task: %#v %v", qualityTasks, err)
	}
	leasedRun, err := qualityStore.LeaseRun(context.Background(), fixture.TenantID, qualityRuns[0].ID, "atomicity-test", qualityTask.LeaseToken, *qualityTask.LeaseExpiresAt, qualityTask.AttemptCount)
	if err != nil {
		t.Fatalf("lease quality source: %v", err)
	}
	resultInput := imagequality.ResultInput{
		LeaseToken: qualityTask.LeaseToken, AttemptNo: leasedRun.AttemptNo, ResultVersion: "atomicity-v1", DurationMS: 25,
		ProcessingStatus: imagequality.ProcessingCompleted, QualityStatus: imagequality.QualityFailed,
		NormalizedFileAssetID: normalized.ID, QualityReport: map[string]any{"blur": 0.8},
		QualityIssues:          []imagequality.Issue{{Code: "blur", Severity: "error", Action: "manual_review"}},
		NormalizationTransform: map[string]any{"rotation": 0}, ErrorDetail: map[string]any{},
	}
	return story060QualityRecovery{PageID: submissionPageID, RunID: leasedRun.ID, Store: qualityStore, Task: qualityTask, Input: resultInput}
}

// 先破坏 Runtime 租约验证整笔回滚，再恢复原租约提交；页面、采集页和 Runtime 终态必须一起变化。
func e2eAssertStory060QualityAtomicity(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, quality story060QualityRecovery) {
	t.Helper()
	qualityStore, qualityTask, resultInput := quality.Store, quality.Task, quality.Input
	if _, err := db.Exec(`UPDATE agent_worker_task SET lease_token='forced-runtime-mismatch' WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, qualityTask.ID); err != nil {
		t.Fatalf("inject runtime completion failure: %v", err)
	}
	if _, err := qualityStore.SubmitResultCommand(context.Background(), fixture.TenantID, fixture.AdminID, quality.RunID, resultInput); !errors.Is(err, workerruntime.ErrLeaseMismatch) {
		t.Fatalf("injected runtime mismatch must roll back the command: %v", err)
	}
	var rolledBackRun, rolledBackPage, rolledBackCapture string
	if err := db.QueryRow(`SELECT r.processing_status,sp.quality_status,cp.status
	FROM submission_page_quality_run r
	JOIN submission_page sp ON sp.tenant_id=r.tenant_id AND sp.id=r.submission_page_id
	JOIN capture_page cp ON cp.tenant_id=sp.tenant_id AND cp.submission_page_id=sp.id
	WHERE r.tenant_id=$1::uuid AND r.id=$2::uuid`, fixture.TenantID, quality.RunID).Scan(&rolledBackRun, &rolledBackPage, &rolledBackCapture); err != nil {
		t.Fatalf("query rolled back quality state: %v", err)
	}
	if rolledBackRun != imagequality.ProcessingProcessing || rolledBackPage != imagequality.QualityUnchecked || rolledBackCapture == "quality_rejected" {
		t.Fatalf("partial quality result escaped rollback: run=%s page=%s capture=%s", rolledBackRun, rolledBackPage, rolledBackCapture)
	}
	if _, err := db.Exec(`UPDATE agent_worker_task SET lease_token=$3 WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, qualityTask.ID, qualityTask.LeaseToken); err != nil {
		t.Fatalf("restore runtime lease after fault injection: %v", err)
	}
	if _, err := qualityStore.SubmitResultCommand(context.Background(), fixture.TenantID, fixture.AdminID, quality.RunID, resultInput); err != nil {
		t.Fatalf("commit coordinated quality result: %v", err)
	}
	var committedRun, committedPage, committedCapture, committedTask string
	if err := db.QueryRow(`SELECT r.processing_status,sp.quality_status,cp.status,t.status
	FROM submission_page_quality_run r
	JOIN submission_page sp ON sp.tenant_id=r.tenant_id AND sp.id=r.submission_page_id
	JOIN capture_page cp ON cp.tenant_id=sp.tenant_id AND cp.submission_page_id=sp.id
	JOIN agent_worker_task t ON t.tenant_id=r.tenant_id AND t.source_type='image_quality_run' AND t.source_id=r.id
	WHERE r.tenant_id=$1::uuid AND r.id=$2::uuid`, fixture.TenantID, quality.RunID).Scan(&committedRun, &committedPage, &committedCapture, &committedTask); err != nil {
		t.Fatalf("query committed quality state: %v", err)
	}
	if committedRun != imagequality.ProcessingCompleted || committedPage != imagequality.QualityFailed || committedCapture != "quality_rejected" || committedTask != workerruntime.StatusSucceeded {
		t.Fatalf("quality command did not commit as one unit: run=%s page=%s capture=%s task=%s", committedRun, committedPage, committedCapture, committedTask)
	}

}

// 人工放行保留原失败证据和操作者，并重新排队配准任务，不能只修改页面状态。
func e2eAssertStory060ManualQualityRecovery(t *testing.T, db *sql.DB, router http.Handler, adminToken string, fixture story056AcceptanceFixture, submissionPageID string) {
	t.Helper()
	// 此场景只验证质量放行恢复配准，页码先由人工确认，不能把解码顺序当作可靠页码。
	if _, err := db.Exec(`UPDATE capture_page SET manual_override=manual_override || jsonb_build_object('page_no',assigned_page_no) WHERE tenant_id=$1::uuid AND submission_page_id=$2::uuid`, fixture.TenantID, submissionPageID); err != nil {
		t.Fatal(err)
	}
	override := e2ePostJSON(t, router, http.MethodPost, "/api/v1/submission-pages/"+submissionPageID+"/quality-override", adminToken, story056JSON(t, map[string]any{
		"reason": "人工核对页面清晰，倾斜与模糊程度不影响阅卷",
	}), http.StatusOK)
	runs := override["registration_runs"].([]any)
	if len(runs) == 0 {
		t.Fatalf("quality override must restore page registration work: %#v", override)
	}
	var pageQualityStatus, pageStatus, decision, reason, actorID, originalStatus, registrationTaskStatus string
	if err := db.QueryRow(`
SELECT sp.quality_status,cp.status,
       sp.quality_override->>'decision',sp.quality_override->>'reason',
       sp.quality_override->>'actor_id',sp.quality_override->>'original_quality_status',
       t.status
FROM submission_page sp
JOIN capture_page cp ON cp.tenant_id=sp.tenant_id AND cp.submission_page_id=sp.id
JOIN page_registration_run r ON r.tenant_id=cp.tenant_id AND r.capture_page_id=cp.id
JOIN agent_worker_task t ON t.tenant_id=r.tenant_id AND t.id=r.runtime_task_id
WHERE sp.tenant_id=$1::uuid AND sp.id=$2::uuid
ORDER BY r.created_at DESC LIMIT 1
`, fixture.TenantID, submissionPageID).Scan(
		&pageQualityStatus, &pageStatus, &decision, &reason, &actorID, &originalStatus, &registrationTaskStatus,
	); err != nil {
		t.Fatalf("query quality override recovery evidence: %v", err)
	}
	if pageQualityStatus != "passed" || pageStatus != "registration" || decision != "accepted" ||
		reason == "" || actorID != fixture.AdminID || originalStatus != "failed" || registrationTaskStatus != "queued" {
		t.Fatalf("quality override evidence or recovery task is incomplete: quality=%s page=%s decision=%s reason=%q actor=%s original=%s task=%s",
			pageQualityStatus, pageStatus, decision, reason, actorID, originalStatus, registrationTaskStatus)
	}
}
