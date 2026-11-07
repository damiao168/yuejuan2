package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// 此测试验证生产权限来自独立证据，且在实际应用 worker 结果之前仍可撤销。
// 预置的 OMR 事实是数据库级测试夹具；worker 提取算法本身由页面处理验收套件验证。
func TestStory056TemplateDifferenceCalibrationApprovalAndRevocationE2E(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping STORY-056 calibration PostgreSQL test")
	}

	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	approverID, approverUsername := e2eCreateStory056CalibrationApprover(t, db, suffix)
	approverToken := e2eLoginWithTenant(t, router, "demo", approverUsername, "ChangeMe123!")
	fixture := e2eCreateStory056AcceptanceFixtureWithOMRProfile(t, db, router, adminToken, suffix, map[string]any{
		"mode": paper.OMRProfileModeTemplateDifference, "version": paper.OMRProfileVersionTemplateDifferenceBubbleV1,
	}, 120)
	e2eSeedStory056AcceptanceAnswersWithQuestionTypes(t, db, fixture, suffix+"-calibration", 120, []string{"single_choice", "true_false", "multiple_choice"})

	seedRun := e2eStartStory056Run(t, router, adminToken, fixture.ExamID, "story056-calibration-seed-"+suffix)
	seedRunID := e2eString(t, seedRun, "id")
	e2eSeedStory056CalibrationOMRFacts(t, db, fixture.TenantID, seedRunID)

	approved := e2eApproveStory056Calibration(t, db, router, story056CalibrationActors{AdminToken: adminToken, ApproverToken: approverToken, ApproverID: approverID}, fixture)
	calibrationID, approvedSession, approvedCases := approved.ID, approved.Session, approved.Cases

	firstCase := e2eStory056CalibrationCaseForQuestionAndStratum(t, approvedCases, fixture.QuestionIDs["single_choice"], "selected_high")
	firstSegmentID := e2eString(t, firstCase, "answer_segment_id")
	firstRunID, firstItem, firstLease := e2eStory056CalibrationReprocessAndClaim(t, router, adminToken, firstSegmentID, suffix+"-approved")
	var profileVersion, profileHash, referenceSHA256, runCalibrationID, runEvidenceHash, autoReason string
	var autoEligible bool
	var minimumConfidence float64
	if err := db.QueryRow(`
SELECT profile_version,profile_hash,reference_sha256,calibration_session_id::text,calibration_evidence_hash,
  auto_confirm_eligible,auto_confirm_reason,auto_confirm_min_confidence
FROM omr_run
WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND deleted_at IS NULL`, fixture.TenantID, firstRunID).Scan(
		&profileVersion, &profileHash, &referenceSHA256, &runCalibrationID, &runEvidenceHash,
		&autoEligible, &autoReason, &minimumConfidence); err != nil {
		t.Fatalf("load approved OMR policy snapshot: %v", err)
	}
	if !autoEligible || autoReason != paper.OMRAutoConfirmReasonCalibrationApproved || runCalibrationID != calibrationID || runEvidenceHash != e2eString(t, approvedSession, "evidence_hash") || minimumConfidence != paper.OMRCalibrationMinimumConfidence {
		t.Fatalf("new OMR work must snapshot the exact approved calibration: eligible=%t reason=%s calibration=%s evidence=%s minimum=%v", autoEligible, autoReason, runCalibrationID, runEvidenceHash, minimumConfidence)
	}
	overlay := e2eCreateStory056Overlay(t, files.NewPostgresStore(db), fixture, firstLease.OMRRunID, 59)
	firstResult := e2ePostWorkerJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+firstLease.OMRRunID+"/result", adminToken,
		story056JSON(t, e2eStory056CalibrationResultPayload(t, firstLease, firstItem, overlay, story056CalibrationProfile{Version: "story056-calibration-approved-" + firstLease.OMRRunID, ProfileVersion: profileVersion, ProfileHash: profileHash, ReferenceSHA256: referenceSHA256})), http.StatusOK)
	if _, found := firstResult["question_grade"]; !found {
		t.Fatalf("approved calibration must permit a high-confidence automatic grade: %#v", firstResult)
	}

	// 模板批准也必须授权另一道被覆盖的题目。
	secondCase := e2eStory056CalibrationCaseForQuestionAndStratum(t, approvedCases, fixture.QuestionIDs["true_false"], "selected_high")
	secondSegmentID := e2eString(t, secondCase, "answer_segment_id")
	secondRunID, secondItem, secondLease := e2eStory056CalibrationReprocessAndClaim(t, router, adminToken, secondSegmentID, suffix+"-revoke")
	var queuedCalibrationID string
	if err := db.QueryRow(`SELECT calibration_session_id::text FROM omr_run WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND deleted_at IS NULL`, fixture.TenantID, secondRunID).Scan(&queuedCalibrationID); err != nil {
		t.Fatalf("load queued calibrated OMR run: %v", err)
	}
	if queuedCalibrationID != calibrationID {
		t.Fatalf("queued OMR must retain the calibration snapshot before revocation: %s", queuedCalibrationID)
	}
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/omr-calibrations/"+calibrationID+"/revoke", approverToken, story056JSON(t, map[string]any{
		"reason": "observed drift requires an immediate automatic-grade stop",
	}), http.StatusOK)
	secondOverlay := e2eCreateStory056Overlay(t, files.NewPostgresStore(db), fixture, secondLease.OMRRunID, 60)
	secondResult := e2ePostWorkerJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+secondLease.OMRRunID+"/result", adminToken,
		story056JSON(t, e2eStory056CalibrationResultPayload(t, secondLease, secondItem, secondOverlay, story056CalibrationProfile{Version: "story056-calibration-revoked-" + secondLease.OMRRunID, ProfileVersion: profileVersion, ProfileHash: profileHash, ReferenceSHA256: referenceSHA256})), http.StatusOK)
	if _, found := secondResult["question_grade"]; found {
		t.Fatalf("revocation before callback must prevent automatic grading: %#v", secondResult)
	}
	var blockedEligible bool
	var blockedReason, reviewReason string
	var blockedGrades int
	if err := db.QueryRow(`
SELECT o.auto_confirm_eligible,o.auto_confirm_reason,
  (SELECT count(*) FROM question_grade g WHERE g.tenant_id=o.tenant_id AND g.scoring_run_id=o.scoring_run_id AND g.answer_segment_id=o.answer_segment_id AND g.deleted_at IS NULL),
  COALESCE((SELECT reason_code FROM review_task r WHERE r.tenant_id=o.tenant_id AND r.scoring_run_id=o.scoring_run_id AND r.answer_segment_id=o.answer_segment_id AND r.source='omr_ambiguous' AND r.deleted_at IS NULL ORDER BY r.created_at DESC LIMIT 1),'')
FROM omr_run o
WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid`, fixture.TenantID, secondLease.OMRRunID).Scan(&blockedEligible, &blockedReason, &blockedGrades, &reviewReason); err != nil {
		t.Fatalf("inspect revoked OMR result: %v", err)
	}
	if blockedEligible || blockedReason != paper.OMRAutoConfirmReasonCalibrationRevoked || blockedGrades != 0 || reviewReason != "omr_"+paper.OMRAutoConfirmReasonCalibrationRevoked {
		t.Fatalf("revoked calibration must become a human-review result: eligible=%t reason=%s grades=%d review=%s", blockedEligible, blockedReason, blockedGrades, reviewReason)
	}
}

type story056CalibrationActors struct {
	AdminToken, ApproverToken, ApproverID string
}

type story056ApprovedCalibration struct {
	ID      string
	Session map[string]any
	Cases   []any
}

// 先冻结样本并完整盲标，创建者始终不能自批；只有独立审批者能冻结证据哈希。
func e2eApproveStory056Calibration(t *testing.T, db *sql.DB, router http.Handler, actors story056CalibrationActors, fixture story056AcceptanceFixture) story056ApprovedCalibration {
	t.Helper()
	adminToken, approverToken, approverID := actors.AdminToken, actors.ApproverToken, actors.ApproverID
	created := e2ePostJSON(t, router, http.MethodPost, "/api/v1/answer-sheet-templates/"+fixture.TemplateID+"/omr-calibrations", adminToken, `{}`, http.StatusCreated)["calibration"].(map[string]any)
	session := created["session"].(map[string]any)
	calibrationID := e2eString(t, session, "id")
	cases, ok := created["cases"].([]any)
	if !ok || len(cases) != paper.OMRCalibrationMinimumSampleCount || e2eFloat(t, session, "sample_count") != paper.OMRCalibrationMinimumSampleCount {
		t.Fatalf("calibration must freeze exactly the required deterministic sample: %#v", created)
	}
	if e2eFloat(t, session, "minimum_confidence") != paper.OMRCalibrationMinimumConfidence || e2eString(t, session, "status") != "draft" {
		t.Fatalf("calibration must freeze the minimum evidence threshold: %#v", session)
	}
	if e2eString(t, session, "scope_type") != "template" {
		t.Fatalf("new calibration must cover the immutable template: %#v", session)
	}

	// 创建者在完成标注工作之前或之后都不能自我批准。
	e2eExpectStatus(t, router, http.MethodPost, "/api/v1/omr-calibrations/"+calibrationID+"/approve", adminToken, story056JSON(t, map[string]any{
		"approval_note": "creator must not approve this calibration",
	}), http.StatusForbidden)
	var labeledCalibration map[string]any
	for _, raw := range cases {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("unexpected calibration case: %#v", raw)
		}
		labeledCalibration = e2ePostJSON(t, router, http.MethodPost, "/api/v1/omr-calibrations/"+calibrationID+"/cases/"+e2eString(t, item, "id")+"/label", adminToken, story056JSON(t, map[string]any{
			"expected_options": e2eStory056CalibrationExpectedOptions(t, db, fixture.TenantID, e2eString(t, item, "id")),
		}), http.StatusOK)["calibration"].(map[string]any)
	}
	labeledSummary := labeledCalibration["session"].(map[string]any)["summary"].(map[string]any)
	if ready, _ := labeledSummary["ready_to_approve"].(bool); !ready {
		t.Fatalf("complete stratified blind labels must be approvable: %#v", labeledSummary)
	}
	if e2eFloat(t, labeledSummary, "eligible_count") == 0 || e2eFloat(t, labeledSummary, "eligible_match_count") != e2eFloat(t, labeledSummary, "eligible_count") || e2eFloat(t, labeledSummary, "eligible_mismatch_count") != 0 {
		t.Fatalf("approval must contain matching automatically accepted evidence: %#v", labeledSummary)
	}

	approved := e2ePostJSON(t, router, http.MethodPost, "/api/v1/omr-calibrations/"+calibrationID+"/approve", approverToken, story056JSON(t, map[string]any{
		"approval_note": "independent reviewer verified the complete calibration evidence",
	}), http.StatusOK)["calibration"].(map[string]any)
	approvedSession := approved["session"].(map[string]any)
	if e2eString(t, approvedSession, "status") != "approved" || e2eString(t, approvedSession, "approved_by") != approverID || !strings.HasPrefix(e2eString(t, approvedSession, "evidence_hash"), "sha256:") {
		t.Fatalf("independent approval must freeze a durable evidence hash: %#v", approvedSession)
	}
	approvedCases := approved["cases"].([]any)

	return story056ApprovedCalibration{ID: calibrationID, Session: approvedSession, Cases: approvedCases}
}

func TestStory056ApprovedCalibrationIsInheritedByTemplateCloneE2E(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping STORY-056 calibration clone PostgreSQL test")
	}

	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	approverID, approverUsername := e2eCreateStory056CalibrationApprover(t, db, suffix+"-clone")
	approverToken := e2eLoginWithTenant(t, router, "demo", approverUsername, "ChangeMe123!")
	fixture := e2eCreateStory056MutableTemplateFixture(t, db, router, adminToken, suffix, map[string]any{
		"mode": paper.OMRProfileModeTemplateDifference, "version": paper.OMRProfileVersionTemplateDifferenceBubbleV1,
	}, 120)
	e2eSeedStory056AcceptanceAnswersWithQuestionTypes(t, db, fixture, suffix+"-clone", 120, []string{"single_choice", "true_false", "multiple_choice"})
	e2eSeedStory056MutableCalibrationOMRFacts(t, db, fixture)

	created := e2ePostJSON(t, router, http.MethodPost, "/api/v1/answer-sheet-templates/"+fixture.TemplateID+"/omr-calibrations", adminToken, `{}`, http.StatusCreated)["calibration"].(map[string]any)
	session := created["session"].(map[string]any)
	calibrationID := e2eString(t, session, "id")
	cases := created["cases"].([]any)
	if len(cases) != paper.OMRCalibrationMinimumSampleCount {
		t.Fatalf("mutable clone fixture must expose complete calibration evidence: %d", len(cases))
	}
	for _, raw := range cases {
		item := raw.(map[string]any)
		e2ePostJSON(t, router, http.MethodPost, "/api/v1/omr-calibrations/"+calibrationID+"/cases/"+e2eString(t, item, "id")+"/label", adminToken, story056JSON(t, map[string]any{
			"expected_options": e2eStory056CalibrationExpectedOptions(t, db, fixture.TenantID, e2eString(t, item, "id")),
		}), http.StatusOK)
	}
	approved := e2ePostJSON(t, router, http.MethodPost, "/api/v1/omr-calibrations/"+calibrationID+"/approve", approverToken, story056JSON(t, map[string]any{
		"approval_note": "independent reviewer verified clone inheritance evidence",
	}), http.StatusOK)["calibration"].(map[string]any)
	approvedSession := approved["session"].(map[string]any)
	if e2eString(t, approvedSession, "approved_by") != approverID {
		t.Fatalf("clone source calibration must be independently approved: %#v", approvedSession)
	}

	clonedTemplate := e2ePostJSON(t, router, http.MethodPost, "/api/v1/answer-sheet-templates/"+fixture.TemplateID+"/clone", adminToken, `{}`, http.StatusCreated)["template"].(map[string]any)
	clonedTemplateID := e2eString(t, clonedTemplate, "id")
	if clonedTemplateID == fixture.TemplateID || e2eString(t, clonedTemplate, "content_hash") != fixture.TemplateContentHash || e2eString(t, clonedTemplate, "status") != "draft" {
		t.Fatalf("clone must be a distinct mutable template with identical immutable content: %#v", clonedTemplate)
	}
	var sourceVersion int
	if err := db.QueryRow(`SELECT version_no FROM answer_sheet_template WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, fixture.TemplateID).Scan(&sourceVersion); err != nil {
		t.Fatalf("load source template version: %v", err)
	}
	if int(e2eFloat(t, clonedTemplate, "version_no")) <= sourceVersion {
		t.Fatalf("clone version must advance beyond source version %d: %#v", sourceVersion, clonedTemplate)
	}

	var inheritedFrom, inheritedStatus, inheritedEvidence string
	var inheritedCaseCount int
	if err := db.QueryRow(`
SELECT s.inherited_from_session_id::text,s.status,s.evidence_hash,
  (SELECT count(*) FROM omr_calibration_case c WHERE c.tenant_id=s.tenant_id AND c.calibration_session_id=s.id)
FROM omr_calibration_session s
WHERE s.tenant_id=$1::uuid AND s.template_id=$2::uuid AND s.deleted_at IS NULL
`, fixture.TenantID, clonedTemplateID).Scan(&inheritedFrom, &inheritedStatus, &inheritedEvidence, &inheritedCaseCount); err != nil {
		t.Fatalf("load inherited clone calibration: %v", err)
	}
	if inheritedFrom != calibrationID || inheritedStatus != "approved" || inheritedEvidence != e2eString(t, approvedSession, "evidence_hash") || inheritedCaseCount != paper.OMRCalibrationMinimumSampleCount {
		t.Fatalf("an unchanged template clone must inherit the exact approved evidence: source=%s status=%s evidence=%s cases=%d", inheritedFrom, inheritedStatus, inheritedEvidence, inheritedCaseCount)
	}
}

func TestStory056TemplateCloneIsRejectedAfterReadinessConfirmationE2E(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping STORY-056 frozen clone PostgreSQL test")
	}

	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	fixture := e2eCreateStory056MutableTemplateFixture(t, db, router, adminToken, suffix, nil)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+fixture.ExamID+"/readiness/confirm", adminToken, `{}`, http.StatusOK)

	rec := e2eExpectStatus(t, router, http.MethodPost, "/api/v1/answer-sheet-templates/"+fixture.TemplateID+"/clone", adminToken, `{}`, http.StatusConflict)
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode frozen clone response: %v", err)
	}
	errorBody, ok := response["error"].(map[string]any)
	if !ok || e2eString(t, errorBody, "code") != "exam_frozen" {
		t.Fatalf("readiness-confirmed clone must fail with exam_frozen: %#v", response)
	}
}

func TestReadinessConfirmationSerializesConcurrentConfigurationWriteE2E(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping transactional readiness PostgreSQL test")
	}

	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	fixture := e2eCreateStory056MutableTemplateFixture(t, db, router, adminToken, suffix, nil)
	if _, err := paper.NewPostgresStore(db).Readiness(context.Background(), fixture.TenantID, fixture.ExamID); err != nil {
		t.Fatalf("calculate baseline readiness directly: %v", err)
	}
	before := e2eGetJSON(t, router, "/api/v1/exams/"+fixture.ExamID+"/readiness", adminToken, http.StatusOK)["readiness"].(map[string]any)
	beforeHash := e2eString(t, before, "configuration_hash")

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin concurrent configuration write: %v", err)
	}
	defer tx.Rollback()
	var lockedStatus string
	if err := tx.QueryRow(`SELECT status FROM exam WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, fixture.TenantID, fixture.ExamID).Scan(&lockedStatus); err != nil {
		t.Fatalf("lock exam configuration: %v", err)
	}
	if _, err := tx.Exec(`UPDATE question SET stem=stem || ' committed-before-confirmation', updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, fixture.TenantID, fixture.QuestionIDs["single_choice"]); err != nil {
		t.Fatalf("update readiness input: %v", err)
	}

	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+fixture.ExamID+"/readiness/confirm", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		response <- rec
	}()

	select {
	case rec := <-response:
		t.Fatalf("readiness confirmation crossed an uncommitted configuration boundary: %d %s", rec.Code, rec.Body.String())
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit concurrent configuration write: %v", err)
	}

	var rec *httptest.ResponseRecorder
	select {
	case rec = <-response:
	case <-time.After(5 * time.Second):
		t.Fatal("readiness confirmation did not resume after configuration commit")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm readiness after serialized write: %d %s", rec.Code, rec.Body.String())
	}
	var confirmed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &confirmed); err != nil {
		t.Fatalf("decode readiness confirmation: %v", err)
	}
	confirmedHash := e2eString(t, confirmed["readiness"].(map[string]any), "configuration_hash")
	if confirmedHash == beforeHash {
		t.Fatal("confirmation reused the pre-write readiness snapshot")
	}
	after := e2eGetJSON(t, router, "/api/v1/exams/"+fixture.ExamID+"/readiness", adminToken, http.StatusOK)["readiness"].(map[string]any)
	if afterHash := e2eString(t, after, "configuration_hash"); afterHash != confirmedHash {
		t.Fatalf("confirmed hash %s does not match committed configuration %s", confirmedHash, afterHash)
	}
}
