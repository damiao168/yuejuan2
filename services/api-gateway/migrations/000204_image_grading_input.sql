-- 先应用 203。旧运行的空值保持原义；只有新图像协议保存不可变输入与冻结上下文。
-- 不回填占位学生答案，不放宽数学 v2 的 verified artifact 要求；应用回滚后保留这些来源事实。
ALTER TABLE subjective_grading_run
  ADD COLUMN input_version_id UUID,
  ADD COLUMN input_mode TEXT,
  ADD COLUMN input_protocol TEXT,
  ADD COLUMN image_context_json JSONB,
  ADD FOREIGN KEY (tenant_id,input_version_id) REFERENCES answer_input_version(tenant_id,id),
  ADD CHECK (input_mode IS NULL OR input_mode IN ('image','text','image_text')),
  ADD CHECK (input_protocol IS NULL OR input_protocol='grading-image-v3'),
  ADD CHECK (num_nonnulls(input_version_id,input_mode,input_protocol,image_context_json) IN (0,4)),
  ADD CHECK ((input_version_id IS NULL AND input_mode IS NULL AND input_protocol IS NULL AND image_context_json IS NULL)
    OR (input_version_id IS NOT NULL AND input_mode='image' AND input_protocol='grading-image-v3' AND jsonb_typeof(image_context_json)='object'));

CREATE FUNCTION immutable_subjective_image_binding() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.input_version_id IS DISTINCT FROM NEW.input_version_id
    OR OLD.input_mode IS DISTINCT FROM NEW.input_mode
    OR OLD.input_protocol IS DISTINCT FROM NEW.input_protocol
    OR OLD.image_context_json IS DISTINCT FROM NEW.image_context_json THEN
    RAISE EXCEPTION 'subjective image input binding is immutable';
  END IF;
  IF OLD.input_version_id IS NOT NULL AND (
    OLD.answer_segment_id IS DISTINCT FROM NEW.answer_segment_id
    OR OLD.answer_version IS DISTINCT FROM NEW.answer_version
    OR OLD.question_id IS DISTINCT FROM NEW.question_id
    OR OLD.exam_question_snapshot_id IS DISTINCT FROM NEW.exam_question_snapshot_id
    OR OLD.rubric_version IS DISTINCT FROM NEW.rubric_version
    OR OLD.model_version IS DISTINCT FROM NEW.model_version
    OR OLD.prompt_version IS DISTINCT FROM NEW.prompt_version
    OR OLD.request_id IS DISTINCT FROM NEW.request_id) THEN
    RAISE EXCEPTION 'subjective image run identity is immutable';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER subjective_image_binding_immutable BEFORE UPDATE ON subjective_grading_run
  FOR EACH ROW EXECUTE FUNCTION immutable_subjective_image_binding();
