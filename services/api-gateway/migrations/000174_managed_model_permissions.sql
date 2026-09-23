-- New model administration permissions coexist with the old grants during
-- the compatibility window. New schools copy the platform permission catalog.
INSERT INTO permission (tenant_id, code, name, resource, action, description)
SELECT tenant.id, item.code, item.name, item.resource, item.action, item.description
FROM tenant
CROSS JOIN (VALUES
  ('model:config:manage', 'Manage school models', 'managed_model_api_config', 'manage', '管理学校模型配置与评分模型绑定'),
  ('model:governance:read', 'Read model governance', 'managed_model_governance', 'read', '查看学校模型评测、批准和使用策略')
) AS item(code, name, resource, action, description)
WHERE tenant.deleted_at IS NULL
ON CONFLICT (tenant_id, code) DO UPDATE
SET name=EXCLUDED.name, resource=EXCLUDED.resource, action=EXCLUDED.action,
    description=EXCLUDED.description, deleted_at=NULL, updated_at=now();

INSERT INTO role_permission (tenant_id, role_id, permission_id)
SELECT role.tenant_id, role.id, permission.id
FROM role JOIN permission ON permission.tenant_id=role.tenant_id
WHERE role.code='platform_admin' AND role.deleted_at IS NULL
  AND permission.deleted_at IS NULL
  AND permission.code IN ('model:config:manage', 'model:governance:read')
ON CONFLICT (tenant_id, role_id, permission_id) DO UPDATE
SET deleted_at=NULL, updated_at=now();
