-- 先增加独立文本证据，再升级评分守卫，最后从历史结构化成功中回填文本证据。
-- 旧结构化成功证明曾收到有效回复，但不能自动升级为 v4 评分资格；/models 成功也不能回填。
ALTER TABLE managed_model_api_config
  ADD COLUMN last_text_status TEXT NOT NULL DEFAULT 'untested',
  ADD COLUMN last_text_message TEXT NOT NULL DEFAULT '',
  ADD COLUMN last_text_tested_at TIMESTAMPTZ,
  ADD COLUMN last_text_usage JSONB,
  ADD COLUMN last_text_diagnostic JSONB,
  ADD CONSTRAINT chk_managed_model_api_text_status
    CHECK (last_text_status IN ('untested', 'success', 'failed')),
  ADD CONSTRAINT chk_managed_model_api_text_usage
    CHECK (last_text_usage IS NULL OR jsonb_typeof(last_text_usage) = 'object'),
  ADD CONSTRAINT chk_managed_model_api_text_diagnostic
    CHECK (last_text_diagnostic IS NULL OR jsonb_typeof(last_text_diagnostic) = 'object');

ALTER TABLE managed_model_api_config
  DROP CONSTRAINT chk_managed_model_api_probe_mode,
  ADD CONSTRAINT chk_managed_model_api_probe_mode
    CHECK (last_probe_mode IN ('', 'quick', 'text', 'structured', 'capability'));

-- 平台评分角色健康仍按结构化证据计算；最近一次连接检查不是生成资格的替代品。
CREATE OR REPLACE FUNCTION refresh_platform_school_model_roles(p_tenant_id UUID)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
  v_previous_scope TEXT := current_setting('edugrade.tenant_id', true);
BEGIN
  IF p_tenant_id IS NULL THEN RETURN; END IF;
  PERFORM set_config('edugrade.tenant_id', p_tenant_id::text, true);
  DELETE FROM platform_school_model_role WHERE tenant_id=p_tenant_id;
  INSERT INTO platform_school_model_role(tenant_id,agent_role,model_name,provider_key,health_status,updated_at)
  SELECT DISTINCT ON (binding.agent_role)
    binding.tenant_id,binding.agent_role,config.model_name,config.provider_key,
    CASE
      WHEN binding.status='disabled' OR config.status='disabled' THEN 'disabled'
      WHEN binding.status='active'
        AND config.status='active'
        AND config.last_capability_status='success'
        AND config.last_capability_probe_version='structured-json-v4' THEN 'healthy'
      ELSE 'warning'
    END,
    greatest(binding.updated_at,config.updated_at)
  FROM model_role_binding binding
  JOIN managed_model_api_config config
    ON config.tenant_id=binding.tenant_id AND config.id=binding.managed_model_api_config_id AND config.deleted_at IS NULL
  WHERE binding.tenant_id=p_tenant_id
  ORDER BY binding.agent_role,(binding.status='active') DESC,binding.updated_at DESC;
  PERFORM set_config('edugrade.tenant_id', COALESCE(v_previous_scope,''), true);
END;
$$;

REVOKE ALL ON FUNCTION refresh_platform_school_model_roles(UUID) FROM PUBLIC, edugrade_tenant_runtime;

-- 评测候选继续要求当前结构化协议；已完成评测记录保持原有不可变规则。
CREATE OR REPLACE FUNCTION enforce_model_evaluation_candidate_scope()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  evaluation model_evaluation_run%ROWTYPE;
  config managed_model_api_config%ROWTYPE;
  deployment_modalities JSONB;
  governed_provider_key TEXT;
  governed_deployment_key TEXT;
  governed_model_version TEXT;
BEGIN
  SELECT * INTO evaluation FROM model_evaluation_run
  WHERE tenant_id = NEW.tenant_id AND id = NEW.run_id;
  IF evaluation.id IS NULL OR evaluation.status <> 'draft'
     OR NEW.evaluated_samples <> evaluation.sample_count
     OR NEW.repeat_comparisons > evaluation.sample_count * (evaluation.repeat_count - 1)
     OR (evaluation.repeat_count = 1
         AND (NEW.repeat_comparisons <> 0 OR NEW.stable_repeat_samples <> 0))
     OR (evaluation.repeat_count > 1 AND NEW.repeat_comparisons = 0)
     OR (evaluation.evidence_class = 'authorized_frozen_set'
         AND NEW.teacher_reviewed_samples <> NEW.evaluated_samples)
     OR (evaluation.evidence_class = 'protocol_fixture'
         AND (NEW.teacher_reviewed_samples <> 0 OR NEW.teacher_accepted_samples <> 0))
  THEN
    RAISE EXCEPTION 'invalid model evaluation candidate scope' USING ERRCODE = '23514';
  END IF;

  IF NEW.deployment_id IS NULL THEN
    SELECT * INTO config FROM managed_model_api_config
    WHERE tenant_id = NEW.tenant_id AND id = NEW.managed_model_api_config_id
      AND deleted_at IS NULL;
    IF config.id IS NULL OR config.status <> 'active'
       OR config.last_capability_status <> 'success'
       OR config.last_capability_probe_version <> 'structured-json-v4'
       OR NOT (config.modalities ? evaluation.modality)
       OR NEW.deployment_key IS NOT NULL
       OR NEW.provider_key <> config.provider_key
       OR NEW.model_name <> config.model_name
       OR NEW.model_version <> config.model_version
    THEN
      RAISE EXCEPTION 'invalid managed model evaluation candidate' USING ERRCODE = '23514';
    END IF;
  ELSE
    SELECT provider.provider_key, deployment.deployment_key,
           deployment.model_version, deployment.modalities
    INTO governed_provider_key, governed_deployment_key,
         governed_model_version, deployment_modalities
    FROM model_deployment deployment
    JOIN model_provider provider
      ON provider.tenant_id = deployment.tenant_id
     AND provider.id = deployment.provider_id
     AND provider.deleted_at IS NULL
    WHERE deployment.tenant_id = NEW.tenant_id
      AND deployment.id = NEW.deployment_id
      AND deployment.deleted_at IS NULL;
    IF NOT FOUND OR NEW.provider_key <> governed_provider_key
       OR NEW.deployment_key <> governed_deployment_key
       OR NEW.model_version <> governed_model_version
       OR NOT (deployment_modalities ? evaluation.modality)
    THEN
      RAISE EXCEPTION 'invalid legacy model evaluation candidate' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

UPDATE managed_model_api_config
SET last_text_status = 'success',
    last_text_message = '历史结构化检测已生成有效回复',
    last_text_tested_at = last_capability_tested_at,
    last_text_usage = last_capability_usage,
    last_text_diagnostic = last_capability_diagnostic,
    updated_at = now()
WHERE last_capability_status = 'success';

-- 没有回填行的学校也须重新计算，避免旧版本健康投影继续显示为 healthy。
DO $$
DECLARE
  v_tenant_id UUID;
BEGIN
  FOR v_tenant_id IN SELECT DISTINCT tenant_id FROM model_role_binding LOOP
    PERFORM refresh_platform_school_model_roles(v_tenant_id);
  END LOOP;
END;
$$;
