-- De-identified, immutable offline observations for A/B/C panel quality.
-- Student answers, images, reviewer identities, and Gold content are excluded.

CREATE TABLE grading_panel_evaluation_observation (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL,
  run_id UUID NOT NULL,
  response_key TEXT NOT NULL,
  response_fingerprint TEXT NOT NULL,
  subject_code TEXT NOT NULL,
  archetype_code TEXT NOT NULL,
  reference_score NUMERIC(8,2) NOT NULL,
  max_score NUMERIC(8,2) NOT NULL,
  score_a NUMERIC(8,2) NOT NULL,
  score_b NUMERIC(8,2) NOT NULL,
  score_c NUMERIC(8,2),
  resolved_score NUMERIC(8,2),
  arbitration_triggered BOOLEAN NOT NULL,
  resolution_source TEXT NOT NULL,
  human_escalated BOOLEAN NOT NULL,
  reference_kind TEXT NOT NULL,
  reference_reviewer_count INT NOT NULL,
  reference_adjudicated BOOLEAN NOT NULL,
  primary_a_cost_micros BIGINT NOT NULL DEFAULT 0,
  primary_b_cost_micros BIGINT NOT NULL DEFAULT 0,
  arbiter_cost_micros BIGINT NOT NULL DEFAULT 0,
  observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, run_id, response_key),
  FOREIGN KEY (tenant_id, run_id) REFERENCES grading_evaluation_run(tenant_id, id),
  CHECK (response_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
  CHECK (response_fingerprint ~ '^[a-f0-9]{64}$'),
  CHECK (subject_code IN ('chinese','mathematics','english','physics','chemistry','biology','history','geography','ethics_politics')),
  CHECK (btrim(archetype_code) <> ''),
  CHECK (max_score > 0 AND reference_score BETWEEN 0 AND max_score
    AND score_a BETWEEN 0 AND max_score AND score_b BETWEEN 0 AND max_score
    AND (score_c IS NULL OR score_c BETWEEN 0 AND max_score)
    AND (resolved_score IS NULL OR resolved_score BETWEEN 0 AND max_score)),
  CHECK (resolution_source IN ('primary_consensus','arbiter','human')),
  CHECK (reference_kind = 'human_adjudicated' AND reference_reviewer_count >= 2 AND reference_adjudicated),
  CHECK (primary_a_cost_micros >= 0 AND primary_b_cost_micros >= 0 AND arbiter_cost_micros >= 0),
  CHECK (resolution_source <> 'arbiter' OR (score_c IS NOT NULL AND arbitration_triggered)),
  CHECK ((score_c IS NULL) OR arbitration_triggered),
  CHECK ((resolution_source = 'human') = human_escalated),
  CHECK (resolution_source <> 'human' OR resolved_score IS NULL),
  CHECK (resolution_source <> 'primary_consensus' OR
    (NOT arbitration_triggered AND score_a=score_b AND resolved_score IS NOT NULL AND resolved_score=score_a)),
  CHECK (resolution_source <> 'arbiter' OR (resolved_score IS NOT NULL AND resolved_score=score_c))
);

CREATE INDEX idx_grading_panel_evaluation_run
  ON grading_panel_evaluation_observation(tenant_id,run_id,observed_at,id);

CREATE OR REPLACE FUNCTION reject_grading_panel_evaluation_mutation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'grading panel evaluation observations are immutable' USING ERRCODE='23514';
END;
$$;

CREATE TRIGGER trg_grading_panel_evaluation_immutable
BEFORE UPDATE OR DELETE ON grading_panel_evaluation_observation
FOR EACH ROW EXECUTE FUNCTION reject_grading_panel_evaluation_mutation();

ALTER TABLE grading_panel_evaluation_observation ENABLE ROW LEVEL SECURITY;
ALTER TABLE grading_panel_evaluation_observation FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON grading_panel_evaluation_observation
  FOR ALL USING (edugrade_tenant_matches(tenant_id))
  WITH CHECK (edugrade_tenant_matches(tenant_id));
