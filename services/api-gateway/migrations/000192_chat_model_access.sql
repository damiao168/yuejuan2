-- 聊天开放范围独立保存；无记录学校沿用原默认模型，不自动开放其他供应商。
-- 每次发送仍核对同校配置、启用状态和普通回复证据，JSON 中的 ID 不授予密钥访问权限。
CREATE TABLE tenant_chat_model_access (
 tenant_id UUID PRIMARY KEY REFERENCES tenant(id),
 model_ids JSONB NOT NULL CHECK (jsonb_typeof(model_ids)='array'),
 revision UUID NOT NULL DEFAULT gen_random_uuid(),
 updated_by UUID NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE tenant_chat_model_access ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_chat_model_access FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON tenant_chat_model_access
 FOR ALL USING (edugrade_tenant_matches(tenant_id)) WITH CHECK (edugrade_tenant_matches(tenant_id));
GRANT SELECT,INSERT,UPDATE,DELETE ON tenant_chat_model_access TO edugrade_tenant_runtime;
