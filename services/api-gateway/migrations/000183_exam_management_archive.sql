-- 整场归档独立于各科业务阶段。存量 status='archived' 保持只读，因原阶段已丢失，不自动补恢复信息。
ALTER TABLE exam_session ADD COLUMN archived_at TIMESTAMPTZ;
ALTER TABLE exam_session ADD COLUMN archived_by UUID REFERENCES app_user(id);
ALTER TABLE exam_session ADD COLUMN archive_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE exam ADD COLUMN archived_at TIMESTAMPTZ;
ALTER TABLE exam ADD COLUMN archived_by UUID REFERENCES app_user(id);
ALTER TABLE exam ADD COLUMN archive_reason TEXT NOT NULL DEFAULT '';

CREATE TABLE exam_management_command (
  tenant_id UUID NOT NULL REFERENCES tenant(id),
  actor_id UUID NOT NULL,
  command_id TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('session','standalone')),
  target_id UUID NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('archive','restore')),
  request_hash TEXT NOT NULL,
  result_json JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, actor_id, command_id),
  FOREIGN KEY (tenant_id, actor_id) REFERENCES app_user(tenant_id,id),
  CHECK (length(command_id) BETWEEN 8 AND 128)
);
ALTER TABLE exam_management_command ENABLE ROW LEVEL SECURITY;
ALTER TABLE exam_management_command FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON exam_management_command
USING (edugrade_tenant_matches(tenant_id)) WITH CHECK (edugrade_tenant_matches(tenant_id));
GRANT SELECT,INSERT ON exam_management_command TO edugrade_tenant_runtime;
REVOKE UPDATE,DELETE ON exam_management_command FROM edugrade_tenant_runtime;

-- 回执、审计和通知与归档事实同事务落库；断网重放只读回执，不重复写审计。
CREATE FUNCTION audit_exam_management_archive_command()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE facts JSONB;
BEGIN
  facts := jsonb_build_object('command_id',NEW.command_id,'kind',NEW.kind,
    'revision',NEW.result_json->'revision','archived_at',NEW.result_json->'archived_at',
    'subject_ids',(SELECT jsonb_agg(item->'id') FROM jsonb_array_elements(NEW.result_json->'exams') item));
  INSERT INTO audit_log(tenant_id,actor_id,action,target_type,target_id,after_value,reason)
  VALUES(NEW.tenant_id,NEW.actor_id,'exam.management.' || NEW.action,
    CASE WHEN NEW.kind='session' THEN 'exam_session' ELSE 'exam' END,NEW.target_id,
    facts,NEW.result_json->>'archive_reason');
  INSERT INTO event_outbox(tenant_id,aggregate_type,aggregate_id,event_type,payload)
  VALUES(NEW.tenant_id,'exam_management',NEW.target_id,'exam.management.' || NEW.action,facts);
  RETURN NEW;
END $$;
CREATE TRIGGER trg_exam_management_command_audit AFTER INSERT ON exam_management_command
FOR EACH ROW EXECUTE FUNCTION audit_exam_management_archive_command();

-- 固定本次迁移覆盖的业务表；审计、读模型和题库的来源凭据不属于考试内容写入。
-- 新增无外键的异步来源时，需同时扩展下方 source_type 映射和归档集成测试。
CREATE TABLE exam_archive_guarded_table (relation REGCLASS PRIMARY KEY);
WITH RECURSIVE dependent(oid) AS (
  SELECT 'exam'::regclass::oid
  UNION
  SELECT c.conrelid FROM pg_constraint c JOIN dependent d ON c.confrelid=d.oid
  WHERE c.contype='f'
    AND c.conrelid::regclass::text NOT IN (
      'activity_event','model_call_fact','file_reconciliation_finding','processing_projection_cursor','submission_page_processing_state',
      'question_bank_import','question_bank_item_asset'
    )
)
INSERT INTO exam_archive_guarded_table SELECT oid::regclass FROM dependent;
GRANT SELECT ON exam_archive_guarded_table TO edugrade_tenant_runtime;
REVOKE INSERT,UPDATE,DELETE ON exam_archive_guarded_table FROM edugrade_tenant_runtime;

-- 通过已有外键追溯到学科；复合外键逐列匹配，避免跨租户同名记录成为归档对象。
CREATE FUNCTION exam_archive_row_exams(target_table REGCLASS, record JSONB, depth INT DEFAULT 0, visited TEXT[] DEFAULT '{}')
RETURNS UUID[] LANGUAGE plpgsql AS $$
DECLARE
  result UUID[] := '{}';
  parent_row JSONB;
  fk RECORD;
  predicates TEXT;
  source_table TEXT;
  source_id TEXT;
  payload JSONB;
  visit_key TEXT;
BEGIN
  IF depth > 16 OR record IS NULL THEN RETURN result; END IF;
  visit_key := target_table::text || ':' || COALESCE(record->>'id',md5(record::text));
  IF visit_key=ANY(visited) THEN RETURN result; END IF;
  visited := visited || visit_key;
  IF target_table='exam'::regclass THEN
    RETURN ARRAY[(record->>'id')::uuid];
  END IF;
  IF NULLIF(record->>'exam_id','') IS NOT NULL THEN
    RETURN ARRAY[(record->>'exam_id')::uuid];
  END IF;

  -- 高频路径直接使用业务主键索引；不能每个待处理任务都展开整张外键图。
  IF NULLIF(record->>'submission_id','') IS NOT NULL THEN
    SELECT ARRAY[s.exam_id] INTO result FROM submission s
    WHERE s.tenant_id=(record->>'tenant_id')::uuid AND s.id=(record->>'submission_id')::uuid;
    RETURN COALESCE(result,'{}');
  END IF;
  IF NULLIF(COALESCE(record->>'answer_segment_id',record->>'segment_id'),'') IS NOT NULL THEN
    SELECT ARRAY[s.exam_id] INTO result FROM answer_segment a
    JOIN submission s ON s.tenant_id=a.tenant_id AND s.id=a.submission_id
    WHERE a.tenant_id=(record->>'tenant_id')::uuid
      AND a.id=COALESCE(record->>'answer_segment_id',record->>'segment_id')::uuid;
    RETURN COALESCE(result,'{}');
  END IF;
  IF NULLIF(record->>'question_id','') IS NOT NULL THEN
    SELECT ARRAY[q.exam_id] INTO result FROM question q
    WHERE q.tenant_id=(record->>'tenant_id')::uuid AND q.id=(record->>'question_id')::uuid;
    RETURN COALESCE(result,'{}');
  END IF;
  IF target_table='paper_import_run'::regclass THEN
    SELECT ARRAY[j.exam_id] INTO result FROM paper_import_job j
    WHERE j.tenant_id=(record->>'tenant_id')::uuid AND j.id=(record->>'paper_import_id')::uuid;
    RETURN COALESCE(result,'{}');
  END IF;
  IF target_table='agent_worker_task'::regclass AND NULLIF(record->>'paper_import_run_id','') IS NOT NULL THEN
    SELECT ARRAY[j.exam_id] INTO result FROM paper_import_run r
    JOIN paper_import_job j ON j.tenant_id=r.tenant_id AND j.id=r.paper_import_id
    WHERE r.tenant_id=(record->>'tenant_id')::uuid AND r.id=(record->>'paper_import_run_id')::uuid;
    IF NULLIF(record->'payload'->>'exam_id','') IS NOT NULL THEN
      result := COALESCE(result,'{}') || ARRAY[(record->'payload'->>'exam_id')::uuid];
    END IF;
    RETURN COALESCE(result,'{}');
  END IF;

  FOR fk IN
    SELECT c.confrelid, c.conkey, c.confkey FROM pg_constraint c
    JOIN exam_archive_guarded_table g ON g.relation=c.confrelid
    WHERE c.conrelid=target_table AND c.contype='f'
    ORDER BY c.confrelid,c.oid
  LOOP
    SELECT string_agg(format('p.%I = ($1->>%L)::%s',pa.attname,ca.attname,format_type(pa.atttypid,pa.atttypmod)),' AND ')
    INTO predicates
    FROM unnest(fk.conkey, fk.confkey) AS keys(child_no,parent_no)
    JOIN pg_attribute ca ON ca.attrelid=target_table AND ca.attnum=keys.child_no
    JOIN pg_attribute pa ON pa.attrelid=fk.confrelid AND pa.attnum=keys.parent_no;
    EXECUTE format('SELECT to_jsonb(p) FROM %s p WHERE %s',fk.confrelid::regclass,predicates)
      INTO parent_row USING record;
    IF parent_row IS NOT NULL THEN
      result := result || exam_archive_row_exams(fk.confrelid::regclass,parent_row,depth+1,visited);
    END IF;
  END LOOP;

  IF target_table='subjective_grading_batch'::regclass THEN
    FOR parent_row IN
      SELECT to_jsonb(s) FROM answer_segment s
      WHERE s.tenant_id=(record->>'tenant_id')::uuid
        AND s.id IN (SELECT jsonb_array_elements_text(COALESCE(record->'segment_ids','[]'))::uuid)
    LOOP
      result := result || exam_archive_row_exams('answer_segment'::regclass,parent_row,depth+1,visited);
    END LOOP;
  END IF;

  -- 通用运行任务的来源没有外键；只允许已知业务来源参与追溯，不能把任意文本当作 SQL 表名。
  IF target_table IN ('agent_worker_task'::regclass,'agent_job'::regclass,'orchestration_run'::regclass) THEN
    source_table := CASE COALESCE(record->>'source_type',record->>'target_type')
      WHEN 'exam' THEN 'exam'
      WHEN 'submission' THEN 'submission'
      WHEN 'submission_page' THEN 'submission_page'
      WHEN 'answer_segment' THEN 'answer_segment'
      WHEN 'question' THEN 'question'
      WHEN 'exam_paper' THEN 'exam_paper'
      WHEN 'ocr_task' THEN 'ocr_task'
      WHEN 'image_quality_run' THEN 'submission_page_quality_run'
      WHEN 'capture_file' THEN 'capture_file'
      WHEN 'page_registration_run' THEN 'page_registration_run'
      WHEN 'page_registration_correction' THEN 'page_registration_correction'
      WHEN 'page_template_match_run' THEN 'page_template_match_run'
      WHEN 'subjective_grading_run' THEN 'subjective_grading_run'
      WHEN 'omr_run' THEN 'omr_run'
      WHEN 'scoring_run' THEN 'scoring_run'
      WHEN 'paper_import_job' THEN 'paper_import_job'
      WHEN 'paper_import_parse' THEN 'paper_import_parse_input'
      WHEN 'paper_import_formula' THEN 'paper_import_formula_input'
      WHEN 'report' THEN 'report'
      WHEN 'report_generate' THEN 'report'
      ELSE NULL END;
    source_id := COALESCE(record->>'source_id',record->>'target_id');
    IF source_table IS NOT NULL AND source_id IS NOT NULL THEN
      EXECUTE format('SELECT to_jsonb(p) FROM %I p WHERE p.id=$1::uuid AND p.tenant_id=$2::uuid',source_table)
        INTO parent_row USING source_id,record->>'tenant_id';
      IF parent_row IS NOT NULL THEN
        result := result || exam_archive_row_exams(source_table::regclass,parent_row,depth+1,visited);
      END IF;
    END IF;
    payload := record->'payload';
    IF NULLIF(payload->>'exam_id','') IS NOT NULL THEN
      result := result || ARRAY[(payload->>'exam_id')::uuid];
    ELSIF NULLIF(payload->>'answer_segment_id','') IS NOT NULL THEN
      SELECT to_jsonb(p) INTO parent_row FROM answer_segment p
      WHERE p.id::text=payload->>'answer_segment_id' AND p.tenant_id::text=record->>'tenant_id';
      result := result || exam_archive_row_exams('answer_segment'::regclass,parent_row,depth+1,visited);
    END IF;
  END IF;
  RETURN ARRAY(SELECT DISTINCT value FROM unnest(result) value WHERE value IS NOT NULL ORDER BY value);
END $$;

-- 与正式成绩发布共用事务级锁。锁等待结束后重新读取归档事实，异步工作者也不能写回旧结果。
CREATE FUNCTION exam_archive_require_mutable(tenant UUID, exam_id UUID)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE locked BOOLEAN; parent_id UUID; parent_locked BOOLEAN;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtext(tenant::text),hashtext(exam_id::text));
  SELECT e.archived_at IS NOT NULL OR e.status='archived',e.exam_session_id
  INTO locked,parent_id FROM exam e
  WHERE e.tenant_id=tenant AND e.id=exam_id FOR SHARE;
  IF parent_id IS NOT NULL THEN
    SELECT archived_at IS NOT NULL OR status='archived' INTO parent_locked
    FROM exam_session WHERE tenant_id=tenant AND id=parent_id FOR SHARE;
  END IF;
  IF locked OR parent_locked THEN
    RAISE EXCEPTION 'exam_archived: archived exam business content is read-only'
      USING ERRCODE='55000';
  END IF;
END $$;

CREATE FUNCTION reject_archived_exam_business_mutation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
  old_record JSONB;
  new_record JSONB;
  exam_ids UUID[] := '{}';
  target_exam UUID;
  parent_archived BOOLEAN;
BEGIN
  IF TG_OP<>'INSERT' THEN old_record:=to_jsonb(OLD); END IF;
  IF TG_OP<>'DELETE' THEN new_record:=to_jsonb(NEW); END IF;

  -- 归档/恢复只改归档元数据和父级版本；任何夹带的业务字段变更仍走只读检查。
  IF TG_TABLE_NAME IN ('exam','exam_session') AND TG_OP='UPDATE'
    AND (old_record - ARRAY['archived_at','archived_by','archive_reason','revision','updated_at'])
       = (new_record - ARRAY['archived_at','archived_by','archive_reason','revision','updated_at']) THEN
    IF TG_TABLE_NAME='exam' AND old_record->>'exam_session_id' IS NOT NULL THEN
      SELECT archived_at IS NOT NULL OR status='archived' INTO parent_archived
      FROM exam_session WHERE tenant_id=(old_record->>'tenant_id')::uuid
        AND id=(old_record->>'exam_session_id')::uuid FOR SHARE;
      IF parent_archived THEN
        RAISE EXCEPTION 'exam_archived: subject metadata cannot override whole exam archive' USING ERRCODE='55000';
      END IF;
    END IF;
    RETURN NEW;
  END IF;

  IF TG_TABLE_NAME='exam_session' THEN
    SELECT COALESCE(array_agg(id ORDER BY id),'{}') INTO exam_ids FROM exam
    WHERE tenant_id=(COALESCE(new_record,old_record)->>'tenant_id')::uuid
      AND exam_session_id::text=COALESCE(new_record,old_record)->>'id';
    IF old_record->>'archived_at' IS NOT NULL OR old_record->>'status'='archived' THEN
      RAISE EXCEPTION 'exam_archived: whole exam is read-only' USING ERRCODE='55000';
    END IF;
  ELSE
    IF old_record IS NOT NULL THEN exam_ids:=exam_ids || exam_archive_row_exams(TG_RELID,old_record); END IF;
    IF new_record IS NOT NULL THEN exam_ids:=exam_ids || exam_archive_row_exams(TG_RELID,new_record); END IF;
  END IF;

  FOR target_exam IN SELECT DISTINCT value FROM unnest(exam_ids) value ORDER BY value LOOP
    PERFORM exam_archive_require_mutable((COALESCE(new_record,old_record)->>'tenant_id')::uuid,target_exam);
  END LOOP;
  IF TG_TABLE_NAME='exam' AND new_record->>'exam_session_id' IS NOT NULL THEN
    SELECT archived_at IS NOT NULL OR status='archived' INTO parent_archived
    FROM exam_session WHERE tenant_id=(new_record->>'tenant_id')::uuid
      AND id=(new_record->>'exam_session_id')::uuid FOR SHARE;
    IF parent_archived THEN
      RAISE EXCEPTION 'exam_archived: cannot add subject to archived exam' USING ERRCODE='55000';
    END IF;
  END IF;
  RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
END $$;

-- agent_job 与 orchestration_run 通过 target_type 关联；不能仅靠声明式外键枚举。
INSERT INTO exam_archive_guarded_table VALUES ('agent_job'::regclass),('orchestration_run'::regclass),('agent_task'::regclass)
ON CONFLICT DO NOTHING;
DO $$ DECLARE target REGCLASS; BEGIN
  FOR target IN SELECT relation FROM exam_archive_guarded_table UNION SELECT 'exam_session'::regclass LOOP
    EXECUTE format('CREATE TRIGGER trg_exam_archive_readonly BEFORE INSERT OR UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION reject_archived_exam_business_mutation()',target);
  END LOOP;
END $$;
