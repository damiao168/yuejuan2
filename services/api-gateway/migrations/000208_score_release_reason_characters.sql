-- 发布、回滚和重评说明统一按 Unicode 字符计数，和服务端的1000字符上限一致。
-- 不截断或改写已发布历史；若存在绕过服务写入的超长历史，迁移拒绝并由迁移事务回滚。
ALTER TABLE score_release
  DROP CONSTRAINT score_release_reason_check,
  ADD CONSTRAINT score_release_reason_check
    CHECK (length(btrim(reason)) > 0 AND char_length(reason) <= 1000);
