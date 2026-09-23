-- Managed calls retain protocol and model snapshots without inventing a legacy deployment key.
ALTER TABLE model_call_fact DROP CONSTRAINT chk_model_call_fact_identity;
ALTER TABLE model_call_fact ADD CONSTRAINT chk_model_call_fact_identity CHECK (
  provider_key ~ '^[a-z0-9][a-z0-9._-]{0,127}$'
  AND adapter_type ~ '^[a-z0-9][a-z0-9._-]{0,127}$'
  AND (
    (managed_model_api_config_id IS NOT NULL AND btrim(model_name) <> '')
    OR (managed_model_api_config_id IS NULL
      AND deployment_key ~ '^[a-z0-9][a-z0-9._-]{0,127}$'
      AND adapter_type !~* 'openai[-_ ]?compatible')
  )
  AND btrim(model_version) <> ''
  AND btrim(prompt_version) <> ''
  AND btrim(rubric_version) <> ''
  AND btrim(capability_profile) <> ''
  AND btrim(deployment_region) <> ''
);
