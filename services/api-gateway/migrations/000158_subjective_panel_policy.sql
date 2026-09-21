-- Versioned, evaluation-backed thresholds for subject/archetype panel routing.
-- A policy remains shadow-only until a completed adjudicated evaluation passes
-- its own frozen readiness gates. Approval does not publish student scores.

-- Historical observations cannot be assigned a stage safely. They stay NULL
-- and are therefore ineligible for policy approval; all new writes require a
-- canonical stage in the application contract.
ALTER TABLE grading_panel_evaluation_observation
  ADD COLUMN education_stage TEXT;
ALTER TABLE grading_panel_evaluation_observation
  ADD CONSTRAINT ck_grading_panel_evaluation_stage
  CHECK (education_stage IS NOT NULL AND education_stage IN ('junior','senior')) NOT VALID;
CREATE INDEX idx_grading_panel_evaluation_scope
  ON grading_panel_evaluation_observation(tenant_id,run_id,education_stage,subject_code,archetype_code);

CREATE TABLE subjective_panel_policy (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL,
  policy_version TEXT NOT NULL,
  education_stage TEXT NOT NULL,
  subject_code TEXT NOT NULL,
  archetype_code TEXT NOT NULL,
  decision_config JSONB NOT NULL,
  readiness_policy JSONB NOT NULL,
  evaluation_run_id UUID,
  readiness_report JSONB NOT NULL DEFAULT '{}'::jsonb,
  status TEXT NOT NULL DEFAULT 'shadow',
  created_by UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  approved_by UUID,
  approved_at TIMESTAMPTZ,
  invalidated_by UUID,
  invalidated_at TIMESTAMPTZ,
  invalidation_reason TEXT NOT NULL DEFAULT '',
  UNIQUE(tenant_id,id),
  UNIQUE(tenant_id,education_stage,subject_code,archetype_code,policy_version),
  FOREIGN KEY(tenant_id,evaluation_run_id) REFERENCES grading_evaluation_run(tenant_id,id),
  FOREIGN KEY(tenant_id,created_by) REFERENCES app_user(tenant_id,id),
  FOREIGN KEY(tenant_id,approved_by) REFERENCES app_user(tenant_id,id),
  FOREIGN KEY(tenant_id,invalidated_by) REFERENCES app_user(tenant_id,id),
  CHECK (policy_version ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
  CHECK (education_stage IN ('junior','senior')),
  CHECK (subject_code IN ('chinese','mathematics','english','physics','chemistry','biology','history','geography','ethics_politics')),
  CHECK (btrim(archetype_code) <> '' AND char_length(archetype_code) <= 128),
  CHECK (jsonb_typeof(decision_config)='object' AND jsonb_typeof(readiness_policy)='object' AND jsonb_typeof(readiness_report)='object'),
  CHECK (status IN ('shadow','approved','invalidated')),
  CHECK (
    (status='shadow' AND evaluation_run_id IS NULL AND readiness_report='{}'::jsonb
      AND approved_by IS NULL AND approved_at IS NULL AND invalidated_by IS NULL AND invalidated_at IS NULL AND invalidation_reason='')
    OR (status='approved' AND evaluation_run_id IS NOT NULL AND readiness_report<>'{}'::jsonb
      AND readiness_report->>'policy_version'=policy_version
      AND readiness_report->>'education_stage'=education_stage
      AND readiness_report->>'subject'=subject_code
      AND readiness_report->>'archetype'=archetype_code
      AND approved_by IS NOT NULL AND approved_at IS NOT NULL AND invalidated_by IS NULL AND invalidated_at IS NULL AND invalidation_reason='')
    OR (status='invalidated' AND invalidated_by IS NOT NULL AND invalidated_at IS NOT NULL AND btrim(invalidation_reason)<>'')
  )
);

CREATE UNIQUE INDEX uq_subjective_panel_policy_approved_scope
  ON subjective_panel_policy(tenant_id,education_stage,subject_code,archetype_code)
  WHERE status='approved';
CREATE INDEX idx_subjective_panel_policy_history
  ON subjective_panel_policy(tenant_id,education_stage,subject_code,archetype_code,created_at DESC,id);

CREATE FUNCTION subjective_panel_policy_transition_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE evaluation_status TEXT;
BEGIN
  IF NEW.policy_version IS DISTINCT FROM OLD.policy_version
    OR NEW.education_stage IS DISTINCT FROM OLD.education_stage
    OR NEW.subject_code IS DISTINCT FROM OLD.subject_code
    OR NEW.archetype_code IS DISTINCT FROM OLD.archetype_code
    OR NEW.decision_config IS DISTINCT FROM OLD.decision_config
    OR NEW.readiness_policy IS DISTINCT FROM OLD.readiness_policy
    OR NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'subjective panel policy provenance is immutable' USING ERRCODE='23514';
  END IF;
  IF OLD.status='shadow' AND NEW.status NOT IN ('approved','invalidated') THEN
    RAISE EXCEPTION 'shadow panel policy can only be approved or invalidated' USING ERRCODE='23514';
  ELSIF OLD.status='approved' AND NEW.status<>'invalidated' THEN
    RAISE EXCEPTION 'approved panel policy can only be invalidated' USING ERRCODE='23514';
  ELSIF OLD.status='invalidated' THEN
    RAISE EXCEPTION 'invalidated panel policy is immutable' USING ERRCODE='23514';
  END IF;
  IF NEW.status='approved' THEN
    SELECT status INTO evaluation_status FROM grading_evaluation_run
      WHERE tenant_id=NEW.tenant_id AND id=NEW.evaluation_run_id;
    IF NOT FOUND OR evaluation_status<>'completed'
      OR jsonb_typeof(NEW.readiness_report->'ready')<>'boolean'
      OR NEW.readiness_report->'ready'<>'true'::jsonb THEN
      RAISE EXCEPTION 'panel policy approval requires completed ready evaluation evidence' USING ERRCODE='23514';
    END IF;
  END IF;
  RETURN NEW;
END $$;

CREATE TRIGGER trg_subjective_panel_policy_transition
BEFORE UPDATE ON subjective_panel_policy
FOR EACH ROW EXECUTE FUNCTION subjective_panel_policy_transition_guard();

CREATE FUNCTION grading_evaluation_approved_policy_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status='completed' AND NEW.status='invalidated' AND EXISTS (
    SELECT 1 FROM subjective_panel_policy
    WHERE tenant_id=OLD.tenant_id AND evaluation_run_id=OLD.id AND status='approved'
  ) THEN
    RAISE EXCEPTION 'invalidate approved panel policy before its evaluation run' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;

CREATE TRIGGER trg_grading_evaluation_approved_policy_guard
BEFORE UPDATE OF status ON grading_evaluation_run
FOR EACH ROW EXECUTE FUNCTION grading_evaluation_approved_policy_guard();

ALTER TABLE subjective_panel_policy ENABLE ROW LEVEL SECURITY;
ALTER TABLE subjective_panel_policy FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON subjective_panel_policy
  FOR ALL USING (edugrade_tenant_matches(tenant_id))
  WITH CHECK (edugrade_tenant_matches(tenant_id));

COMMENT ON TABLE subjective_panel_policy IS
'Versioned per-stage/subject/archetype panel thresholds. Approved status requires a completed, human-adjudicated shadow evaluation report; approval never grants model score authority.';
