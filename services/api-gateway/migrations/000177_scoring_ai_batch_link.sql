ALTER TABLE subjective_grading_batch
  ADD COLUMN IF NOT EXISTS scoring_run_id UUID REFERENCES scoring_run(id);

CREATE INDEX IF NOT EXISTS idx_subjective_grading_batch_scoring_run
  ON subjective_grading_batch (tenant_id, scoring_run_id)
  WHERE scoring_run_id IS NOT NULL AND deleted_at IS NULL;

CREATE OR REPLACE FUNCTION guard_scoring_ai_batch() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_status text;
BEGIN
  IF NEW.scoring_run_id IS NULL THEN RETURN NEW; END IF;
  SELECT status INTO current_status FROM scoring_run
    WHERE tenant_id=NEW.tenant_id AND id=NEW.scoring_run_id AND deleted_at IS NULL FOR SHARE;
  IF current_status IS NULL OR current_status NOT IN ('queued','processing','needs_review','failed') THEN
    RAISE EXCEPTION 'scoring run is not active' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS trg_guard_scoring_ai_batch ON subjective_grading_batch;
CREATE TRIGGER trg_guard_scoring_ai_batch BEFORE INSERT OR UPDATE OF scoring_run_id
  ON subjective_grading_batch FOR EACH ROW EXECUTE FUNCTION guard_scoring_ai_batch();

CREATE OR REPLACE FUNCTION guard_scoring_ai_task() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_status text;
BEGIN
  IF NEW.source_type <> 'subjective_grading_run' THEN RETURN NEW; END IF;
  SELECT sr.status INTO current_status
  FROM subjective_grading_run ai_run
  JOIN subjective_grading_batch batch ON batch.tenant_id=ai_run.tenant_id AND batch.id=ai_run.batch_id
  JOIN scoring_run sr ON sr.tenant_id=batch.tenant_id AND sr.id=batch.scoring_run_id
  WHERE ai_run.tenant_id=NEW.tenant_id AND ai_run.id=NEW.source_id AND batch.scoring_run_id IS NOT NULL
  FOR SHARE OF sr;
  IF current_status IS NOT NULL AND current_status NOT IN ('queued','processing','needs_review','failed') THEN
    RAISE EXCEPTION 'scoring run is not active' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS trg_guard_scoring_ai_task ON agent_worker_task;
CREATE TRIGGER trg_guard_scoring_ai_task BEFORE INSERT ON agent_worker_task
  FOR EACH ROW EXECUTE FUNCTION guard_scoring_ai_task();
