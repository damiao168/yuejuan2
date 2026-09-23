-- Additive bridge from the legacy provider/deployment inventory to the
-- tenant-owned managed model configuration. Legacy columns remain readable.

ALTER TABLE managed_model_api_config
  ADD COLUMN modalities JSONB NOT NULL DEFAULT '["text"]'::jsonb,
  ADD COLUMN capability_profile TEXT NOT NULL DEFAULT 'general',
  ADD COLUMN pricing_policy JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN data_policy JSONB NOT NULL DEFAULT '{"training_allowed":false,"retention_mode":"no_store"}'::jsonb,
  ADD COLUMN health_state TEXT NOT NULL DEFAULT 'unverified',
  ADD CONSTRAINT chk_managed_model_governance_modalities
    CHECK (jsonb_typeof(modalities) = 'array' AND jsonb_array_length(modalities) > 0),
  ADD CONSTRAINT chk_managed_model_governance_pricing
    CHECK (jsonb_typeof(pricing_policy) = 'object'),
  ADD CONSTRAINT chk_managed_model_governance_data
    CHECK (jsonb_typeof(data_policy) = 'object'
      AND data_policy->>'training_allowed' = 'false'
      AND data_policy->>'retention_mode' IN ('no_store', 'contractual')),
  ADD CONSTRAINT chk_managed_model_governance_health
    CHECK (health_state IN ('unverified', 'available', 'degraded', 'rate_limited', 'unavailable', 'disabled'));

ALTER TABLE model_evaluation_candidate
  ADD COLUMN managed_model_api_config_id UUID,
  ADD COLUMN model_name TEXT NOT NULL DEFAULT '',
  ADD CONSTRAINT fk_model_evaluation_candidate_managed_config
    FOREIGN KEY (tenant_id, managed_model_api_config_id)
    REFERENCES managed_model_api_config(tenant_id, id);

ALTER TABLE model_approval
  ADD COLUMN managed_model_api_config_id UUID,
  ADD COLUMN model_name TEXT NOT NULL DEFAULT '',
  ADD CONSTRAINT fk_model_approval_managed_config
    FOREIGN KEY (tenant_id, managed_model_api_config_id)
    REFERENCES managed_model_api_config(tenant_id, id);

ALTER TABLE model_sandbox_approval
  ADD COLUMN managed_model_api_config_id UUID,
  ADD CONSTRAINT fk_model_sandbox_approval_managed_config
    FOREIGN KEY (tenant_id, managed_model_api_config_id)
    REFERENCES managed_model_api_config(tenant_id, id);

ALTER TABLE model_call_fact
  ADD COLUMN managed_model_api_config_id UUID,
  ADD COLUMN model_name TEXT NOT NULL DEFAULT '',
  ADD CONSTRAINT fk_model_call_fact_managed_config
    FOREIGN KEY (tenant_id, managed_model_api_config_id)
    REFERENCES managed_model_api_config(tenant_id, id);

CREATE INDEX idx_model_evaluation_candidate_managed_config
  ON model_evaluation_candidate (tenant_id, managed_model_api_config_id)
  WHERE managed_model_api_config_id IS NOT NULL;
CREATE INDEX idx_model_approval_managed_config
  ON model_approval (tenant_id, managed_model_api_config_id)
  WHERE managed_model_api_config_id IS NOT NULL;
CREATE INDEX idx_model_sandbox_approval_managed_config
  ON model_sandbox_approval (tenant_id, managed_model_api_config_id)
  WHERE managed_model_api_config_id IS NOT NULL;
CREATE INDEX idx_model_call_fact_managed_config
  ON model_call_fact (tenant_id, managed_model_api_config_id, created_at DESC)
  WHERE managed_model_api_config_id IS NOT NULL;

-- Snapshot model names even when no managed configuration can be identified.
-- The legacy evidence trigger rejects all candidate updates, including a
-- migration backfill. The migrator applies this file as one transaction.
ALTER TABLE model_evaluation_candidate DISABLE TRIGGER trg_model_evaluation_candidate_immutable;

UPDATE model_evaluation_candidate candidate
SET model_name = deployment.model_name
FROM model_deployment deployment
WHERE deployment.tenant_id = candidate.tenant_id
  AND deployment.id = candidate.deployment_id;

-- A match must be exact and unique. Ambiguous or missing history stays
-- legacy-only (managed_model_api_config_id IS NULL); no synthetic key is made.
WITH matches AS (
  SELECT candidate.id AS candidate_id, config.id AS config_id,
         count(*) OVER (PARTITION BY candidate.id) AS match_count
  FROM model_evaluation_candidate candidate
  JOIN model_deployment deployment
    ON deployment.tenant_id = candidate.tenant_id AND deployment.id = candidate.deployment_id
  JOIN model_provider provider
    ON provider.tenant_id = deployment.tenant_id AND provider.id = deployment.provider_id
  JOIN managed_model_api_config config
    ON config.tenant_id = candidate.tenant_id
   AND config.provider_key = provider.provider_key
   AND config.model_name = deployment.model_name
   AND config.model_version = candidate.model_version
   AND config.region = deployment.region
)
UPDATE model_evaluation_candidate candidate
SET managed_model_api_config_id = matches.config_id
FROM matches
WHERE candidate.id = matches.candidate_id AND matches.match_count = 1;

ALTER TABLE model_evaluation_candidate ENABLE TRIGGER trg_model_evaluation_candidate_immutable;

-- Approval evidence is immutable to application writes. Migration-owned
-- backfill runs inside the migrator's transaction and restores the trigger
-- before commit.
ALTER TABLE model_approval DISABLE TRIGGER trg_model_approval_immutable;

UPDATE model_approval approval
SET managed_model_api_config_id = candidate.managed_model_api_config_id,
    model_name = candidate.model_name
FROM model_evaluation_candidate candidate
WHERE candidate.tenant_id = approval.tenant_id
  AND candidate.id = approval.evaluation_candidate_id;

ALTER TABLE model_approval ENABLE TRIGGER trg_model_approval_immutable;

WITH matches AS (
  SELECT approval.id AS approval_id, config.id AS config_id,
         count(*) OVER (PARTITION BY approval.id) AS match_count
  FROM model_sandbox_approval approval
  JOIN model_deployment deployment
    ON deployment.tenant_id = approval.tenant_id AND deployment.id = approval.deployment_id
  JOIN model_provider provider
    ON provider.tenant_id = deployment.tenant_id AND provider.id = deployment.provider_id
  JOIN managed_model_api_config config
    ON config.tenant_id = approval.tenant_id
   AND config.provider_key = provider.provider_key
   AND config.model_name = deployment.model_name
   AND config.model_version = deployment.model_version
   AND config.region = deployment.region
)
UPDATE model_sandbox_approval approval
SET managed_model_api_config_id = matches.config_id
FROM matches
WHERE approval.id = matches.approval_id AND matches.match_count = 1;

UPDATE model_call_fact fact
SET model_name = deployment.model_name
FROM model_deployment deployment
WHERE deployment.tenant_id = fact.tenant_id
  AND deployment.deployment_key = fact.deployment_key
  AND deployment.model_version = fact.model_version;

WITH matches AS (
  SELECT fact.id AS fact_id, config.id AS config_id,
         count(*) OVER (PARTITION BY fact.id) AS match_count
  FROM model_call_fact fact
  JOIN model_deployment deployment
    ON deployment.tenant_id = fact.tenant_id
   AND deployment.deployment_key = fact.deployment_key
   AND deployment.model_version = fact.model_version
  JOIN model_provider provider
    ON provider.tenant_id = deployment.tenant_id AND provider.id = deployment.provider_id
   AND provider.provider_key = fact.provider_key
  JOIN managed_model_api_config config
    ON config.tenant_id = fact.tenant_id
   AND config.provider_key = fact.provider_key
   AND config.model_name = deployment.model_name
   AND config.model_version = fact.model_version
   AND config.region = fact.deployment_region
)
UPDATE model_call_fact fact
SET managed_model_api_config_id = matches.config_id
FROM matches
WHERE fact.id = matches.fact_id AND matches.match_count = 1;
