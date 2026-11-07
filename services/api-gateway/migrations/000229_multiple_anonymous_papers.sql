-- 同一发布可分享多份匿名优秀答卷，每份都可能包含第1页；不能再用发布ID和页码唯一约束限制为单份。
-- 原有来源页唯一约束和匿名资产归属约束继续防止重复登记、跨发布引用，不修改历史匿名资源。
ALTER TABLE score_release_anonymous_page
  DROP CONSTRAINT score_release_anonymous_page_tenant_id_release_id_page_no_key;
