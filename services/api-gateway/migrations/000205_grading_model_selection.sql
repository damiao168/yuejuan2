-- 自动阅卷模型由平台按学校独立指定；存量学校保持未配置，不从聊天、答题卡或本地模型推断。
CREATE TABLE tenant_grading_model_selection (
  tenant_id UUID PRIMARY KEY REFERENCES tenant(id),
  mode TEXT NOT NULL CHECK (mode IN ('unconfigured','model')),
  managed_model_api_config_id UUID,
  revision UUID NOT NULL DEFAULT gen_random_uuid(),
  updated_by UUID NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((mode='unconfigured' AND managed_model_api_config_id IS NULL) OR (mode='model' AND managed_model_api_config_id IS NOT NULL)),
  FOREIGN KEY (tenant_id,managed_model_api_config_id) REFERENCES managed_model_api_config(tenant_id,id)
);
ALTER TABLE tenant_grading_model_selection ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_grading_model_selection FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON tenant_grading_model_selection
  FOR ALL USING (edugrade_tenant_matches(tenant_id)) WITH CHECK (edugrade_tenant_matches(tenant_id));
GRANT SELECT,INSERT,UPDATE,DELETE ON tenant_grading_model_selection TO edugrade_tenant_runtime;

