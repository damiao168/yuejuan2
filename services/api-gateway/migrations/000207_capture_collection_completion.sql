-- 导入结束按考试冻结名册核验；独立版本不混用考试配置或评分状态。
CREATE TABLE capture_collection (
  tenant_id UUID NOT NULL,
  exam_id UUID NOT NULL,
  revision BIGINT NOT NULL DEFAULT 0,
  completed_at TIMESTAMPTZ,
  completed_by UUID,
  PRIMARY KEY (tenant_id,exam_id),
  FOREIGN KEY (tenant_id,exam_id) REFERENCES exam(tenant_id,id)
);
INSERT INTO capture_collection(tenant_id,exam_id) SELECT tenant_id,id FROM exam;
CREATE TABLE capture_collection_completion (
  tenant_id UUID NOT NULL,
  exam_id UUID NOT NULL,
  command_id TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  actor_id UUID NOT NULL,
  result JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(tenant_id,exam_id,command_id),
  FOREIGN KEY(tenant_id,exam_id) REFERENCES capture_collection(tenant_id,exam_id)
);
ALTER TABLE capture_batch ADD COLUMN purpose TEXT NOT NULL DEFAULT 'initial',
  ADD COLUMN supplement_reason TEXT,
  ADD CONSTRAINT capture_batch_purpose_check CHECK(purpose IN ('initial','supplement'));
ALTER TABLE capture_file ADD COLUMN exclusion_reason TEXT,
  ADD COLUMN excluded_by UUID,
  ADD COLUMN replaced_by_file_id UUID;

-- 所有导入事实写入与结束命令共用这一行锁。任务只在提交结果时持锁，不能在模型调用期间持锁。
CREATE FUNCTION capture_touch_collection(t UUID,e UUID,reopen BOOLEAN DEFAULT false) RETURNS void LANGUAGE plpgsql AS $$
DECLARE closed TIMESTAMPTZ;
BEGIN
  INSERT INTO capture_collection(tenant_id,exam_id) VALUES(t,e) ON CONFLICT DO NOTHING;
  SELECT completed_at INTO closed FROM capture_collection WHERE tenant_id=t AND exam_id=e FOR UPDATE;
  IF closed IS NOT NULL AND NOT reopen THEN
    RAISE EXCEPTION 'capture_collection_closed' USING ERRCODE='P0001';
  END IF;
  UPDATE capture_collection SET revision=revision+1,
    completed_at=CASE WHEN reopen THEN NULL ELSE completed_at END,
    completed_by=CASE WHEN reopen THEN NULL ELSE completed_by END
  WHERE tenant_id=t AND exam_id=e;
END $$;

CREATE FUNCTION capture_collection_fact_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE doc JSONB; t UUID; e UUID; bid UUID; reopen BOOLEAN := false;
BEGIN
  IF TG_OP='DELETE' THEN doc:=to_jsonb(OLD); ELSE doc:=to_jsonb(NEW); END IF;
  t:=(doc->>'tenant_id')::uuid;
  IF TG_OP='UPDATE' AND TG_TABLE_NAME='capture_file' AND to_jsonb(OLD)->>'deleted_at' IS NOT NULL THEN
    RAISE EXCEPTION 'capture_file_excluded' USING ERRCODE='P0001';
  END IF;
  IF TG_TABLE_NAME='capture_page' AND EXISTS(SELECT 1 FROM capture_file WHERE tenant_id=t AND id=(doc->>'capture_file_id')::uuid AND deleted_at IS NOT NULL) THEN
    RAISE EXCEPTION 'capture_file_excluded' USING ERRCODE='P0001';
  END IF;
  IF TG_OP='UPDATE' AND TG_TABLE_NAME='submission' AND
    (doc->'student_id',doc->'identity_status',doc->'expected_page_count',doc->'actual_page_count',doc->'quality_status',doc->'deleted_at') IS NOT DISTINCT FROM
    (to_jsonb(OLD)->'student_id',to_jsonb(OLD)->'identity_status',to_jsonb(OLD)->'expected_page_count',to_jsonb(OLD)->'actual_page_count',to_jsonb(OLD)->'quality_status',to_jsonb(OLD)->'deleted_at') THEN RETURN NEW; END IF;
  IF TG_TABLE_NAME IN ('capture_batch','submission','exam_student_attendance','exam_candidate_snapshot','capture_upload_session') THEN
    e:=(doc->>'exam_id')::uuid;
  ELSIF TG_TABLE_NAME='submission_page' THEN
    SELECT exam_id INTO e FROM submission WHERE tenant_id=t AND id=(doc->>'submission_id')::uuid;
  ELSE
    bid:=(doc->>'capture_batch_id')::uuid;
    SELECT exam_id INTO e FROM capture_batch WHERE tenant_id=t AND id=bid;
  END IF;
  IF TG_TABLE_NAME='capture_batch' AND TG_OP='INSERT' AND doc->>'purpose'='supplement' THEN
    IF length(btrim(COALESCE(doc->>'supplement_reason','')))=0 OR NOT EXISTS(
      SELECT 1 FROM exam WHERE tenant_id=t AND id=e AND status IN ('collecting','grading','reviewing')) THEN
      RAISE EXCEPTION 'capture_invalid_supplement' USING ERRCODE='P0001';
    END IF;
    reopen:=true;
  END IF;
  -- 出勤调整保留其原有原因与审计；调整后需要重新核对导入，不能沿用旧的完成结论。
  IF TG_TABLE_NAME='exam_student_attendance' THEN reopen:=true; END IF;
  IF e IS NOT NULL THEN PERFORM capture_touch_collection(t,e,reopen); END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER capture_collection_batch BEFORE INSERT OR UPDATE OR DELETE ON capture_batch FOR EACH ROW EXECUTE FUNCTION capture_collection_fact_changed();
CREATE TRIGGER capture_collection_file BEFORE INSERT OR UPDATE OR DELETE ON capture_file FOR EACH ROW EXECUTE FUNCTION capture_collection_fact_changed();
CREATE TRIGGER capture_collection_page BEFORE INSERT OR UPDATE OR DELETE ON capture_page FOR EACH ROW EXECUTE FUNCTION capture_collection_fact_changed();
CREATE TRIGGER capture_collection_submission BEFORE INSERT OR DELETE OR UPDATE OF student_id,identity_status,expected_page_count,actual_page_count,quality_status,deleted_at ON submission FOR EACH ROW EXECUTE FUNCTION capture_collection_fact_changed();
CREATE TRIGGER capture_collection_submission_page BEFORE INSERT OR DELETE OR UPDATE OF page_no,file_asset_id,deleted_at ON submission_page FOR EACH ROW EXECUTE FUNCTION capture_collection_fact_changed();
CREATE TRIGGER capture_collection_attendance BEFORE INSERT OR UPDATE OR DELETE ON exam_student_attendance FOR EACH ROW EXECUTE FUNCTION capture_collection_fact_changed();
CREATE TRIGGER capture_collection_roster BEFORE INSERT OR UPDATE OR DELETE ON exam_candidate_snapshot FOR EACH ROW EXECUTE FUNCTION capture_collection_fact_changed();
CREATE TRIGGER capture_collection_upload BEFORE INSERT OR UPDATE OR DELETE ON capture_upload_session FOR EACH ROW EXECUTE FUNCTION capture_collection_fact_changed();

-- 评分创建与材料排除在切片行上互斥。排除不能在检查“尚无评分”后，漏掉同时到达的新评分。
CREATE FUNCTION capture_require_active_grade_source() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.answer_segment_id IS NOT NULL THEN
    PERFORM 1 FROM answer_segment WHERE tenant_id=NEW.tenant_id AND id=NEW.answer_segment_id AND deleted_at IS NULL FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION 'capture_file_excluded' USING ERRCODE='P0001'; END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER capture_active_grade_source BEFORE INSERT ON ai_grade FOR EACH ROW EXECUTE FUNCTION capture_require_active_grade_source();
CREATE TRIGGER capture_active_grade_source BEFORE INSERT ON human_grade FOR EACH ROW EXECUTE FUNCTION capture_require_active_grade_source();
CREATE TRIGGER capture_active_grade_source BEFORE INSERT ON question_grade FOR EACH ROW EXECUTE FUNCTION capture_require_active_grade_source();
CREATE TRIGGER capture_active_grade_source BEFORE INSERT ON final_grade FOR EACH ROW EXECUTE FUNCTION capture_require_active_grade_source();
CREATE TRIGGER capture_active_grade_source BEFORE INSERT ON subjective_grading_run FOR EACH ROW EXECUTE FUNCTION capture_require_active_grade_source();
CREATE TRIGGER capture_active_grade_source BEFORE INSERT ON scoring_run_item FOR EACH ROW EXECUTE FUNCTION capture_require_active_grade_source();

-- 考试进入阅卷不等于导入结束。只有结束命令能写 completed_at，分批阅卷不能伪造全场齐卷。

ALTER TABLE capture_collection ENABLE ROW LEVEL SECURITY;
ALTER TABLE capture_collection FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON capture_collection FOR ALL USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id));
ALTER TABLE capture_collection_completion ENABLE ROW LEVEL SECURITY;
ALTER TABLE capture_collection_completion FORCE ROW LEVEL SECURITY;
CREATE POLICY edugrade_tenant_isolation ON capture_collection_completion FOR ALL USING(edugrade_tenant_matches(tenant_id)) WITH CHECK(edugrade_tenant_matches(tenant_id));
GRANT SELECT,INSERT,UPDATE,DELETE ON capture_collection,capture_collection_completion TO edugrade_tenant_runtime;
INSERT INTO exam_archive_guarded_table VALUES('capture_collection'::regclass),('capture_collection_completion'::regclass);
CREATE TRIGGER trg_exam_archive_readonly BEFORE INSERT OR UPDATE OR DELETE ON capture_collection FOR EACH ROW EXECUTE FUNCTION reject_archived_exam_business_mutation();
CREATE TRIGGER trg_exam_archive_readonly BEFORE INSERT OR UPDATE OR DELETE ON capture_collection_completion FOR EACH ROW EXECUTE FUNCTION reject_archived_exam_business_mutation();
