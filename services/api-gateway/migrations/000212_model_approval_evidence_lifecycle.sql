-- 来源作废与依赖批准撤销必须同事务提交，保留来源和批准的历史事实。
-- 创建批准先共享锁住来源，和来源作废互斥，避免作废事务漏掉并发插入。
CREATE OR REPLACE FUNCTION lock_live_model_approval_source()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source_status TEXT;
BEGIN
  SELECT status INTO source_status FROM model_evaluation_run
  WHERE tenant_id=NEW.tenant_id AND id=NEW.evaluation_run_id FOR SHARE;
  IF source_status IS DISTINCT FROM 'completed' THEN
    RAISE EXCEPTION 'model approval source is not completed' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER model_approval_live_source
BEFORE INSERT ON model_approval
FOR EACH ROW EXECUTE FUNCTION lock_live_model_approval_source();

CREATE OR REPLACE FUNCTION revoke_invalidated_evaluation_approvals()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  UPDATE model_approval
  SET revoked_at=NEW.invalidated_at, revoked_by=NEW.invalidated_by,
      revocation_reason='evaluation_invalidated: ' || NEW.invalidation_reason,
      revision=revision+1
  WHERE tenant_id=NEW.tenant_id AND evaluation_run_id=NEW.id AND revoked_at IS NULL;
  RETURN NEW;
END;
$$;

CREATE TRIGGER model_evaluation_invalidate_approvals
AFTER UPDATE OF status ON model_evaluation_run
FOR EACH ROW WHEN (NEW.status='invalidated' AND OLD.status IS DISTINCT FROM 'invalidated')
EXECUTE FUNCTION revoke_invalidated_evaluation_approvals();

-- 旧缺陷留下的有效批准也需撤销；不重写已经撤销的时间和说明。
UPDATE model_approval approval
SET revoked_at=evaluation.invalidated_at, revoked_by=evaluation.invalidated_by,
    revocation_reason='evaluation_invalidated: ' || evaluation.invalidation_reason,
    revision=approval.revision+1
FROM model_evaluation_run evaluation
WHERE evaluation.tenant_id=approval.tenant_id AND evaluation.id=approval.evaluation_run_id
  AND evaluation.status='invalidated' AND approval.revoked_at IS NULL;
