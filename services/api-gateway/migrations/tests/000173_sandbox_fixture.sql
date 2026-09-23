-- Run on the bridge fixture after 000172, before 000173.
INSERT INTO model_sandbox_approval (
  id, tenant_id, provider_id, deployment_id, managed_model_api_config_id,
  protocol, approval_reference, approved_region, sandbox_account,
  contract_reviewed, retention_reviewed, data_residency_reviewed,
  pricing_reviewed, synthetic_data_only, expires_at
) VALUES (
  '99999999-9999-4999-8999-999999999999',
  '11111111-1111-4111-8111-111111111111',
  '22222222-2222-4222-8222-222222222222',
  '33333333-3333-4333-8333-333333333333',
  '44444444-4444-4444-8444-444444444444',
  'dashscope_native', 'bridge-approval', 'cn', true,
  true, true, true, true, true, now() + interval '30 days'
);
