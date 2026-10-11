-- 结构确认只属于当前识别代次；追加或替换资料后旧确认自动失效。
-- 存量待核对导入保持未确认，历史已应用导入沿用原流程，不批量改写题目。
ALTER TABLE paper_import_job ADD COLUMN structure_confirmed_generation BIGINT;
ALTER TABLE paper_import_job ADD COLUMN structure_confirmed_at TIMESTAMPTZ;
ALTER TABLE paper_import_job ADD COLUMN structure_confirmed_by UUID REFERENCES app_user(id);
COMMENT ON COLUMN paper_import_job.structure_confirmed_generation IS '管理员确认完整试卷并采用题目结构的识别代次；小题分值和评分细则仍需通过就绪检查';

-- 只有与考试同时创建且存在预设分区的题目标记为预设分值，其他存量人工内容不作推断。
ALTER TABLE question ADD COLUMN score_source TEXT NOT NULL DEFAULT 'human' CHECK(score_source IN ('human','preset','material','missing'));
UPDATE question q SET score_source='preset' FROM exam e WHERE q.tenant_id=e.tenant_id AND q.exam_id=e.id AND q.created_at=e.created_at AND EXISTS(SELECT 1 FROM exam_blueprint_section b WHERE b.tenant_id=e.tenant_id AND b.exam_id=e.id AND b.deleted_at IS NULL);
COMMENT ON COLUMN question.score_source IS 'human 人工分值；preset 预设方案；material 试卷资料；missing 待确认，不允许进入正式阅卷';
