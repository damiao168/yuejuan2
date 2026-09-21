-- Rubric-driven subjective scoring panel. Each model execution remains one
-- subjective_grading_run; the panel records orchestration and resolution only.

ALTER TABLE subjective_grading_run
  ADD CONSTRAINT uq_subjective_grading_run_tenant_id UNIQUE (tenant_id, id);

CREATE TABLE subjective_grading_panel (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenant(id),
  answer_segment_id UUID NOT NULL,
  answer_version TEXT NOT NULL,
  question_id UUID NOT NULL,
  rubric_version TEXT NOT NULL,
  policy_version TEXT NOT NULL,
  primary_a_run_id UUID,
  primary_b_run_id UUID,
  arbiter_run_id UUID,
  score_a NUMERIC(8,2),
  score_b NUMERIC(8,2),
  score_c NUMERIC(8,2),
  max_score NUMERIC(8,2) NOT NULL,
  score_gap NUMERIC(7,6),
  criterion_gap NUMERIC(7,6),
  required_point_conflict BOOLEAN NOT NULL DEFAULT FALSE,
  evidence_conflict BOOLEAN NOT NULL DEFAULT FALSE,
  confidence_conflict BOOLEAN NOT NULL DEFAULT FALSE,
  trigger_codes JSONB NOT NULL DEFAULT '[]'::jsonb,
  decision_config JSONB NOT NULL,
  status TEXT NOT NULL DEFAULT 'primary_pending',
  resolved_score NUMERIC(8,2),
  resolution_source TEXT,
  review_task_id UUID,
  created_by UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  deleted_at TIMESTAMPTZ,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, answer_segment_id) REFERENCES answer_segment(tenant_id, id),
  FOREIGN KEY (tenant_id, question_id) REFERENCES question(tenant_id, id),
  FOREIGN KEY (tenant_id, review_task_id) REFERENCES review_task(tenant_id, id),
  FOREIGN KEY (tenant_id, created_by) REFERENCES app_user(tenant_id, id),
  CHECK (btrim(answer_version) <> '' AND btrim(rubric_version) <> '' AND btrim(policy_version) <> ''),
  CHECK (max_score > 0),
  CHECK (score_a IS NULL OR (score_a >= 0 AND score_a <= max_score)),
  CHECK (score_b IS NULL OR (score_b >= 0 AND score_b <= max_score)),
  CHECK (score_c IS NULL OR (score_c >= 0 AND score_c <= max_score)),
  CHECK (resolved_score IS NULL OR (resolved_score >= 0 AND resolved_score <= max_score)),
  CHECK (score_gap IS NULL OR (score_gap >= 0 AND score_gap <= 1)),
  CHECK (criterion_gap IS NULL OR (criterion_gap >= 0 AND criterion_gap <= 1)),
  CHECK (jsonb_typeof(trigger_codes) = 'array'),
  CHECK (jsonb_typeof(decision_config) = 'object'),
  CHECK (status IN ('primary_pending','comparing','arbitration_pending','resolved','human_review','failed')),
  CHECK (resolution_source IS NULL OR resolution_source IN ('primary_consensus','arbiter','human')),
  CHECK ((status = 'resolved' AND resolved_score IS NOT NULL AND resolution_source IN ('primary_consensus','arbiter','human') AND completed_at IS NOT NULL)
    OR (status <> 'resolved' AND NOT (resolution_source IN ('primary_consensus','arbiter')))),
  CHECK (status <> 'human_review' OR resolution_source IS NULL OR resolution_source = 'human')
);

ALTER TABLE subjective_grading_run
  ADD COLUMN panel_id UUID,
  ADD COLUMN agent_role TEXT NOT NULL DEFAULT 'single',
  ADD CONSTRAINT chk_subjective_grading_run_agent_role
    CHECK (agent_role IN ('single','primary_a','primary_b','arbiter')),
  ADD CONSTRAINT chk_subjective_grading_run_panel_role
    CHECK ((panel_id IS NULL AND agent_role = 'single') OR (panel_id IS NOT NULL AND agent_role <> 'single')),
  ADD CONSTRAINT fk_subjective_grading_run_panel_tenant
    FOREIGN KEY (tenant_id, panel_id) REFERENCES subjective_grading_panel(tenant_id, id);

ALTER TABLE subjective_grading_panel
  ADD CONSTRAINT fk_subjective_panel_primary_a_run
    FOREIGN KEY (tenant_id, primary_a_run_id) REFERENCES subjective_grading_run(tenant_id, id),
  ADD CONSTRAINT fk_subjective_panel_primary_b_run
    FOREIGN KEY (tenant_id, primary_b_run_id) REFERENCES subjective_grading_run(tenant_id, id),
  ADD CONSTRAINT fk_subjective_panel_arbiter_run
    FOREIGN KEY (tenant_id, arbiter_run_id) REFERENCES subjective_grading_run(tenant_id, id);

CREATE UNIQUE INDEX uq_subjective_grading_run_panel_role
  ON subjective_grading_run (tenant_id, panel_id, agent_role)
  WHERE panel_id IS NOT NULL AND deleted_at IS NULL;

-- One frozen answer/rubric version has one panel across its whole lifecycle.
-- Retrying a completed shadow panel must not launch another set of model runs.
CREATE UNIQUE INDEX uq_subjective_grading_panel_answer_version
  ON subjective_grading_panel (tenant_id, answer_segment_id, answer_version, rubric_version)
  WHERE deleted_at IS NULL;

CREATE FUNCTION subjective_panel_run_binding_guard() RETURNS trigger
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

CREATE TRIGGER trg_subjective_panel_run_binding_guard
BEFORE INSERT OR UPDATE OF panel_id,agent_role,answer_segment_id,answer_version,question_id,rubric_version
ON subjective_grading_run FOR EACH ROW EXECUTE FUNCTION subjective_panel_run_binding_guard();

CREATE FUNCTION subjective_panel_role_link_guard() RETURNS trigger
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

CREATE TRIGGER trg_subjective_panel_role_link_guard
BEFORE INSERT OR UPDATE OF primary_a_run_id,primary_b_run_id,arbiter_run_id
ON subjective_grading_panel FOR EACH ROW EXECUTE FUNCTION subjective_panel_role_link_guard();

CREATE FUNCTION subjective_panel_status_transition_guard() RETURNS trigger
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

CREATE TRIGGER trg_subjective_panel_status_transition_guard
BEFORE UPDATE OF status ON subjective_grading_panel
FOR EACH ROW EXECUTE FUNCTION subjective_panel_status_transition_guard();

CREATE FUNCTION subjective_panel_completed_roles_guard() RETURNS trigger
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

CREATE TRIGGER trg_subjective_panel_completed_roles_guard
BEFORE INSERT OR UPDATE OF status,resolution_source,primary_a_run_id,primary_b_run_id,arbiter_run_id
ON subjective_grading_panel FOR EACH ROW EXECUTE FUNCTION subjective_panel_completed_roles_guard();

CREATE INDEX idx_subjective_grading_panel_queue
  ON subjective_grading_panel (tenant_id, status, created_at, id)
  WHERE deleted_at IS NULL;

ALTER TABLE review_task
  ADD COLUMN subjective_panel_id UUID,
  ADD CONSTRAINT fk_review_task_subjective_panel
    FOREIGN KEY (tenant_id, subjective_panel_id) REFERENCES subjective_grading_panel(tenant_id, id),
  ADD CONSTRAINT chk_review_task_subjective_panel_source
    CHECK ((source='ai_panel_disagreement')=(subjective_panel_id IS NOT NULL));

-- The historical source-level key is retained for every non-panel source.
-- Panel reviews are keyed by the frozen panel so a newer answer version never
-- reuses an active task created for an older answer version.
DROP INDEX uq_review_task_active_source;
CREATE UNIQUE INDEX uq_review_task_active_source
  ON review_task(tenant_id,answer_segment_id,source,grade_round)
  WHERE status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL
    AND source <> 'ai_panel_disagreement';
CREATE UNIQUE INDEX uq_review_task_active_subjective_panel
  ON review_task(tenant_id,subjective_panel_id)
  WHERE subjective_panel_id IS NOT NULL
    AND status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL;

ALTER TABLE review_task DROP CONSTRAINT review_task_source_check;
ALTER TABLE review_task ADD CONSTRAINT review_task_source_check CHECK (source IN (
  'ai_low_confidence', 'ocr_low_confidence', 'subjective_default_review',
  'evidence_verification_failed', 'double_mark_required', 'score_anomaly',
  'manual_sample', 'omr_ambiguous', 'rule_review_required', 'grading_failure',
  'answer_group_outlier', 'ai_human_disagreement', 'ai_panel_disagreement'
));

ALTER TABLE subjective_grading_panel ENABLE ROW LEVEL SECURITY;
ALTER TABLE subjective_grading_panel FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON subjective_grading_panel
  FOR ALL USING (edugrade_tenant_matches(tenant_id))
  WITH CHECK (edugrade_tenant_matches(tenant_id));

COMMENT ON TABLE subjective_grading_panel IS
'Governed A/B blind-primary and blind-arbiter orchestration. Scores are deterministic projections of frozen rubric classifications, never model-authored final grades.';
COMMENT ON COLUMN subjective_grading_panel.decision_config IS
'Frozen subject/archetype thresholds used for this panel decision; later policy changes cannot rewrite the audit trail.';
COMMENT ON COLUMN subjective_grading_run.agent_role IS
'single for legacy runs; panel runs are primary_a, primary_b, or arbiter. Primary and arbiter inputs never contain peer scores.';
