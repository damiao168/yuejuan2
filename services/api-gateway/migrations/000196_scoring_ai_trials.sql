-- 人工基准与样本批次同事务保存，扩批批准与扩批批次同事务保存；刷新只恢复已持久化的范围。
-- 迁移前的运行保留既有恢复能力；新增运行必须通过服务端试改，不能以“尚无试改资源”绕过闸门。
ALTER TABLE scoring_run ADD COLUMN ai_trial_required BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE scoring_run ALTER COLUMN ai_trial_required SET DEFAULT true;
CREATE TABLE scoring_ai_trial (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id UUID NOT NULL REFERENCES tenant(id),
 scoring_run_id UUID NOT NULL REFERENCES scoring_run(id),
 exam_id UUID NOT NULL REFERENCES exam(id),
 question_id UUID NOT NULL REFERENCES question(id),
 command_id TEXT NOT NULL CHECK(octet_length(command_id) BETWEEN 1 AND 128),
 created_by UUID NOT NULL REFERENCES app_user(id),
 status TEXT NOT NULL CHECK(status IN ('awaiting_enqueue','running','reviewing','stopped','expanded','invalidated')),
 revision BIGINT NOT NULL CHECK(revision>0),
 sample_batch_id UUID NOT NULL,
 expansion_batch_id UUID,
 request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
 payload JSONB NOT NULL CHECK(jsonb_typeof(payload)='object' AND jsonb_array_length(payload->'samples') BETWEEN 5 AND 10),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,id),
 UNIQUE(tenant_id,scoring_run_id,command_id),
 FOREIGN KEY(tenant_id,sample_batch_id) REFERENCES subjective_grading_batch(tenant_id,id),
 FOREIGN KEY(tenant_id,expansion_batch_id) REFERENCES subjective_grading_batch(tenant_id,id)
);
CREATE INDEX idx_scoring_ai_trial_run ON scoring_ai_trial(tenant_id,scoring_run_id,created_at);
ALTER TABLE scoring_ai_trial ENABLE ROW LEVEL SECURITY;
ALTER TABLE scoring_ai_trial FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scoring_ai_trial USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id));
GRANT SELECT,INSERT,UPDATE ON scoring_ai_trial TO edugrade_tenant_runtime;
REVOKE DELETE ON scoring_ai_trial FROM edugrade_tenant_runtime;
INSERT INTO exam_archive_guarded_table VALUES ('scoring_ai_trial'::regclass);
CREATE TRIGGER trg_exam_archive_readonly BEFORE INSERT OR UPDATE OR DELETE ON scoring_ai_trial FOR EACH ROW EXECUTE FUNCTION reject_archived_exam_business_mutation();

-- 试改样本始终带结构化风险，不能因供应商省略标记而进入快捷确认。
CREATE FUNCTION mark_scoring_trial_sample_grade() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM scoring_ai_trial trial CROSS JOIN LATERAL jsonb_array_elements(trial.payload->'samples') sample
   WHERE trial.tenant_id=NEW.tenant_id AND sample->>'segment_id'=NEW.answer_segment_id::text) THEN
   NEW.risk_flags=COALESCE(NEW.risk_flags,'[]'::jsonb);
   IF NOT NEW.risk_flags @> '["trial_sample"]'::jsonb THEN NEW.risk_flags=NEW.risk_flags || '["trial_sample"]'::jsonb; END IF;
   NEW.needs_human_review=true;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER trg_scoring_trial_sample_grade BEFORE INSERT ON ai_grade FOR EACH ROW EXECUTE FUNCTION mark_scoring_trial_sample_grade();
