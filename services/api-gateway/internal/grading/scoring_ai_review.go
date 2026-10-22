package grading

import (
	"context"
	"database/sql"
)

// 调用方先锁 scoring_run；补建任务与运行计数一起提交，不改动已有教师任务或成绩。
// 图像建议必须仍绑定当前裁图、快照和成功运行；旧输入的成功回执不能恢复为待阅任务。
func reconcileScoringAIReviewTasksTx(ctx context.Context, tx *sql.Tx, tenantID, runID string) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO review_task(
  tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,
  source,status,priority,grade_round,reason_code,scoring_run_id,created_by
)
SELECT item.tenant_id,sr.exam_id,item.question_id,q.question_no,item.answer_segment_id,sub.id,
  COALESCE(NULLIF(sub.candidate_no,''),sub.id::text),
  'subjective_default_review','pending',80,'single','ai_suggestion_ready',sr.id,sr.started_by
FROM scoring_run_item item
JOIN scoring_run sr ON sr.tenant_id=item.tenant_id AND sr.id=item.scoring_run_id
  AND sr.status IN ('queued','processing','needs_review','failed') AND sr.deleted_at IS NULL
JOIN answer_input_version iv ON iv.tenant_id=item.tenant_id AND iv.id=item.input_version_id
JOIN answer_segment seg ON seg.tenant_id=item.tenant_id AND seg.id=item.answer_segment_id
  AND seg.question_id=item.question_id AND seg.submission_id=iv.submission_id
  AND seg.crop_file_asset_id=iv.crop_file_asset_id AND seg.crop_sha256=iv.crop_sha256 AND seg.deleted_at IS NULL
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id
  AND sub.exam_id=sr.exam_id AND sub.deleted_at IS NULL
JOIN question q ON q.tenant_id=item.tenant_id AND q.id=item.question_id AND q.exam_id=sr.exam_id AND q.deleted_at IS NULL
JOIN LATERAL (
  SELECT id,rubric_snapshot_json FROM exam_question_snapshot
  WHERE tenant_id=item.tenant_id AND exam_id=sr.exam_id AND question_id=item.question_id
  ORDER BY snapshot_version DESC,id DESC LIMIT 1
) snapshot ON snapshot.id=item.assessment_snapshot_id
JOIN subjective_grading_run ai ON ai.tenant_id=item.tenant_id AND ai.id=item.subjective_run_id
  AND ai.status='succeeded' AND ai.deleted_at IS NULL AND ai.input_mode='image'
  AND ai.answer_segment_id=item.answer_segment_id AND ai.question_id=item.question_id
  AND ai.input_version_id=item.input_version_id AND ai.answer_version=item.input_version_id::text
  AND ai.exam_question_snapshot_id=item.assessment_snapshot_id
JOIN ai_grade grade ON grade.tenant_id=ai.tenant_id AND grade.id=ai.grade_id
  AND grade.subjective_grading_run_id=ai.id AND grade.status='succeeded' AND grade.deleted_at IS NULL
  AND NOT grade.mock AND grade.delivery_mode IN ('teacher_suggestion','teacher_review') AND grade.needs_human_review
  AND grade.max_score=q.score
  AND grade.answer_segment_id=item.answer_segment_id AND grade.question_id=item.question_id
  AND grade.answer_version=ai.answer_version AND grade.exam_question_snapshot_id=snapshot.id
  AND grade.rubric_version=ai.rubric_version AND grade.rubric_version=snapshot.rubric_snapshot_json->>'version'
  AND ai.input_protocol='grading-image-v3' AND grade.raw_output->>'schema_version'='grading-image-v3'
  AND grade.raw_output->>'input_version_id'=item.input_version_id::text
  AND grade.raw_output->>'assessment_snapshot_id'=snapshot.id::text
  AND grade.raw_output->'observation'->>'input_version_id'=item.input_version_id::text
  AND grade.raw_output->'observation'->>'crop_sha256'=iv.crop_sha256
  AND jsonb_array_length(CASE WHEN jsonb_typeof(grade.raw_output->'observation'->'members')='array'
    THEN grade.raw_output->'observation'->'members' ELSE '[]'::jsonb END)=1
  AND grade.raw_output->'observation'->'members'->0->>'question_id'=item.question_id::text
WHERE item.tenant_id=$1::uuid AND item.scoring_run_id=$2::uuid AND item.route='image_ai'
  AND item.processing_status='succeeded' AND item.decision_status='suggestion_ready'
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
