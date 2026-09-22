-- One school may use one provider credential for several distinct models.
-- Keep one configuration per provider/model and preserve the separate default guard.
ALTER TABLE managed_model_api_config
  DROP CONSTRAINT uq_managed_model_api_provider;

CREATE UNIQUE INDEX uq_managed_model_api_provider_model
  ON managed_model_api_config (tenant_id, provider_key, model_name)
  WHERE deleted_at IS NULL;
