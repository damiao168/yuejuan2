-- 空设置延续历史行为：试卷解析跟随聊天默认模型。独立选择另存，不改变聊天或评分角色。
-- 复合外键限定学校归属；应用使用 revision 拒绝旧页面覆盖，并与模型软删除串行检查引用。
CREATE TABLE tenant_document_model_selection (
  tenant_id UUID PRIMARY KEY REFERENCES tenant(id),
  mode TEXT NOT NULL CHECK (mode IN ('follow_chat', 'model')),
  managed_model_api_config_id UUID,
  revision UUID NOT NULL DEFAULT gen_random_uuid(),
  updated_by UUID NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((mode='follow_chat' AND managed_model_api_config_id IS NULL) OR (mode='model' AND managed_model_api_config_id IS NOT NULL)),
  FOREIGN KEY (tenant_id, managed_model_api_config_id) REFERENCES managed_model_api_config(tenant_id, id)
);
ALTER TABLE tenant_document_model_selection ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_document_model_selection FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON tenant_document_model_selection
  FOR ALL USING (edugrade_tenant_matches(tenant_id)) WITH CHECK (edugrade_tenant_matches(tenant_id));
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_document_model_selection TO edugrade_tenant_runtime;
