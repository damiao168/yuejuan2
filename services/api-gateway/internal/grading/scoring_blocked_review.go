package grading

import (
	"context"
	"database/sql"
)

// 自动链路因配置或能力问题停住时，仍应允许教师按原图评分。
// 调用方已锁父运行；只补当前输入、当前快照缺失的任务，不伪造 AI 建议，也不改已有教师工作。
func reconcileBlockedScoringReviewTasksTx(ctx context.Context, tx *sql.Tx, tenantID, runID string) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO review_task(
  tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,
  source,status,priority,grade_round,reason_code,scoring_run_id,created_by
)
SELECT item.tenant_id,sr.exam_id,item.question_id,q.question_no,item.answer_segment_id,sub.id,
  COALESCE(NULLIF(sub.candidate_no,''),sub.id::text),
  CASE WHEN item.route='image_ai' THEN 'subjective_default_review' ELSE 'rule_review_required' END,
  'pending',80,'single',COALESCE(NULLIF(item.reason_code,''),'automatic_processing_blocked'),sr.id,sr.started_by
FROM scoring_run_item item
JOIN scoring_run sr ON sr.tenant_id=item.tenant_id AND sr.id=item.scoring_run_id
  AND sr.status IN ('queued','processing','needs_review','failed') AND sr.deleted_at IS NULL
JOIN answer_input_version iv ON iv.tenant_id=item.tenant_id AND iv.id=item.input_version_id
JOIN answer_segment seg ON seg.tenant_id=item.tenant_id AND seg.id=item.answer_segment_id
  AND seg.question_id=item.question_id AND seg.submission_id=iv.submission_id
  AND seg.crop_file_asset_id=iv.crop_file_asset_id AND seg.crop_sha256=iv.crop_sha256
  AND seg.registration_run_id IS NOT DISTINCT FROM iv.registration_run_id
  AND seg.processing_status='completed' AND seg.deleted_at IS NULL
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id
  AND sub.exam_id=sr.exam_id AND sub.deleted_at IS NULL
JOIN question q ON q.tenant_id=item.tenant_id AND q.id=item.question_id AND q.exam_id=sr.exam_id AND q.deleted_at IS NULL
JOIN LATERAL (
  SELECT id FROM exam_question_snapshot
  WHERE tenant_id=item.tenant_id AND exam_id=sr.exam_id AND question_id=item.question_id
  ORDER BY snapshot_version DESC,id DESC LIMIT 1
) snapshot ON snapshot.id=item.assessment_snapshot_id
WHERE item.tenant_id=$1::uuid AND item.scoring_run_id=$2::uuid
  AND item.route IN ('image_ai','observe_rule','rule_input') AND item.processing_status='blocked'
  AND NOT EXISTS (
    SELECT 1 FROM question_grade g WHERE g.tenant_id=item.tenant_id
      AND g.answer_segment_id=item.answer_segment_id AND g.is_current AND g.deleted_at IS NULL
  )
  AND NOT EXISTS (
    SELECT 1 FROM review_task existing WHERE existing.tenant_id=item.tenant_id
      AND existing.answer_segment_id=item.answer_segment_id AND existing.deleted_at IS NULL
      AND existing.status IN ('pending','assigned','in_progress','returned','submitted','completed')
  )
ON CONFLICT (tenant_id,answer_segment_id,source,grade_round)
  WHERE status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL
    AND source <> 'ai_panel_disagreement'
DO NOTHING`, tenantID, runID)
	return err
}
