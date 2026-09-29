-- 仅供隔离 E2E 数据库：固定 ID 直接预置已完成 OCR 的合成业务事实，不验收采集与 OCR 链路。
DO $$
DECLARE
  tenant_uuid UUID := '00000000-0000-0000-0000-000000000001';
  actor_uuid UUID;
BEGIN
  SELECT id INTO actor_uuid
  FROM app_user
  WHERE tenant_id = tenant_uuid AND username = 'platform_admin' AND deleted_at IS NULL;
  IF actor_uuid IS NULL THEN
    RAISE EXCEPTION 'STORY-060 bootstrap administrator is missing';
  END IF;

  INSERT INTO school (id, tenant_id, name, code, status)
  VALUES ('00000000-0000-0000-0000-000000000601', tenant_uuid, 'STORY-060 Synthetic School', 'S060', 'active');

  INSERT INTO academic_year (
    id, tenant_id, school_id, name, start_year, end_year, starts_at, ends_at, is_current, status
  ) VALUES (
    '00000000-0000-0000-0000-000000000620', tenant_uuid, '00000000-0000-0000-0000-000000000601',
    '2026-2027学年', 2026, 2027, DATE '2026-09-01', DATE '2027-08-31', true, 'active'
  );

  INSERT INTO grade_cohort (
    id, tenant_id, school_id, education_stage, entry_year, expected_graduation_year, name, status
  ) VALUES (
    '00000000-0000-0000-0000-000000000621', tenant_uuid, '00000000-0000-0000-0000-000000000601',
    'junior', 2025, 2028, '2025级', 'active'
  );

  INSERT INTO grade (
    id, tenant_id, school_id, academic_year_id, grade_cohort_id, name, level_no, academic_year, status
  ) VALUES (
    '00000000-0000-0000-0000-000000000602', tenant_uuid, '00000000-0000-0000-0000-000000000601',
    '00000000-0000-0000-0000-000000000620', '00000000-0000-0000-0000-000000000621',
    'Grade 8', 8, '2026', 'active'
  );

  INSERT INTO school_class (
    id, tenant_id, school_id, grade_id, academic_year_id, grade_cohort_id, name, code, status
  ) VALUES (
    '00000000-0000-0000-0000-000000000603', tenant_uuid, '00000000-0000-0000-0000-000000000601',
    '00000000-0000-0000-0000-000000000602', '00000000-0000-0000-0000-000000000620',
    '00000000-0000-0000-0000-000000000621', 'Synthetic Class', 'S060-C1', 'active'
  );

  INSERT INTO student (
    id, tenant_id, school_id, class_id, student_no, name, status, admission_year
  ) VALUES (
    '00000000-0000-0000-0000-000000000622', tenant_uuid,
    '00000000-0000-0000-0000-000000000601', '00000000-0000-0000-0000-000000000603',
    'S060-001', 'STORY-060 Synthetic Student', 'active', 2025
  );

  INSERT INTO student_enrollment (
    id, tenant_id, school_id, student_id, academic_year_id, grade_cohort_id,
    class_id, status, start_date
  ) VALUES (
    '00000000-0000-0000-0000-000000000623', tenant_uuid,
    '00000000-0000-0000-0000-000000000601', '00000000-0000-0000-0000-000000000622',
    '00000000-0000-0000-0000-000000000620', '00000000-0000-0000-0000-000000000621',
    '00000000-0000-0000-0000-000000000603', 'enrolled', DATE '2026-09-01'
  );

  INSERT INTO exam (id, tenant_id, school_id, name, subject, exam_type, total_score, status, grading_mode, appeal_enabled, publish_policy, created_by)
  VALUES ('00000000-0000-0000-0000-000000000604', tenant_uuid, '00000000-0000-0000-0000-000000000601', 'STORY-060 Synthetic Chinese Exam', 'chinese', 'formal_exam', 4, 'draft', 'ai_assisted', true, 'after_admin_approval', actor_uuid);

  INSERT INTO exam_class (id, tenant_id, exam_id, class_id)
  VALUES ('00000000-0000-0000-0000-000000000605', tenant_uuid, '00000000-0000-0000-0000-000000000604', '00000000-0000-0000-0000-000000000603');

  INSERT INTO file_asset (
    id, tenant_id, school_id, exam_id, owner_type, owner_id, original_name, content_type,
    size_bytes, hash_sha256, storage_bucket, storage_key, visibility, uploaded_by
  ) VALUES (
    '00000000-0000-0000-0000-000000000606', tenant_uuid, '00000000-0000-0000-0000-000000000601',
    '00000000-0000-0000-0000-000000000604', 'exam', '00000000-0000-0000-0000-000000000604',
    'story060-synthetic-paper.pdf', 'application/pdf', 1,
    '0600000000000000000000000000000000000000000000000000000000000606',
    'story060-e2e-files', 'synthetic/paper.pdf', 'private', actor_uuid
  );

  INSERT INTO exam_paper (id, tenant_id, exam_id, file_asset_id, version_no, status, uploaded_by)
  VALUES ('00000000-0000-0000-0000-000000000607', tenant_uuid, '00000000-0000-0000-0000-000000000604', '00000000-0000-0000-0000-000000000606', 1, 'active', actor_uuid);

  INSERT INTO question (
    id, tenant_id, exam_id, exam_paper_id, question_no, question_type, score, stem,
    knowledge_points, answer_area, sort_order, status
  ) VALUES (
    '00000000-0000-0000-0000-000000000608', tenant_uuid, '00000000-0000-0000-0000-000000000604',
    '00000000-0000-0000-0000-000000000607', 'Q1', 'short_answer', 4,
    '请概括文中主人公选择留下的原因。', '[]', '{"page_no":1,"x":0.1,"y":0.1,"width":0.8,"height":0.3}', 1, 'active'
  );

  INSERT INTO rubric_version (id, tenant_id, question_id, version, status, content_hash, created_by, approved_by, approved_at)
  VALUES (
    '00000000-0000-0000-0000-000000000609', tenant_uuid, '00000000-0000-0000-0000-000000000608',
    'rubric-v3', 'approved', 'story060-rubric-v3', actor_uuid, actor_uuid, now()
  );

  INSERT INTO question_rubric (
    id, tenant_id, question_id, rubric_version_id, status, max_score, points, deductions,
    examples, created_by, approved_by, approved_at
  ) VALUES (
    '00000000-0000-0000-0000-000000000610', tenant_uuid, '00000000-0000-0000-0000-000000000608',
    '00000000-0000-0000-0000-000000000609', 'approved', 4,
    '[{"id":"p1","description":"指出主人公对家乡有责任感","score":2,"required":true},{"id":"p2","description":"指出主人公希望帮助孩子继续读书","score":2,"required":true}]',
    '[]', '[]', actor_uuid, actor_uuid, now()
  );

  -- Follow the production lifecycle so STORY-A01 can create and freeze the
  -- assessment profile before grading facts are seeded.
  UPDATE exam
  SET status = 'ready', updated_at = now()
  WHERE tenant_id = tenant_uuid
    AND id = '00000000-0000-0000-0000-000000000604';

  UPDATE exam
  SET status = 'grading', updated_at = now()
  WHERE tenant_id = tenant_uuid
    AND id = '00000000-0000-0000-0000-000000000604';

  -- Admit this deterministic protocol-emulator run through the same A14-A16
  -- evidence gates as production. These are isolated integration facts, not
  -- claims about real model quality.
  INSERT INTO ai_eligibility_policy (
    id, tenant_id, subject_code, education_stage, archetype_code, risk_tier,
    min_ocr_quality, min_parser_quality, min_eval_n, max_severe_error_rate,
    allowed_modes_json, version, status
  ) VALUES (
    '00000000-0000-0000-0000-000000000616', tenant_uuid, 'chinese', 'junior',
    'short_constructed', 'R2', 0.8, 0.8, 1, 0.1, '["AI_ASSIST"]', 1, 'active'
  );

  INSERT INTO grading_evaluation_run (
    id, tenant_id, run_key, display_name, model_reference, prompt_version,
    rubric_version, dataset_reference, dataset_sha256, status, created_by
  ) VALUES (
    '00000000-0000-0000-0000-000000000617', tenant_uuid,
    'story060-protocol-evaluation', 'STORY-060 protocol emulator evaluation',
    'Qwen/Qwen3-4B-GGUF:Q4_K_M', 'subjective-governed-cn-subject-routing-v6', 'rubric-v3',
    'story060-protocol-fixture', repeat('0', 64), 'draft', actor_uuid
  );

  INSERT INTO grading_evaluation_observation (
    id, tenant_id, run_id, response_key, response_fingerprint, reference_kind,
    subject, archetype, ocr_quality, answer_length, rubric_complexity,
    reference_score, model_score, max_score, reference_score_band
  ) VALUES (
    '00000000-0000-0000-0000-000000000618', tenant_uuid,
    '00000000-0000-0000-0000-000000000617', 'story060-response-1', repeat('1', 64),
    'human_adjudicated', 'chinese', 'short_constructed', 'high', 'short', 'low',
    4, 4, 4, 'full'
  );

  UPDATE grading_evaluation_run
  SET status = 'completed', completed_at = now()
  WHERE tenant_id = tenant_uuid
    AND id = '00000000-0000-0000-0000-000000000617';

  INSERT INTO model_calibration (
    id, tenant_id, calibration_key, evaluation_run_id, model_reference,
    prompt_version, rubric_version, subject_code, archetype_code, slice_key,
    method, status, calibration_n, artifact_uri, artifact_sha256, artifact_json,
    created_by, completed_at, approved_at, approved_by
  ) VALUES (
    '00000000-0000-0000-0000-000000000619', tenant_uuid,
    'story060-protocol-calibration', '00000000-0000-0000-0000-000000000617',
    'Qwen/Qwen3-4B-GGUF:Q4_K_M', 'subjective-governed-cn-subject-routing-v6', 'rubric-v3',
    'chinese', 'short_constructed', 'all', 'isotonic', 'approved', 1,
    'fixture://story060/protocol-calibration', repeat('2', 64),
    '{"schema_version":1,"method":"isotonic","bins":[{"min_raw_confidence":0,"max_raw_confidence":1,"calibrated_confidence":0.9,"sample_count":1,"correct_count":1}],"metrics":{"sample_count":1,"brier_score":0.01,"expected_calibration_error":0.1,"middle_score_sample_count":0},"risk_coverage_curve":[{"threshold":0,"coverage":1,"sample_count":1,"empirical_risk":0,"severe_error_rate":0}]}',
    actor_uuid, now(), now(), actor_uuid
  );

  INSERT INTO submission (
    id, tenant_id, exam_id, candidate_no, source_type, status, expected_page_count,
    actual_page_count, quality_status, quality_issues, collected_by
  ) VALUES (
    '00000000-0000-0000-0000-000000000611', tenant_uuid, '00000000-0000-0000-0000-000000000604',
    'SYNTHETIC-STORY060', 'manual_import', 'ready_for_ocr', 1, 1, 'passed', '[]', actor_uuid
  );

  INSERT INTO file_asset (
    id, tenant_id, school_id, exam_id, submission_id, owner_type, owner_id, original_name,
    content_type, size_bytes, hash_sha256, storage_bucket, storage_key, visibility, uploaded_by
  ) VALUES (
    '00000000-0000-0000-0000-000000000612', tenant_uuid, '00000000-0000-0000-0000-000000000601',
    '00000000-0000-0000-0000-000000000604', '00000000-0000-0000-0000-000000000611',
    'submission_page_original', '00000000-0000-0000-0000-000000000611', 'story060-synthetic-page.png',
    'image/png', 1, '0600000000000000000000000000000000000000000000000000000000000612',
    'story060-e2e-files', 'synthetic/page.png', 'private', actor_uuid
  );

  INSERT INTO submission_page (id, tenant_id, submission_id, file_asset_id, page_no, status, quality_issues, quality_status)
  VALUES (
    '00000000-0000-0000-0000-000000000613', tenant_uuid, '00000000-0000-0000-0000-000000000611',
    '00000000-0000-0000-0000-000000000612', 1, 'accepted', '[]', 'passed'
  );

  INSERT INTO submission_page_processing_state (
    tenant_id, page_id, submission_id, exam_id, current_stage, blocking,
    parser_quality_json
  ) VALUES (
    tenant_uuid, '00000000-0000-0000-0000-000000000613',
    '00000000-0000-0000-0000-000000000611',
    '00000000-0000-0000-0000-000000000604', 'READY', false,
    '{"text_quality":0.96}'
  );

  INSERT INTO answer_segment (
    id, tenant_id, submission_id, submission_page_id, question_id, question_no,
    bbox, source, status
  ) VALUES (
    '00000000-0000-0000-0000-000000000614', tenant_uuid, '00000000-0000-0000-0000-000000000611',
    '00000000-0000-0000-0000-000000000613', '00000000-0000-0000-0000-000000000608',
    'Q1', '[100,100,800,300]', 'manual', 'accepted'
  );

  INSERT INTO answer_segment_answer (
    id, tenant_id, answer_segment_id, answer_text, answer_payload, source, confidence, recorded_by
  ) VALUES (
    '00000000-0000-0000-0000-000000000615', tenant_uuid, '00000000-0000-0000-0000-000000000614',
    '因为他对家乡有责任感，也希望帮助村里的孩子继续读书。', '{"synthetic":true}', 'ocr_text', 0.96, actor_uuid
  );

  UPDATE app_user
  SET school_id = '00000000-0000-0000-0000-000000000601',
      updated_at = now()
  WHERE tenant_id = tenant_uuid
    AND username = 'story060_school_admin'
    AND deleted_at IS NULL;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'STORY-060 school administrator was not seeded';
  END IF;

  UPDATE user_role ur
  SET data_scope = jsonb_build_object(
        'scope', 'school',
        'school_id', '00000000-0000-0000-0000-000000000601'::uuid,
        'school_name', 'STORY-060 Synthetic School',
        'synthetic', true
      ),
      updated_at = now()
  FROM app_user u, role r
  WHERE ur.tenant_id = tenant_uuid
    AND ur.user_id = u.id
    AND ur.role_id = r.id
    AND u.tenant_id = tenant_uuid
    AND u.username = 'story060_school_admin'
    AND r.tenant_id = tenant_uuid
    AND r.code = 'school_admin';
  IF NOT FOUND THEN
    RAISE EXCEPTION 'STORY-060 school administrator scope was not seeded';
  END IF;

  UPDATE user_role ur
  SET data_scope = jsonb_build_object('scope', 'service', 'synthetic', true),
      updated_at = now()
  FROM app_user u, role r
  WHERE ur.tenant_id = tenant_uuid
    AND ur.user_id = u.id
    AND ur.role_id = r.id
    AND u.tenant_id = tenant_uuid
    AND u.username = 'story060_subjective_worker'
    AND r.tenant_id = tenant_uuid
    AND r.code = 'subjective_grading_worker';
  IF NOT FOUND THEN
    RAISE EXCEPTION 'STORY-060 subjective worker scope was not seeded';
  END IF;
END $$;
