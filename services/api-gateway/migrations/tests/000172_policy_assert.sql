DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM tenant_model_policy_model
    WHERE tenant_id = '11111111-1111-4111-8111-111111111111'
      AND policy_id = (SELECT id FROM tenant_model_policy
                       WHERE tenant_id = '11111111-1111-4111-8111-111111111111'
                         AND policy_key = 'default')
      AND managed_model_api_config_id = '44444444-4444-4444-8444-444444444444'
      AND enabled
  ) THEN
    RAISE EXCEPTION 'legacy policy did not backfill unique managed model';
  END IF;
  IF (SELECT allowed_deployments FROM tenant_model_policy
      WHERE tenant_id = '11111111-1111-4111-8111-111111111111' AND policy_key = 'default')
      <> '["qwen-legacy","missing-legacy"]'::jsonb THEN
    RAISE EXCEPTION 'unmatched legacy policy history was lost';
  END IF;
  IF (SELECT count(*) FROM tenant_model_policy_model
      WHERE tenant_id = '11111111-1111-4111-8111-111111111111') <> 1 THEN
    RAISE EXCEPTION 'unmatched deployment was fabricated';
  END IF;
END;
$$;
