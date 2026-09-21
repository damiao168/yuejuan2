CREATE TEMP TABLE story060_expected_migration_count (value INT NOT NULL);
INSERT INTO story060_expected_migration_count (value) VALUES (:'expected_migration_count'::INT);

DO $$
DECLARE
  expected_migration_count INT;
  migration_count INT;
  worker_scope_count INT;
  batch_count INT;
  worker_task_count INT;
  grade_count INT;
  audit_count INT;
  final_count INT;
BEGIN
  SELECT value INTO expected_migration_count FROM story060_expected_migration_count;
  SELECT count(*) INTO migration_count FROM schema_migration;
  IF migration_count <> expected_migration_count THEN
    RAISE EXCEPTION 'expected % migrations, found %', expected_migration_count, migration_count;
  END IF;

  SELECT count(*) INTO worker_scope_count
  FROM app_user u
  JOIN user_role ur ON ur.tenant_id = u.tenant_id AND ur.user_id = u.id AND ur.deleted_at IS NULL
  JOIN role r ON r.tenant_id = ur.tenant_id AND r.id = ur.role_id AND r.deleted_at IS NULL
  WHERE u.tenant_id = '00000000-0000-0000-0000-000000000001'
    AND u.username = 'story060_subjective_worker'
    AND u.status = 'active'
    AND u.deleted_at IS NULL
    AND r.code = 'subjective_grading_worker'
    AND ur.data_scope->>'scope' = 'service';
  IF worker_scope_count <> 1 THEN
    RAISE EXCEPTION 'expected one service-scoped subjective worker assignment, found %', worker_scope_count;
  END IF;

  SELECT count(*) INTO batch_count
  FROM subjective_grading_batch
  WHERE tenant_id = '00000000-0000-0000-0000-000000000001'
    AND idempotency_key = 'story060-real-worker-batch'
    AND status = 'completed'
    AND total_count = 1
    AND succeeded_count = 1
    AND failed_count = 0;
  IF batch_count <> 1 THEN
    RAISE EXCEPTION 'expected one completed subjective batch, found %', batch_count;
  END IF;

  SELECT count(*) INTO worker_task_count
  FROM agent_worker_task
  WHERE tenant_id = '00000000-0000-0000-0000-000000000001'
    AND queue_name = 'subjective-grading'
    AND source_type = 'subjective_grading_run'
    AND status = 'succeeded';
  IF worker_task_count <> 1 THEN
    RAISE EXCEPTION 'expected one succeeded subjective worker task, found %', worker_task_count;
  END IF;

  SELECT count(*) INTO grade_count
  FROM ai_grade
  WHERE tenant_id = '00000000-0000-0000-0000-000000000001'
    AND answer_segment_id = '00000000-0000-0000-0000-000000000614'
    AND status = 'succeeded'
    AND grader_type = 'llm_subjective'
    AND mock = false
    AND needs_human_review = true
    AND confidence = 0
    AND answer_version = '00000000-0000-0000-0000-000000000615'
    AND model_version = 'Qwen/Qwen3-4B-GGUF:Q4_K_M'
    AND prompt_version = 'subjective-governed-cn-subject-routing-v6'
    AND rubric_version = 'rubric-v3'
    AND delivery_mode = 'teacher_suggestion'
    AND capability_profile = 'local-pilot-v1'
    AND adapter_name = 'local_llama_cpp'
    AND adapter_attempts BETWEEN 1 AND 2
    AND adapter_latency_ms >= 0;
  IF grade_count <> 1 THEN
    RAISE EXCEPTION 'expected one governed AI suggestion, found %', grade_count;
  END IF;

  SELECT count(*) INTO audit_count
  FROM audit_log
  WHERE tenant_id = '00000000-0000-0000-0000-000000000001'
    AND action = 'subjective.worker_completed'
    AND target_type = 'subjective_grading_run';
  IF audit_count <> 1 THEN
    RAISE EXCEPTION 'expected one worker completion audit event, found %', audit_count;
  END IF;

  SELECT count(*) INTO final_count
  FROM final_grade
  WHERE tenant_id = '00000000-0000-0000-0000-000000000001'
    AND answer_segment_id = '00000000-0000-0000-0000-000000000614';
  IF final_count <> 0 THEN
    RAISE EXCEPTION 'grading agent must not create final grades';
  END IF;
END $$;

SELECT 'STORY-060 PostgreSQL governance verification passed' AS result;
