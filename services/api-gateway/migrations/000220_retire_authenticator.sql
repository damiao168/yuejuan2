-- 身份验证器退役：先移除学校汇总函数的凭据表依赖，再清理密钥、恢复码和挑战。
-- 已有登录会话、密码、用户和脱敏审计/发件箱事实不受影响；旧版本实例应先停止再执行本迁移。
DROP TRIGGER IF EXISTS trg_platform_metrics_totp ON auth_totp;

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
  -- 触发器只读取受影响租户的源数据；请求处理器不能获得维护作用域。
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
    active_session_count, last_login_at,
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

ALTER TABLE platform_school_metrics DROP COLUMN mfa_enabled_admin_count;
DROP TABLE auth_mfa_challenge;
DROP TABLE auth_mfa_recovery_code;
DROP TABLE auth_totp;
-- 此表只有身份验证器待发送通知；保留 event_outbox 与 audit_log 中已写入的历史事实。
DROP TABLE auth_security_notification_intent;
