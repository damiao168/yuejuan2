-- 只允许管理员显式重开过时且未发布的复评。原申诉来源仍不可变；本轮来源只能来自系统写入的重新处理事件。
-- 已发布快照继续由既有不可变触发器保护；作废只适用于尚未发布的草稿，归档检查原本只阻断 draft。
ALTER TABLE score_release DROP CONSTRAINT IF EXISTS score_release_status_check;
ALTER TABLE score_release ADD CONSTRAINT score_release_status_check CHECK(status IN ('draft','published','discarded'));
-- 作废草稿从未公布，继续要求没有发布人/时间；不能为了新增状态放松正式发布的留痕约束。
ALTER TABLE score_release DROP CONSTRAINT IF EXISTS score_release_check;
ALTER TABLE score_release ADD CONSTRAINT score_release_check CHECK(
  (status IN ('draft','discarded') AND published_at IS NULL AND published_by IS NULL)
  OR (status='published' AND published_at IS NOT NULL AND published_by IS NOT NULL)
);
ALTER TABLE question_appeal_event DROP CONSTRAINT IF EXISTS question_appeal_event_event_type_check;
ALTER TABLE question_appeal_event ADD CONSTRAINT question_appeal_event_event_type_check
  CHECK(event_type IN ('submitted','review_started','review_restarted','review_opinion_submitted','decided','resolved'));

CREATE OR REPLACE FUNCTION enforce_question_appeal_source_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  valid_resolution BOOLEAN;
  valid_regrade BOOLEAN;
BEGIN
  IF NEW.tenant_id <> OLD.tenant_id OR NEW.exam_id <> OLD.exam_id
    OR NEW.student_id <> OLD.student_id OR NEW.submission_id <> OLD.submission_id
    OR NEW.source_release_id <> OLD.source_release_id OR NEW.source_release_version <> OLD.source_release_version
    OR NEW.question_id <> OLD.question_id OR NEW.question_no <> OLD.question_no
    OR NEW.source_final_grade_id <> OLD.source_final_grade_id
    OR NEW.source_score <> OLD.source_score OR NEW.source_max_score <> OLD.source_max_score
    OR NEW.reason_code <> OLD.reason_code OR NEW.reason <> OLD.reason
    OR NEW.selected_region <> OLD.selected_region OR NEW.created_by <> OLD.created_by
    OR NEW.created_at <> OLD.created_at THEN
    RAISE EXCEPTION 'question appeal source facts are immutable';
  END IF;
  IF NEW.new_release_id IS NOT NULL THEN
    SELECT EXISTS (
      SELECT 1 FROM score_release successor
      JOIN score_release_item successor_item
        ON successor_item.tenant_id = successor.tenant_id AND successor_item.release_id = successor.id
      JOIN score_release_question successor_question
        ON successor_question.tenant_id = successor.tenant_id
       AND successor_question.release_id = successor.id
       AND successor_question.submission_id = successor_item.submission_id
      WHERE successor.tenant_id = NEW.tenant_id AND successor.id = NEW.new_release_id
        AND successor.exam_id = NEW.exam_id AND successor.status = 'published'
        AND successor.version > NEW.source_release_version
        AND successor_item.student_id = NEW.student_id
        AND successor_item.submission_id = NEW.submission_id
        AND successor_question.question_id = NEW.question_id
    ) INTO valid_resolution;
    IF NOT valid_resolution THEN
      RAISE EXCEPTION 'question appeal resolution must reference a published successor release';
    END IF;
  END IF;
  IF NEW.regrade_job_id IS NOT NULL THEN
    SELECT EXISTS (
      SELECT 1 FROM regrade_job
      WHERE tenant_id = NEW.tenant_id AND id = NEW.regrade_job_id
        AND exam_id = NEW.exam_id AND question_id = NEW.question_id
        AND source_release_id = COALESCE((
          SELECT NULLIF(restart.payload->>'review_source_release_id','')::uuid
          FROM question_appeal_event restart
          WHERE restart.tenant_id=NEW.tenant_id AND restart.appeal_id=NEW.id AND restart.event_type='review_restarted'
          ORDER BY (restart.payload->>'revision')::bigint DESC LIMIT 1
        ),NEW.source_release_id)
    ) INTO valid_regrade;
    IF NOT valid_regrade THEN
      RAISE EXCEPTION 'question appeal must reference a matching regrade job';
    END IF;
  END IF;
  RETURN NEW;
END
$$;

