package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

// OMR 回调不能把暂时的规则发布竞争变成没有后续操作的成功 worker 任务。
// 任务和 OMR 事实仍已持久化，缺失的确认目标则成为显式人工复核项。
func TestStory056OMRAutoGradeConfirmationFailureFallsBackToReviewE2E(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping OMR confirmation fallback PostgreSQL test")
	}

	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, adminToken, suffix)
	e2eSeedStory056AcceptanceAnswersCount(t, db, fixture, suffix, 1)

	run := e2eStartStory056Run(t, router, adminToken, fixture.ExamID, "story056-confirmation-fallback-"+suffix)
	runID := e2eString(t, run, "id")
	e2eEnableStory056AutoConfirmation(t, db, fixture.TenantID, runID)
	item, lease := e2eStory056ClaimSingleOMR(t, router, adminToken, runID, "story056-confirmation-fallback-worker")
	omrRunID := e2eString(t, item, "omr_run_id")
	segmentID := e2eString(t, item, "answer_segment_id")
	questionID := e2eString(t, item, "question_id")

	// 此处复现 worker 已领取任务后规则变为不可用的情况。
	// retired 是有效的不可变历史规则状态，可在不破坏夹具的情况下使确认查询失败。
	result, err := db.Exec(`UPDATE scoring_rule SET status='retired',updated_at=now() WHERE tenant_id=$1::uuid AND question_id=$2::uuid AND status='published' AND deleted_at IS NULL`, fixture.TenantID, questionID)
	if err != nil {
		t.Fatalf("retire claimed task's scoring rule: %v", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		t.Fatalf("expected exactly one published scoring rule to retire, got %d", count)
	}

	overlay := e2eCreateStory056Overlay(t, files.NewPostgresStore(db), fixture, omrRunID, 0)
	response := e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+omrRunID+"/result", adminToken, story056JSON(t, e2eStory056OMRResultPayload(t, lease, item, overlay, "story056-confirmation-fallback-v1-"+omrRunID, []map[string]any{{"option": "A", "fill_ratio": 0.93}}, false)), http.StatusOK)
	if _, found := response["question_grade"]; found {
		t.Fatalf("confirmation failure must not return a question grade: %#v", response)
	}

	var taskStatus, omrStatus, runStatus string
	if err := db.QueryRow(`SELECT status FROM agent_worker_task WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, lease.TaskID).Scan(&taskStatus); err != nil {
		t.Fatalf("load completed worker task: %v", err)
	}
	if err := db.QueryRow(`SELECT status FROM omr_run WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, omrRunID).Scan(&omrStatus); err != nil {
		t.Fatalf("load completed OMR run: %v", err)
	}
	if err := db.QueryRow(`SELECT status FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, runID).Scan(&runStatus); err != nil {
		t.Fatalf("load scoring run after confirmation fallback: %v", err)
	}
	if taskStatus != "succeeded" || omrStatus != "completed" || runStatus != "needs_review" {
		t.Fatalf("confirmation fallback must complete the task and OMR run, then require review: task=%s omr=%s run=%s", taskStatus, omrStatus, runStatus)
	}

	var allReviews, gradingFailureReviews, candidates, answers, grades int
	if err := db.QueryRow(`SELECT count(*),count(*) FILTER (WHERE source='grading_failure' AND status='pending' AND reason_code='auto_grade_confirmation_failed') FROM review_task WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND answer_segment_id=$3::uuid AND deleted_at IS NULL`, fixture.TenantID, runID, segmentID).Scan(&allReviews, &gradingFailureReviews); err != nil {
		t.Fatalf("inspect confirmation fallback review task: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM answer_candidate WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND answer_segment_id=$3::uuid AND source='omr' AND deleted_at IS NULL`, fixture.TenantID, runID, segmentID).Scan(&candidates); err != nil {
		t.Fatalf("count OMR candidates after confirmation fallback: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM answer_segment_answer WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND source='omr' AND deleted_at IS NULL`, fixture.TenantID, segmentID).Scan(&answers); err != nil {
		t.Fatalf("count OMR answers after confirmation fallback: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM question_grade WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND answer_segment_id=$3::uuid AND deleted_at IS NULL`, fixture.TenantID, runID, segmentID).Scan(&grades); err != nil {
		t.Fatalf("count grades after confirmation fallback: %v", err)
	}
	if allReviews != 1 || gradingFailureReviews != 1 || candidates != 1 || answers != 1 || grades != 0 {
		t.Fatalf("confirmation failure must leave one grading_failure review and no grade: reviews=%d grading_failure=%d candidates=%d answers=%d grades=%d", allReviews, gradingFailureReviews, candidates, answers, grades)
	}
}

// worker 可以识别出明确的所选答案，但当前原始填涂比例配置只提供建议。
// 此验收测试验证：控制转入持久化自动成绩的是服务器端资格快照，而非 worker 声称的置信度。
func TestStory056ManualOnlyOMRProfileRequiresReviewE2E(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping STORY-056 manual-only OMR gate test")
	}

	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, adminToken, suffix)
	e2eSeedStory056AcceptanceAnswersCount(t, db, fixture, suffix, 1)

	run := e2eStartStory056Run(t, router, adminToken, fixture.ExamID, "story056-manual-only-"+suffix)
	runID := e2eString(t, run, "id")
	item, lease := e2eStory056ClaimSingleOMR(t, router, adminToken, runID, "story056-manual-only-worker")
	omrRunID := e2eString(t, item, "omr_run_id")
	segmentID := e2eString(t, item, "answer_segment_id")
	overlay := e2eCreateStory056Overlay(t, files.NewPostgresStore(db), fixture, omrRunID, 1)
	response := e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+omrRunID+"/result", adminToken, story056JSON(t, e2eStory056OMRResultPayload(t, lease, item, overlay, "story056-manual-only-v1-"+omrRunID, []map[string]any{{"option": "A", "fill_ratio": 0.93}}, false)), http.StatusOK)
	if _, found := response["question_grade"]; found {
		t.Fatalf("manual-only raw OMR must not return an automatic grade: %#v", response)
	}

	var runStatus, candidateDecision, reviewReason, autoReason string
	var autoConfirmed, gradeCount int
	var autoEligible bool
	err := db.QueryRow(`
SELECT r.status,r.auto_confirmed_count,o.auto_confirm_eligible,o.auto_confirm_reason,
       COALESCE((SELECT decision FROM answer_candidate WHERE tenant_id=o.tenant_id AND answer_segment_id=o.answer_segment_id AND scoring_run_id=o.scoring_run_id AND source='omr' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1),''),
       (SELECT count(*) FROM question_grade WHERE tenant_id=o.tenant_id AND answer_segment_id=o.answer_segment_id AND scoring_run_id=o.scoring_run_id AND deleted_at IS NULL),
       COALESCE((SELECT reason_code FROM review_task WHERE tenant_id=o.tenant_id AND answer_segment_id=o.answer_segment_id AND scoring_run_id=o.scoring_run_id AND source='omr_ambiguous' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1),'')
FROM scoring_run r
JOIN omr_run o ON o.tenant_id=r.tenant_id AND o.scoring_run_id=r.id AND o.id=$3::uuid
WHERE r.tenant_id=$1::uuid AND r.id=$2::uuid
`, fixture.TenantID, runID, omrRunID).Scan(&runStatus, &autoConfirmed, &autoEligible, &autoReason, &candidateDecision, &gradeCount, &reviewReason)
	if err != nil {
		t.Fatalf("inspect manual-only OMR gate facts: %v", err)
	}
	if runStatus != "needs_review" || autoConfirmed != 0 || autoEligible || autoReason != "manual_only_profile" || candidateDecision != "selected" || gradeCount != 0 || reviewReason != "omr_manual_only_profile" {
		t.Fatalf("manual-only OMR must persist a suggestion and review only: status=%s auto=%d eligible=%t reason=%s candidate=%s grades=%d review=%s segment=%s", runStatus, autoConfirmed, autoEligible, autoReason, candidateDecision, gradeCount, reviewReason, segmentID)
	}
}

func TestStory056RejectsMismatchedOMRProfileBeforeCompletingTaskE2E(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping STORY-056 OMR profile mismatch test")
	}

	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, adminToken, suffix)
	e2eSeedStory056AcceptanceAnswersCount(t, db, fixture, suffix, 1)

	run := e2eStartStory056Run(t, router, adminToken, fixture.ExamID, "story056-profile-mismatch-"+suffix)
	runID := e2eString(t, run, "id")
	item, lease := e2eStory056ClaimSingleOMR(t, router, adminToken, runID, "story056-profile-mismatch-worker")
	omrRunID := e2eString(t, item, "omr_run_id")
	segmentID := e2eString(t, item, "answer_segment_id")
	overlay := e2eCreateStory056Overlay(t, files.NewPostgresStore(db), fixture, omrRunID, 1)
	payload := e2eStory056OMRResultPayload(t, lease, item, overlay, "story056-profile-mismatch-v1-"+omrRunID, []map[string]any{{"option": "A", "fill_ratio": 0.93}}, false)
	payload["profile_hash"] = "sha256:unexpected-profile"
	response := e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+omrRunID+"/result", adminToken, story056JSON(t, payload), http.StatusBadRequest)
	errorBody, ok := response["error"].(map[string]any)
	if !ok || errorBody["code"] != "invalid_grading_input" {
		t.Fatalf("mismatched worker profile must be rejected as invalid input: %#v", response)
	}

	var omrStatus, taskStatus string
	if err := db.QueryRow(`SELECT status FROM omr_run WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, omrRunID).Scan(&omrStatus); err != nil {
		t.Fatalf("load OMR run after profile mismatch: %v", err)
	}
	if err := db.QueryRow(`SELECT status FROM agent_worker_task WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, lease.TaskID).Scan(&taskStatus); err != nil {
		t.Fatalf("load worker task after profile mismatch: %v", err)
	}
	if omrStatus != "queued" || taskStatus != "leased" {
		t.Fatalf("profile mismatch must leave the task retryable and OMR untouched: omr=%s task=%s", omrStatus, taskStatus)
	}

	var candidates, answers, grades int
	if err := db.QueryRow(`SELECT count(*) FROM answer_candidate WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid AND source='omr' AND deleted_at IS NULL`, fixture.TenantID, segmentID, runID).Scan(&candidates); err != nil {
		t.Fatalf("count candidates after profile mismatch: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM answer_segment_answer WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND source='omr' AND deleted_at IS NULL`, fixture.TenantID, segmentID).Scan(&answers); err != nil {
		t.Fatalf("count answers after profile mismatch: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid AND deleted_at IS NULL`, fixture.TenantID, segmentID, runID).Scan(&grades); err != nil {
		t.Fatalf("count grades after profile mismatch: %v", err)
	}
	if candidates != 0 || answers != 0 || grades != 0 {
		t.Fatalf("profile mismatch must not make scoring facts durable: candidates=%d answers=%d grades=%d", candidates, answers, grades)
	}
}

// 模板差分任务只能使用锁定模板中冻结的参考资源。
// 此测试在真实 worker 读取任何字节之前核验完整服务器链路：模板绑定、排队任务载荷、
// 不可变 OMR 运行快照、回调拒绝和仅人工确认完成。
func TestStory056TemplateDifferenceReferenceSnapshotE2E(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping STORY-056 template-difference reference test")
	}

	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	fixture := e2eCreateStory056AcceptanceFixtureWithOMRProfile(t, db, router, adminToken, suffix, map[string]any{
		"mode": paper.OMRProfileModeTemplateDifference, "version": paper.OMRProfileVersionTemplateDifferenceBubbleV1,
	})
	e2eSeedStory056AcceptanceAnswersCount(t, db, fixture, suffix, 1)

	run := e2eStartStory056Run(t, router, adminToken, fixture.ExamID, "story056-template-difference-"+suffix)
	runID := e2eString(t, run, "id")
	item, lease := e2eStory056ClaimSingleOMR(t, router, adminToken, runID, "story056-template-difference-worker")
	omrRunID := e2eString(t, item, "omr_run_id")
	segmentID := e2eString(t, item, "answer_segment_id")

	snapshot := e2eAssertTemplateDifferenceSnapshot(t, db, fixture, omrRunID)
	profileVersion, profileHash, referenceSHA256 := snapshot.ProfileVersion, snapshot.ProfileHash, snapshot.ReferenceSHA256
	overlay := e2eCreateStory056Overlay(t, files.NewPostgresStore(db), fixture, omrRunID, 1)
	wrongReference := e2eStory056OMRResultPayload(t, lease, item, overlay, "story056-reference-wrong-"+omrRunID, []map[string]any{{"option": "A", "foreground_delta": 0.8}}, false)
	wrongReference["profile_version"] = profileVersion
	wrongReference["profile_hash"] = profileHash
	wrongReference["reference_sha256"] = "sha256:replacement-reference"
	response := e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+omrRunID+"/result", adminToken, story056JSON(t, wrongReference), http.StatusBadRequest)
	errorBody, ok := response["error"].(map[string]any)
	if !ok || errorBody["code"] != "invalid_grading_input" {
		t.Fatalf("reference substitution must be rejected as invalid input: %#v", response)
	}
	var pendingOMRStatus, pendingTaskStatus string
	if err := db.QueryRow(`SELECT status FROM omr_run WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, omrRunID).Scan(&pendingOMRStatus); err != nil {
		t.Fatalf("load OMR status after reference rejection: %v", err)
	}
	if err := db.QueryRow(`SELECT status FROM agent_worker_task WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, lease.TaskID).Scan(&pendingTaskStatus); err != nil {
		t.Fatalf("load worker task after reference rejection: %v", err)
	}
	if pendingOMRStatus != "queued" || pendingTaskStatus != "leased" {
		t.Fatalf("reference rejection must leave work retryable: omr=%s task=%s", pendingOMRStatus, pendingTaskStatus)
	}

	accepted := e2eStory056OMRResultPayload(t, lease, item, overlay, "story056-reference-correct-"+omrRunID, []map[string]any{{"option": "A", "foreground_delta": 0.8}}, false)
	accepted["profile_version"] = profileVersion
	accepted["profile_hash"] = profileHash
	accepted["reference_sha256"] = referenceSHA256
	completed := e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+omrRunID+"/result", adminToken, story056JSON(t, accepted), http.StatusOK)
	if _, found := completed["question_grade"]; found {
		t.Fatalf("uncalibrated template-difference OMR must remain manual-only: %#v", completed)
	}

	var completedOMRStatus, completedTaskStatus, candidateDecision, reviewReason string
	var gradeCount int
	var candidateEvidence []byte
	err := db.QueryRow(`
SELECT o.status,t.status,
  COALESCE((SELECT decision FROM answer_candidate WHERE tenant_id=o.tenant_id AND answer_segment_id=o.answer_segment_id AND scoring_run_id=o.scoring_run_id AND source='omr' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1),''),
  COALESCE((SELECT evidence FROM answer_candidate WHERE tenant_id=o.tenant_id AND answer_segment_id=o.answer_segment_id AND scoring_run_id=o.scoring_run_id AND source='omr' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1),'{}'::jsonb),
  COALESCE((SELECT reason_code FROM review_task WHERE tenant_id=o.tenant_id AND answer_segment_id=o.answer_segment_id AND scoring_run_id=o.scoring_run_id AND source='omr_ambiguous' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1),''),
  (SELECT count(*) FROM question_grade WHERE tenant_id=o.tenant_id AND answer_segment_id=o.answer_segment_id AND scoring_run_id=o.scoring_run_id AND deleted_at IS NULL)
FROM omr_run o
JOIN agent_worker_task t ON t.tenant_id=o.tenant_id AND t.id=o.runtime_task_id
WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid
`, fixture.TenantID, omrRunID).Scan(&completedOMRStatus, &completedTaskStatus, &candidateDecision, &candidateEvidence, &reviewReason, &gradeCount)
	if err != nil {
		t.Fatalf("load completed template-difference facts: %v", err)
	}
	if completedOMRStatus != "completed" || completedTaskStatus != "succeeded" || candidateDecision != "selected" || reviewReason != "omr_"+paper.OMRAutoConfirmReasonCalibrationUnapproved || gradeCount != 0 {
		t.Fatalf("difference result must persist a reviewable suggestion only: omr=%s task=%s candidate=%s review=%s grades=%d segment=%s", completedOMRStatus, completedTaskStatus, candidateDecision, reviewReason, gradeCount, segmentID)
	}
	var evidence map[string]any
	if err := json.Unmarshal(candidateEvidence, &evidence); err != nil {
		t.Fatalf("decode template-difference candidate evidence: %v", err)
	}
	if e2eString(t, evidence, "reference_sha256") != referenceSHA256 || e2eString(t, evidence, "profile_hash") != profileHash {
		t.Fatalf("candidate evidence must preserve the server snapshot: %#v", evidence)
	}
}

type templateDifferenceSnapshot struct {
	ProfileVersion, ProfileHash, ReferenceSHA256 string
}

// 先核对运行、模板文件和 worker 载荷里的同一冻结身份，再允许验证回调的参考资源。
func e2eAssertTemplateDifferenceSnapshot(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, omrRunID string) templateDifferenceSnapshot {
	t.Helper()
	var profileVersion, profileHash, referenceAssetID, referenceSHA256, autoReason, schemaVersion string
	var eligible bool
	var taskPayloadRaw []byte
	err := db.QueryRow(`
SELECT o.profile_version,o.profile_hash,COALESCE(o.reference_file_asset_id::text,''),COALESCE(o.reference_sha256,''),o.auto_confirm_eligible,o.auto_confirm_reason,t.payload,t.payload_schema_version
FROM omr_run o
JOIN agent_worker_task t ON t.tenant_id=o.tenant_id AND t.id=o.runtime_task_id
WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid
`, fixture.TenantID, omrRunID).Scan(&profileVersion, &profileHash, &referenceAssetID, &referenceSHA256, &eligible, &autoReason, &taskPayloadRaw, &schemaVersion)
	if err != nil {
		t.Fatalf("load template-difference OMR snapshot: %v", err)
	}
	if profileVersion != paper.OMRProfileVersionTemplateDifferenceBubbleV1 || profileHash == "" || referenceAssetID != fixture.PaperFileAssetID || referenceSHA256 == "" || eligible || autoReason != paper.OMRAutoConfirmReasonCalibrationUnapproved || schemaVersion != "omr-task-v2" {
		t.Fatalf("unexpected template-difference snapshot: version=%s hash=%s asset=%s reference=%s eligible=%t reason=%s schema=%s", profileVersion, profileHash, referenceAssetID, referenceSHA256, eligible, autoReason, schemaVersion)
	}
	var paperSHA256 string
	if err := db.QueryRow(`SELECT hash_sha256 FROM file_asset WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, fixture.TenantID, fixture.PaperFileAssetID).Scan(&paperSHA256); err != nil {
		t.Fatalf("load frozen paper asset hash: %v", err)
	}
	if referenceSHA256 != paperSHA256 {
		t.Fatalf("OMR reference must snapshot the template paper hash: reference=%s paper=%s", referenceSHA256, paperSHA256)
	}
	var taskPayload map[string]any
	if err := json.Unmarshal(taskPayloadRaw, &taskPayload); err != nil {
		t.Fatalf("decode OMR worker payload: %v", err)
	}
	profilePayload, ok := taskPayload["profile"].(map[string]any)
	if !ok || e2eString(t, profilePayload, "mode") != paper.OMRProfileModeTemplateDifference || e2eString(t, profilePayload, "version") != paper.OMRProfileVersionTemplateDifferenceBubbleV1 {
		t.Fatalf("worker must receive the difference runtime profile: %#v", taskPayload)
	}
	referencePayload, ok := taskPayload["reference"].(map[string]any)
	if !ok || e2eString(t, referencePayload, "file_asset_id") != referenceAssetID || e2eString(t, referencePayload, "sha256") != referenceSHA256 || e2eString(t, referencePayload, "download_url") != "/api/v1/files/"+referenceAssetID+"/download" {
		t.Fatalf("worker must receive the frozen reference asset: %#v", taskPayload)
	}
	if _, ok := referencePayload["question_region"].(map[string]any); !ok {
		t.Fatalf("difference task must include the locked normalized question crop: %#v", referencePayload)
	}

	return templateDifferenceSnapshot{ProfileVersion: profileVersion, ProfileHash: profileHash, ReferenceSHA256: referenceSHA256}
}

// Worker Runtime 的语义结果哈希是最后一道检查，防止 OMR 任务成功后重放改变评分证据。
func TestStory056OMRSameVersionChangedMeasurementsIsConflictE2E(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping OMR semantic replay PostgreSQL test")
	}

	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, adminToken, suffix)
	e2eSeedStory056AcceptanceAnswersCount(t, db, fixture, suffix, 1)

	run := e2eStartStory056Run(t, router, adminToken, fixture.ExamID, "story056-semantic-replay-"+suffix)
	runID := e2eString(t, run, "id")
	e2eEnableStory056AutoConfirmation(t, db, fixture.TenantID, runID)
	item, lease := e2eStory056ClaimSingleOMR(t, router, adminToken, runID, "story056-semantic-replay-worker")
	omrRunID := e2eString(t, item, "omr_run_id")
	segmentID := e2eString(t, item, "answer_segment_id")
	overlay := e2eCreateStory056Overlay(t, files.NewPostgresStore(db), fixture, omrRunID, 1)
	version := "story056-semantic-replay-v1-" + omrRunID
	initial := e2eStory056OMRResultPayload(t, lease, item, overlay, version, []map[string]any{{"option": "A", "fill_ratio": 0.93}}, false)
	first := e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+omrRunID+"/result", adminToken, story056JSON(t, initial), http.StatusOK)
	if _, found := first["question_grade"]; !found {
		t.Fatalf("normal one-answer OMR completion should auto-confirm a grade: %#v", first)
	}
	before := e2eStory056CompletedOMRFacts(t, db, fixture.TenantID, lease.TaskID, omrRunID, segmentID, runID)
	if before.TaskStatus != "succeeded" || before.OMRStatus != "completed" || before.Candidates != 1 || before.Answers != 1 || before.Grades != 1 || before.Reviews != 0 {
		t.Fatalf("unexpected durable facts before semantic replay: %#v", before)
	}

	replayed := e2eStory056OMRResultPayload(t, lease, item, overlay, version, []map[string]any{{"option": "A", "fill_ratio": 0.11}}, false)
	conflict := e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+omrRunID+"/result", adminToken, story056JSON(t, replayed), http.StatusConflict)
	errorBody, ok := conflict["error"].(map[string]any)
	if !ok || errorBody["code"] != "omr_task_completion_failed" {
		t.Fatalf("same-version semantic replay must be rejected by the completion gate: %#v", conflict)
	}
	after := e2eStory056CompletedOMRFacts(t, db, fixture.TenantID, lease.TaskID, omrRunID, segmentID, runID)
	if after != before {
		t.Fatalf("rejected semantic replay must not change durable scoring facts:\nbefore=%#v\nafter=%#v", before, after)
	}
}

func e2eStory056ClaimSingleOMR(t *testing.T, router http.Handler, adminToken, runID, workerService string) (map[string]any, story056WorkerLease) {
	t.Helper()
	detail := e2eGetJSON(t, router, "/api/v1/scoring-runs/"+runID, adminToken, http.StatusOK)
	items := e2eStory056RunItems(t, detail)
	if len(items) != 1 {
		t.Fatalf("single-answer STORY-056 setup should expose one item, got %d", len(items))
	}
	item := items[0]
	omrRunID := e2eString(t, item, "omr_run_id")
	leases := e2eClaimStory056OMRTasks(t, router, adminToken, 1, workerService, "single")
	lease, ok := leases[omrRunID]
	if !ok || len(leases) != 1 {
		t.Fatalf("single-answer STORY-056 setup should claim its one OMR task: %#v", leases)
	}
	return item, lease
}

func e2eStory056OMRResultPayload(t *testing.T, lease story056WorkerLease, item map[string]any, overlay files.FileAsset, version string, measurements []map[string]any, needsHumanReview bool) map[string]any {
	t.Helper()
	return map[string]any{
		"task_id":               lease.TaskID,
		"lease_token":           lease.Token,
		"result_version":        version,
		"duration_ms":           1,
		"decision":              "selected",
		"selected":              e2eStory056SelectedOptions(t, e2eString(t, item, "question_type")),
		"confidence":            0.99,
		"needs_human_review":    needsHumanReview,
		"measurements":          measurements,
		"profile_version":       "opencv-fill-v1",
		"profile_hash":          e2eStory056ProfileHash(),
		"thresholds":            map[string]any{"marked_threshold": 0.18, "minimum_margin": 0.06},
		"overlay_file_asset_id": overlay.ID,
		"overlay_sha256":        overlay.HashSHA256,
	}
}

func e2eStory056ProfileHash() string {
	return paper.DefaultOMRRuntimeProfileHash()
}

// 原始 STORY-056 评分测试独立于配置校准，覆盖自动成绩分支。
// STORY-056 安全门禁默认把快照设为 false，因此这个仅供测试的辅助函数模拟未来获批配置，
// 同时不允许任何生产请求自行授予该权限。
func e2eEnableStory056AutoConfirmation(t *testing.T, db *sql.DB, tenantID, runID string) {
	t.Helper()
	result, err := db.Exec(`UPDATE omr_run SET auto_confirm_eligible=true,auto_confirm_reason='test_verified_template_profile' WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND deleted_at IS NULL`, tenantID, runID)
	if err != nil {
		t.Fatalf("mark fixture OMR runs as test-verified: %v", err)
	}
	if count, _ := result.RowsAffected(); count < 1 {
		t.Fatalf("mark fixture OMR runs as test-verified: no OMR runs in %s", runID)
	}
}

type story056CompletedOMRFacts struct {
	TaskStatus        string
	TaskHash          string
	TaskResult        string
	OMRStatus         string
	OMRDecision       string
	OMRMeasurements   string
	OMRSelected       string
	CandidatePayload  string
	CandidateEvidence string
	Candidates        int
	Answers           int
	Grades            int
	Reviews           int
}

func e2eStory056CompletedOMRFacts(t *testing.T, db *sql.DB, tenantID, taskID, omrRunID, segmentID, runID string) story056CompletedOMRFacts {
	t.Helper()
	var out story056CompletedOMRFacts
	if err := db.QueryRow(`SELECT wt.status,COALESCE(wt.result_payload_hash,''),COALESCE(wt.result::text,''),o.status,COALESCE(o.decision,''),o.measurements::text,o.selected_options::text FROM agent_worker_task wt JOIN omr_run o ON o.tenant_id=wt.tenant_id AND o.runtime_task_id=wt.id WHERE wt.tenant_id=$1::uuid AND wt.id=$2::uuid AND o.id=$3::uuid`, tenantID, taskID, omrRunID).Scan(&out.TaskStatus, &out.TaskHash, &out.TaskResult, &out.OMRStatus, &out.OMRDecision, &out.OMRMeasurements, &out.OMRSelected); err != nil {
		t.Fatalf("snapshot worker and OMR facts: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*),COALESCE((SELECT payload::text FROM answer_candidate WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid AND source='omr' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1),''),COALESCE((SELECT evidence::text FROM answer_candidate WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid AND source='omr' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1),'') FROM answer_candidate WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid AND source='omr' AND deleted_at IS NULL`, tenantID, segmentID, runID).Scan(&out.Candidates, &out.CandidatePayload, &out.CandidateEvidence); err != nil {
		t.Fatalf("snapshot OMR candidates: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM answer_segment_answer WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND source='omr' AND deleted_at IS NULL`, tenantID, segmentID).Scan(&out.Answers); err != nil {
		t.Fatalf("snapshot OMR answers: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid AND deleted_at IS NULL`, tenantID, segmentID, runID).Scan(&out.Grades); err != nil {
		t.Fatalf("snapshot grades: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM review_task WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid AND deleted_at IS NULL`, tenantID, segmentID, runID).Scan(&out.Reviews); err != nil {
		t.Fatalf("snapshot reviews: %v", err)
	}
	return out
}

// 此测试保护评分与 worker 的原子边界。过时 worker 不能持久化 OMR 结果、答案候选或成绩。
func TestStory056ExpiredOMRLeaseLeavesScoringFactsUntouchedE2E(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping OMR stale-lease PostgreSQL acceptance test")
	}

	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, adminToken, suffix)
	e2eSeedStory056AcceptanceAnswers(t, db, fixture, suffix)

	run := e2eStartStory056Run(t, router, adminToken, fixture.ExamID, "story056-stale-lease-"+suffix)
	runID := e2eString(t, run, "id")
	detail := e2eGetJSON(t, router, "/api/v1/scoring-runs/"+runID, adminToken, http.StatusOK)
	items := e2eStory056RunItems(t, detail)
	if len(items) != story056AcceptanceAnswerCount {
		t.Fatalf("expected %d OMR items, got %d", story056AcceptanceAnswerCount, len(items))
	}
	leases := e2eClaimStory056OMRTasks(t, router, adminToken, 1, "story056-stale-lease-worker", "initial")
	if len(leases) != 1 {
		t.Fatalf("expected exactly one claimed OMR task, got %#v", leases)
	}
	var item map[string]any
	var omrRunID string
	var lease story056WorkerLease
	for _, candidate := range items {
		candidateRunID := e2eString(t, candidate, "omr_run_id")
		if candidateLease, ok := leases[candidateRunID]; ok {
			item = candidate
			omrRunID = candidateRunID
			lease = candidateLease
			break
		}
	}
	if item == nil {
		t.Fatalf("claimed OMR task did not match a scoring run item: %#v", leases)
	}
	segmentID := e2eString(t, item, "answer_segment_id")

	fileStore := files.NewPostgresStore(db)
	overlay := e2eCreateStory056Overlay(t, fileStore, fixture, omrRunID, 0)
	if _, err := db.Exec(`UPDATE agent_worker_task SET lease_expires_at=now() - interval '1 second' WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, lease.TaskID); err != nil {
		t.Fatalf("expire OMR lease: %v", err)
	}

	response := e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+omrRunID+"/result", adminToken, story056JSON(t, map[string]any{
		"task_id":               lease.TaskID,
		"lease_token":           lease.Token,
		"result_version":        "story056-stale-lease-v1-" + omrRunID,
		"duration_ms":           1,
		"decision":              "selected",
		"selected":              e2eStory056SelectedOptions(t, e2eString(t, item, "question_type")),
		"confidence":            0.99,
		"needs_human_review":    false,
		"measurements":          []map[string]any{},
		"profile_version":       "opencv-fill-v1",
		"profile_hash":          e2eStory056ProfileHash(),
		"thresholds":            map[string]any{"marked_threshold": 0.18, "minimum_margin": 0.06},
		"overlay_file_asset_id": overlay.ID,
		"overlay_sha256":        overlay.HashSHA256,
	}), http.StatusConflict)
	errorBody, ok := response["error"].(map[string]any)
	if !ok || errorBody["code"] != "omr_task_completion_failed" {
		t.Fatalf("expired OMR lease should fail through the task completion gate: %#v", response)
	}

	var omrStatus, taskStatus string
	if err := db.QueryRow(`SELECT status FROM omr_run WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, omrRunID).Scan(&omrStatus); err != nil {
		t.Fatalf("load OMR run after rejected result: %v", err)
	}
	if err := db.QueryRow(`SELECT status FROM agent_worker_task WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, lease.TaskID).Scan(&taskStatus); err != nil {
		t.Fatalf("load worker task after rejected result: %v", err)
	}
	if omrStatus != "queued" || taskStatus != "leased" {
		t.Fatalf("expired completion must not change OMR or task state: omr=%s task=%s", omrStatus, taskStatus)
	}

	var candidates, answers, grades int
	if err := db.QueryRow(`SELECT count(*) FROM answer_candidate WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid AND source='omr' AND deleted_at IS NULL`, fixture.TenantID, segmentID, runID).Scan(&candidates); err != nil {
		t.Fatalf("count OMR candidates after rejected result: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM answer_segment_answer WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND source='omr' AND deleted_at IS NULL`, fixture.TenantID, segmentID).Scan(&answers); err != nil {
		t.Fatalf("count OMR answers after rejected result: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid AND deleted_at IS NULL`, fixture.TenantID, segmentID, runID).Scan(&grades); err != nil {
		t.Fatalf("count grades after rejected result: %v", err)
	}
	if candidates != 0 || answers != 0 || grades != 0 {
		t.Fatalf("expired completion must leave no scoring facts: candidates=%d answers=%d grades=%d", candidates, answers, grades)
	}
}
