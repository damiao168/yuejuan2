-- Seed against schema 000166, then apply 000167, 000168 and 000169 and inspect assertions below.
INSERT INTO tenant (id, tenant_id, name, code, status)
VALUES ('11111111-1111-4111-8111-111111111111', '11111111-1111-4111-8111-111111111111', 'Bridge School', 'bridge-school', 'active');

INSERT INTO model_provider (id, tenant_id, provider_key, display_name, provider_kind, adapter_type, credential_ref, region)
VALUES ('22222222-2222-4222-8222-222222222222', '11111111-1111-4111-8111-111111111111', 'aliyun', 'Aliyun', 'external', 'dashscope_native', 'env://BRIDGE_TEST_KEY', 'cn');

INSERT INTO model_deployment (id, tenant_id, provider_id, deployment_key, model_name, model_version, region, capability_profile)
VALUES ('33333333-3333-4333-8333-333333333333', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'qwen-legacy', 'qwen-test', '2026-09', 'cn', 'general');

INSERT INTO model_provider (id, tenant_id, provider_key, display_name, provider_kind, adapter_type, credential_ref, region)
VALUES ('22222222-2222-4222-8222-222222222223', '11111111-1111-4111-8111-111111111111', 'local', 'Local', 'local', 'local_native', '', 'cn');

INSERT INTO model_deployment (id, tenant_id, provider_id, deployment_key, model_name, model_version, region, capability_profile)
VALUES ('33333333-3333-4333-8333-333333333334', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222223', 'local-baseline', 'local-model', '2026-09', 'cn', 'general');

INSERT INTO managed_model_api_config (
  id, tenant_id, provider_key, display_name, adapter_type, base_url, model_name, model_version,
  region, credential_ciphertext, credential_nonce
)
VALUES (
  '44444444-4444-4444-8444-444444444444', '11111111-1111-4111-8111-111111111111',
  'aliyun', 'Qwen Test', 'openai_compatible', 'https://example.test', 'qwen-test', '2026-09',
  'cn', decode(repeat('ab', 16), 'hex'), decode(repeat('cd', 12), 'hex')
);

INSERT INTO model_evaluation_run (
  id, tenant_id, run_key, display_name, dataset_reference, dataset_sha256,
  authorization_reference, evidence_class, subject, grade, question_type, modality,
  sample_count, repeat_count, status, completed_at
)
VALUES (
  '55555555-5555-4555-8555-555555555555', '11111111-1111-4111-8111-111111111111',
  'bridge-run', 'Bridge Run', 'frozen-set', repeat('a', 64), 'approved-data',
  'authorized_frozen_set', 'Math', 'High School', 'essay', 'text',
  10, 1, 'draft', NULL
);

INSERT INTO model_evaluation_candidate (
  id, tenant_id, run_id, deployment_id, provider_key, deployment_key, model_version,
  prompt_version, rubric_version, evaluated_samples, teacher_reviewed_samples,
  teacher_accepted_samples, serious_error_samples, evidence_valid_samples,
  repeat_comparisons, stable_repeat_samples, p95_latency_ms, total_cost_micros
)
VALUES (
  '66666666-6666-4666-8666-666666666666', '11111111-1111-4111-8111-111111111111',
  '55555555-5555-4555-8555-555555555555', '33333333-3333-4333-8333-333333333333',
  'aliyun', 'qwen-legacy', '2026-09', 'prompt-v1', 'rubric-v1', 10, 10, 9, 0, 10, 0, 0, 100, 200
);

INSERT INTO model_evaluation_candidate (
  id, tenant_id, run_id, deployment_id, provider_key, deployment_key, model_version,
  prompt_version, rubric_version, evaluated_samples, teacher_reviewed_samples,
  teacher_accepted_samples, serious_error_samples, evidence_valid_samples,
  repeat_comparisons, stable_repeat_samples, p95_latency_ms, total_cost_micros
)
VALUES (
  '66666666-6666-4666-8666-666666666667', '11111111-1111-4111-8111-111111111111',
  '55555555-5555-4555-8555-555555555555', '33333333-3333-4333-8333-333333333334',
  'local', 'local-baseline', '2026-09', 'prompt-v1', 'rubric-v1', 10, 10, 8, 0, 10, 0, 0, 100, 0
);

UPDATE model_evaluation_run
SET status = 'completed', completed_at = now()
WHERE id = '55555555-5555-4555-8555-555555555555';

INSERT INTO model_approval (
  id, tenant_id, evaluation_run_id, evaluation_candidate_id, deployment_id,
  provider_key, deployment_key, model_version, prompt_version, rubric_version,
  dataset_reference, dataset_sha256, authorization_reference, subject, grade,
  question_type, modality, manual_review_rate, decision_reference, expires_at
)
VALUES (
  '77777777-7777-4777-8777-777777777777', '11111111-1111-4111-8111-111111111111',
  '55555555-5555-4555-8555-555555555555', '66666666-6666-4666-8666-666666666666',
  '33333333-3333-4333-8333-333333333333', 'aliyun', 'qwen-legacy', '2026-09',
  'prompt-v1', 'rubric-v1', 'frozen-set', repeat('a', 64), 'approved-data',
  'Math', 'High School', 'essay', 'text', 0.1, 'bridge-decision', now() + interval '30 days'
);
