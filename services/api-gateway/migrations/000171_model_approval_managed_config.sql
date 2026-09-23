-- Approval evidence uses the same immutable model identity as its candidate.
-- Old deployment approvals remain available for history and revocation.
ALTER TABLE model_approval
  ALTER COLUMN deployment_id DROP NOT NULL,
  ALTER COLUMN deployment_key DROP NOT NULL,
  ADD CONSTRAINT chk_model_approval_source
    CHECK (managed_model_api_config_id IS NOT NULL OR deployment_id IS NOT NULL);

CREATE INDEX idx_model_approval_active_managed_scope
  ON model_approval (
    tenant_id, managed_model_api_config_id, subject, grade, question_type,
    modality, model_version, prompt_version, rubric_version, expires_at DESC
  )
  WHERE revoked_at IS NULL AND managed_model_api_config_id IS NOT NULL;

CREATE OR REPLACE FUNCTION enforce_model_approval_evidence()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  evaluation model_evaluation_run%ROWTYPE;
  candidate model_evaluation_candidate%ROWTYPE;
BEGIN
  SELECT * INTO evaluation FROM model_evaluation_run
  WHERE tenant_id = NEW.tenant_id AND id = NEW.evaluation_run_id;
  SELECT * INTO candidate FROM model_evaluation_candidate
  WHERE tenant_id = NEW.tenant_id AND id = NEW.evaluation_candidate_id
    AND run_id = NEW.evaluation_run_id;

  IF evaluation.id IS NULL OR candidate.id IS NULL
     OR evaluation.status <> 'completed'
     OR evaluation.evidence_class <> 'authorized_frozen_set'
     OR candidate.deployment_id IS DISTINCT FROM NEW.deployment_id
     OR candidate.managed_model_api_config_id IS DISTINCT FROM NEW.managed_model_api_config_id
     OR NEW.provider_key <> candidate.provider_key
     OR NEW.deployment_key IS DISTINCT FROM candidate.deployment_key
     OR NEW.model_name <> candidate.model_name
     OR NEW.model_version <> candidate.model_version
     OR NEW.prompt_version <> candidate.prompt_version
     OR NEW.rubric_version <> candidate.rubric_version
     OR NEW.dataset_reference <> evaluation.dataset_reference
     OR NEW.dataset_sha256 <> evaluation.dataset_sha256
     OR NEW.authorization_reference <> evaluation.authorization_reference
     OR NEW.subject <> evaluation.subject
     OR NEW.grade <> evaluation.grade
     OR NEW.question_type <> evaluation.question_type
     OR NEW.modality <> evaluation.modality
  THEN
    RAISE EXCEPTION 'model approval must exactly snapshot authorized evaluation evidence'
      USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enforce_model_approval_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
     OR NEW.evaluation_run_id IS DISTINCT FROM OLD.evaluation_run_id
     OR NEW.evaluation_candidate_id IS DISTINCT FROM OLD.evaluation_candidate_id
     OR NEW.deployment_id IS DISTINCT FROM OLD.deployment_id
     OR NEW.managed_model_api_config_id IS DISTINCT FROM OLD.managed_model_api_config_id
     OR NEW.provider_key IS DISTINCT FROM OLD.provider_key
     OR NEW.deployment_key IS DISTINCT FROM OLD.deployment_key
     OR NEW.model_name IS DISTINCT FROM OLD.model_name
     OR NEW.model_version IS DISTINCT FROM OLD.model_version
     OR NEW.prompt_version IS DISTINCT FROM OLD.prompt_version
     OR NEW.rubric_version IS DISTINCT FROM OLD.rubric_version
     OR NEW.dataset_reference IS DISTINCT FROM OLD.dataset_reference
     OR NEW.dataset_sha256 IS DISTINCT FROM OLD.dataset_sha256
     OR NEW.authorization_reference IS DISTINCT FROM OLD.authorization_reference
     OR NEW.subject IS DISTINCT FROM OLD.subject
     OR NEW.grade IS DISTINCT FROM OLD.grade
     OR NEW.question_type IS DISTINCT FROM OLD.question_type
     OR NEW.modality IS DISTINCT FROM OLD.modality
     OR NEW.manual_review_rate IS DISTINCT FROM OLD.manual_review_rate
     OR NEW.decision_reference IS DISTINCT FROM OLD.decision_reference
     OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
     OR NEW.created_by IS DISTINCT FROM OLD.created_by
     OR NEW.created_at IS DISTINCT FROM OLD.created_at
     OR OLD.revoked_at IS NOT NULL
     OR NEW.revoked_at IS NULL
     OR btrim(NEW.revocation_reason) = ''
  THEN
    RAISE EXCEPTION 'model approval evidence is immutable' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;
