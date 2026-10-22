-- 新运行冻结实际使用的题目快照。旧行没有可证明的快照来源，保持 NULL，不根据当前题目回填。
ALTER TABLE subjective_grading_run
  ADD COLUMN exam_question_snapshot_id UUID,
  ADD CONSTRAINT fk_subjective_run_assessment_snapshot
    FOREIGN KEY (tenant_id, exam_question_snapshot_id)
    REFERENCES exam_question_snapshot (tenant_id, id);

CREATE INDEX idx_subjective_run_current_snapshot
  ON subjective_grading_run (tenant_id, answer_segment_id, exam_question_snapshot_id, created_at DESC)
  WHERE deleted_at IS NULL AND exam_question_snapshot_id IS NOT NULL;
