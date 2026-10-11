-- 每个存量角色取得独立修订；新建默认修订供受控导入使用。
-- 应用保存必须显式比较 expected_revision，并在成功后更换修订，避免旧页面覆盖并发更新。
ALTER TABLE model_role_binding
  ADD COLUMN revision UUID NOT NULL DEFAULT gen_random_uuid();
