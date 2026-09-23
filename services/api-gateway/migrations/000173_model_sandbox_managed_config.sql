-- Keep historical provider/deployment approvals readable while new approvals
-- identify the managed school model directly.
ALTER TABLE model_sandbox_approval
  ALTER COLUMN provider_id DROP NOT NULL,
  ALTER COLUMN deployment_id DROP NOT NULL,
  ADD COLUMN provider_key TEXT NOT NULL DEFAULT '',
  ADD COLUMN model_name TEXT NOT NULL DEFAULT '',
  ADD COLUMN model_version TEXT NOT NULL DEFAULT '';

UPDATE model_sandbox_approval approval
SET provider_key = provider.provider_key,
    model_name = deployment.model_name,
    model_version = deployment.model_version
FROM model_deployment deployment
JOIN model_provider provider
  ON provider.tenant_id = deployment.tenant_id AND provider.id = deployment.provider_id
WHERE approval.tenant_id = deployment.tenant_id
  AND approval.deployment_id = deployment.id;

ALTER TABLE model_sandbox_approval
  ADD CONSTRAINT chk_model_sandbox_approval_source CHECK (
    (managed_model_api_config_id IS NOT NULL AND provider_id IS NULL AND deployment_id IS NULL)
    OR (provider_id IS NOT NULL AND deployment_id IS NOT NULL)
  );

CREATE UNIQUE INDEX uq_model_sandbox_approval_active_config
  ON model_sandbox_approval (tenant_id, managed_model_api_config_id)
  WHERE revoked_at IS NULL AND managed_model_api_config_id IS NOT NULL;
