-- Run after 000169_bridge_fixture.sql on schema 000171, before 000172.
UPDATE tenant_model_policy
SET display_name = 'Legacy model policy', mode = 'cloud_suggestion',
    external_enabled = true, text_export_enabled = true,
    allowed_deployments = '["qwen-legacy","missing-legacy"]'::jsonb
WHERE tenant_id = '11111111-1111-4111-8111-111111111111'
  AND policy_key = 'default';
