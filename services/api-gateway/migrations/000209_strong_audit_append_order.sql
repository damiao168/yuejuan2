-- 新增明确追加顺序，不回写已有审计或重新计算旧摘要。历史分叉只保留证据，不能据此宣称历史链已恢复。
-- 迁移在调用方事务中运行；表锁阻止旧触发器在新列、索引和函数切换期间追加。
LOCK TABLE audit_log IN SHARE ROW EXCLUSIVE MODE;

ALTER TABLE audit_log ADD COLUMN append_sequence BIGINT CHECK (append_sequence > 0);
CREATE SEQUENCE audit_log_append_sequence_seq AS BIGINT;
ALTER SEQUENCE audit_log_append_sequence_seq OWNED BY audit_log.append_sequence;

CREATE UNIQUE INDEX uq_audit_log_append_sequence
  ON audit_log(tenant_id, append_sequence) WHERE append_sequence IS NOT NULL;
CREATE INDEX idx_audit_log_previous_hash
  ON audit_log(tenant_id, previous_hash) WHERE record_hash IS NOT NULL;

CREATE OR REPLACE FUNCTION audit_log_chain_before_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  prior_hash TEXT;
  canonical TEXT;
BEGIN
  -- 先按租户串行，再分配序号；提前 nextval 会让等待锁的事务落到较小序号，重新误选链尾。
  PERFORM pg_advisory_xact_lock(hashtextextended(NEW.tenant_id::text, 0));
  NEW.append_sequence := nextval('audit_log_append_sequence_seq');

  SELECT record_hash INTO prior_hash
  FROM audit_log
  WHERE tenant_id = NEW.tenant_id AND append_sequence IS NOT NULL
  ORDER BY append_sequence DESC
  LIMIT 1;

  IF prior_hash IS NULL THEN
    -- 首次升级后的写入只接一条确定的历史末端；有多个末端时不合并、不重写既有分叉。
    SELECT history.record_hash INTO prior_hash
    FROM audit_log history
    WHERE history.tenant_id = NEW.tenant_id AND history.record_hash IS NOT NULL
      AND NOT EXISTS (
        SELECT 1 FROM audit_log successor
        WHERE successor.tenant_id = history.tenant_id AND successor.previous_hash = history.record_hash
          AND successor.record_hash IS NOT NULL
      )
    ORDER BY history.created_at DESC, history.id DESC
    LIMIT 1;
    IF prior_hash IS NULL AND EXISTS (
      SELECT 1 FROM audit_log WHERE tenant_id = NEW.tenant_id AND record_hash IS NOT NULL
    ) THEN
      RAISE EXCEPTION 'audit chain has no historical terminal for tenant %', NEW.tenant_id;
    END IF;
  END IF;

  -- 链尾只从实际插入的记录读取；回滚或 ON CONFLICT 跳过可留下序号空洞，不能留下虚假的链头。
  NEW.previous_hash := COALESCE(prior_hash, repeat('0', 64));
  canonical := concat_ws('|',
    NEW.previous_hash,
    NEW.tenant_id::text,
    COALESCE(NEW.actor_id::text, ''),
    NEW.action,
    NEW.target_type,
    COALESCE(NEW.target_id::text, ''),
    COALESCE(NEW.before_value::text, ''),
    COALESCE(NEW.after_value::text, ''),
    COALESCE(NEW.reason, ''),
    COALESCE(NEW.request_id, ''),
    NEW.created_at::text,
    NEW.chain_version::text
  );
  NEW.record_hash := encode(digest(convert_to(canonical, 'UTF8'), 'sha256'), 'hex');
  RETURN NEW;
END
$$;

COMMENT ON COLUMN audit_log.append_sequence IS
'租户串行锁内分配的追加顺序，仅覆盖迁移后的记录；历史 NULL 不重写。回滚或跳过插入会产生合法空洞，摘要公式保持不变。';
