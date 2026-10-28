-- 题库交接独立于冻结题目事实；历史版本保持未指派，仍可由有职责人员领取。
-- 状态、事件与正式审核转换由同一业务事务提交；不回写内容修订或既有审核记录。
CREATE TABLE question_bank_handoff (
 tenant_id UUID NOT NULL,
 version_id UUID NOT NULL,
 revision BIGINT NOT NULL CHECK(revision>0),
 reviewer_id UUID,
 publisher_id UUID,
 claimed_by UUID,
 claimed_at TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,version_id),
 FOREIGN KEY(tenant_id,version_id) REFERENCES question_bank_item_version(tenant_id,id),
 FOREIGN KEY(tenant_id,reviewer_id) REFERENCES app_user(tenant_id,id),
 FOREIGN KEY(tenant_id,publisher_id) REFERENCES app_user(tenant_id,id),
 FOREIGN KEY(tenant_id,claimed_by) REFERENCES app_user(tenant_id,id),
 CHECK((claimed_by IS NULL)=(claimed_at IS NULL))
);
CREATE TABLE question_bank_handoff_event (
 id UUID NOT NULL DEFAULT gen_random_uuid(),
 tenant_id UUID NOT NULL,
 version_id UUID NOT NULL,
 handoff_revision BIGINT NOT NULL CHECK(handoff_revision>0),
 content_revision BIGINT NOT NULL CHECK(content_revision>0),
 kind TEXT NOT NULL CHECK(kind IN ('assigned','reassigned','claimed','approved','published','returned')),
 actor_id UUID NOT NULL,
 recipient_id UUID,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,id),
 FOREIGN KEY(tenant_id,version_id) REFERENCES question_bank_item_version(tenant_id,id),
 FOREIGN KEY(tenant_id,actor_id) REFERENCES app_user(tenant_id,id),
 FOREIGN KEY(tenant_id,recipient_id) REFERENCES app_user(tenant_id,id)
);
CREATE INDEX idx_question_bank_handoff_recipient ON question_bank_handoff(tenant_id,reviewer_id,publisher_id);
CREATE INDEX idx_question_bank_handoff_event_version ON question_bank_handoff_event(tenant_id,version_id,created_at DESC,id);
DO $$ DECLARE n TEXT; BEGIN
 FOREACH n IN ARRAY ARRAY['question_bank_handoff','question_bank_handoff_event'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',n);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',n);
  EXECUTE format('CREATE POLICY edugrade_tenant_isolation ON %I FOR ALL USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id))',n);
 END LOOP;
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='edugrade_tenant_runtime') THEN
  GRANT SELECT,INSERT,UPDATE ON question_bank_handoff TO edugrade_tenant_runtime;
  GRANT SELECT,INSERT ON question_bank_handoff_event TO edugrade_tenant_runtime;
 END IF;
END $$;
CREATE FUNCTION guard_question_bank_handoff_event() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'question bank handoff events are immutable' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER trg_question_bank_handoff_event BEFORE UPDATE OR DELETE ON question_bank_handoff_event
 FOR EACH ROW EXECUTE FUNCTION guard_question_bank_handoff_event();
