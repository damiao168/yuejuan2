-- 名册在准备确认后由快照代表；任何绕过应用入口的 exam_class 变更也必须失败。
CREATE OR REPLACE FUNCTION reject_exam_candidate_roster_mutation()
RETURNS TRIGGER AS $$
DECLARE
  target_status TEXT;
BEGIN
  -- 与准备确认和状态转换共用考试行锁，避免检查阶段后并发提交班级变更。
  IF TG_OP IN ('UPDATE', 'DELETE') THEN
    SELECT e.status INTO target_status
    FROM exam e
    WHERE e.tenant_id = OLD.tenant_id AND e.id = OLD.exam_id AND e.deleted_at IS NULL
    FOR UPDATE;
    IF target_status IS NOT NULL AND target_status NOT IN ('draft', 'configured') THEN
      RAISE EXCEPTION USING ERRCODE = '55000', MESSAGE = 'exam candidate roster is frozen';
    END IF;
  END IF;

  IF TG_OP IN ('INSERT', 'UPDATE') THEN
    SELECT e.status INTO target_status
    FROM exam e
    WHERE e.tenant_id = NEW.tenant_id AND e.id = NEW.exam_id AND e.deleted_at IS NULL
    FOR UPDATE;
    IF target_status IS NOT NULL AND target_status NOT IN ('draft', 'configured') THEN
      RAISE EXCEPTION USING ERRCODE = '55000', MESSAGE = 'exam candidate roster is frozen';
    END IF;
  END IF;

  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_exam_candidate_roster_freeze ON exam_class;
CREATE TRIGGER trg_exam_candidate_roster_freeze
BEFORE INSERT OR UPDATE OR DELETE ON exam_class
FOR EACH ROW EXECUTE FUNCTION reject_exam_candidate_roster_mutation();
