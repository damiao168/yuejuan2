package server

import (
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
	"strings"
	"testing"
)

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
  confidence=CASE c.stratum_index WHEN 0 THEN 0.99 WHEN 1 THEN 0.80 WHEN 2 THEN 0.99 ELSE 0.50 END,
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
	e2eSeedStory056CalibrationWorkerResults(t, db, tenantID, runID)
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
  CASE c.stratum_index WHEN 0 THEN 0.99 WHEN 1 THEN 0.80 WHEN 2 THEN 0.99 ELSE 0.50 END,
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
	e2eSeedStory056CalibrationWorkerResults(t, db, fixture.TenantID, runID)
}

// 校准冻结精确已完成 Worker 的复核标志和集合质量，不能仅把 omr_run 标成 completed。
// 这里构造完整合成结果和规范摘要；配置阶段夹具补齐任务绑定，仍不伪装为实际算法验收。
func e2eSeedStory056CalibrationWorkerResults(t *testing.T, db *sql.DB, tenantID, runID string) {
	t.Helper()
	if _, err := db.Exec(`WITH inserted AS (
 INSERT INTO agent_worker_task(tenant_id,task_type,queue_name,source_type,source_id,payload_schema_version,idempotency_key,dedupe_key,created_by)
 SELECT o.tenant_id,'omr_extract','page-processing','omr_run',o.id,'omr-task-v2','synthetic-calibration:'||o.id::text,'synthetic-calibration:'||o.id::text,run.started_by
 FROM omr_run o JOIN scoring_run run ON run.tenant_id=o.tenant_id AND run.id=o.scoring_run_id
 WHERE o.tenant_id=$1::uuid AND o.scoring_run_id=$2::uuid AND o.runtime_task_id IS NULL AND o.deleted_at IS NULL
 RETURNING tenant_id,id,source_id
) UPDATE omr_run o SET runtime_task_id=task.id FROM inserted task WHERE o.tenant_id=task.tenant_id AND o.id=task.source_id`, tenantID, runID); err != nil {
		t.Fatalf("bind synthetic calibration workers: %v", err)
	}
	result, err := db.Exec(`UPDATE agent_worker_task task SET status='succeeded',result_schema_version='omr-result-v1',duration_ms=1,completed_at=now(),updated_at=now(),
 result=jsonb_build_object(
  'omr_run_id',o.id::text,'result_version',o.result_version,'decision',o.decision,'selected',o.selected_options,
  'confidence',o.confidence,'needs_human_review',(o.decision='ambiguous' OR o.confidence<$3),
  'measurements',o.measurements,'profile_version',o.profile_version,'profile_hash',o.profile_hash,'reference_sha256',o.reference_sha256,
  'overlay_file_asset_id',COALESCE(o.overlay_file_asset_id::text,''),'overlay_sha256','',
  'thresholds',jsonb_build_object('quality',jsonb_build_object(
   'version','omr-set-quality-v1','question_type',CASE WHEN q.question_type='multiple_choice' THEN 'multiple_choice' ELSE 'single_choice' END,
   'classification',CASE o.decision WHEN 'blank' THEN 'blank' WHEN 'selected' THEN 'clear' ELSE 'uncertain_mark' END,
   'set_confidence',o.confidence,'option_count',CASE q.question_type WHEN 'true_false' THEN 2 ELSE 4 END,
   'selected_count',jsonb_array_length(o.selected_options),
   'risk_flags',CASE WHEN o.decision='ambiguous' THEN '["uncertain_mark"]'::jsonb ELSE '[]'::jsonb END)))
 FROM omr_run o JOIN answer_segment seg ON seg.tenant_id=o.tenant_id AND seg.id=o.answer_segment_id
 JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id
 WHERE task.tenant_id=o.tenant_id AND task.id=o.runtime_task_id AND task.source_type='omr_run' AND task.source_id=o.id
 AND o.tenant_id=$1::uuid AND o.scoring_run_id=$2::uuid AND o.deleted_at IS NULL`, tenantID, runID, paper.OMRCalibrationMinimumConfidence)
	if err != nil {
		t.Fatalf("persist synthetic calibration Worker results: %v", err)
	}
	if count, _ := result.RowsAffected(); count != 120 {
		t.Fatalf("expected 120 exact-version Worker results, got %d", count)
	}
	rows, err := db.Query(`SELECT task.id::text,task.result FROM agent_worker_task task JOIN omr_run o ON o.tenant_id=task.tenant_id AND o.runtime_task_id=task.id WHERE o.tenant_id=$1::uuid AND o.scoring_run_id=$2::uuid AND o.deleted_at IS NULL`, tenantID, runID)
	if err != nil {
		t.Fatal(err)
	}
	hashes := map[string]string{}
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		var payload map[string]any
		if err = json.Unmarshal(raw, &payload); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		hashes[id], err = workerruntime.ResultPayloadHash("omr-result-v1", payload)
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	for id, hash := range hashes {
		if _, err = db.Exec(`UPDATE agent_worker_task SET result_payload_hash=$3 WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, id, hash); err != nil {
			t.Fatal(err)
		}
	}
}
