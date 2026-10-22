-- 校准证据与完成操作锁同一父记录；等待后重新检查草稿，不接受完成之后的迟到追加。
-- 对观察列加别名，保留000086原来源轴、切片和派生事实校验，避免列名与局部变量冲突。
CREATE OR REPLACE FUNCTION validate_model_calibration_evidence()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
  calibration_status TEXT;
  expected_run UUID;
  expected_model TEXT;
  expected_prompt TEXT;
  expected_rubric TEXT;
  expected_subject TEXT;
  expected_archetype TEXT;
  expected_slice TEXT;
  run_status TEXT;
  run_model TEXT;
  run_prompt TEXT;
  run_rubric TEXT;
  observed_subject TEXT;
  observed_archetype TEXT;
  observed_ocr TEXT;
  observed_band TEXT;
  reference_score DOUBLE PRECISION;
  model_score DOUBLE PRECISION;
  max_score DOUBLE PRECISION;
  expected_correct BOOLEAN;
  expected_severe BOOLEAN;
BEGIN
  SELECT status,evaluation_run_id,model_reference,prompt_version,rubric_version,subject_code,archetype_code,slice_key
    INTO calibration_status,expected_run,expected_model,expected_prompt,expected_rubric,expected_subject,expected_archetype,expected_slice
    FROM model_calibration WHERE tenant_id=NEW.tenant_id AND id=NEW.calibration_id FOR UPDATE;
  IF NOT FOUND OR calibration_status <> 'draft' OR expected_run <> NEW.evaluation_run_id THEN
    RAISE EXCEPTION 'calibration evidence requires a matching draft artifact' USING ERRCODE='23514';
  END IF;
  SELECT status,model_reference,prompt_version,rubric_version INTO run_status,run_model,run_prompt,run_rubric
    FROM grading_evaluation_run WHERE tenant_id=NEW.tenant_id AND id=NEW.evaluation_run_id;
  IF NOT FOUND OR run_status <> 'completed' OR run_model <> expected_model OR run_prompt <> expected_prompt OR run_rubric <> expected_rubric THEN
    RAISE EXCEPTION 'calibration evidence requires completed matching evaluation provenance' USING ERRCODE='23514';
  END IF;
  SELECT observation.subject,observation.archetype,observation.ocr_quality,observation.reference_score_band,observation.reference_score,observation.model_score,observation.max_score
    INTO observed_subject,observed_archetype,observed_ocr,observed_band,reference_score,model_score,max_score
    FROM grading_evaluation_observation observation WHERE observation.tenant_id=NEW.tenant_id AND observation.run_id=NEW.evaluation_run_id AND observation.response_key=NEW.response_key;
  IF NOT FOUND OR observed_subject <> expected_subject OR observed_archetype <> expected_archetype
     OR (expected_slice LIKE 'score_band:%' AND expected_slice <> 'score_band:' || observed_band)
     OR (expected_slice LIKE 'ocr_quality:%' AND expected_slice <> 'ocr_quality:' || observed_ocr) THEN
    RAISE EXCEPTION 'calibration evidence response does not match artifact axis' USING ERRCODE='23514';
  END IF;
  expected_correct := abs(model_score-reference_score) < 0.000000001;
  expected_severe := abs(model_score-reference_score) >= greatest(1.0,max_score*.4)-0.000000001;
  IF NEW.correct <> expected_correct OR NEW.severe_error <> expected_severe OR NEW.score_band <> observed_band OR NEW.ocr_quality <> observed_ocr THEN
    RAISE EXCEPTION 'calibration evidence facts must equal aligned evaluation facts' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;

-- 普通评测观察与完成共享父运行锁；数据库直接写入也不能绕过冻结边界。
CREATE OR REPLACE FUNCTION enforce_grading_evaluation_observation_write()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE run_status TEXT;
BEGIN
  SELECT status INTO run_status FROM grading_evaluation_run WHERE tenant_id = NEW.tenant_id AND id = NEW.run_id FOR UPDATE;
  IF NOT FOUND OR run_status <> 'draft' THEN
    RAISE EXCEPTION 'evaluation observations may only be added to a draft run' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;
