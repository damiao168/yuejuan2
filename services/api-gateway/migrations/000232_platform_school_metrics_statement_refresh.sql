-- 批量语句先收集受影响的租户，再按固定顺序刷新每个租户的学校汇总。
-- UPDATE 合并变更前后的租户，避免移动记录时旧租户留下过期计数。
CREATE OR REPLACE FUNCTION refresh_platform_school_metrics_trigger()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
  affected_tenant UUID;
  affected_tenants UUID[];
  previous_scope TEXT := current_setting('edugrade.tenant_id', true);
BEGIN
  IF TG_OP = 'INSERT' THEN
    SELECT array_agg(DISTINCT tenant_id ORDER BY tenant_id) INTO affected_tenants FROM changed_rows_new;
  ELSIF TG_OP = 'DELETE' THEN
    SELECT array_agg(DISTINCT tenant_id ORDER BY tenant_id) INTO affected_tenants FROM changed_rows_old;
  ELSE
    SELECT array_agg(tenant_id ORDER BY tenant_id) INTO affected_tenants FROM (
      SELECT tenant_id FROM changed_rows_old
      UNION
      SELECT tenant_id FROM changed_rows_new
    ) AS changed_tenants;
  END IF;
  IF affected_tenants IS NULL THEN
    RETURN NULL;
  END IF;
  FOREACH affected_tenant IN ARRAY affected_tenants LOOP
    IF affected_tenant = '00000000-0000-0000-0000-000000000001'::UUID THEN
      CONTINUE;
    END IF;
    -- RLS 下先进入被刷新的租户作用域，再锁投影并读取源数据。
    PERFORM set_config('edugrade.tenant_id', affected_tenant::text, true);
    PERFORM 1 FROM platform_school_metrics WHERE tenant_id = affected_tenant FOR UPDATE;
    IF NOT FOUND THEN
      -- 首次建校时没有投影行可锁。锁租户非键列，与源记录的外键检查兼容。
      PERFORM 1 FROM tenant WHERE id = affected_tenant FOR NO KEY UPDATE;
      -- 等待期间另一事务可能已创建投影，先拿到该行锁再重新聚合。
      PERFORM 1 FROM platform_school_metrics WHERE tenant_id = affected_tenant FOR UPDATE;
    END IF;
    PERFORM refresh_platform_school_metrics(affected_tenant);
  END LOOP;
  PERFORM set_config('edugrade.tenant_id', COALESCE(previous_scope, ''), true);
  RETURN NULL;
END;
$$;

REVOKE ALL ON FUNCTION refresh_platform_school_metrics_trigger() FROM PUBLIC, edugrade_tenant_runtime;

-- 汇总是可重建的派生数据。删除当前学校时先清除旧汇总，随后语句触发器会选择同租户的其余学校。
-- 保留延迟到语句末的检查会让父表外键先拒绝删除，汇总刷新便无法执行。
ALTER TABLE platform_school_metrics DROP CONSTRAINT platform_school_metrics_tenant_id_school_id_fkey;
ALTER TABLE platform_school_metrics ADD CONSTRAINT platform_school_metrics_tenant_id_school_id_fkey
  FOREIGN KEY (tenant_id, school_id) REFERENCES school (tenant_id, id) ON DELETE CASCADE;

-- PostgreSQL 的转换表按事件提供；混合 UPSERT 的 INSERT 和 UPDATE 可以分别刷新一次。
-- 仅替换学校汇总触发器，模型角色及用量投影继续由原有触发器维护。
DO $$
DECLARE
  source RECORD;
BEGIN
  FOR source IN
    SELECT * FROM (VALUES
      ('school', 'school'),
      ('app_user', 'user'),
      ('user_role', 'user_role'),
      ('role', 'role'),
      ('managed_model_api_config', 'model_config'),
      ('student', 'student'),
      ('school_class', 'class'),
      ('exam', 'exam'),
      ('activity_event', 'activity'),
      ('auth_session', 'session')
    ) AS sources(table_name, trigger_suffix)
  LOOP
    EXECUTE format('DROP TRIGGER %I ON %I', 'trg_platform_metrics_' || source.trigger_suffix, source.table_name);
    EXECUTE format(
      'CREATE TRIGGER %I AFTER INSERT ON %I REFERENCING NEW TABLE AS changed_rows_new
       FOR EACH STATEMENT EXECUTE FUNCTION refresh_platform_school_metrics_trigger()',
      'trg_platform_metrics_' || source.trigger_suffix || '_insert', source.table_name
    );
    EXECUTE format(
      'CREATE TRIGGER %I AFTER UPDATE ON %I REFERENCING OLD TABLE AS changed_rows_old NEW TABLE AS changed_rows_new
       FOR EACH STATEMENT EXECUTE FUNCTION refresh_platform_school_metrics_trigger()',
      'trg_platform_metrics_' || source.trigger_suffix || '_update', source.table_name
    );
    EXECUTE format(
      'CREATE TRIGGER %I AFTER DELETE ON %I REFERENCING OLD TABLE AS changed_rows_old
       FOR EACH STATEMENT EXECUTE FUNCTION refresh_platform_school_metrics_trigger()',
      'trg_platform_metrics_' || source.trigger_suffix || '_delete', source.table_name
    );
  END LOOP;
END;
$$;
