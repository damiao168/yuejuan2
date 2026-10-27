-- 分组确认先锁成员片段，再回读最新答案；所有答案写入必须遵守同一片段锁，
-- 防止核验完成后、确认提交前又出现新版本。历史答案不改写。
CREATE OR REPLACE FUNCTION lock_answer_segment_answer_source()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  old_tenant UUID;
  old_segment UUID;
  new_tenant UUID;
  new_segment UUID;
BEGIN
  IF TG_OP <> 'INSERT' THEN
    old_tenant := OLD.tenant_id;
    old_segment := OLD.answer_segment_id;
  END IF;
  IF TG_OP <> 'DELETE' THEN
    new_tenant := NEW.tenant_id;
    new_segment := NEW.answer_segment_id;
  END IF;
  -- 身份迁移同时锁前后归属，按稳定顺序取锁；普通追加只涉及一个片段。
  PERFORM segment.id FROM answer_segment segment
  WHERE (segment.tenant_id, segment.id) IN ((old_tenant, old_segment), (new_tenant, new_segment))
  ORDER BY segment.tenant_id, segment.id
  FOR UPDATE;
  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS answer_segment_answer_source_lock ON answer_segment_answer;
CREATE TRIGGER answer_segment_answer_source_lock
BEFORE INSERT OR UPDATE OR DELETE ON answer_segment_answer
FOR EACH ROW EXECUTE FUNCTION lock_answer_segment_answer_source();
