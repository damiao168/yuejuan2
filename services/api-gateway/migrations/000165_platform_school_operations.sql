-- Platform school operations centre: an AI usage ledger plus deliberately
-- low-sensitivity, cross-tenant projections. Platform request handlers read
-- these projections instead of bypassing tenant RLS on student/grading data.

CREATE TABLE model_usage_event (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenant(id),
  school_id UUID,
  request_id TEXT NOT NULL,
  feature TEXT NOT NULL,
  agent_role TEXT,
  provider_key TEXT NOT NULL,
  model_name TEXT NOT NULL,
  request_count BIGINT NOT NULL DEFAULT 1,
  input_tokens BIGINT NOT NULL DEFAULT 0,
  output_tokens BIGINT NOT NULL DEFAULT 0,
  cached_input_tokens BIGINT NOT NULL DEFAULT 0,
  reasoning_tokens BIGINT NOT NULL DEFAULT 0,
  total_tokens BIGINT NOT NULL DEFAULT 0,
  estimated_cost_microusd BIGINT,
  status TEXT NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  UNIQUE (tenant_id, request_id, feature),
  FOREIGN KEY (tenant_id, school_id) REFERENCES school(tenant_id, id),
  CHECK (feature IN ('paper_import','school_ai_chat','subjective_grading','model_probe','model_evaluation','other')),
  CHECK (agent_role IS NULL OR agent_role IN ('single','primary_a','primary_b','arbiter')),
  CHECK (status IN ('succeeded','failed','replayed')),
  CHECK (request_count >= 1 AND input_tokens >= 0 AND output_tokens >= 0 AND cached_input_tokens >= 0 AND reasoning_tokens >= 0 AND total_tokens >= 0),
  CHECK (estimated_cost_microusd IS NULL OR estimated_cost_microusd >= 0),
  CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE INDEX idx_model_usage_event_tenant_time
  ON model_usage_event (tenant_id, occurred_at DESC);
CREATE INDEX idx_model_usage_event_school_time
  ON model_usage_event (tenant_id, school_id, occurred_at DESC);
CREATE INDEX idx_model_usage_event_dimensions
  ON model_usage_event (tenant_id, feature, agent_role, provider_key, model_name, occurred_at DESC);
CREATE INDEX idx_model_usage_event_completed_time
  ON model_usage_event (tenant_id, occurred_at DESC, id DESC)
  WHERE status IN ('succeeded','replayed');
CREATE INDEX idx_model_usage_event_failed_time
  ON model_usage_event (tenant_id, occurred_at DESC, id DESC)
  WHERE status='failed';

CREATE TABLE model_usage_daily (
  usage_date DATE NOT NULL,
  tenant_id UUID NOT NULL REFERENCES tenant(id),
  school_id UUID,
  feature TEXT NOT NULL,
  agent_role TEXT NOT NULL DEFAULT '',
  provider_key TEXT NOT NULL,
  model_name TEXT NOT NULL,
  request_count BIGINT NOT NULL DEFAULT 0,
  input_tokens BIGINT NOT NULL DEFAULT 0,
  output_tokens BIGINT NOT NULL DEFAULT 0,
  cached_input_tokens BIGINT NOT NULL DEFAULT 0,
  reasoning_tokens BIGINT NOT NULL DEFAULT 0,
  total_tokens BIGINT NOT NULL DEFAULT 0,
  estimated_cost_microusd BIGINT NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT uq_model_usage_daily_dimensions
    UNIQUE NULLS NOT DISTINCT (usage_date, tenant_id, school_id, feature, agent_role, provider_key, model_name),
  FOREIGN KEY (tenant_id, school_id) REFERENCES school(tenant_id, id),
  CHECK (request_count >= 0 AND input_tokens >= 0 AND output_tokens >= 0 AND cached_input_tokens >= 0 AND reasoning_tokens >= 0 AND total_tokens >= 0 AND estimated_cost_microusd >= 0)
);

CREATE INDEX idx_model_usage_daily_school_date
  ON model_usage_daily (tenant_id, school_id, usage_date DESC);

CREATE TABLE platform_school_metrics (
  tenant_id UUID PRIMARY KEY REFERENCES tenant(id) ON DELETE CASCADE,
  school_id UUID NOT NULL UNIQUE,
  account_count BIGINT NOT NULL DEFAULT 0,
  active_account_count BIGINT NOT NULL DEFAULT 0,
  admin_count BIGINT NOT NULL DEFAULT 0,
  teacher_count BIGINT NOT NULL DEFAULT 0,
  grader_count BIGINT NOT NULL DEFAULT 0,
  student_count BIGINT NOT NULL DEFAULT 0,
  class_count BIGINT NOT NULL DEFAULT 0,
  exam_count BIGINT NOT NULL DEFAULT 0,
  last_activity_at TIMESTAMPTZ,
  consecutive_ai_failures BIGINT NOT NULL DEFAULT 0,
  active_admin_count BIGINT NOT NULL DEFAULT 0,
  mfa_enabled_admin_count BIGINT NOT NULL DEFAULT 0,
  active_session_count BIGINT NOT NULL DEFAULT 0,
  last_login_at TIMESTAMPTZ,
  admin_id UUID,
  admin_display_name TEXT NOT NULL DEFAULT '',
  admin_username TEXT NOT NULL DEFAULT '',
  admin_last_login_at TIMESTAMPTZ,
  model_config_id UUID,
  model_display_name TEXT NOT NULL DEFAULT '',
  model_provider_key TEXT NOT NULL DEFAULT '',
  model_name TEXT NOT NULL DEFAULT '',
  model_capability_version TEXT NOT NULL DEFAULT '',
  model_credential_hint TEXT NOT NULL DEFAULT '',
  model_status TEXT NOT NULL DEFAULT '',
  model_test_status TEXT NOT NULL DEFAULT '',
  model_test_message TEXT NOT NULL DEFAULT '',
  model_capability_status TEXT NOT NULL DEFAULT '',
  model_capability_message TEXT NOT NULL DEFAULT '',
  model_test_latency_ms BIGINT NOT NULL DEFAULT 0,
  model_tested_at TIMESTAMPTZ,
  model_capability_tested_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (tenant_id, school_id) REFERENCES school(tenant_id, id),
  CHECK (account_count >= 0 AND active_account_count >= 0 AND admin_count >= 0 AND teacher_count >= 0 AND grader_count >= 0 AND student_count >= 0 AND class_count >= 0 AND exam_count >= 0),
  CHECK (consecutive_ai_failures >= 0 AND active_admin_count >= 0 AND mfa_enabled_admin_count >= 0 AND active_session_count >= 0)
);

COMMENT ON TABLE platform_school_metrics IS
'Low-sensitivity counts only. It intentionally excludes student identity, answers, scores, OCR and grading evidence.';

CREATE TABLE platform_school_model_role (
  tenant_id UUID NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  agent_role TEXT NOT NULL,
  model_name TEXT NOT NULL,
  provider_key TEXT NOT NULL,
  health_status TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, agent_role),
  CHECK (agent_role IN ('primary_a','primary_b','arbiter')),
  CHECK (health_status IN ('healthy','warning','disabled'))
);

-- Derive the current failure streak from event time, not insertion order. This
-- keeps late-arriving worker events from corrupting the operational signal.
CREATE OR REPLACE FUNCTION model_usage_consecutive_failures(p_tenant_id UUID)
RETURNS BIGINT
LANGUAGE sql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
  WITH last_completed AS (
    SELECT completed.occurred_at,completed.id
    FROM model_usage_event completed
    WHERE completed.tenant_id=p_tenant_id
      AND completed.status IN ('succeeded','replayed')
    ORDER BY completed.occurred_at DESC,completed.id DESC
    LIMIT 1
  )
  SELECT count(failed.id)::bigint
  FROM model_usage_event failed
  LEFT JOIN last_completed ON true
  WHERE failed.tenant_id = p_tenant_id
    AND failed.status = 'failed'
    AND (
      last_completed.id IS NULL
      OR (failed.occurred_at,failed.id)>(last_completed.occurred_at,last_completed.id)
    )
$$;

REVOKE ALL ON FUNCTION model_usage_consecutive_failures(UUID) FROM PUBLIC, edugrade_tenant_runtime;

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
        AND config.last_test_status='success'
        AND config.last_capability_status='success'
        AND config.last_capability_probe_version='structured-json-v3' THEN 'healthy'
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

CREATE OR REPLACE FUNCTION refresh_platform_school_model_roles_trigger()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
  PERFORM refresh_platform_school_model_roles(CASE WHEN TG_OP='DELETE' THEN OLD.tenant_id ELSE NEW.tenant_id END);
  IF TG_OP='UPDATE' AND OLD.tenant_id IS DISTINCT FROM NEW.tenant_id THEN
    PERFORM refresh_platform_school_model_roles(OLD.tenant_id);
  END IF;
  RETURN COALESCE(NEW,OLD);
END;
$$;

REVOKE ALL ON FUNCTION refresh_platform_school_model_roles_trigger() FROM PUBLIC, edugrade_tenant_runtime;

CREATE TRIGGER trg_platform_school_model_roles
AFTER INSERT OR UPDATE OR DELETE ON model_role_binding
FOR EACH ROW EXECUTE FUNCTION refresh_platform_school_model_roles_trigger();

CREATE TRIGGER trg_platform_school_model_roles_config
AFTER INSERT OR UPDATE OR DELETE ON managed_model_api_config
FOR EACH ROW EXECUTE FUNCTION refresh_platform_school_model_roles_trigger();

CREATE OR REPLACE FUNCTION refresh_platform_school_metrics(p_tenant_id UUID)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
  v_school_id UUID;
  v_previous_scope TEXT := current_setting('edugrade.tenant_id', true);
BEGIN
  IF p_tenant_id IS NULL OR p_tenant_id = '00000000-0000-0000-0000-000000000001'::uuid THEN
    RETURN;
  END IF;
  -- The trigger owner reads only the affected tenant's protected source rows;
  -- request handlers never receive the maintenance RLS marker.
  PERFORM set_config('edugrade.tenant_id', p_tenant_id::text, true);
  SELECT id INTO v_school_id
  FROM school
  WHERE tenant_id = p_tenant_id AND deleted_at IS NULL
  ORDER BY created_at, id
  LIMIT 1;
  IF v_school_id IS NULL THEN
    DELETE FROM platform_school_metrics WHERE tenant_id = p_tenant_id;
    PERFORM set_config('edugrade.tenant_id', COALESCE(v_previous_scope,''), true);
    RETURN;
  END IF;

  INSERT INTO platform_school_metrics (
    tenant_id, school_id, account_count, active_account_count,
    admin_count, teacher_count, grader_count, student_count,
    class_count, exam_count, last_activity_at, consecutive_ai_failures, active_admin_count,
    mfa_enabled_admin_count, active_session_count, last_login_at,
    admin_id,admin_display_name,admin_username,admin_last_login_at,
    model_config_id,model_display_name,model_provider_key,model_name,model_capability_version,
    model_credential_hint,model_status,model_test_status,model_test_message,
    model_capability_status,model_capability_message,model_test_latency_ms,
    model_tested_at,model_capability_tested_at,updated_at
  )
  SELECT
    p_tenant_id,
    v_school_id,
    (SELECT count(*) FROM app_user u WHERE u.tenant_id=p_tenant_id AND u.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL AND r.deleted_at IS NULL AND (r.code='student' OR right(r.code,7)='_worker'))),
    (SELECT count(*) FROM app_user u WHERE u.tenant_id=p_tenant_id AND u.deleted_at IS NULL AND u.status='active' AND NOT EXISTS (SELECT 1 FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL AND r.deleted_at IS NULL AND (r.code='student' OR right(r.code,7)='_worker'))),
    (SELECT count(DISTINCT ur.user_id) FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL JOIN app_user u ON u.tenant_id=ur.tenant_id AND u.id=ur.user_id AND u.deleted_at IS NULL WHERE ur.tenant_id=p_tenant_id AND ur.deleted_at IS NULL AND r.code IN ('tenant_admin','school_admin')),
    (SELECT count(DISTINCT ur.user_id) FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL JOIN app_user u ON u.tenant_id=ur.tenant_id AND u.id=ur.user_id AND u.deleted_at IS NULL WHERE ur.tenant_id=p_tenant_id AND ur.deleted_at IS NULL AND r.code='teacher'),
    (SELECT count(DISTINCT ur.user_id) FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL JOIN app_user u ON u.tenant_id=ur.tenant_id AND u.id=ur.user_id AND u.deleted_at IS NULL WHERE ur.tenant_id=p_tenant_id AND ur.deleted_at IS NULL AND r.code IN ('grader','arbitrator')),
    (SELECT count(*) FROM student st WHERE st.tenant_id=p_tenant_id AND st.deleted_at IS NULL),
    (SELECT count(*) FROM school_class sc WHERE sc.tenant_id=p_tenant_id AND sc.deleted_at IS NULL),
    (SELECT count(*) FROM exam e WHERE e.tenant_id=p_tenant_id AND e.deleted_at IS NULL),
    (SELECT max(ae.happened_at) FROM activity_event ae WHERE ae.tenant_id=p_tenant_id),
    model_usage_consecutive_failures(p_tenant_id),
    (SELECT count(DISTINCT ur.user_id) FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL JOIN app_user u ON u.tenant_id=ur.tenant_id AND u.id=ur.user_id AND u.deleted_at IS NULL AND u.status='active' WHERE ur.tenant_id=p_tenant_id AND ur.deleted_at IS NULL AND r.code IN ('tenant_admin','school_admin')),
    (SELECT count(DISTINCT ur.user_id) FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL JOIN auth_totp mfa ON mfa.tenant_id=ur.tenant_id AND mfa.user_id=ur.user_id AND mfa.enabled_at IS NOT NULL WHERE ur.tenant_id=p_tenant_id AND ur.deleted_at IS NULL AND r.code IN ('tenant_admin','school_admin')),
    (SELECT count(*) FROM auth_session ses WHERE ses.tenant_id=p_tenant_id AND ses.revoked_at IS NULL AND ses.expires_at > now()),
    (SELECT max(u.last_login_at) FROM app_user u WHERE u.tenant_id=p_tenant_id AND u.deleted_at IS NULL),
    admin.id,COALESCE(admin.display_name,''),COALESCE(admin.username,''),admin.last_login_at,
    model.id,COALESCE(model.display_name,''),COALESCE(model.provider_key,''),COALESCE(model.model_name,''),COALESCE(model.last_capability_probe_version,''),
    COALESCE(model.credential_hint,''),COALESCE(model.status,''),COALESCE(model.last_test_status,''),COALESCE(model.last_test_message,''),
    COALESCE(model.last_capability_status,''),COALESCE(model.last_capability_message,''),COALESCE(model.last_test_latency_ms,0),
    model.last_tested_at,model.last_capability_tested_at,now()
  FROM (VALUES (p_tenant_id)) AS tenant_scope(id)
  LEFT JOIN LATERAL (
    SELECT u.id,u.display_name,u.username,u.last_login_at
    FROM app_user u
    WHERE u.tenant_id=tenant_scope.id AND u.deleted_at IS NULL AND EXISTS (
      SELECT 1 FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id
      WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL AND r.deleted_at IS NULL
        AND r.code IN ('tenant_admin','school_admin'))
    ORDER BY (u.status='active') DESC,u.created_at,u.id LIMIT 1
  ) admin ON true
  LEFT JOIN LATERAL (
    SELECT c.id,c.display_name,c.provider_key,c.model_name,c.last_capability_probe_version,c.credential_hint,c.status,
      c.last_test_status,c.last_test_message,c.last_capability_status,c.last_capability_message,
      c.last_test_latency_ms,c.last_tested_at,c.last_capability_tested_at
    FROM managed_model_api_config c
    WHERE c.tenant_id=tenant_scope.id AND c.deleted_at IS NULL AND c.is_default
    ORDER BY c.updated_at DESC,c.id LIMIT 1
  ) model ON true
  ON CONFLICT (tenant_id) DO UPDATE SET
    school_id=EXCLUDED.school_id,
    account_count=EXCLUDED.account_count,
    active_account_count=EXCLUDED.active_account_count,
    admin_count=EXCLUDED.admin_count,
    teacher_count=EXCLUDED.teacher_count,
    grader_count=EXCLUDED.grader_count,
    student_count=EXCLUDED.student_count,
    class_count=EXCLUDED.class_count,
    exam_count=EXCLUDED.exam_count,
    last_activity_at=EXCLUDED.last_activity_at,
    consecutive_ai_failures=EXCLUDED.consecutive_ai_failures,
    active_admin_count=EXCLUDED.active_admin_count,
    mfa_enabled_admin_count=EXCLUDED.mfa_enabled_admin_count,
    active_session_count=EXCLUDED.active_session_count,
    last_login_at=EXCLUDED.last_login_at,
    admin_id=EXCLUDED.admin_id,
    admin_display_name=EXCLUDED.admin_display_name,
    admin_username=EXCLUDED.admin_username,
    admin_last_login_at=EXCLUDED.admin_last_login_at,
    model_config_id=EXCLUDED.model_config_id,
    model_display_name=EXCLUDED.model_display_name,
    model_provider_key=EXCLUDED.model_provider_key,
    model_name=EXCLUDED.model_name,
    model_capability_version=EXCLUDED.model_capability_version,
    model_credential_hint=EXCLUDED.model_credential_hint,
    model_status=EXCLUDED.model_status,
    model_test_status=EXCLUDED.model_test_status,
    model_test_message=EXCLUDED.model_test_message,
    model_capability_status=EXCLUDED.model_capability_status,
    model_capability_message=EXCLUDED.model_capability_message,
    model_test_latency_ms=EXCLUDED.model_test_latency_ms,
    model_tested_at=EXCLUDED.model_tested_at,
    model_capability_tested_at=EXCLUDED.model_capability_tested_at,
    updated_at=now();
  PERFORM set_config('edugrade.tenant_id', COALESCE(v_previous_scope,''), true);
END;
$$;

REVOKE ALL ON FUNCTION refresh_platform_school_metrics(UUID) FROM PUBLIC, edugrade_tenant_runtime;

CREATE OR REPLACE FUNCTION refresh_platform_school_metrics_trigger()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
  PERFORM refresh_platform_school_metrics(CASE WHEN TG_OP='DELETE' THEN OLD.tenant_id ELSE NEW.tenant_id END);
  IF TG_OP='UPDATE' AND OLD.tenant_id IS DISTINCT FROM NEW.tenant_id THEN
    PERFORM refresh_platform_school_metrics(OLD.tenant_id);
  END IF;
  RETURN COALESCE(NEW, OLD);
END;
$$;

REVOKE ALL ON FUNCTION refresh_platform_school_metrics_trigger() FROM PUBLIC, edugrade_tenant_runtime;

CREATE TRIGGER trg_platform_metrics_school
AFTER INSERT OR UPDATE OR DELETE ON school
FOR EACH ROW EXECUTE FUNCTION refresh_platform_school_metrics_trigger();
CREATE TRIGGER trg_platform_metrics_user
AFTER INSERT OR UPDATE OR DELETE ON app_user
FOR EACH ROW EXECUTE FUNCTION refresh_platform_school_metrics_trigger();
CREATE TRIGGER trg_platform_metrics_user_role
AFTER INSERT OR UPDATE OR DELETE ON user_role
FOR EACH ROW EXECUTE FUNCTION refresh_platform_school_metrics_trigger();
CREATE TRIGGER trg_platform_metrics_role
AFTER INSERT OR UPDATE OR DELETE ON role
FOR EACH ROW EXECUTE FUNCTION refresh_platform_school_metrics_trigger();
CREATE TRIGGER trg_platform_metrics_model_config
AFTER INSERT OR UPDATE OR DELETE ON managed_model_api_config
FOR EACH ROW EXECUTE FUNCTION refresh_platform_school_metrics_trigger();
CREATE TRIGGER trg_platform_metrics_student
AFTER INSERT OR UPDATE OR DELETE ON student
FOR EACH ROW EXECUTE FUNCTION refresh_platform_school_metrics_trigger();
CREATE TRIGGER trg_platform_metrics_class
AFTER INSERT OR UPDATE OR DELETE ON school_class
FOR EACH ROW EXECUTE FUNCTION refresh_platform_school_metrics_trigger();
CREATE TRIGGER trg_platform_metrics_exam
AFTER INSERT OR UPDATE OR DELETE ON exam
FOR EACH ROW EXECUTE FUNCTION refresh_platform_school_metrics_trigger();
CREATE TRIGGER trg_platform_metrics_activity
AFTER INSERT OR UPDATE OR DELETE ON activity_event
FOR EACH ROW EXECUTE FUNCTION refresh_platform_school_metrics_trigger();
CREATE TRIGGER trg_platform_metrics_session
AFTER INSERT OR UPDATE OR DELETE ON auth_session
FOR EACH ROW EXECUTE FUNCTION refresh_platform_school_metrics_trigger();
CREATE TRIGGER trg_platform_metrics_totp
AFTER INSERT OR UPDATE OR DELETE ON auth_totp
FOR EACH ROW EXECUTE FUNCTION refresh_platform_school_metrics_trigger();

CREATE OR REPLACE FUNCTION aggregate_model_usage_event()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
  INSERT INTO model_usage_daily (
    usage_date, tenant_id, school_id, feature, agent_role, provider_key, model_name,
    request_count, input_tokens, output_tokens, cached_input_tokens, reasoning_tokens,
    total_tokens, estimated_cost_microusd, updated_at
  ) VALUES (
    (NEW.occurred_at AT TIME ZONE 'UTC')::date, NEW.tenant_id, NEW.school_id, NEW.feature,
    COALESCE(NEW.agent_role,''), NEW.provider_key, NEW.model_name,
    NEW.request_count, NEW.input_tokens, NEW.output_tokens, NEW.cached_input_tokens, NEW.reasoning_tokens,
    NEW.total_tokens, COALESCE(NEW.estimated_cost_microusd,0), now()
  )
  ON CONFLICT (usage_date, tenant_id, school_id, feature, agent_role, provider_key, model_name)
  DO UPDATE SET
    request_count=model_usage_daily.request_count+EXCLUDED.request_count,
    input_tokens=model_usage_daily.input_tokens+EXCLUDED.input_tokens,
    output_tokens=model_usage_daily.output_tokens+EXCLUDED.output_tokens,
    cached_input_tokens=model_usage_daily.cached_input_tokens+EXCLUDED.cached_input_tokens,
    reasoning_tokens=model_usage_daily.reasoning_tokens+EXCLUDED.reasoning_tokens,
    total_tokens=model_usage_daily.total_tokens+EXCLUDED.total_tokens,
    estimated_cost_microusd=model_usage_daily.estimated_cost_microusd+EXCLUDED.estimated_cost_microusd,
    updated_at=now();

  UPDATE platform_school_metrics SET
    consecutive_ai_failures=model_usage_consecutive_failures(NEW.tenant_id),
    updated_at=now()
  WHERE tenant_id=NEW.tenant_id;
  RETURN NEW;
END;
$$;

REVOKE ALL ON FUNCTION aggregate_model_usage_event() FROM PUBLIC, edugrade_tenant_runtime;

CREATE TRIGGER trg_model_usage_daily
AFTER INSERT ON model_usage_event
FOR EACH ROW EXECUTE FUNCTION aggregate_model_usage_event();

-- Existing governed subjective calls become ledger events. Token columns are
-- now populated by the grading-agent telemetry; historical rows remain honest
-- zeroes rather than estimates.
CREATE OR REPLACE FUNCTION project_model_call_fact_usage()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_school_id UUID;
  v_agent_role TEXT;
BEGIN
  SELECT e.school_id INTO v_school_id
  FROM answer_segment seg
  JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id
  JOIN exam e ON e.tenant_id=sub.tenant_id AND e.id=sub.exam_id
  WHERE seg.tenant_id=NEW.tenant_id AND seg.id=NEW.answer_segment_id;

  SELECT NULLIF(run.agent_role,'single') INTO v_agent_role
  FROM ai_grade grade
  JOIN subjective_grading_run run
    ON run.tenant_id=grade.tenant_id AND run.id=grade.subjective_grading_run_id
  WHERE grade.tenant_id=NEW.tenant_id
    AND grade.adapter_request_id=NEW.request_id
    AND grade.deleted_at IS NULL
  ORDER BY grade.created_at DESC LIMIT 1;

  INSERT INTO model_usage_event (
    tenant_id, school_id, request_id, feature, agent_role, provider_key, model_name,
    input_tokens, output_tokens, total_tokens, estimated_cost_microusd, status, occurred_at,
    metadata
  ) VALUES (
    NEW.tenant_id, v_school_id, NEW.request_id, 'subjective_grading', v_agent_role,
    NEW.provider_key, COALESCE(NULLIF(NEW.model_version,''),NEW.deployment_key),
    NEW.input_units, NEW.output_units, NEW.input_units+NEW.output_units,
    NEW.estimated_cost_micros, NEW.status, NEW.created_at,
    jsonb_build_object('deployment_key',NEW.deployment_key,'question_id',NEW.question_id,'answer_segment_id',NEW.answer_segment_id)
  ) ON CONFLICT (tenant_id, request_id, feature) DO NOTHING;
  RETURN NEW;
END;
$$;

REVOKE ALL ON FUNCTION project_model_call_fact_usage() FROM PUBLIC, edugrade_tenant_runtime;

CREATE TRIGGER trg_model_call_fact_usage
AFTER INSERT ON model_call_fact
FOR EACH ROW EXECUTE FUNCTION project_model_call_fact_usage();

-- Backfill low-sensitivity projections while migrations run with the trusted
-- migration identity. No request handler is granted maintenance scope.
SELECT refresh_platform_school_metrics(id)
FROM tenant
WHERE deleted_at IS NULL AND code <> 'platform';

SELECT refresh_platform_school_model_roles(id)
FROM tenant
WHERE deleted_at IS NULL AND code <> 'platform';

-- Preserve provider-reported usage already stored on completed paper imports.
-- The historical payload did not retain provider/model identity, so label it
-- unknown instead of attributing it to the tenant's current default model.
INSERT INTO model_usage_event (
  tenant_id,school_id,request_id,feature,provider_key,model_name,request_count,
  input_tokens,output_tokens,cached_input_tokens,reasoning_tokens,total_tokens,
  status,occurred_at,metadata
)
SELECT
  job.tenant_id,exam.school_id,
  'paper-import:' || COALESCE(run.id,job.id)::text,
  'paper_import','unknown','unknown',GREATEST(COALESCE(usage.request_count,1),1),
  COALESCE(usage.input_tokens,0),COALESCE(usage.output_tokens,0),
  COALESCE(usage.cached_input_tokens,0),COALESCE(usage.reasoning_tokens,0),
  CASE WHEN COALESCE(usage.total_tokens,0)>0 THEN usage.total_tokens
       ELSE COALESCE(usage.input_tokens,0)+COALESCE(usage.output_tokens,0) END,
  'succeeded',COALESCE(run.completed_at,job.updated_at),
  jsonb_build_object('paper_import_id',job.id,'generation',COALESCE(job.result_generation,job.current_generation),'backfilled',true)
FROM paper_import_job job
JOIN exam ON exam.tenant_id=job.tenant_id AND exam.id=job.exam_id
LEFT JOIN LATERAL (
  SELECT r.id,r.completed_at
  FROM paper_import_run r
  WHERE r.tenant_id=job.tenant_id AND r.paper_import_id=job.id
    AND r.generation=COALESCE(job.result_generation,job.current_generation)
  ORDER BY r.updated_at DESC,r.id
  LIMIT 1
) run ON true
LEFT JOIN LATERAL jsonb_to_record(job.model_usage) AS usage(
  request_count BIGINT,
  input_tokens BIGINT,output_tokens BIGINT,cached_input_tokens BIGINT,
  reasoning_tokens BIGINT,total_tokens BIGINT
) ON true
WHERE job.deleted_at IS NULL
  AND (
    COALESCE(usage.input_tokens,0)>0 OR COALESCE(usage.output_tokens,0)>0
    OR COALESCE(usage.cached_input_tokens,0)>0 OR COALESCE(usage.reasoning_tokens,0)>0
    OR COALESCE(usage.total_tokens,0)>0
  )
ON CONFLICT (tenant_id,request_id,feature) DO NOTHING;

-- High-risk answer/submission tables are FORCE-RLS protected. Backfill one
-- tenant at a time under that tenant's setting; never use maintenance scope.
DO $$
DECLARE
  v_tenant_id UUID;
  v_previous_scope TEXT := current_setting('edugrade.tenant_id', true);
BEGIN
  FOR v_tenant_id IN
    SELECT DISTINCT tenant_id FROM model_call_fact
  LOOP
    PERFORM set_config('edugrade.tenant_id',v_tenant_id::text,true);
    INSERT INTO model_usage_event (
      tenant_id,school_id,request_id,feature,agent_role,provider_key,model_name,
      input_tokens,output_tokens,total_tokens,estimated_cost_microusd,status,occurred_at,metadata
    )
    SELECT
      fact.tenant_id,exam.school_id,fact.request_id,'subjective_grading',
      NULLIF(role.agent_role,'single'),fact.provider_key,
      COALESCE(NULLIF(fact.model_version,''),fact.deployment_key),
      fact.input_units,fact.output_units,fact.input_units+fact.output_units,
      fact.estimated_cost_micros,fact.status,fact.created_at,
      jsonb_build_object(
        'deployment_key',fact.deployment_key,'question_id',fact.question_id,
        'answer_segment_id',fact.answer_segment_id,'backfilled',true
      )
    FROM model_call_fact fact
    LEFT JOIN answer_segment segment
      ON segment.tenant_id=fact.tenant_id AND segment.id=fact.answer_segment_id
    LEFT JOIN submission submission
      ON submission.tenant_id=segment.tenant_id AND submission.id=segment.submission_id
    LEFT JOIN exam
      ON exam.tenant_id=submission.tenant_id AND exam.id=submission.exam_id
    LEFT JOIN LATERAL (
      SELECT run.agent_role
      FROM ai_grade grade
      JOIN subjective_grading_run run
        ON run.tenant_id=grade.tenant_id AND run.id=grade.subjective_grading_run_id
      WHERE grade.tenant_id=fact.tenant_id
        AND grade.adapter_request_id=fact.request_id
        AND grade.deleted_at IS NULL
      ORDER BY grade.created_at DESC
      LIMIT 1
    ) role ON true
    WHERE fact.tenant_id=v_tenant_id
    ON CONFLICT (tenant_id,request_id,feature) DO NOTHING;
  END LOOP;
  PERFORM set_config('edugrade.tenant_id',COALESCE(v_previous_scope,''),true);
END
$$;

-- Backfill the last known paid capability probe per managed configuration.
INSERT INTO model_usage_event (
  tenant_id,school_id,request_id,feature,provider_key,model_name,
  input_tokens,output_tokens,cached_input_tokens,reasoning_tokens,total_tokens,
  status,occurred_at,metadata
)
SELECT
  config.tenant_id,school.id,'model-probe-backfill:' || config.id::text,
  'model_probe',config.provider_key,config.model_name,
  COALESCE(usage.input_tokens,0),COALESCE(usage.output_tokens,0),
  COALESCE(usage.cached_input_tokens,0),COALESCE(usage.reasoning_tokens,0),
  CASE WHEN COALESCE(usage.total_tokens,0)>0 THEN usage.total_tokens
       ELSE COALESCE(usage.input_tokens,0)+COALESCE(usage.output_tokens,0) END,
  CASE WHEN config.last_capability_status='success' THEN 'succeeded' ELSE 'failed' END,
  COALESCE(config.last_capability_tested_at,config.updated_at),
  jsonb_build_object('probe_mode','capability','probe_version',config.last_capability_probe_version,'backfilled',true)
FROM managed_model_api_config config
LEFT JOIN LATERAL (
  SELECT s.id FROM school s
  WHERE s.tenant_id=config.tenant_id AND s.deleted_at IS NULL
  ORDER BY s.created_at,s.id LIMIT 1
) school ON true
LEFT JOIN LATERAL jsonb_to_record(config.last_capability_usage) AS usage(
  input_tokens BIGINT,output_tokens BIGINT,cached_input_tokens BIGINT,
  reasoning_tokens BIGINT,total_tokens BIGINT
) ON true
WHERE (
  COALESCE(usage.input_tokens,0)>0 OR COALESCE(usage.output_tokens,0)>0
  OR COALESCE(usage.cached_input_tokens,0)>0 OR COALESCE(usage.reasoning_tokens,0)>0
  OR COALESCE(usage.total_tokens,0)>0
)
ON CONFLICT (tenant_id,request_id,feature) DO NOTHING;

-- Runtime requests may append immutable tenant-scoped ledger events. Daily
-- usage and school-health projections are trigger-maintained and runtime
-- read-only. The platform tenant may read only the low-sensitivity projections.
ALTER TABLE model_usage_event ENABLE ROW LEVEL SECURITY;
ALTER TABLE model_usage_event FORCE ROW LEVEL SECURITY;
CREATE POLICY model_usage_event_read ON model_usage_event
  FOR SELECT USING (edugrade_tenant_matches(tenant_id));
CREATE POLICY model_usage_event_append ON model_usage_event
  FOR INSERT WITH CHECK (edugrade_tenant_matches(tenant_id));

ALTER TABLE model_usage_daily ENABLE ROW LEVEL SECURITY;
ALTER TABLE model_usage_daily FORCE ROW LEVEL SECURITY;
CREATE POLICY model_usage_daily_read ON model_usage_daily
  FOR SELECT USING (
    edugrade_tenant_matches(tenant_id)
    OR current_setting('edugrade.tenant_id',true)='00000000-0000-0000-0000-000000000001'
  );
CREATE POLICY model_usage_daily_insert ON model_usage_daily
  FOR INSERT WITH CHECK (edugrade_tenant_matches(tenant_id));
CREATE POLICY model_usage_daily_update ON model_usage_daily
  FOR UPDATE USING (edugrade_tenant_matches(tenant_id))
  WITH CHECK (edugrade_tenant_matches(tenant_id));

ALTER TABLE platform_school_metrics ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_school_metrics FORCE ROW LEVEL SECURITY;
CREATE POLICY platform_school_metrics_read ON platform_school_metrics
  FOR SELECT USING (
    edugrade_tenant_matches(tenant_id)
    OR current_setting('edugrade.tenant_id',true)='00000000-0000-0000-0000-000000000001'
  );
CREATE POLICY platform_school_metrics_maintain ON platform_school_metrics
  FOR ALL USING (edugrade_tenant_matches(tenant_id))
  WITH CHECK (edugrade_tenant_matches(tenant_id));

ALTER TABLE platform_school_model_role ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_school_model_role FORCE ROW LEVEL SECURITY;
CREATE POLICY platform_school_model_role_read ON platform_school_model_role
  FOR SELECT USING (
    edugrade_tenant_matches(tenant_id)
    OR current_setting('edugrade.tenant_id',true)='00000000-0000-0000-0000-000000000001'
  );
CREATE POLICY platform_school_model_role_maintain ON platform_school_model_role
  FOR ALL USING (edugrade_tenant_matches(tenant_id))
  WITH CHECK (edugrade_tenant_matches(tenant_id));

REVOKE ALL ON model_usage_event,model_usage_daily,platform_school_metrics,platform_school_model_role
  FROM edugrade_tenant_runtime;
GRANT SELECT,INSERT ON model_usage_event TO edugrade_tenant_runtime;
GRANT SELECT ON model_usage_daily,platform_school_metrics,platform_school_model_role
  TO edugrade_tenant_runtime;
