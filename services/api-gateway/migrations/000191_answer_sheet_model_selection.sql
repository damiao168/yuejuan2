-- 答题卡模型选择独立于聊天和评分；存量学校保持未配置，由平台管理员按学校指定。
CREATE TABLE tenant_answer_sheet_model_selection (
  tenant_id UUID PRIMARY KEY REFERENCES tenant(id),
  mode TEXT NOT NULL CHECK (mode IN ('unconfigured','model')),
  managed_model_api_config_id UUID,
  revision UUID NOT NULL DEFAULT gen_random_uuid(),
  updated_by UUID NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((mode='unconfigured' AND managed_model_api_config_id IS NULL) OR (mode='model' AND managed_model_api_config_id IS NOT NULL)),
  FOREIGN KEY (tenant_id,managed_model_api_config_id) REFERENCES managed_model_api_config(tenant_id,id)
);
ALTER TABLE tenant_answer_sheet_model_selection ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_answer_sheet_model_selection FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON tenant_answer_sheet_model_selection
  FOR ALL USING (edugrade_tenant_matches(tenant_id)) WITH CHECK (edugrade_tenant_matches(tenant_id));
GRANT SELECT,INSERT,UPDATE,DELETE ON tenant_answer_sheet_model_selection TO edugrade_tenant_runtime;

