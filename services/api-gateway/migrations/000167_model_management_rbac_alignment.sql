-- Separate platform model configuration and panel binding writes from
-- historical provider/deployment governance. Tenant administrators retain
-- model read, policy and evaluation rights, but cannot manage school API keys.
INSERT INTO permission (tenant_id, code, name, resource, action, description)
SELECT tenant.id, permission.code, permission.name, permission.resource, permission.action, permission.description
FROM tenant
CROSS JOIN (VALUES
  ('model:managed_api:manage', 'Manage school model APIs', 'managed_model_api_config', 'manage', '管理学校模型 API 配置和密钥'),
  ('model:panel:manage', 'Manage panel model bindings', 'model_role_binding', 'manage', '管理评分智能体模型绑定')
) AS permission(code, name, resource, action, description)
WHERE tenant.deleted_at IS NULL
ON CONFLICT (tenant_id, code) DO UPDATE
SET name = EXCLUDED.name,
    resource = EXCLUDED.resource,
    action = EXCLUDED.action,
    description = EXCLUDED.description,
    deleted_at = NULL,
    updated_at = now();

INSERT INTO role_permission (tenant_id, role_id, permission_id)
SELECT role.tenant_id, role.id, permission.id
FROM role
JOIN permission ON permission.tenant_id = role.tenant_id
WHERE role.code = 'platform_admin'
  AND permission.code IN ('model:managed_api:manage', 'model:panel:manage')
  AND role.deleted_at IS NULL
  AND permission.deleted_at IS NULL
ON CONFLICT (tenant_id, role_id, permission_id) DO UPDATE
SET deleted_at = NULL, updated_at = now();
