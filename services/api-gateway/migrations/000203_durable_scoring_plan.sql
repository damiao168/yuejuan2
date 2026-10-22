-- 只为新运行建立持久计划；旧运行保持可读，不能在迁移时猜测旧输入或复活历史规则。
-- 先应用本迁移，再应用图像主观评分的输入版本扩展；回滚应用时保留这些来源事实。
CREATE TABLE answer_input_version (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL,
  answer_segment_id UUID NOT NULL,
  submission_id UUID NOT NULL,
  crop_file_asset_id UUID NOT NULL,
  crop_sha256 TEXT NOT NULL,
  registration_run_id UUID,
  template_content_hash TEXT NOT NULL,
  answer_scope TEXT NOT NULL,
  members_json JSONB NOT NULL,
  input_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id,id),
  UNIQUE (tenant_id,input_hash),
  FOREIGN KEY (tenant_id,answer_segment_id) REFERENCES answer_segment(tenant_id,id),
  FOREIGN KEY (tenant_id,submission_id) REFERENCES submission(tenant_id,id),
  FOREIGN KEY (tenant_id,crop_file_asset_id) REFERENCES file_asset(tenant_id,id),
  CHECK (answer_scope IN ('question','grading_group')),
  CHECK (jsonb_typeof(members_json)='array' AND jsonb_array_length(members_json)>0),
  CHECK (length(input_hash)>=64 AND length(crop_sha256)>=16)
);

CREATE TABLE scoring_run_item (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL,
  scoring_run_id UUID NOT NULL,
  answer_segment_id UUID NOT NULL,
  question_id UUID NOT NULL,
  plan_generation INT NOT NULL DEFAULT 1,
  input_version_id UUID NOT NULL,
  assessment_snapshot_id UUID NOT NULL,
  frozen_rule_id UUID,
  route TEXT NOT NULL,
  stage TEXT NOT NULL DEFAULT 'prepare',
  processing_status TEXT NOT NULL DEFAULT 'waiting_dependency',
  decision_status TEXT NOT NULL DEFAULT 'undecided',
  runtime_task_id UUID,
  observation_task_id UUID,
  subjective_run_id UUID,
  artifact_ids JSONB NOT NULL DEFAULT '[]',
  observation_json JSONB,
  reason_code TEXT NOT NULL DEFAULT '',
  next_action TEXT NOT NULL DEFAULT 'wait',
  recognition_completed BOOLEAN NOT NULL DEFAULT false,
  model_invoked BOOLEAN,
  retryable BOOLEAN NOT NULL DEFAULT false,
  revision BIGINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id,id),
  UNIQUE (tenant_id,scoring_run_id,answer_segment_id,plan_generation),
  FOREIGN KEY (tenant_id,scoring_run_id) REFERENCES scoring_run(tenant_id,id),
  FOREIGN KEY (tenant_id,question_id) REFERENCES question(tenant_id,id),
  FOREIGN KEY (tenant_id,answer_segment_id) REFERENCES answer_segment(tenant_id,id),
  FOREIGN KEY (tenant_id,input_version_id) REFERENCES answer_input_version(tenant_id,id),
  FOREIGN KEY (tenant_id,assessment_snapshot_id) REFERENCES exam_question_snapshot(tenant_id,id),
  FOREIGN KEY (tenant_id,runtime_task_id) REFERENCES agent_worker_task(tenant_id,id),
  FOREIGN KEY (tenant_id,observation_task_id) REFERENCES agent_worker_task(tenant_id,id),
  FOREIGN KEY (tenant_id,frozen_rule_id) REFERENCES scoring_rule(tenant_id,id),
  CHECK (stage IN ('prepare','observe','verify','score','done')),
  CHECK (plan_generation>0 AND revision>0),
  CHECK (jsonb_typeof(artifact_ids)='array'),
  CHECK (observation_json IS NULL OR jsonb_typeof(observation_json)='object'),
  CHECK (processing_status IN ('waiting_dependency','queued','running','retry_wait','succeeded','blocked','failed','superseded','cancelled')),
  CHECK (decision_status IN ('undecided','auto_confirmed','suggestion_ready','review_needed','human_confirmed','manual_only'))
);
CREATE INDEX idx_scoring_run_item_resume ON scoring_run_item(tenant_id,scoring_run_id,processing_status);
ALTER TABLE answer_candidate ADD COLUMN input_version_id UUID,
  ADD COLUMN presence TEXT,
  ADD COLUMN association_status TEXT;
ALTER TABLE answer_candidate ADD FOREIGN KEY (tenant_id,input_version_id) REFERENCES answer_input_version(tenant_id,id);
ALTER TABLE answer_candidate ADD CHECK (presence IS NULL OR presence IN ('present','blank','uncertain','unreadable')),
  ADD CHECK (association_status IS NULL OR association_status IN ('matched','unresolved'));
CREATE UNIQUE INDEX uq_scoring_observation_candidate ON answer_candidate(tenant_id,scoring_run_id,answer_segment_id,input_version_id)
  WHERE input_version_id IS NOT NULL AND deleted_at IS NULL;

CREATE FUNCTION immutable_answer_input_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'answer_input_version is immutable';
END;
$$;
CREATE TRIGGER answer_input_version_immutable BEFORE UPDATE OR DELETE ON answer_input_version
  FOR EACH ROW EXECUTE FUNCTION immutable_answer_input_version();

ALTER TABLE answer_input_version ENABLE ROW LEVEL SECURITY;
ALTER TABLE answer_input_version FORCE ROW LEVEL SECURITY;
CREATE POLICY answer_input_version_tenant ON answer_input_version
  FOR ALL USING (edugrade_tenant_matches(tenant_id)) WITH CHECK (edugrade_tenant_matches(tenant_id));
ALTER TABLE scoring_run_item ENABLE ROW LEVEL SECURITY;
ALTER TABLE scoring_run_item FORCE ROW LEVEL SECURITY;
CREATE POLICY scoring_run_item_tenant ON scoring_run_item
  FOR ALL USING (edugrade_tenant_matches(tenant_id)) WITH CHECK (edugrade_tenant_matches(tenant_id));
GRANT SELECT,INSERT,UPDATE,DELETE ON answer_input_version,scoring_run_item TO edugrade_tenant_runtime;
