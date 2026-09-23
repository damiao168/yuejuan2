-- Run on a schema migrated through 000171. A new evaluation and approval
-- must contain only managed configuration IDs and frozen identity snapshots.
INSERT INTO tenant (id, tenant_id, name, code, status)
VALUES ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        'Managed School', 'managed-school', 'active');

INSERT INTO managed_model_api_config (
  id, tenant_id, provider_key, display_name, adapter_type, base_url,
  model_name, model_version, region, credential_ciphertext, credential_nonce,
  last_capability_status, last_capability_probe_version
)
VALUES
  ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
   'deepseek', 'Model A', 'openai_compatible', 'https://example.test/a',
   'model-a', 'v1', 'global', decode(repeat('ab', 16), 'hex'),
   decode(repeat('cd', 12), 'hex'), 'success', 'structured-json-v3'),
  ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb2', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
   'deepseek', 'Model B', 'openai_compatible', 'https://example.test/b',
   'model-b', 'v1', 'global', decode(repeat('ab', 16), 'hex'),
   decode(repeat('cd', 12), 'hex'), 'success', 'structured-json-v3');

INSERT INTO model_evaluation_run (
  id, tenant_id, run_key, display_name, dataset_reference, dataset_sha256,
  authorization_reference, evidence_class, subject, grade, question_type,
  modality, sample_count, repeat_count
)
VALUES ('cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        'managed-run', 'Managed Run', 'frozen-set', repeat('a', 64), 'approved-data',
        'authorized_frozen_set', 'Math', 'High School', 'essay', 'text', 10, 1);

INSERT INTO model_evaluation_candidate (
  id, tenant_id, run_id, managed_model_api_config_id, provider_key, model_name,
  model_version, prompt_version, rubric_version, evaluated_samples,
  teacher_reviewed_samples, teacher_accepted_samples, serious_error_samples,
  evidence_valid_samples, repeat_comparisons, stable_repeat_samples,
  p95_latency_ms, total_cost_micros
)
VALUES
  ('dddddddd-dddd-4ddd-8ddd-ddddddddddd1', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
   'cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1',
   'deepseek', 'model-a', 'v1', 'prompt-v1', 'rubric-v1', 10, 10, 9, 0, 10, 0, 0, 100, 200),
  ('dddddddd-dddd-4ddd-8ddd-ddddddddddd2', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
   'cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb2',
   'deepseek', 'model-b', 'v1', 'prompt-v1', 'rubric-v1', 10, 10, 8, 0, 10, 0, 0, 110, 210);

UPDATE model_evaluation_run
SET status = 'completed', completed_at = now()
WHERE id = 'cccccccc-cccc-4ccc-8ccc-cccccccccccc';

INSERT INTO model_approval (
  id, tenant_id, evaluation_run_id, evaluation_candidate_id,
  managed_model_api_config_id, provider_key, model_name, model_version,
  prompt_version, rubric_version, dataset_reference, dataset_sha256,
  authorization_reference, subject, grade, question_type, modality,
  manual_review_rate, decision_reference, expires_at
)
VALUES ('eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        'cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'dddddddd-dddd-4ddd-8ddd-ddddddddddd1',
        'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1', 'deepseek', 'model-a', 'v1',
        'prompt-v1', 'rubric-v1', 'frozen-set', repeat('a', 64), 'approved-data',
        'Math', 'High School', 'essay', 'text', 0.1, 'managed-decision', now() + interval '30 days');

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM model_evaluation_candidate
    WHERE id = 'dddddddd-dddd-4ddd-8ddd-ddddddddddd1'
      AND managed_model_api_config_id = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1'
      AND deployment_id IS NULL AND deployment_key IS NULL
      AND provider_key = 'deepseek' AND model_name = 'model-a' AND model_version = 'v1'
  ) THEN
    RAISE EXCEPTION 'managed candidate did not retain sole managed identity';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM model_approval
    WHERE id = 'eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee'
      AND managed_model_api_config_id = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1'
      AND deployment_id IS NULL AND deployment_key IS NULL
      AND model_name = 'model-a' AND model_version = 'v1'
  ) THEN
    RAISE EXCEPTION 'managed approval did not retain sole managed identity';
  END IF;
END;
$$;
