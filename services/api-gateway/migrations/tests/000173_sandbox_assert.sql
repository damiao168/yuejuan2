DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM model_sandbox_approval
    WHERE id='99999999-9999-4999-8999-999999999999'
      AND managed_model_api_config_id='44444444-4444-4444-8444-444444444444'
      AND provider_key='aliyun' AND model_name='qwen-test' AND model_version='2026-09'
  ) THEN RAISE EXCEPTION 'historical sandbox approval not preserved'; END IF;
END $$;

UPDATE model_sandbox_approval
SET revoked_at=now(), revoke_reason='superseded-by-managed-config'
WHERE id='99999999-9999-4999-8999-999999999999';

INSERT INTO model_sandbox_approval (
  id, tenant_id, managed_model_api_config_id, provider_key, model_name, model_version,
  protocol, approval_reference, approved_region, sandbox_account,
  contract_reviewed, retention_reviewed, data_residency_reviewed,
  pricing_reviewed, synthetic_data_only, expires_at
) VALUES (
  '99999999-9999-4999-8999-999999999998',
  '11111111-1111-4111-8111-111111111111',
  '44444444-4444-4444-8444-444444444444', 'aliyun', 'qwen-test', '2026-09',
  'dashscope_native', 'managed-approval', 'cn', true,
  true, true, true, true, true, now() + interval '30 days'
);

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM model_sandbox_approval
    WHERE id='99999999-9999-4999-8999-999999999998'
      AND managed_model_api_config_id='44444444-4444-4444-8444-444444444444'
      AND provider_id IS NULL AND deployment_id IS NULL
  ) THEN RAISE EXCEPTION 'managed sandbox approval still depends on legacy deployment'; END IF;
END $$;
