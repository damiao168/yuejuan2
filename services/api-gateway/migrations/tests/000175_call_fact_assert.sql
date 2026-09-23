INSERT INTO model_call_fact (
  tenant_id, request_id, provider_key, deployment_key, adapter_type,
  managed_model_api_config_id, model_name, model_version, prompt_version,
  rubric_version, capability_profile, deployment_region, status
) VALUES (
  '11111111-1111-4111-8111-111111111111', 'managed-fact-173', 'aliyun', '',
  'openai_compatible', '44444444-4444-4444-8444-444444444444',
  'qwen-test', '2026-09', 'prompt-v1', 'rubric-v1', 'general', 'cn', 'succeeded'
);
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM model_call_fact
      WHERE request_id='managed-fact-173'
        AND managed_model_api_config_id='44444444-4444-4444-8444-444444444444'
        AND model_name='qwen-test' AND deployment_key='') THEN
    RAISE EXCEPTION 'managed call fact still requires a legacy deployment';
  END IF;
END $$;
