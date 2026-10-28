-- 未提交敏感意见只属于当前租户和操作者，保留独立修订墓碑以防多个页面覆盖或复活已清理内容。
-- 业务入口逐次验证记录权限；表按租户RLS隔离，所有读写再显式限制actor_id，不提供管理员读取他人或列表接口。
CREATE TABLE private_opinion_draft (
 tenant_id UUID NOT NULL REFERENCES tenant(id),
 actor_id UUID NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('question-appeal','legacy-appeal','arbitration')),
 record_id UUID NOT NULL,
 revision BIGINT NOT NULL CHECK(revision>0),
 draft_json JSONB,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,actor_id,kind,record_id),
 FOREIGN KEY(tenant_id,actor_id) REFERENCES app_user(tenant_id,id),
 CHECK(draft_json IS NULL OR (jsonb_typeof(draft_json)='object' AND octet_length(draft_json::text)<=40960))
);
ALTER TABLE private_opinion_draft ENABLE ROW LEVEL SECURITY;
ALTER TABLE private_opinion_draft FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON private_opinion_draft FOR ALL USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id));
DO $$ BEGIN IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='edugrade_tenant_runtime') THEN GRANT SELECT,INSERT,UPDATE ON private_opinion_draft TO edugrade_tenant_runtime; END IF; END $$;

-- 保存和正式提交统一先锁父资源、后锁草稿。旧页面不能在正式提交清理后再插回旧意见。
CREATE FUNCTION validate_private_opinion_draft_resource() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE actual_source TEXT; actual_revision BIGINT; state TEXT; assignee UUID; allowed BOOLEAN; permissions TEXT[];
BEGIN
 IF NEW.draft_json IS NULL THEN RETURN NEW; END IF;
 IF NEW.kind='question-appeal' THEN
  SELECT source_release_id::text,revision,status,assigned_to INTO actual_source,actual_revision,state,assignee
  FROM question_appeal WHERE tenant_id=NEW.tenant_id AND id=NEW.record_id FOR SHARE;
 ELSIF NEW.kind='legacy-appeal' THEN
  SELECT exam_id::text||':'||COALESCE(final_grade_id::text,submission_grade_id::text),revision,status,assigned_to
  INTO actual_source,actual_revision,state,assignee FROM appeal WHERE tenant_id=NEW.tenant_id AND id=NEW.record_id AND deleted_at IS NULL FOR SHARE;
 ELSE
  SELECT exam_id::text||':'||question_id::text,revision,status,assigned_to INTO actual_source,actual_revision,state,assignee
  FROM arbitration_task WHERE tenant_id=NEW.tenant_id AND id=NEW.record_id FOR SHARE;
 END IF;
 IF actual_revision IS NULL OR NEW.draft_json->>'source' IS DISTINCT FROM actual_source OR (NEW.draft_json->>'source_revision')::bigint IS DISTINCT FROM actual_revision THEN
  RAISE EXCEPTION 'private draft source changed' USING ERRCODE='23514';
 END IF;
 PERFORM 1 FROM app_user WHERE tenant_id=NEW.tenant_id AND id=NEW.actor_id AND status='active' AND deleted_at IS NULL FOR SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'private draft actor unavailable' USING ERRCODE='23514'; END IF;
 SELECT array_agg(DISTINCT p.code) INTO permissions FROM user_role ur
 JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
 JOIN role_permission rp ON rp.tenant_id=r.tenant_id AND rp.role_id=r.id AND rp.deleted_at IS NULL
 JOIN permission p ON p.tenant_id=rp.tenant_id AND p.id=rp.permission_id AND p.deleted_at IS NULL
 WHERE ur.tenant_id=NEW.tenant_id AND ur.user_id=NEW.actor_id AND ur.deleted_at IS NULL;
 IF NEW.kind='arbitration' THEN allowed:=assignee=NEW.actor_id AND state<>'submitted' AND 'arbitration:work'=ANY(permissions);
 ELSIF NEW.kind='question-appeal' THEN allowed:=('appeal:manage'=ANY(permissions) AND state IN ('under_review','upheld_pending_regrade')) OR ('appeal:work'=ANY(permissions) AND assignee=NEW.actor_id AND state='under_review');
 ELSE allowed:=state IN ('submitted','pending','under_review','need_more_info') AND ('appeal:manage'=ANY(permissions) OR ('appeal:work'=ANY(permissions) AND assignee=NEW.actor_id)); END IF;
 IF NOT COALESCE(allowed,FALSE) THEN RAISE EXCEPTION 'private draft access changed' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER trg_private_opinion_draft_resource BEFORE INSERT OR UPDATE ON private_opinion_draft FOR EACH ROW EXECUTE FUNCTION validate_private_opinion_draft_resource();

-- 清理跟随正式成功事实提交；分派或普通读取不清理，失败事务也不能提前丢弃草稿。
CREATE FUNCTION clear_submitted_private_opinion_draft() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE actor UUID; resource UUID; resource_kind TEXT; resource_revision BIGINT; resource_source TEXT;
BEGIN
 IF TG_TABLE_NAME='question_appeal_event' THEN
  IF NEW.event_type NOT IN ('review_opinion_submitted','decided','resolved') THEN RETURN NEW; END IF;
  actor:=NEW.actor_id; resource:=NEW.appeal_id; resource_kind:='question-appeal';
  SELECT revision,source_release_id::text INTO resource_revision,resource_source FROM question_appeal WHERE tenant_id=NEW.tenant_id AND id=resource;
 ELSIF TG_TABLE_NAME='appeal' THEN
  IF NEW.closed_at IS DISTINCT FROM OLD.closed_at THEN actor:=NEW.closed_by;
  ELSIF NEW.reviewed_at IS DISTINCT FROM OLD.reviewed_at THEN actor:=NEW.reviewed_by;
  ELSIF NEW.teacher_recommendation_at IS DISTINCT FROM OLD.teacher_recommendation_at THEN actor:=NEW.teacher_recommendation_by;
  ELSE RETURN NEW; END IF;
  resource:=NEW.id; resource_kind:='legacy-appeal'; resource_revision:=NEW.revision;resource_source:=NEW.exam_id::text||':'||COALESCE(NEW.final_grade_id::text,NEW.submission_grade_id::text);
 ELSE
  IF NEW.status<>'submitted' OR OLD.status='submitted' THEN RETURN NEW; END IF;
  actor:=NEW.assigned_to; resource:=NEW.id; resource_kind:='arbitration'; resource_revision:=NEW.revision;resource_source:=NEW.exam_id::text||':'||NEW.question_id::text;
 END IF;
 UPDATE private_opinion_draft SET revision=revision+1,draft_json=NULL,updated_at=now()
 WHERE tenant_id=NEW.tenant_id AND actor_id=actor AND kind=resource_kind AND record_id=resource
 AND draft_json->>'source'=resource_source AND (draft_json->>'source_revision')::bigint<resource_revision;
 RETURN NEW;
END $$;
CREATE TRIGGER trg_question_appeal_private_draft AFTER INSERT ON question_appeal_event FOR EACH ROW EXECUTE FUNCTION clear_submitted_private_opinion_draft();
CREATE TRIGGER trg_appeal_private_draft AFTER UPDATE ON appeal FOR EACH ROW EXECUTE FUNCTION clear_submitted_private_opinion_draft();
CREATE TRIGGER trg_arbitration_private_draft AFTER UPDATE ON arbitration_task FOR EACH ROW EXECUTE FUNCTION clear_submitted_private_opinion_draft();
