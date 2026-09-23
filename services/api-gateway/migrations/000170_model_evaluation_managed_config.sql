-- New evaluation candidates reference a school model directly. Historical
-- deployment candidates remain readable and immutable.
ALTER TABLE model_evaluation_candidate
  ALTER COLUMN deployment_id DROP NOT NULL,
  ALTER COLUMN deployment_key DROP NOT NULL,
  ADD CONSTRAINT chk_model_evaluation_candidate_source
    CHECK (managed_model_api_config_id IS NOT NULL OR deployment_id IS NOT NULL);

CREATE UNIQUE INDEX uq_model_evaluation_candidate_managed_config
  ON model_evaluation_candidate (tenant_id, run_id, managed_model_api_config_id)
  WHERE deployment_id IS NULL;

CREATE OR REPLACE FUNCTION enforce_model_evaluation_candidate_scope()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  evaluation model_evaluation_run%ROWTYPE;
  config managed_model_api_config%ROWTYPE;
  deployment_modalities JSONB;
  governed_provider_key TEXT;
  governed_deployment_key TEXT;
  governed_model_version TEXT;
BEGIN
  SELECT * INTO evaluation FROM model_evaluation_run
  WHERE tenant_id = NEW.tenant_id AND id = NEW.run_id;
  IF evaluation.id IS NULL OR evaluation.status <> 'draft'
     OR NEW.evaluated_samples <> evaluation.sample_count
     OR NEW.repeat_comparisons > evaluation.sample_count * (evaluation.repeat_count - 1)
     OR (evaluation.repeat_count = 1
         AND (NEW.repeat_comparisons <> 0 OR NEW.stable_repeat_samples <> 0))
     OR (evaluation.repeat_count > 1 AND NEW.repeat_comparisons = 0)
     OR (evaluation.evidence_class = 'authorized_frozen_set'
         AND NEW.teacher_reviewed_samples <> NEW.evaluated_samples)
     OR (evaluation.evidence_class = 'protocol_fixture'
         AND (NEW.teacher_reviewed_samples <> 0 OR NEW.teacher_accepted_samples <> 0))
  THEN
    RAISE EXCEPTION 'invalid model evaluation candidate scope' USING ERRCODE = '23514';
  END IF;

  IF NEW.deployment_id IS NULL THEN
    SELECT * INTO config FROM managed_model_api_config
    WHERE tenant_id = NEW.tenant_id AND id = NEW.managed_model_api_config_id
      AND deleted_at IS NULL;
    IF config.id IS NULL OR config.status <> 'active'
       OR config.last_capability_status <> 'success'
       OR config.last_capability_probe_version <> 'structured-json-v3'
       OR NOT (config.modalities ? evaluation.modality)
       OR NEW.deployment_key IS NOT NULL
       OR NEW.provider_key <> config.provider_key
       OR NEW.model_name <> config.model_name
       OR NEW.model_version <> config.model_version
    THEN
      RAISE EXCEPTION 'invalid managed model evaluation candidate' USING ERRCODE = '23514';
    END IF;
  ELSE
    SELECT provider.provider_key, deployment.deployment_key,
           deployment.model_version, deployment.modalities
    INTO governed_provider_key, governed_deployment_key,
         governed_model_version, deployment_modalities
    FROM model_deployment deployment
    JOIN model_provider provider
      ON provider.tenant_id = deployment.tenant_id
     AND provider.id = deployment.provider_id
     AND provider.deleted_at IS NULL
    WHERE deployment.tenant_id = NEW.tenant_id
      AND deployment.id = NEW.deployment_id
      AND deployment.deleted_at IS NULL;
    IF NOT FOUND OR NEW.provider_key <> governed_provider_key
       OR NEW.deployment_key <> governed_deployment_key
       OR NEW.model_version <> governed_model_version
       OR NOT (deployment_modalities ? evaluation.modality)
    THEN
      RAISE EXCEPTION 'invalid legacy model evaluation candidate' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enforce_model_evaluation_completion()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  candidate_count INT;
  managed_count INT;
  local_candidate_count INT;
BEGIN
  IF NEW.run_key IS DISTINCT FROM OLD.run_key
     OR NEW.display_name IS DISTINCT FROM OLD.display_name
     OR NEW.dataset_reference IS DISTINCT FROM OLD.dataset_reference
     OR NEW.dataset_sha256 IS DISTINCT FROM OLD.dataset_sha256
     OR NEW.authorization_reference IS DISTINCT FROM OLD.authorization_reference
     OR NEW.evidence_class IS DISTINCT FROM OLD.evidence_class
     OR NEW.subject IS DISTINCT FROM OLD.subject
     OR NEW.grade IS DISTINCT FROM OLD.grade
     OR NEW.question_type IS DISTINCT FROM OLD.question_type
     OR NEW.modality IS DISTINCT FROM OLD.modality
     OR NEW.sample_count IS DISTINCT FROM OLD.sample_count
     OR NEW.repeat_count IS DISTINCT FROM OLD.repeat_count
     OR NEW.created_by IS DISTINCT FROM OLD.created_by
     OR NEW.created_at IS DISTINCT FROM OLD.created_at
     OR NEW.status = OLD.status
     OR OLD.status = 'invalidated'
     OR (OLD.status = 'completed' AND NEW.status NOT IN ('completed', 'invalidated'))
     OR (OLD.status = 'draft' AND NEW.status NOT IN ('completed', 'invalidated'))
     OR (OLD.status = 'completed'
         AND (NEW.completed_at IS DISTINCT FROM OLD.completed_at
              OR NEW.completed_by IS DISTINCT FROM OLD.completed_by))
  THEN
    RAISE EXCEPTION 'invalid model evaluation status transition' USING ERRCODE = '23514';
  END IF;

  IF NEW.status = 'completed' AND OLD.status = 'draft' THEN
    SELECT count(*)::int,
           count(*) FILTER (WHERE candidate.deployment_id IS NULL)::int,
           count(*) FILTER (WHERE provider.provider_kind = 'local')::int
    INTO candidate_count, managed_count, local_candidate_count
    FROM model_evaluation_candidate candidate
    LEFT JOIN model_deployment deployment
      ON deployment.tenant_id = candidate.tenant_id AND deployment.id = candidate.deployment_id
    LEFT JOIN model_provider provider
      ON provider.tenant_id = deployment.tenant_id AND provider.id = deployment.provider_id
    WHERE candidate.tenant_id = NEW.tenant_id AND candidate.run_id = NEW.id;
    IF candidate_count < 2
       OR (managed_count > 0 AND managed_count <> candidate_count)
       OR (managed_count = 0 AND local_candidate_count < 1)
    THEN
      RAISE EXCEPTION 'model evaluation completion requires comparable candidates'
        USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;
