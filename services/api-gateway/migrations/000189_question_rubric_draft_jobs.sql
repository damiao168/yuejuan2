-- 建议与正式细则分开存储；只保存输入、建议与模型摘要，不保存受管密钥。
CREATE TABLE question_rubric_draft_job (
  id UUID PRIMARY KEY,
  tenant_id UUID NOT NULL REFERENCES tenant(id),
  actor_id UUID NOT NULL,
  exam_id UUID NOT NULL REFERENCES exam(id),
  question_id UUID NOT NULL REFERENCES question(id),
  request_id UUID NOT NULL,
  fingerprint TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('queued','running','cancelling','cancelled','succeeded','failed','stale')),
  local_model BOOLEAN NOT NULL,
  payload JSONB NOT NULL,
  lease_token TEXT NOT NULL DEFAULT '',
  lease_until TIMESTAMPTZ,
  available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(tenant_id, actor_id, request_id)
);
CREATE INDEX question_rubric_draft_job_queue ON question_rubric_draft_job(status, available_at, created_at);
CREATE INDEX question_rubric_draft_job_owner ON question_rubric_draft_job(tenant_id, actor_id, exam_id, created_at DESC);
ALTER TABLE question_rubric_draft_job ENABLE ROW LEVEL SECURITY;
ALTER TABLE question_rubric_draft_job FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON question_rubric_draft_job FOR ALL
  USING (edugrade_tenant_matches(tenant_id)) WITH CHECK (edugrade_tenant_matches(tenant_id));
