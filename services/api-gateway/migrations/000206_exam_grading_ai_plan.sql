-- 学校管理员在阅卷前确认本场模型和分差规则；启动运行另外冻结公开身份，不保存接口地址或凭据。
CREATE TABLE exam_grading_ai_plan (
  tenant_id UUID NOT NULL REFERENCES tenant(id),
  exam_id UUID NOT NULL,
  revision UUID NOT NULL DEFAULT gen_random_uuid(),
  configuration JSONB NOT NULL DEFAULT '{}'::jsonb,
  confirmed_by UUID NOT NULL REFERENCES app_user(id),
  confirmed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, exam_id),
  FOREIGN KEY (tenant_id, exam_id) REFERENCES exam(tenant_id, id),
  CHECK (jsonb_typeof(configuration) = 'object')
);
ALTER TABLE exam_grading_ai_plan ENABLE ROW LEVEL SECURITY;
ALTER TABLE exam_grading_ai_plan FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON exam_grading_ai_plan
  FOR ALL USING (edugrade_tenant_matches(tenant_id)) WITH CHECK (edugrade_tenant_matches(tenant_id));
GRANT SELECT,INSERT,UPDATE,DELETE ON exam_grading_ai_plan TO edugrade_tenant_runtime;

-- 新增考试子表继续遵守既有归档只读边界，不能通过方案设置改写归档考试。
INSERT INTO exam_archive_guarded_table VALUES ('exam_grading_ai_plan'::regclass);
CREATE TRIGGER trg_exam_archive_readonly BEFORE INSERT OR UPDATE OR DELETE ON exam_grading_ai_plan
  FOR EACH ROW EXECUTE FUNCTION reject_archived_exam_business_mutation();
