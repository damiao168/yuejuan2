-- An early deployed 000155 panel schema predates the final guard set. Preserve
-- its recorded checksum and add the missing protections in a forward migration.
-- On databases created from the final 000155 this migration is idempotent.

CREATE UNIQUE INDEX IF NOT EXISTS uq_subjective_grading_panel_answer_version
  ON subjective_grading_panel (tenant_id, answer_segment_id, answer_version, rubric_version)
  WHERE deleted_at IS NULL;
DROP INDEX IF EXISTS uq_subjective_grading_panel_active_segment;

CREATE OR REPLACE FUNCTION subjective_panel_run_binding_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE parent subjective_grading_panel%ROWTYPE;
BEGIN
  IF TG_OP='UPDATE' AND OLD.panel_id IS NOT NULL AND (
    NEW.panel_id IS DISTINCT FROM OLD.panel_id OR NEW.agent_role IS DISTINCT FROM OLD.agent_role
    OR NEW.answer_segment_id IS DISTINCT FROM OLD.answer_segment_id
    OR NEW.answer_version IS DISTINCT FROM OLD.answer_version
    OR NEW.question_id IS DISTINCT FROM OLD.question_id
    OR NEW.rubric_version IS DISTINCT FROM OLD.rubric_version
  ) THEN
    RAISE EXCEPTION 'subjective panel run binding is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.panel_id IS NULL THEN
    RETURN NEW;
  END IF;
  SELECT * INTO parent FROM subjective_grading_panel
  WHERE tenant_id=NEW.tenant_id AND id=NEW.panel_id AND deleted_at IS NULL;
  IF NOT FOUND OR parent.answer_segment_id <> NEW.answer_segment_id
    OR parent.answer_version <> NEW.answer_version OR parent.question_id <> NEW.question_id
    OR parent.rubric_version <> NEW.rubric_version THEN
    RAISE EXCEPTION 'subjective panel run snapshot mismatch' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION subjective_panel_role_link_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.primary_a_run_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM subjective_grading_run r
    WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.primary_a_run_id
      AND r.panel_id=NEW.id AND r.agent_role='primary_a' AND r.deleted_at IS NULL
  ) THEN
    RAISE EXCEPTION 'invalid primary_a panel run' USING ERRCODE='23514';
  END IF;
  IF NEW.primary_b_run_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM subjective_grading_run r
    WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.primary_b_run_id
      AND r.panel_id=NEW.id AND r.agent_role='primary_b' AND r.deleted_at IS NULL
  ) THEN
    RAISE EXCEPTION 'invalid primary_b panel run' USING ERRCODE='23514';
  END IF;
  IF NEW.arbiter_run_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM subjective_grading_run r
    WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.arbiter_run_id
      AND r.panel_id=NEW.id AND r.agent_role='arbiter' AND r.deleted_at IS NULL
  ) THEN
    RAISE EXCEPTION 'invalid arbiter panel run' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION subjective_panel_status_transition_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status=OLD.status THEN
    RETURN NEW;
  END IF;
  IF (OLD.status='primary_pending' AND NEW.status IN ('comparing','human_review','failed'))
    OR (OLD.status='comparing' AND NEW.status IN ('arbitration_pending','resolved','human_review','failed'))
    OR (OLD.status='arbitration_pending' AND NEW.status IN ('resolved','human_review','failed'))
    OR (OLD.status='human_review' AND NEW.status='resolved') THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'invalid subjective panel status transition' USING ERRCODE='23514';
END $$;

CREATE OR REPLACE FUNCTION subjective_panel_completed_roles_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status IN ('comparing','arbitration_pending')
    OR (NEW.status='resolved' AND NEW.resolution_source IN ('primary_consensus','arbiter')) THEN
    IF NEW.primary_a_run_id IS NULL OR NEW.primary_b_run_id IS NULL
      OR NOT EXISTS (
        SELECT 1 FROM subjective_grading_run r
        WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.primary_a_run_id AND r.panel_id=NEW.id
          AND r.agent_role='primary_a' AND r.status='succeeded' AND r.grade_id IS NOT NULL AND r.deleted_at IS NULL
      ) OR NOT EXISTS (
        SELECT 1 FROM subjective_grading_run r
        WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.primary_b_run_id AND r.panel_id=NEW.id
          AND r.agent_role='primary_b' AND r.status='succeeded' AND r.grade_id IS NOT NULL AND r.deleted_at IS NULL
      ) THEN
      RAISE EXCEPTION 'subjective panel primary roles are incomplete' USING ERRCODE='23514';
    END IF;
  END IF;
  IF NEW.status='resolved' AND NEW.resolution_source='arbiter' AND (
    NEW.arbiter_run_id IS NULL OR NOT EXISTS (
      SELECT 1 FROM subjective_grading_run r
      WHERE r.tenant_id=NEW.tenant_id AND r.id=NEW.arbiter_run_id AND r.panel_id=NEW.id
        AND r.agent_role='arbiter' AND r.status='succeeded' AND r.grade_id IS NOT NULL AND r.deleted_at IS NULL
    )
  ) THEN
    RAISE EXCEPTION 'subjective panel arbiter role is incomplete' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='subjective_grading_run'::regclass
    AND tgname='trg_subjective_panel_run_binding_guard' AND NOT tgisinternal) THEN
    CREATE TRIGGER trg_subjective_panel_run_binding_guard
      BEFORE INSERT OR UPDATE OF panel_id,agent_role,answer_segment_id,answer_version,question_id,rubric_version
      ON subjective_grading_run FOR EACH ROW EXECUTE FUNCTION subjective_panel_run_binding_guard();
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='subjective_grading_panel'::regclass
    AND tgname='trg_subjective_panel_role_link_guard' AND NOT tgisinternal) THEN
    CREATE TRIGGER trg_subjective_panel_role_link_guard
      BEFORE INSERT OR UPDATE OF primary_a_run_id,primary_b_run_id,arbiter_run_id
      ON subjective_grading_panel FOR EACH ROW EXECUTE FUNCTION subjective_panel_role_link_guard();
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='subjective_grading_panel'::regclass
    AND tgname='trg_subjective_panel_status_transition_guard' AND NOT tgisinternal) THEN
    CREATE TRIGGER trg_subjective_panel_status_transition_guard
      BEFORE UPDATE OF status ON subjective_grading_panel
      FOR EACH ROW EXECUTE FUNCTION subjective_panel_status_transition_guard();
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='subjective_grading_panel'::regclass
    AND tgname='trg_subjective_panel_completed_roles_guard' AND NOT tgisinternal) THEN
    CREATE TRIGGER trg_subjective_panel_completed_roles_guard
      BEFORE INSERT OR UPDATE OF status,resolution_source,primary_a_run_id,primary_b_run_id,arbiter_run_id
      ON subjective_grading_panel FOR EACH ROW EXECUTE FUNCTION subjective_panel_completed_roles_guard();
  END IF;
END $$;

ALTER TABLE review_task ADD COLUMN IF NOT EXISTS subjective_panel_id UUID;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='review_task'::regclass
    AND conname='fk_review_task_subjective_panel') THEN
    ALTER TABLE review_task ADD CONSTRAINT fk_review_task_subjective_panel
      FOREIGN KEY (tenant_id, subjective_panel_id) REFERENCES subjective_grading_panel(tenant_id, id);
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='review_task'::regclass
    AND conname='chk_review_task_subjective_panel_source') THEN
    ALTER TABLE review_task ADD CONSTRAINT chk_review_task_subjective_panel_source
      CHECK ((source='ai_panel_disagreement')=(subjective_panel_id IS NOT NULL));
  END IF;
END $$;

DROP INDEX IF EXISTS uq_review_task_active_source;
CREATE UNIQUE INDEX uq_review_task_active_source
  ON review_task(tenant_id,answer_segment_id,source,grade_round)
  WHERE status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL
    AND source <> 'ai_panel_disagreement';
CREATE UNIQUE INDEX IF NOT EXISTS uq_review_task_active_subjective_panel
  ON review_task(tenant_id,subjective_panel_id)
  WHERE subjective_panel_id IS NOT NULL
    AND status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL;
