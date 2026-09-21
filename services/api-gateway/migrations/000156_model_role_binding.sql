-- Governed model assignments for rubric-scoring panel roles. Bindings are
-- stage/subject/archetype scoped; the arbiter must be configured with a higher
-- evaluated strength rank than both blind primaries.

CREATE TABLE model_role_binding (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL,
  education_stage TEXT NOT NULL,
  subject_code TEXT NOT NULL,
  archetype_code TEXT NOT NULL DEFAULT '*',
  agent_role TEXT NOT NULL,
  managed_model_api_config_id UUID NOT NULL,
  prompt_version TEXT NOT NULL,
  strength_rank INT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  created_by UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, education_stage, subject_code, archetype_code, agent_role),
  FOREIGN KEY (tenant_id, managed_model_api_config_id) REFERENCES managed_model_api_config(tenant_id, id),
  FOREIGN KEY (tenant_id, created_by) REFERENCES app_user(tenant_id, id),
  CHECK (education_stage IN ('junior','senior')),
  CHECK (subject_code IN ('chinese','mathematics','english','physics','chemistry','biology','history','geography','ethics_politics')),
  CHECK (agent_role IN ('primary_a','primary_b','arbiter')),
  CHECK (btrim(archetype_code) <> '' AND btrim(prompt_version) <> ''),
  CHECK (strength_rank > 0),
  CHECK (status IN ('active','disabled'))
);

CREATE INDEX idx_model_role_binding_resolution
  ON model_role_binding(tenant_id,education_stage,subject_code,archetype_code,agent_role,status);

ALTER TABLE model_role_binding ENABLE ROW LEVEL SECURITY;
ALTER TABLE model_role_binding FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON model_role_binding
  FOR ALL USING (edugrade_tenant_matches(tenant_id))
  WITH CHECK (edugrade_tenant_matches(tenant_id));

COMMENT ON TABLE model_role_binding IS
'Governed A/B/C model role assignment. Runtime resolution additionally requires an active, capability-tested managed model configuration.';
