package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
)

// ReprocessCompletedObservation 在原计划内重辨认一个尚无教师工作的 observe_rule 条目。
// 调用方负责维护权限和审计；本命令不新建评分运行，共区输入必须另行处理，不能拆开共享观察。
// 原观察任务代表命令身份；接受后的重放只返回新任务，不能再次撤销已产生的教师工作。
func (s *PostgresStore) ReprocessCompletedObservation(ctx context.Context, tenantID, runID, itemID, oldObservationTaskID, actorID string) (workerruntime.Task, error) {
	for _, id := range []string{tenantID, runID, itemID, oldObservationTaskID, actorID} {
		if _, err := uuid.Parse(id); err != nil {
			return workerruntime.Task{}, ErrInvalidInput
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workerruntime.Task{}, err
	}
	defer tx.Rollback()
	// 与完成、取消及教师提交使用相同的父运行锁；所有撤销和排队必须一起提交。
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID).Scan(&status); err != nil {
		return workerruntime.Task{}, mapCompletedOMRLookupError(err)
	}
	key := "observation-reprocess:" + oldObservationTaskID
	var replayID string
	err = tx.QueryRowContext(ctx, `SELECT wt.id::text FROM agent_worker_task wt JOIN scoring_run_item item ON item.tenant_id=wt.tenant_id AND item.id=$3::uuid AND item.scoring_run_id=$2::uuid AND item.input_version_id=wt.source_id WHERE wt.tenant_id=$1::uuid AND wt.task_type='ocr' AND wt.source_type='answer_input_version' AND wt.idempotency_key=$4 AND wt.payload->>'scoring_run_id'=$2::text`, tenantID, runID, itemID, key).Scan(&replayID)
	if err == nil {
		if err = tx.Commit(); err != nil {
			return workerruntime.Task{}, err
		}
		return workerruntime.NewPostgresStore(s.db).Get(ctx, tenantID, replayID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return workerruntime.Task{}, err
	}
	if status == "cancelling" || status == "cancelled" {
		return workerruntime.Task{}, ErrInvalidTransition
	}
	var segmentID, inputID string
	var current bool
	err = tx.QueryRowContext(ctx, `SELECT item.answer_segment_id::text,item.input_version_id::text,COALESCE((
 item.route='observe_rule' AND item.processing_status='succeeded' AND item.decision_status IN ('auto_confirmed','review_needed')
 AND item.observation_task_id=$4::uuid AND item.runtime_task_id=$4::uuid AND item.subjective_run_id IS NULL AND item.observation_json IS NOT NULL
 AND seg.deleted_at IS NULL AND seg.processing_status='completed' AND seg.question_id=item.question_id AND seg.submission_id=iv.submission_id
 AND seg.crop_file_asset_id=iv.crop_file_asset_id AND seg.crop_sha256=iv.crop_sha256
 AND seg.registration_run_id IS NOT DISTINCT FROM iv.registration_run_id AND seg.template_content_hash=iv.template_content_hash
 AND iv.answer_segment_id=seg.id AND iv.answer_scope='question' AND jsonb_array_length(iv.members_json)=1
 AND iv.members_json->0->>'answer_segment_id'=seg.id::text AND iv.members_json->0->>'question_id'=item.question_id::text
 AND NOT EXISTS(SELECT 1 FROM scoring_run_item other WHERE other.tenant_id=item.tenant_id AND other.scoring_run_id=item.scoring_run_id AND other.input_version_id=iv.id AND other.id<>item.id)
 AND sr.rule_snapshot->'questions'->(item.question_id::text)->'assessment_snapshot'->>'id'=item.assessment_snapshot_id::text
 AND sr.rule_snapshot->'questions'->(item.question_id::text)->'rule'->>'id'=item.frozen_rule_id::text
 AND item.assessment_snapshot_id=(SELECT id FROM exam_question_snapshot WHERE tenant_id=item.tenant_id AND exam_id=sr.exam_id AND question_id=item.question_id ORDER BY snapshot_version DESC,id DESC LIMIT 1)
 ),false)
 FROM scoring_run_item item JOIN scoring_run sr ON sr.tenant_id=item.tenant_id AND sr.id=item.scoring_run_id
 JOIN answer_input_version iv ON iv.tenant_id=item.tenant_id AND iv.id=item.input_version_id
 JOIN answer_segment seg ON seg.tenant_id=item.tenant_id AND seg.id=item.answer_segment_id
 WHERE item.tenant_id=$1::uuid AND item.scoring_run_id=$2::uuid AND item.id=$3::uuid FOR UPDATE OF seg,iv,item`, tenantID, runID, itemID, oldObservationTaskID).Scan(&segmentID, &inputID, &current)
	if err != nil {
		return workerruntime.Task{}, mapCompletedOMRLookupError(err)
	}
	if !current {
		return workerruntime.Task{}, ErrRevisionConflict
	}
	// 先锁人工任务再检查领取、打开和草稿；只有本轮尚未接手的规则复核可撤回。
	rows, err := tx.QueryContext(ctx, `SELECT COALESCE(status='pending' AND assigned_to IS NULL AND claimed_at IS NULL AND last_opened_at IS NULL AND current_grade_id IS NULL AND scoring_run_id=$3::uuid AND grade_round='single' AND source IN ('grading_failure','rule_review_required'),false) FROM review_task WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL AND status<>'cancelled' ORDER BY id FOR UPDATE`, tenantID, segmentID, runID)
	if err != nil {
		return workerruntime.Task{}, err
	}
	untouched := true
	for rows.Next() {
		var pending bool
		if err = rows.Scan(&pending); err != nil {
			rows.Close()
			return workerruntime.Task{}, err
		}
		untouched = untouched && pending
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return workerruntime.Task{}, err
	}
	var protected bool
	err = tx.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM answer_segment_answer WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND source='manual_entry' AND deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM answer_candidate WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL AND (is_current AND (source<>'ocr' OR scoring_run_id IS DISTINCT FROM $3::uuid OR input_version_id IS DISTINCT FROM $4::uuid) OR scoring_run_id=$3::uuid AND input_version_id=$4::uuid AND source<>'ocr'))
 OR EXISTS(SELECT 1 FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL AND (source='human' OR is_current AND (source<>'rule_confirmed' OR scoring_run_id IS DISTINCT FROM $3::uuid)))
 OR EXISTS(SELECT 1 FROM review_draft d JOIN review_task rt ON rt.tenant_id=d.tenant_id AND rt.id=d.review_task_id WHERE rt.tenant_id=$1::uuid AND rt.answer_segment_id=$2::uuid AND d.deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM double_mark_session WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM final_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL)`, tenantID, segmentID, runID, inputID).Scan(&protected)
	if err != nil {
		return workerruntime.Task{}, err
	}
	if !untouched || protected {
		return workerruntime.Task{}, ErrInvalidTransition
	}
	var payload []byte
	var schema string
	var priority, maxAttempts, backoff int
	err = tx.QueryRowContext(ctx, `SELECT payload,payload_schema_version,priority,max_attempts,retry_backoff_seconds FROM agent_worker_task WHERE tenant_id=$1::uuid AND id=$2::uuid AND task_type='ocr' AND queue_name=$3 AND source_type='answer_input_version' AND source_id=$4::uuid AND status='succeeded' AND payload->>'scoring_run_id'=$5 AND payload->>'input_version_id'=$4::text FOR UPDATE`, tenantID, oldObservationTaskID, scoringCoordinatorQueue, inputID, runID).Scan(&payload, &schema, &priority, &maxAttempts, &backoff)
	if err != nil {
		return workerruntime.Task{}, mapCompletedOMRLookupError(err)
	}
	var frozen map[string]any
	if json.Unmarshal(payload, &frozen) != nil || frozen == nil || schema != "scoring-observation-task-v1" {
		return workerruntime.Task{}, ErrInvalidInput
	}
	// 输入和本轮规则都保持冻结，仅更换任务身份。软删除旧候选释放同 run/input 的唯一键，旧证据仍留在历史行。
	task, err := workerruntime.CreateTaskInTx(ctx, tx, tenantID, actorID, workerruntime.CreateTaskInput{TaskType: "ocr", QueueName: scoringCoordinatorQueue, SourceType: "answer_input_version", SourceID: inputID, Priority: priority, Payload: frozen, PayloadSchemaVersion: schema, IdempotencyKey: key, DedupeKey: key, MaxAttempts: maxAttempts, RetryBackoffSeconds: backoff})
	if err != nil {
		return workerruntime.Task{}, err
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`UPDATE answer_candidate SET is_current=false,deleted_at=now() WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid AND input_version_id=$4::uuid AND source='ocr' AND deleted_at IS NULL`, []any{tenantID, segmentID, runID, inputID}},
		{`UPDATE answer_segment_answer SET deleted_at=now() WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND source='ocr_text' AND answer_payload->>'observation_task_id'=$3 AND deleted_at IS NULL`, []any{tenantID, segmentID, oldObservationTaskID}},
		{`UPDATE question_grade SET is_current=false,status='invalidated' WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid AND is_current AND source='rule_confirmed' AND deleted_at IS NULL`, []any{tenantID, segmentID, runID}},
		{`UPDATE review_task SET status='cancelled',revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid AND grade_round='single' AND source IN ('grading_failure','rule_review_required') AND status='pending' AND assigned_to IS NULL AND deleted_at IS NULL`, []any{tenantID, segmentID, runID}},
		{`UPDATE scoring_run_item SET observation_task_id=$3::uuid,runtime_task_id=$3::uuid,observation_json=NULL,stage='observe',processing_status='queued',decision_status='undecided',recognition_completed=false,model_invoked=NULL,artifact_ids='[]',reason_code='observation_pending',next_action='wait',retryable=false,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, []any{tenantID, itemID, task.ID}},
	} {
		if _, err = tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return workerruntime.Task{}, err
		}
	}
	if _, err = s.refreshScoringRunTx(ctx, tx, tenantID, runID); err != nil {
		return workerruntime.Task{}, err
	}
	if err = tx.Commit(); err != nil {
		return workerruntime.Task{}, err
	}
	return task, nil
}
