-- 将升级前的默认模型固化为聊天开放范围；以后清除默认或停启配置，不应撤销这份开放记录。
-- 已保存过开放范围的学校保留其选择，包括显式空列表；缺失历史操作者时使用零 UUID 标记迁移。
INSERT INTO tenant_chat_model_access (tenant_id,model_ids,revision,updated_by)
SELECT tenant_id,jsonb_build_array(id),gen_random_uuid(),COALESCE(created_by,'00000000-0000-0000-0000-000000000000'::uuid)
FROM managed_model_api_config
WHERE is_default AND deleted_at IS NULL
ON CONFLICT (tenant_id) DO NOTHING;
