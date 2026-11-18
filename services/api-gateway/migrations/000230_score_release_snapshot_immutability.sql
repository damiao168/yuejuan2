-- 历史兼容接口只公开了工作表。把已有公开成绩冻结为迁移版本，保留原发布时间和题分口径。
-- 只处理没有当前发布的考试。迁移版本不补造质量证据、评语或未发布学生成绩。
-- 归档触发器保护日常业务；本次只补读模型，不改动任何已归档的工作表数据。
DO $$
DECLARE
  legacy RECORD;
  snapshot_id UUID;
  previous_scope TEXT := current_setting('edugrade.tenant_id', true);
BEGIN
  -- 迁移角色在强制行级策略下也要读取各租户历史；作用域只用于本段回填。
  PERFORM set_config('edugrade.tenant_id', 'maintenance', true);
  ALTER TABLE score_release DISABLE TRIGGER trg_exam_archive_readonly;
  ALTER TABLE score_release_item DISABLE TRIGGER trg_exam_archive_readonly;
  ALTER TABLE score_release_question DISABLE TRIGGER trg_exam_archive_readonly;
  ALTER TABLE score_release_current DISABLE TRIGGER trg_exam_archive_readonly;

  FOR legacy IN
    SELECT DISTINCT ON (grade.tenant_id, grade.exam_id)
      grade.tenant_id, grade.exam_id, COALESCE(grade.published_by, grade.confirmed_by, grade.created_by) AS actor_id,
      COALESCE(grade.published_at, grade.updated_at) AS published_at
    FROM submission_grade grade
    WHERE grade.status='published' AND grade.locked AND grade.deleted_at IS NULL
      AND NOT EXISTS (SELECT 1 FROM score_release_current current
        WHERE current.tenant_id=grade.tenant_id AND current.exam_id=grade.exam_id)
    ORDER BY grade.tenant_id, grade.exam_id, COALESCE(grade.published_at, grade.updated_at) DESC, grade.id
  LOOP
    INSERT INTO score_release (tenant_id,exam_id,version,source,reason,idempotency_key,visibility_policy,created_by)
    SELECT legacy.tenant_id, legacy.exam_id, COALESCE(MAX(version),0)+1, 'migration', '冻结历史公开成绩',
      'legacy-published:'||legacy.exam_id::text, '{"show_question_scores":true}', legacy.actor_id
    FROM score_release WHERE tenant_id=legacy.tenant_id AND exam_id=legacy.exam_id
    RETURNING id INTO snapshot_id;

    INSERT INTO score_release_item (tenant_id,release_id,student_id,submission_id,total_score,max_score,status,snapshot_hash)
    SELECT grade.tenant_id, snapshot_id, grade.student_id, grade.submission_id, grade.total_score, grade.max_score, 'published',
      encode(digest(jsonb_build_array(grade.id,grade.student_id,grade.submission_id,grade.total_score,grade.max_score)::text,'sha256'),'hex')
    FROM submission_grade grade
    WHERE grade.tenant_id=legacy.tenant_id AND grade.exam_id=legacy.exam_id
      AND grade.status='published' AND grade.locked AND grade.deleted_at IS NULL;

    INSERT INTO score_release_question (tenant_id,release_id,submission_id,question_id,question_no,final_grade_id,score,max_score,source_type)
    SELECT DISTINCT ON (grade.submission_id,grade.question_id)
      grade.tenant_id, snapshot_id, grade.submission_id, grade.question_id, grade.question_no,
      grade.id, grade.score, grade.max_score, grade.source
    FROM final_grade grade
    JOIN score_release_item item ON item.tenant_id=grade.tenant_id AND item.release_id=snapshot_id AND item.submission_id=grade.submission_id
    WHERE grade.tenant_id=legacy.tenant_id AND grade.exam_id=legacy.exam_id AND grade.deleted_at IS NULL
    ORDER BY grade.submission_id, grade.question_id, grade.created_at DESC, grade.id DESC;

    UPDATE score_release SET status='published', published_by=legacy.actor_id, published_at=legacy.published_at
    WHERE tenant_id=legacy.tenant_id AND id=snapshot_id;
    INSERT INTO score_release_current (tenant_id,exam_id,release_id,updated_at)
    VALUES (legacy.tenant_id,legacy.exam_id,snapshot_id,legacy.published_at);
  END LOOP;

  ALTER TABLE score_release_current ENABLE TRIGGER trg_exam_archive_readonly;
  ALTER TABLE score_release_question ENABLE TRIGGER trg_exam_archive_readonly;
  ALTER TABLE score_release_item ENABLE TRIGGER trg_exam_archive_readonly;
  ALTER TABLE score_release ENABLE TRIGGER trg_exam_archive_readonly;
  PERFORM set_config('edugrade.tenant_id', COALESCE(previous_scope, ''), true);
END $$;

CREATE FUNCTION require_draft_score_release_details()
RETURNS TRIGGER
LANGUAGE plpgsql
SET search_path = public
AS $$
DECLARE
  old_tenant UUID;
  old_release UUID;
  new_tenant UUID;
  new_release UUID;
  target RECORD;
  found_old BOOLEAN := TG_OP = 'INSERT';
  found_new BOOLEAN := TG_OP = 'DELETE';
BEGIN
  IF TG_OP <> 'INSERT' THEN
    old_tenant := OLD.tenant_id;
    old_release := OLD.release_id;
  END IF;
  IF TG_OP <> 'DELETE' THEN
    new_tenant := NEW.tenant_id;
    new_release := NEW.release_id;
  END IF;

  -- 锁住新旧发布行，阻止发布与明细写入交错。只锁定当前操作涉及的版本，按稳定顺序取得行锁。
  FOR target IN
    SELECT tenant_id, id, status FROM score_release
    WHERE (tenant_id=old_tenant AND id=old_release) OR (tenant_id=new_tenant AND id=new_release)
    ORDER BY tenant_id,id FOR SHARE
  LOOP
    IF target.status <> 'draft' THEN
      RAISE EXCEPTION 'published score release snapshot is immutable' USING ERRCODE='55000';
    END IF;
    found_old := found_old OR (target.tenant_id = old_tenant AND target.id = old_release);
    found_new := found_new OR (target.tenant_id = new_tenant AND target.id = new_release);
  END LOOP;
  -- 行级策略也可能隐藏父版本。无法验证归属时不能让明细通过，再依赖外键判定。
  IF NOT (found_old AND found_new) THEN
    RAISE EXCEPTION 'score release snapshot parent is not accessible' USING ERRCODE='23503';
  END IF;
  RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
END $$;

DROP TRIGGER trg_score_release_item_immutable ON score_release_item;
CREATE TRIGGER trg_score_release_item_immutable BEFORE INSERT OR UPDATE OR DELETE ON score_release_item
FOR EACH ROW EXECUTE FUNCTION require_draft_score_release_details();

DROP TRIGGER trg_score_release_question_immutable ON score_release_question;
CREATE TRIGGER trg_score_release_question_immutable BEFORE INSERT OR UPDATE OR DELETE ON score_release_question
FOR EACH ROW EXECUTE FUNCTION require_draft_score_release_details();
