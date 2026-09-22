-- Bind an approved stage/subject/archetype policy to the exact A/B/C model
-- and prompt versions used by its completed Shadow evaluation. Historical
-- policies remain readable but have NULL provenance and fail the runtime gate.
ALTER TABLE subjective_panel_policy
  ADD COLUMN model_set_reference TEXT;

ALTER TABLE subjective_panel_policy
  ADD CONSTRAINT chk_subjective_panel_model_set_reference
  CHECK (model_set_reference IS NULL OR model_set_reference ~ '^panel:[0-9a-f]{64}$') NOT VALID;

CREATE FUNCTION subjective_panel_model_set_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE evaluated_model_set TEXT;
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.model_set_reference IS NULL OR NEW.model_set_reference !~ '^panel:[0-9a-f]{64}$' THEN
      RAISE EXCEPTION 'new panel policies require a model set reference' USING ERRCODE='23514';
    END IF;
  ELSIF NEW.model_set_reference IS DISTINCT FROM OLD.model_set_reference THEN
    RAISE EXCEPTION 'panel model set provenance is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.status = 'approved' THEN
    SELECT model_reference INTO evaluated_model_set
      FROM grading_evaluation_run
      WHERE tenant_id=NEW.tenant_id AND id=NEW.evaluation_run_id;
    IF NEW.model_set_reference IS NULL OR evaluated_model_set IS DISTINCT FROM NEW.model_set_reference THEN
      RAISE EXCEPTION 'panel policy model set differs from evaluation' USING ERRCODE='23514';
    END IF;
  END IF;
  RETURN NEW;
END $$;

CREATE TRIGGER trg_subjective_panel_model_set_guard
BEFORE INSERT OR UPDATE ON subjective_panel_policy
FOR EACH ROW EXECUTE FUNCTION subjective_panel_model_set_guard();
