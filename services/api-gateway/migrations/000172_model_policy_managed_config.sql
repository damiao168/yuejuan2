-- Managed model authorization is a tenant-bound relationship, not a list of
-- deployment keys. Keep the legacy JSON for historical policy inspection.
CREATE TABLE tenant_model_policy_model (
  tenant_id UUID NOT NULL,
  policy_id UUID NOT NULL,
  managed_model_api_config_id UUID NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, policy_id, managed_model_api_config_id),
  CONSTRAINT fk_tenant_model_policy_model_policy
    FOREIGN KEY (tenant_id, policy_id) REFERENCES tenant_model_policy(tenant_id, id),
  CONSTRAINT fk_tenant_model_policy_model_config
    FOREIGN KEY (tenant_id, managed_model_api_config_id)
    REFERENCES managed_model_api_config(tenant_id, id)
);

CREATE INDEX idx_tenant_model_policy_model_config
  ON tenant_model_policy_model (tenant_id, managed_model_api_config_id)
  WHERE enabled;

ALTER TABLE tenant_model_policy_model ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_model_policy_model FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON tenant_model_policy_model
  FOR ALL USING (edugrade_tenant_matches(tenant_id))
  WITH CHECK (edugrade_tenant_matches(tenant_id));
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_model_policy_model TO edugrade_tenant_runtime;

-- Backfill only unambiguous exact matches. Legacy keys that cannot be
-- resolved stay in allowed_deployments for history, never fabricated.
WITH matches AS (
  SELECT policy.tenant_id, policy.id AS policy_id, config.id AS config_id,
         count(*) OVER (PARTITION BY policy.id, legacy.deployment_key) AS match_count
  FROM tenant_model_policy policy
  CROSS JOIN LATERAL jsonb_array_elements_text(policy.allowed_deployments)
    AS legacy(deployment_key)
  JOIN model_deployment deployment
    ON deployment.tenant_id = policy.tenant_id
   AND deployment.deployment_key = legacy.deployment_key
  JOIN model_provider provider
    ON provider.tenant_id = deployment.tenant_id
   AND provider.id = deployment.provider_id
  JOIN managed_model_api_config config
    ON config.tenant_id = policy.tenant_id
   AND config.provider_key = provider.provider_key
   AND config.model_name = deployment.model_name
   AND config.model_version = deployment.model_version
   AND config.region = deployment.region
)
INSERT INTO tenant_model_policy_model (
  tenant_id, policy_id, managed_model_api_config_id
)
SELECT tenant_id, policy_id, config_id
FROM matches
WHERE match_count = 1
ON CONFLICT DO NOTHING;
