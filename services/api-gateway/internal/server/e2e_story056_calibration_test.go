package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/paper"
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
	})
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
	firstResult := e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+firstLease.OMRRunID+"/result", adminToken,
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
	secondResult := e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/omr-runs/"+secondLease.OMRRunID+"/result", adminToken,
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
	})
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

func e2eCreateStory056CalibrationApprover(t *testing.T, db *sql.DB, suffix string) (string, string) {
	t.Helper()
	username := "story056_calibration_approver_" + strings.ReplaceAll(suffix, ".", "_")
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash STORY-056 calibration approver password: %v", err)
	}
	var userID string
	err = db.QueryRow(`
WITH demo AS (
  SELECT id FROM tenant WHERE code='demo' AND deleted_at IS NULL
), admin_role AS (
  SELECT r.id,r.tenant_id FROM role r JOIN demo d ON d.id=r.tenant_id WHERE r.code='tenant_admin' AND r.deleted_at IS NULL
), new_user AS (
  INSERT INTO app_user (tenant_id,username,display_name,password_hash,status)
  SELECT d.id,$1,'STORY-056 Independent Calibration Approver',$2,'active' FROM demo d
  RETURNING id,tenant_id
)
INSERT INTO user_role (tenant_id,user_id,role_id,data_scope)
SELECT r.tenant_id,u.id,r.id,jsonb_build_object('scope','tenant')
FROM admin_role r JOIN new_user u ON u.tenant_id=r.tenant_id
RETURNING user_id::text`, username, hash).Scan(&userID)
	if err != nil {
		t.Fatalf("create independent STORY-056 calibration approver: %v", err)
	}
	return userID, username
}

func e2eSeedStory056CalibrationOMRFacts(t *testing.T, db *sql.DB, tenantID, runID string) {
	t.Helper()
	result, err := db.Exec(`
WITH candidates AS (
  SELECT o.id,q.question_type,
    (row_number() OVER (PARTITION BY seg.question_id ORDER BY o.id) - 1) AS question_rank
  FROM omr_run o
  JOIN answer_segment seg ON seg.tenant_id=o.tenant_id AND seg.id=o.answer_segment_id AND seg.deleted_at IS NULL
  JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
  WHERE o.tenant_id=$1::uuid AND o.scoring_run_id=$2::uuid AND o.deleted_at IS NULL
  ORDER BY o.id
), classified AS (
  SELECT *,
    CASE WHEN question_type='true_false'
      THEN CASE ((question_rank / 4) % 2) WHEN 0 THEN 'true' ELSE 'false' END
      ELSE CASE ((question_rank / 4) % 4) WHEN 0 THEN 'A' WHEN 1 THEN 'B' WHEN 2 THEN 'C' ELSE 'D' END
    END AS option_label,
    (question_rank % 4) AS stratum_index
  FROM candidates
)
UPDATE omr_run o
SET status='completed',
  decision=CASE c.stratum_index WHEN 2 THEN 'blank' WHEN 3 THEN 'ambiguous' ELSE 'selected' END,
  selected_options=CASE WHEN c.stratum_index IN (0,1) THEN jsonb_build_array(c.option_label) ELSE '[]'::jsonb END,
  confidence=CASE c.stratum_index WHEN 0 THEN 0.99 WHEN 1 THEN 0.80 WHEN 2 THEN 0.95 ELSE 0.50 END,
  measurements=jsonb_build_array(jsonb_build_object('option',c.option_label,'foreground_delta',0.9)),
  result_version='story056-calibration-seed-v1',duration_ms=1,completed_at=now(),updated_at=now()
FROM classified c
WHERE o.id=c.id`, tenantID, runID)
	if err != nil {
		t.Fatalf("seed completed OMR calibration evidence: %v", err)
	}
	if count, _ := result.RowsAffected(); count != 120 {
		t.Fatalf("expected 120 stratified calibration OMR facts, got %d", count)
	}
	result, err = db.Exec(`
UPDATE agent_worker_task task
SET status='succeeded',result_schema_version='story056-calibration-seed-v1',result_payload_hash='sha256:story056-calibration-seed',duration_ms=1,completed_at=now(),updated_at=now()
FROM omr_run o
WHERE task.tenant_id=o.tenant_id AND task.id=o.runtime_task_id
  AND o.tenant_id=$1::uuid AND o.scoring_run_id=$2::uuid AND o.deleted_at IS NULL`, tenantID, runID)
	if err != nil {
		t.Fatalf("retire seeded calibration worker tasks: %v", err)
	}
	if count, _ := result.RowsAffected(); count != 120 {
		t.Fatalf("expected 120 retired calibration worker tasks, got %d", count)
	}
	if _, err = db.Exec(`UPDATE scoring_run SET status='completed',queued_count=0,review_count=0,failed_count=0,auto_confirmed_count=0,completed_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, runID); err != nil {
		t.Fatalf("complete seeded calibration scoring run: %v", err)
	}
}

// 此夹具创建已完成的 worker 证据，但不把考试推进到运行阶段。
// 这样克隆继承仍发生在配置阶段，生产评分运行 API 也仍正确地限制为 collecting 和 grading 考试。
func e2eSeedStory056MutableCalibrationOMRFacts(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture) {
	t.Helper()
	var layoutRaw []byte
	var templateStatus, templateHash string
	if err := db.QueryRow(`
SELECT layout,status,content_hash
FROM answer_sheet_template
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, fixture.TenantID, fixture.TemplateID).Scan(&layoutRaw, &templateStatus, &templateHash); err != nil {
		t.Fatalf("load mutable calibration template: %v", err)
	}
	var layout paper.TemplateLayout
	if err := json.Unmarshal(layoutRaw, &layout); err != nil {
		t.Fatalf("decode mutable calibration template layout: %v", err)
	}
	if layout.OMRProfile.Reference == nil {
		t.Fatal("template-difference calibration fixture must contain a server-bound reference")
	}
	reference := *layout.OMRProfile.Reference
	policy := paper.OMRAutoConfirmPolicyForTemplateReference(
		layout, templateStatus, templateHash, templateHash, reference, fixture.QuestionIDs["single_choice"],
	)
	if policy.Reference == nil || policy.RuntimeProfile.Mode != paper.OMRProfileModeTemplateDifference {
		t.Fatalf("mutable calibration fixture did not resolve template-difference evidence: %#v", policy)
	}

	var runID string
	if err := db.QueryRow(`
INSERT INTO scoring_run (
  tenant_id,exam_id,idempotency_key,status,total_count,queued_count,
  auto_confirmed_count,review_count,failed_count,started_by,started_at,completed_at
)
VALUES ($1::uuid,$2::uuid,$3,'completed',120,0,0,0,0,$4::uuid,now(),now())
RETURNING id::text`, fixture.TenantID, fixture.ExamID, "story056-mutable-calibration-"+fixture.TemplateID, fixture.AdminID).Scan(&runID); err != nil {
		t.Fatalf("create mutable calibration evidence run: %v", err)
	}
	result, err := db.Exec(`
WITH candidates AS (
  SELECT seg.id,seg.crop_file_asset_id,seg.crop_sha256,q.question_type,
    (row_number() OVER (PARTITION BY seg.question_id ORDER BY seg.id) - 1) AS question_rank
  FROM answer_segment seg
  JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
  WHERE seg.tenant_id=$1::uuid AND seg.template_id=$2::uuid AND seg.deleted_at IS NULL
), classified AS (
  SELECT *,
    CASE WHEN question_type='true_false'
      THEN CASE ((question_rank / 4) % 2) WHEN 0 THEN 'true' ELSE 'false' END
      ELSE CASE ((question_rank / 4) % 4) WHEN 0 THEN 'A' WHEN 1 THEN 'B' WHEN 2 THEN 'C' ELSE 'D' END
    END AS option_label,
    (question_rank % 4) AS stratum_index
  FROM candidates
)
INSERT INTO omr_run (
  tenant_id,scoring_run_id,answer_segment_id,crop_file_asset_id,crop_sha256,
  template_id,template_content_hash,profile_version,profile_hash,
  reference_file_asset_id,reference_sha256,auto_confirm_min_confidence,
  auto_confirm_eligible,auto_confirm_reason,status,decision,selected_options,
  confidence,measurements,result_version,duration_ms,completed_at
)
SELECT $1::uuid,$3::uuid,c.id,c.crop_file_asset_id,c.crop_sha256,
  $2::uuid,$4,$5,$6,$7::uuid,$8,$9,false,$10,'completed',
  CASE c.stratum_index WHEN 2 THEN 'blank' WHEN 3 THEN 'ambiguous' ELSE 'selected' END,
  CASE WHEN c.stratum_index IN (0,1) THEN jsonb_build_array(c.option_label) ELSE '[]'::jsonb END,
  CASE c.stratum_index WHEN 0 THEN 0.99 WHEN 1 THEN 0.80 WHEN 2 THEN 0.95 ELSE 0.50 END,
  jsonb_build_array(jsonb_build_object('option',c.option_label,'foreground_delta',0.9)),
  'story056-mutable-calibration-v1',1,now()
FROM classified c`, fixture.TenantID, fixture.TemplateID, runID, templateHash,
		policy.RuntimeProfile.Version, policy.ProfileHash, policy.Reference.FileAssetID, policy.Reference.HashSHA256,
		paper.OMRCalibrationMinimumConfidence, paper.OMRAutoConfirmReasonCalibrationUnapproved)
	if err != nil {
		t.Fatalf("seed mutable completed OMR calibration evidence: %v", err)
	}
	if count, _ := result.RowsAffected(); count != 120 {
		t.Fatalf("expected 120 mutable calibration OMR facts, got %d", count)
	}
}

func e2eStory056CalibrationExpectedOptions(t *testing.T, db *sql.DB, tenantID, caseID string) []string {
	t.Helper()
	var decision string
	var observedRaw, measurementsRaw []byte
	if err := db.QueryRow(`
SELECT observed_decision,observed_options,measurements
FROM omr_calibration_case
WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, caseID).Scan(&decision, &observedRaw, &measurementsRaw); err != nil {
		t.Fatalf("load blind calibration ground truth fixture: %v", err)
	}
	var options []string
	if err := json.Unmarshal(observedRaw, &options); err != nil {
		t.Fatalf("decode calibration observed options: %v", err)
	}
	if len(options) > 0 {
		return options
	}
	if decision == "blank" {
		return []string{}
	}
	var measurements []map[string]any
	if err := json.Unmarshal(measurementsRaw, &measurements); err != nil {
		t.Fatalf("decode calibration measurements: %v", err)
	}
	if len(measurements) == 0 {
		t.Fatalf("ambiguous calibration case has no fixture ground truth: %s", caseID)
	}
	option, ok := measurements[0]["option"].(string)
	if !ok || option == "" {
		t.Fatalf("ambiguous calibration case has invalid fixture ground truth: %#v", measurements[0])
	}
	return []string{option}
}

func e2eStory056CalibrationCaseForQuestionAndStratum(t *testing.T, cases []any, questionID, stratum string) map[string]any {
	t.Helper()
	for _, raw := range cases {
		item, ok := raw.(map[string]any)
		if ok && e2eString(t, item, "question_id") == questionID && e2eString(t, item, "sample_stratum") == stratum {
			return item
		}
	}
	t.Fatalf("missing calibration case for question %s in stratum %s", questionID, stratum)
	return nil
}

func e2eStory056CalibrationReprocessAndClaim(t *testing.T, router http.Handler, adminToken, segmentID, suffix string) (string, map[string]any, story056WorkerLease) {
	t.Helper()
	run := e2ePostJSON(t, router, http.MethodPost, "/api/v1/answer-segments/"+segmentID+"/reprocess-score", adminToken, story056JSON(t, map[string]any{
		"idempotency_key": "story056-calibration-" + suffix,
	}), http.StatusCreated)["scoring_run"].(map[string]any)
	runID := e2eString(t, run, "id")
	item, lease := e2eStory056ClaimSingleOMR(t, router, adminToken, runID, "story056-calibration-worker")
	return runID, item, lease
}

// 校准结果仍分别携带算法版本、配置版本、配置哈希和参考图哈希，不能用一个版本值替代其余证据。
type story056CalibrationProfile struct {
	Version, ProfileVersion, ProfileHash, ReferenceSHA256 string
}

func e2eStory056CalibrationResultPayload(t *testing.T, lease story056WorkerLease, item map[string]any, overlay files.FileAsset, profile story056CalibrationProfile) map[string]any {
	t.Helper()
	version, profileVersion, profileHash, referenceSHA256 := profile.Version, profile.ProfileVersion, profile.ProfileHash, profile.ReferenceSHA256
	payload := e2eStory056OMRResultPayload(t, lease, item, overlay, version, []map[string]any{{"option": "A", "foreground_delta": 0.9}}, false)
	payload["profile_version"] = profileVersion
	payload["profile_hash"] = profileHash
	payload["reference_sha256"] = referenceSHA256
	payload["thresholds"] = map[string]any{
		"marked_threshold": 0.12, "ambiguous_threshold": 0.04, "minimum_margin": 0.04,
		"border_fraction": 0.12, "reference_mask_dilation_pixels": 1,
	}
	return payload
}
