-- 审计正文和摘要链仍是只追加证据，不能为恢复学校列表而 UPDATE 历史 audit_log。
-- 归属旁表仅作授权检索投影，不属于既有 record_hash 公式；历史归属从现存目标关系回填，无法确认的记录继续只对租户管理员开放。
CREATE TABLE audit_log_scope (
  audit_id UUID PRIMARY KEY REFERENCES audit_log(id),
  tenant_id UUID NOT NULL,
  school_id UUID NOT NULL,
  exam_id UUID,
  happened_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_audit_log_scope_school ON audit_log_scope(tenant_id,school_id,happened_at DESC,audit_id DESC);
CREATE INDEX idx_audit_log_scope_exam ON audit_log_scope(tenant_id,exam_id) WHERE exam_id IS NOT NULL;
CREATE TRIGGER trg_audit_log_scope_append_only BEFORE UPDATE OR DELETE ON audit_log_scope
FOR EACH ROW EXECUTE FUNCTION reject_audit_log_mutation();

-- 只认服务端维护的实体关系，不从 before/after、操作者学校或请求参数猜测目标归属。
-- 所有递归关系都落向固定白名单，未知类型及已物理删除的目标不会扩大学校可见范围。
CREATE FUNCTION resolve_audit_target_scope(p_tenant UUID,p_type TEXT,p_target UUID)
RETURNS TABLE(school_id UUID,exam_id UUID) LANGUAGE plpgsql VOLATILE SET search_path = public, pg_temp AS $$
DECLARE resource_table TEXT; parent_column TEXT; parent_type TEXT; parent_id UUID;
BEGIN
 IF p_target IS NULL THEN RETURN; END IF;
 CASE p_type
 WHEN 'user','app_user' THEN resource_table:='app_user';
 WHEN 'class','school_class' THEN resource_table:='school_class';
 WHEN 'student','grade','question_bank','exam_session','answer_sheet_template_library' THEN resource_table:=p_type;
 WHEN 'school' THEN RETURN QUERY SELECT s.id,NULL::uuid FROM school s WHERE s.tenant_id=p_tenant AND s.id=p_target; RETURN;
 WHEN 'file_asset' THEN
   RETURN QUERY SELECT COALESCE(f.school_id,e.school_id),e.id FROM file_asset f
   LEFT JOIN submission sub ON sub.tenant_id=f.tenant_id AND sub.id=f.submission_id
   LEFT JOIN exam e ON e.tenant_id=f.tenant_id AND e.id=COALESCE(f.exam_id,sub.exam_id)
   WHERE f.tenant_id=p_tenant AND f.id=p_target AND COALESCE(f.school_id,e.school_id) IS NOT NULL;
   RETURN;
 WHEN 'exam' THEN RETURN QUERY SELECT e.school_id,e.id FROM exam e WHERE e.tenant_id=p_tenant AND e.id=p_target; RETURN;
 WHEN 'question','submission','paper_import_job','answer_sheet_template','exam_paper','review_task','arbitration_task',
      'scoring_rule','scoring_run','capture_batch','final_grade','submission_grade','appeal','question_appeal',
      'operational_exception','page_template_match_run','double_mark_session','score_release','answer_group',
      'backmark_batch','grader_calibration_session','grading_gold_paper','grading_quality_incident','regrade_job',
      'release_gate_evidence','release_gate_waiver_request','capture_upload_session' THEN
   resource_table:=p_type; parent_column:='exam_id'; parent_type:='exam';
 WHEN 'paper_import' THEN resource_table:='paper_import_job'; parent_column:='exam_id'; parent_type:='exam';
 WHEN 'processing_exception' THEN resource_table:='operational_exception'; parent_column:='exam_id'; parent_type:='exam';
 WHEN 'gold_paper' THEN resource_table:='grading_gold_paper'; parent_column:='exam_id'; parent_type:='exam';
 WHEN 'release_gate_waiver' THEN resource_table:='release_gate_waiver_request'; parent_column:='exam_id'; parent_type:='exam';
 WHEN 'capture_upload' THEN resource_table:='capture_upload_session'; parent_column:='exam_id'; parent_type:='exam';
 WHEN 'answer_segment','submission_page','ocr_task','ai_human_disagreement','regrade_item' THEN
   resource_table:=p_type; parent_column:='submission_id'; parent_type:='submission';
 WHEN 'ai_grade','subjective_grading_run','omr_run','math_understanding_artifact' THEN
   resource_table:=p_type; parent_column:='answer_segment_id'; parent_type:='answer_segment';
 WHEN 'human_grade','review_annotation','backmark_item' THEN
   resource_table:=p_type; parent_column:='review_task_id'; parent_type:='review_task';
 WHEN 'scoring_run_item' THEN resource_table:=p_type; parent_column:='scoring_run_id'; parent_type:='scoring_run';
 WHEN 'capture_file','capture_page' THEN resource_table:=p_type; parent_column:='capture_batch_id'; parent_type:='capture_batch';
 WHEN 'page_registration_run','page_registration_correction' THEN resource_table:=p_type; parent_column:='capture_page_id'; parent_type:='capture_page';
 WHEN 'auth_session','user_role' THEN resource_table:=p_type; parent_column:='user_id'; parent_type:='user';
 ELSE RETURN;
 END CASE;
 IF parent_column IS NULL THEN
   RETURN QUERY EXECUTE format('SELECT school_id,NULL::uuid FROM %I WHERE tenant_id=$1 AND id=$2 AND school_id IS NOT NULL',resource_table) USING p_tenant,p_target;
 ELSE
   EXECUTE format('SELECT %I FROM %I WHERE tenant_id=$1 AND id=$2',parent_column,resource_table) INTO parent_id USING p_tenant,p_target;
   IF parent_id IS NOT NULL THEN RETURN QUERY SELECT * FROM resolve_audit_target_scope(p_tenant,parent_type,parent_id); END IF;
 END IF;
END $$;
REVOKE ALL ON FUNCTION resolve_audit_target_scope(UUID,TEXT,UUID) FROM PUBLIC,edugrade_tenant_runtime;

-- 登录前的安全审计也会追加。触发器只用 NEW 的租户建立短暂上下文，完成后恢复调用方上下文；运行身份不能直接写归属旁表。
CREATE FUNCTION project_audit_log_scope() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE previous_tenant TEXT:=current_setting('edugrade.tenant_id',true);
BEGIN
 PERFORM set_config('edugrade.tenant_id',NEW.tenant_id::text,true);
 INSERT INTO audit_log_scope(audit_id,tenant_id,school_id,exam_id,happened_at)
 SELECT NEW.id,NEW.tenant_id,s.school_id,s.exam_id,NEW.created_at FROM resolve_audit_target_scope(NEW.tenant_id,NEW.target_type,NEW.target_id) s
 WHERE s.school_id IS NOT NULL;
 PERFORM set_config('edugrade.tenant_id',COALESCE(previous_tenant,''),true);
 RETURN NEW;
END $$;
CREATE TRIGGER trg_audit_log_scope AFTER INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION project_audit_log_scope();
REVOKE ALL ON FUNCTION project_audit_log_scope() FROM PUBLIC,edugrade_tenant_runtime;

DO $$
DECLARE audit_tenant UUID; previous_tenant TEXT:=current_setting('edugrade.tenant_id',true);
BEGIN
 FOR audit_tenant IN SELECT DISTINCT tenant_id FROM audit_log LOOP
   PERFORM set_config('edugrade.tenant_id',audit_tenant::text,true);
   INSERT INTO audit_log_scope(audit_id,tenant_id,school_id,exam_id,happened_at)
   SELECT a.id,a.tenant_id,s.school_id,s.exam_id,a.created_at FROM audit_log a
   CROSS JOIN LATERAL resolve_audit_target_scope(a.tenant_id,a.target_type,a.target_id) s
   WHERE a.tenant_id=audit_tenant AND s.school_id IS NOT NULL;
 END LOOP;
 PERFORM set_config('edugrade.tenant_id',COALESCE(previous_tenant,''),true);
END $$;
ALTER TABLE audit_log_scope ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log_scope FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON audit_log_scope FOR ALL
USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id));
REVOKE INSERT,UPDATE,DELETE ON audit_log_scope FROM edugrade_tenant_runtime;
GRANT SELECT ON audit_log_scope TO edugrade_tenant_runtime;
