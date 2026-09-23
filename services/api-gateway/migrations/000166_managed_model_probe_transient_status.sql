-- A network probe is an observation, not proof that a previously working model is invalid.
-- Keep the last successful check distinct from the latest attempt.

ALTER TABLE managed_model_api_config
  DROP CONSTRAINT IF EXISTS chk_managed_model_api_test_status,
  ADD CONSTRAINT chk_managed_model_api_test_status
    CHECK (last_test_status IN ('untested', 'success', 'failed', 'temporary_unavailable')) NOT VALID,
  ADD COLUMN IF NOT EXISTS last_successful_tested_at TIMESTAMPTZ;

-- NOT VALID avoids scanning the table while holding the stronger schema-change
-- lock. The former constraint already guaranteed every existing row is a valid
-- subset; validation then uses a lighter lock and keeps this migration retryable.
ALTER TABLE managed_model_api_config
  VALIDATE CONSTRAINT chk_managed_model_api_test_status;

UPDATE managed_model_api_config
SET last_successful_tested_at = last_tested_at
WHERE last_test_status = 'success' AND last_tested_at IS NOT NULL;

-- Capability verification also proves the connection worked at that time.
UPDATE managed_model_api_config
SET last_successful_tested_at = last_capability_tested_at
WHERE last_successful_tested_at IS NULL
  AND last_capability_status = 'success'
  AND last_capability_tested_at IS NOT NULL;

-- Reclassify only the exact historical network-error messages emitted by this
-- gateway; never reinterpret authentication or model-permission failures.
UPDATE managed_model_api_config
SET last_test_status = 'temporary_unavailable'
WHERE last_test_status = 'failed'
  AND last_test_message IN ('模型服务连接超时，请稍后重试', '无法连接模型供应商');
