package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
)

// ReprocessCompletedOMR 在原评分计划内重建一个已完成的 OMR，不重新执行其它题的模型观察。
// 调用方负责维护权限和操作审计；本命令校验租户、冻结输入及教师工作，并在一个事务内替换当前事实。
// 原 OMR ID 同时代表这次重建命令；重复调用返回已接受的新运行，不再次创建 Worker 任务。
func (s *PostgresStore) ReprocessCompletedOMR(ctx context.Context, tenantID, runID, oldOMRID, actorID string) (OMRRun, error) {
	for _, id := range []string{tenantID, runID, oldOMRID, actorID} {
		if _, err := uuid.Parse(id); err != nil {
			return OMRRun{}, ErrInvalidInput
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OMRRun{}, err
	}
	defer tx.Rollback()
	// 与回调、取消和成绩提交一致：先锁父运行，再锁来源、计划成员和人工任务。
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID).Scan(&status); err != nil {
		return OMRRun{}, mapCompletedOMRLookupError(err)
	}
	var segmentID, oldStatus string
	if err = tx.QueryRowContext(ctx, `SELECT answer_segment_id::text,status FROM omr_run WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND id=$3::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID, oldOMRID).Scan(&segmentID, &oldStatus); err != nil {
		return OMRRun{}, mapCompletedOMRLookupError(err)
	}
	key := "omr-reprocess:" + oldOMRID
	var newOMRID string
	err = tx.QueryRowContext(ctx, `SELECT source_id::text FROM agent_worker_task WHERE tenant_id=$1::uuid AND task_type='omr_extract' AND idempotency_key=$2 AND source_type='omr_run'`, tenantID, key).Scan(&newOMRID)
	if err == nil {
		// 已接受命令先重放；新运行可能已被教师接手，重放不能重新撤销教师工作。
		if err = tx.Commit(); err != nil {
			return OMRRun{}, err
		}
		return s.GetOMRRun(ctx, tenantID, newOMRID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return OMRRun{}, err
	}
	if status == "cancelling" || status == "cancelled" || oldStatus != "completed" {
		return OMRRun{}, ErrInvalidTransition
	}
	var itemID string
	var current bool
	err = tx.QueryRowContext(ctx, `SELECT item.id::text,COALESCE((
 seg.deleted_at IS NULL AND seg.processing_status='completed' AND seg.crop_file_asset_id=o.crop_file_asset_id AND seg.crop_sha256=o.crop_sha256
 AND seg.crop_file_asset_id=iv.crop_file_asset_id AND seg.crop_sha256=iv.crop_sha256
 AND seg.registration_run_id IS NOT DISTINCT FROM iv.registration_run_id
 AND seg.template_id=o.template_id AND seg.template_content_hash=o.template_content_hash AND seg.template_content_hash=iv.template_content_hash
 AND item.processing_status='succeeded' AND item.decision_status<>'human_confirmed' AND item.subjective_run_id IS NULL),false)
 FROM omr_run o JOIN answer_segment seg ON seg.tenant_id=o.tenant_id AND seg.id=o.answer_segment_id
 JOIN scoring_run_item item ON item.tenant_id=o.tenant_id AND item.scoring_run_id=o.scoring_run_id AND item.answer_segment_id=o.answer_segment_id AND item.runtime_task_id=o.runtime_task_id AND item.route='omr'
 JOIN answer_input_version iv ON iv.tenant_id=item.tenant_id AND iv.id=item.input_version_id
 WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid FOR UPDATE OF seg,item`, tenantID, oldOMRID).Scan(&itemID, &current)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !current {
		return OMRRun{}, ErrRevisionConflict
	}
	if err != nil {
		return OMRRun{}, err
	}
	// 领取和草稿保存都先锁人工任务；逐行锁定后再检查，避免检查通过后教师恰好开始工作。
	rows, err := tx.QueryContext(ctx, `SELECT id::text,COALESCE(status='pending' AND assigned_to IS NULL AND claimed_at IS NULL AND last_opened_at IS NULL AND current_grade_id IS NULL AND scoring_run_id=$3::uuid AND grade_round='single' AND source IN ('omr_ambiguous','grading_failure','rule_review_required'),false) FROM review_task WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL AND status<>'cancelled' ORDER BY id FOR UPDATE`, tenantID, segmentID, runID)
	if err != nil {
		return OMRRun{}, err
	}
	untouched := true
	for rows.Next() {
		var id string
		var pending bool
		if err = rows.Scan(&id, &pending); err != nil {
			rows.Close()
			return OMRRun{}, err
		}
		untouched = untouched && pending
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return OMRRun{}, err
	}
	var teacherWork, otherOMR bool
	err = tx.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND source='human' AND deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM review_draft d JOIN review_task rt ON rt.tenant_id=d.tenant_id AND rt.id=d.review_task_id WHERE rt.tenant_id=$1::uuid AND rt.answer_segment_id=$2::uuid AND d.deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM double_mark_session WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM final_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL),
 EXISTS(SELECT 1 FROM omr_run WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND id<>$3::uuid AND status<>'invalidated' AND deleted_at IS NULL)`, tenantID, segmentID, oldOMRID).Scan(&teacherWork, &otherOMR)
	if err != nil {
		return OMRRun{}, err
	}
	if !untouched || teacherWork || otherOMR {
		return OMRRun{}, ErrInvalidTransition
	}
	var payload []byte
	var schema string
	var priority, maxAttempts, backoff int
	err = tx.QueryRowContext(ctx, `SELECT wt.payload,wt.payload_schema_version,wt.priority,wt.max_attempts,wt.retry_backoff_seconds FROM agent_worker_task wt JOIN omr_run o ON o.tenant_id=wt.tenant_id AND o.runtime_task_id=wt.id WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid AND wt.source_type='omr_run' AND wt.source_id=o.id AND wt.task_type='omr_extract' AND wt.status='succeeded' FOR UPDATE OF wt`, tenantID, oldOMRID).Scan(&payload, &schema, &priority, &maxAttempts, &backoff)
	if err != nil {
		return OMRRun{}, mapCompletedOMRLookupError(err)
	}
	var frozen map[string]json.RawMessage
	if json.Unmarshal(payload, &frozen) != nil || frozen == nil {
		return OMRRun{}, ErrInvalidInput
	}
	policy, err := reprocessOMRAutoConfirmPolicyTx(ctx, tx, tenantID, oldOMRID, frozen)
	if err != nil {
		return OMRRun{}, err
	}
	// 选项位置、裁图、参考图和提取 profile 原样复制；仅新运行可重新取得默认填涂的服务端资格。
	err = tx.QueryRowContext(ctx, `INSERT INTO omr_run(tenant_id,scoring_run_id,answer_segment_id,crop_file_asset_id,crop_sha256,template_id,template_content_hash,profile_version,profile_hash,reference_file_asset_id,reference_sha256,calibration_session_id,calibration_evidence_hash,auto_confirm_min_confidence,auto_confirm_eligible,auto_confirm_reason)
 SELECT tenant_id,scoring_run_id,answer_segment_id,crop_file_asset_id,crop_sha256,template_id,template_content_hash,profile_version,profile_hash,reference_file_asset_id,reference_sha256,calibration_session_id,calibration_evidence_hash,$3,$4,$5 FROM omr_run WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING id::text`, tenantID, oldOMRID, policy.MinimumConfidence, policy.AutoConfirmEligible, policy.Reason).Scan(&newOMRID)
	if err != nil {
		return OMRRun{}, err
	}
	newPayload := map[string]any{}
	for name, raw := range frozen {
		newPayload[name] = raw
	}
	newPayload["omr_run_id"] = newOMRID
	task, err := workerruntime.CreateTaskInTx(ctx, tx, tenantID, actorID, workerruntime.CreateTaskInput{TaskType: "omr_extract", QueueName: "page-processing", SourceType: "omr_run", SourceID: newOMRID, Priority: priority, Payload: newPayload, PayloadSchemaVersion: schema, IdempotencyKey: key, DedupeKey: key, MaxAttempts: maxAttempts, RetryBackoffSeconds: backoff})
	if err != nil {
		return OMRRun{}, err
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`UPDATE omr_run SET status='invalidated',updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, []any{tenantID, oldOMRID}},
		{`UPDATE omr_run SET runtime_task_id=$3::uuid WHERE tenant_id=$1::uuid AND id=$2::uuid`, []any{tenantID, newOMRID, task.ID}},
		{`UPDATE answer_candidate SET is_current=false WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current`, []any{tenantID, segmentID}},
		{`UPDATE question_grade SET is_current=false,status='invalidated' WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND source='rule_confirmed' AND deleted_at IS NULL`, []any{tenantID, segmentID}},
		{`UPDATE review_task SET status='cancelled',revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid AND grade_round='single' AND source IN ('omr_ambiguous','grading_failure','rule_review_required') AND status='pending' AND assigned_to IS NULL AND deleted_at IS NULL`, []any{tenantID, segmentID, runID}},
		{`UPDATE scoring_run_item SET runtime_task_id=$3::uuid,stage='observe',processing_status='queued',decision_status='undecided',recognition_completed=false,artifact_ids='[]',reason_code='',next_action='wait',retryable=false,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, []any{tenantID, itemID, task.ID}},
	} {
		if _, err = tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return OMRRun{}, err
		}
	}
	if _, err = s.refreshScoringRunTx(ctx, tx, tenantID, runID); err != nil {
		return OMRRun{}, err
	}
	if err = tx.Commit(); err != nil {
		return OMRRun{}, err
	}
	return s.GetOMRRun(ctx, tenantID, newOMRID)
}

// reprocessOMRAutoConfirmPolicyTx 只更新新运行的默认填涂资格，不改旧事实、已冻结的提取输入或差分校准范围。
// 历史仅人工原因、运行版本与哈希、Worker 载荷及当前同校锁定模板全部相符时才采用最新默认规则。
func reprocessOMRAutoConfirmPolicyTx(ctx context.Context, tx *sql.Tx, tenantID, oldOMRID string, frozen map[string]json.RawMessage) (paper.OMRAutoConfirmPolicy, error) {
	var old OMRRun
	var templateID, templateHash string
	err := tx.QueryRowContext(ctx, `SELECT template_id::text,template_content_hash,profile_version,profile_hash,COALESCE(reference_file_asset_id::text,''),COALESCE(reference_sha256,''),COALESCE(calibration_session_id::text,''),COALESCE(calibration_evidence_hash,''),auto_confirm_min_confidence,auto_confirm_eligible,auto_confirm_reason FROM omr_run WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, oldOMRID).Scan(
		&templateID, &templateHash, &old.ProfileVersion, &old.ProfileHash, &old.ReferenceFileAssetID, &old.ReferenceSHA256,
		&old.CalibrationSessionID, &old.CalibrationEvidenceHash, &old.AutoConfirmMinConfidence, &old.AutoConfirmEligible, &old.AutoConfirmReason)
	if err != nil {
		return paper.OMRAutoConfirmPolicy{}, err
	}
	policy := paper.OMRAutoConfirmPolicy{MinimumConfidence: old.AutoConfirmMinConfidence, AutoConfirmEligible: old.AutoConfirmEligible, Reason: old.AutoConfirmReason}
	if !legacyDefaultOMRReprocessMatches(old, frozen) {
		return policy, nil
	}
	var template paper.AnswerSheetTemplate
	var layout []byte
	err = tx.QueryRowContext(ctx, `SELECT id::text,status,content_hash,layout FROM answer_sheet_template WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR SHARE`, tenantID, templateID).Scan(&template.ID, &template.Status, &template.ContentHash, &layout)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, nil
	}
	if err != nil {
		return paper.OMRAutoConfirmPolicy{}, err
	}
	if err = decodeJSONB(layout, &template.Layout, "answer_sheet_template.layout"); err != nil {
		return paper.OMRAutoConfirmPolicy{}, err
	}
	current := paper.OMRAutoConfirmPolicyForTemplateReferenceAndCalibration(template, templateHash, paper.TemplateOMRReference{}, "", nil)
	if current.AutoConfirmEligible && current.RuntimeProfile.Mode == paper.OMRProfileModeManualOnly && current.ProfileHash == old.ProfileHash {
		return current, nil
	}
	return policy, nil
}

func legacyDefaultOMRReprocessMatches(old OMRRun, frozen map[string]json.RawMessage) bool {
	if old.AutoConfirmEligible || old.AutoConfirmReason != paper.OMRAutoConfirmReasonManualOnlyProfile || old.ProfileVersion != paper.OMRProfileVersionOpenCVFillV1 || old.ProfileHash != paper.DefaultOMRRuntimeProfileHash() ||
		old.ReferenceFileAssetID != "" || old.ReferenceSHA256 != "" || old.CalibrationSessionID != "" || old.CalibrationEvidenceHash != "" {
		return false
	}
	var profileHash string
	var profile paper.OMRRuntimeProfile
	if json.Unmarshal(frozen["profile_hash"], &profileHash) != nil || profileHash != old.ProfileHash || json.Unmarshal(frozen["profile"], &profile) != nil {
		return false
	}
	if reference, exists := frozen["reference"]; exists && string(reference) != "null" {
		return false
	}
	return profile.Mode == paper.OMRProfileModeManualOnly && profile.Version == old.ProfileVersion && paper.OMRRuntimeProfileHash(profile) == old.ProfileHash
}

func mapCompletedOMRLookupError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
