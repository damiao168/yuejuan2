-- 学校通过标准只从已保存的题目策略批准为冻结快照；撤回保留历史，不倒改已采用该标准的题目策略或阅卷资格。
CREATE TABLE school_calibration_template (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 tenant_id UUID NOT NULL REFERENCES tenant(id),
 school_id UUID NOT NULL,
 subject TEXT NOT NULL CHECK(length(btrim(subject))>0),
 name TEXT NOT NULL CHECK(length(btrim(name)) BETWEEN 1 AND 80),
 source_exam_id UUID NOT NULL,
 source_question_id UUID NOT NULL,
 source_policy_id UUID NOT NULL,
 source_policy_revision BIGINT NOT NULL CHECK(source_policy_revision>0),
 archetype_code TEXT NOT NULL,
 max_score NUMERIC(8,2) NOT NULL CHECK(max_score>0),
 policy_json JSONB NOT NULL CHECK(jsonb_typeof(policy_json)='object'),
 status TEXT NOT NULL DEFAULT 'approved' CHECK(status IN ('approved','withdrawn')),
 revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0),
 approved_by UUID NOT NULL,
 approved_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 withdrawn_by UUID,
 withdrawn_at TIMESTAMPTZ,
 withdraw_reason TEXT NOT NULL DEFAULT '',
 UNIQUE(tenant_id,id),
 UNIQUE(tenant_id,source_policy_id,source_policy_revision,name),
 FOREIGN KEY(tenant_id,school_id) REFERENCES school(tenant_id,id),
 FOREIGN KEY(tenant_id,source_exam_id) REFERENCES exam(tenant_id,id),
 FOREIGN KEY(tenant_id,source_question_id) REFERENCES question(tenant_id,id),
 FOREIGN KEY(tenant_id,source_policy_id) REFERENCES grader_calibration_policy(tenant_id,id),
 FOREIGN KEY(tenant_id,approved_by) REFERENCES app_user(tenant_id,id),
 FOREIGN KEY(tenant_id,withdrawn_by) REFERENCES app_user(tenant_id,id),
 CHECK((status='approved' AND revision=1 AND withdrawn_by IS NULL AND withdrawn_at IS NULL AND withdraw_reason='') OR
       (status='withdrawn' AND revision=2 AND withdrawn_by IS NOT NULL AND withdrawn_at IS NOT NULL AND length(btrim(withdraw_reason)) BETWEEN 1 AND 500))
);
CREATE INDEX idx_school_calibration_template_approved ON school_calibration_template(tenant_id,school_id,subject,archetype_code,max_score,approved_at DESC,id) WHERE status='approved';
ALTER TABLE school_calibration_template ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_calibration_template FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON school_calibration_template FOR ALL USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id));
DO $$ BEGIN IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='edugrade_tenant_runtime') THEN GRANT SELECT,INSERT,UPDATE ON school_calibration_template TO edugrade_tenant_runtime; END IF; END $$;

-- 只允许一次批准到撤回；不原地改阈值、来源、名称或批准人，避免已保存策略的来源指向另一份标准。
CREATE FUNCTION preserve_school_calibration_template() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'school calibration template history is immutable' USING ERRCODE='23514'; END IF;
 IF (to_jsonb(NEW)-ARRAY['status','revision','withdrawn_by','withdrawn_at','withdraw_reason']) IS DISTINCT FROM
    (to_jsonb(OLD)-ARRAY['status','revision','withdrawn_by','withdrawn_at','withdraw_reason']) OR
    OLD.status<>'approved' OR NEW.status<>'withdrawn' OR NEW.revision<>OLD.revision+1 THEN
  RAISE EXCEPTION 'school calibration template history is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER trg_preserve_school_calibration_template BEFORE UPDATE OR DELETE ON school_calibration_template FOR EACH ROW EXECUTE FUNCTION preserve_school_calibration_template();

ALTER TABLE grader_calibration_policy ADD COLUMN school_template_id UUID,
 ADD COLUMN school_template_revision BIGINT,
 ADD FOREIGN KEY(tenant_id,school_template_id) REFERENCES school_calibration_template(tenant_id,id),
 ADD CHECK((school_template_id IS NULL AND school_template_revision IS NULL) OR (school_template_id IS NOT NULL AND school_template_revision IS NOT NULL AND school_template_revision=1));
