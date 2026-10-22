package grading

import (
	"context"
	"database/sql"
	"encoding/json"
)

// 实际执行器和人工结果是事实来源，条目只投影其当前状态。AI 成功产生建议，不等于最终成绩确认。
// OMR 仅取计划当前绑定的有效任务；重处理留下的历史不能把新任务提前投影成成功。
func refreshScoringItemsTx(ctx context.Context, tx *sql.Tx, tenantID, runID string) error {
	_, err := tx.ExecContext(ctx, `WITH facts AS (
 SELECT item.id,item.route,item.observation_json,item.processing_status,item.decision_status,
   o.status AS omr_status,wt.status AS runtime_status,ai.status AS ai_status,ai.error_code AS ai_error,
   g.source AS grade_source,rt.status AS review_status,
   EXISTS(SELECT 1 FROM answer_candidate ac WHERE ac.tenant_id=item.tenant_id AND ac.scoring_run_id=item.scoring_run_id AND ac.answer_segment_id=item.answer_segment_id AND ac.is_current AND ac.deleted_at IS NULL) AS has_candidate
 FROM scoring_run_item item
 LEFT JOIN omr_run o ON o.tenant_id=item.tenant_id AND o.scoring_run_id=item.scoring_run_id AND o.answer_segment_id=item.answer_segment_id AND o.runtime_task_id=item.runtime_task_id AND o.status<>'invalidated' AND o.deleted_at IS NULL
 LEFT JOIN subjective_grading_run ai ON ai.tenant_id=item.tenant_id AND ai.id=item.subjective_run_id AND ai.deleted_at IS NULL
 LEFT JOIN agent_worker_task wt ON wt.tenant_id=item.tenant_id AND wt.id=item.runtime_task_id
 LEFT JOIN LATERAL(SELECT source FROM question_grade WHERE tenant_id=item.tenant_id AND answer_segment_id=item.answer_segment_id AND is_current AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1)g ON true
 LEFT JOIN LATERAL(SELECT status FROM review_task WHERE tenant_id=item.tenant_id AND answer_segment_id=item.answer_segment_id AND scoring_run_id=item.scoring_run_id AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1)rt ON true
 WHERE item.tenant_id=$1::uuid AND item.scoring_run_id=$2::uuid
), projected AS (
 SELECT id,
 CASE WHEN processing_status IN ('cancelled','superseded') THEN processing_status
      WHEN grade_source IS NOT NULL THEN 'succeeded'
      WHEN ai_status='succeeded' THEN 'succeeded'
      WHEN omr_status='completed' OR review_status IN ('pending','assigned','in_progress','returned') AND route='omr' THEN 'succeeded'
      WHEN runtime_status IN ('dead_letter','failed') THEN CASE WHEN processing_status='blocked' THEN 'blocked' ELSE 'failed' END
      WHEN runtime_status IN ('leased','running') THEN 'running'
      WHEN runtime_status='queued' THEN CASE WHEN processing_status='retry_wait' THEN 'retry_wait' ELSE 'queued' END
      ELSE processing_status END AS processing,
 CASE WHEN grade_source='human' THEN 'human_confirmed'
      WHEN grade_source='rule_confirmed' THEN 'auto_confirmed'
      WHEN ai_status='succeeded' THEN 'suggestion_ready'
      WHEN omr_status='completed' OR review_status IN ('pending','assigned','in_progress','returned') AND route='omr' THEN 'review_needed'
      ELSE decision_status END AS decision,
 has_candidate OR observation_json IS NOT NULL AS recognized,
 ai_status,ai_error
 FROM facts
)
UPDATE scoring_run_item item SET processing_status=p.processing,decision_status=p.decision,
 stage=CASE WHEN p.processing='succeeded' THEN 'done' ELSE item.stage END,
 recognition_completed=item.recognition_completed OR (item.route='omr' AND p.recognized),
 reason_code=CASE WHEN p.decision='suggestion_ready' THEN 'ai_suggestion_ready' WHEN p.decision IN ('auto_confirmed','human_confirmed') THEN '' WHEN p.ai_status IN ('failed','conflict') THEN COALESCE(p.ai_error,'ai_suggestion_failed') ELSE item.reason_code END,
 next_action=CASE WHEN p.decision IN ('suggestion_ready','review_needed','manual_only') THEN 'teacher_review' WHEN p.decision IN ('auto_confirmed','human_confirmed') THEN 'none' ELSE item.next_action END,
 retryable=CASE WHEN p.processing='failed' THEN true WHEN p.processing='succeeded' THEN false ELSE item.retryable END,
 revision=item.revision+1,updated_at=now()
FROM projected p WHERE item.tenant_id=$1::uuid AND item.id=p.id
 AND (item.processing_status IS DISTINCT FROM p.processing OR item.decision_status IS DISTINCT FROM p.decision OR item.route='omr' AND NOT item.recognition_completed AND p.recognized)`, tenantID, runID)
	return err
}

func (s *PostgresStore) decorateScoringPlanItems(ctx context.Context, tenantID, runID, examID string, items []ScoringRunItem) error {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT ON(item.answer_segment_id) item.answer_segment_id::text,item.input_version_id::text,item.route,item.stage,item.processing_status,item.decision_status,item.next_action,item.revision,item.recognition_completed,item.model_invoked,item.retryable,item.artifact_ids,item.reason_code,COALESCE(item.runtime_task_id::text,''),COALESCE(wt.status,''),COALESCE(wt.error_code,''),item.assessment_snapshot_id::text FROM scoring_run_item item JOIN scoring_run sr ON sr.tenant_id=item.tenant_id AND sr.id=item.scoring_run_id LEFT JOIN agent_worker_task wt ON wt.tenant_id=item.tenant_id AND wt.id=item.runtime_task_id WHERE item.tenant_id=$1::uuid AND ((NULLIF($2,'')::uuid IS NOT NULL AND item.scoring_run_id=NULLIF($2,'')::uuid) OR ($2='' AND sr.exam_id=$3::uuid)) ORDER BY item.answer_segment_id,sr.created_at DESC,item.plan_generation DESC`, tenantID, runID, examID)
	if err != nil {
		return err
	}
	defer rows.Close()
	index := map[string]int{}
	for i, item := range items {
		index[item.AnswerSegmentID] = i
	}
	for rows.Next() {
		var item ScoringRunItem
		var raw []byte
		var invoked sql.NullBool
		if err = rows.Scan(&item.AnswerSegmentID, &item.InputVersionID, &item.Route, &item.Stage, &item.ProcessingStatus, &item.DecisionStatus, &item.NextAction, &item.Revision, &item.RecognitionCompleted, &invoked, &item.Retryable, &raw, &item.ReasonCode, &item.RuntimeTaskID, &item.RuntimeStatus, &item.ErrorCode, &item.AssessmentSnapshotID); err != nil {
			return err
		}
		i, ok := index[item.AnswerSegmentID]
		if !ok {
			continue
		}
		current := &items[i]
		current.InputVersionID = item.InputVersionID
		current.AssessmentSnapshotID = item.AssessmentSnapshotID
		current.Route = item.Route
		current.Stage = item.Stage
		current.ProcessingStatus = item.ProcessingStatus
		current.DecisionStatus = item.DecisionStatus
		current.NextAction = item.NextAction
		current.Revision = item.Revision
		current.RecognitionCompleted = item.RecognitionCompleted
		current.Retryable = item.Retryable
		if invoked.Valid {
			value := invoked.Bool
			current.ModelInvoked = &value
		}
		if err = json.Unmarshal(raw, &current.ArtifactIDs); err != nil {
			return err
		}
		if item.ReasonCode != "" {
			current.ReasonCode = item.ReasonCode
		}
		if item.RuntimeTaskID != "" {
			current.RuntimeTaskID = item.RuntimeTaskID
			current.RuntimeStatus = item.RuntimeStatus
			current.ErrorCode = item.ErrorCode
		}
		switch {
		case item.ProcessingStatus == "cancelled":
			current.State = "cancelled"
		case item.ProcessingStatus == "failed":
			current.State = "failed"
		case item.DecisionStatus == "human_confirmed" || item.DecisionStatus == "auto_confirmed":
			current.State = "confirmed"
		case item.DecisionStatus == "suggestion_ready" || item.DecisionStatus == "review_needed" || item.DecisionStatus == "manual_only" || item.ProcessingStatus == "blocked" || item.ProcessingStatus == "superseded":
			current.State = "review"
		default:
			current.State = "processing"
		}
	}
	return rows.Err()
}
