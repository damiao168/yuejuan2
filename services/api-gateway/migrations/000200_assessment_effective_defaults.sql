-- 只替换今后的默认解析和物化规则，不回填已有人工配置或已冻结快照。
-- 准备预览与 ready 触发器必须读取同一有效方案；确认事务仍比较冻结后的完整事实，变化时整体回滚。
CREATE OR REPLACE FUNCTION assessment_resolve_exam_stage(p_tenant_id UUID, p_exam_id UUID)
RETURNS TABLE (education_stage TEXT, blocking_reason TEXT)
LANGUAGE sql STABLE
AS $$
  WITH exam_context AS (
    SELECT e.* FROM exam e
    WHERE e.tenant_id=p_tenant_id AND e.id=p_exam_id AND e.deleted_at IS NULL
  ), grade_refs AS (
    SELECT session.grade_id
    FROM exam_context e
    LEFT JOIN exam_session session ON session.tenant_id=e.tenant_id AND session.id=e.exam_session_id
      AND session.school_id=e.school_id AND session.deleted_at IS NULL
    WHERE e.exam_session_id IS NOT NULL
    UNION
    SELECT cls.grade_id
    FROM exam_context e
    JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL
    LEFT JOIN school_class cls ON cls.tenant_id=ec.tenant_id AND cls.id=ec.class_id
      AND cls.school_id=e.school_id AND cls.deleted_at IS NULL
  ), grade_stages AS (
    SELECT grade.id,
      NULLIF(btrim(grade.education_stage),'') AS declared_stage,
      CASE WHEN grade.level_no BETWEEN 7 AND 9 THEN 'junior'
        WHEN grade.level_no BETWEEN 10 AND 12 THEN 'senior' END AS numbered_stage
    FROM grade_refs ref
    CROSS JOIN exam_context e
    LEFT JOIN grade ON grade.tenant_id=e.tenant_id AND grade.id=ref.grade_id
      AND grade.school_id=e.school_id AND grade.deleted_at IS NULL
  ), resolved AS (
    SELECT count(*) AS grade_count,
      count(*) FILTER (WHERE id IS NULL OR COALESCE(declared_stage,numbered_stage) IS NULL) AS missing_count,
      count(DISTINCT COALESCE(declared_stage,numbered_stage)) AS stage_count,
      bool_or(declared_stage IS NOT NULL AND numbered_stage IS NOT NULL AND declared_stage<>numbered_stage) AS field_conflict,
      min(COALESCE(declared_stage,numbered_stage)) AS stage
    FROM grade_stages
  )
  SELECT CASE WHEN grade_count>0 AND missing_count=0 AND stage_count=1 AND NOT COALESCE(field_conflict,false)
    THEN stage END,
    CASE WHEN grade_count=0 OR missing_count>0 THEN '考试年级的学段缺失，请在考试基本信息核对关联年级和选班'
      WHEN stage_count<>1 OR COALESCE(field_conflict,false) THEN '考试关联年级或选班的学段冲突，请在考试基本信息核对年级学段和年级序号'
      ELSE '' END
  FROM resolved
$$;

-- education_stage 支持校内按 1–3 编排的年级；只有 7–12 的全局序号参与学段交叉核对。
-- 显式配置不依赖默认学段解析，避免已保存的人工、双评方案被缺失的选班信息替换或阻断。
CREATE OR REPLACE FUNCTION assessment_effective_question_config(p_tenant_id UUID, p_exam_id UUID, p_question_id UUID)
RETURNS TABLE (
  subject_profile_id UUID, archetype_code TEXT, allowed_evidence_types JSONB, risk_tier TEXT,
  scoring_policy_json JSONB, profile_snapshot_json JSONB, archetype_snapshot_json JSONB,
  source TEXT, blocking_reason TEXT, blocking_section TEXT
)
LANGUAGE sql STABLE
AS $$
  WITH question_context AS (
    SELECT q.*, e.grading_mode,
      CASE lower(btrim(e.subject))
        WHEN 'math' THEN 'mathematics' WHEN '数学' THEN 'mathematics'
        WHEN 'politics' THEN 'ethics_politics' WHEN 'civics' THEN 'ethics_politics'
        WHEN '政治' THEN 'ethics_politics' WHEN '道德与法治' THEN 'ethics_politics' WHEN '思想政治' THEN 'ethics_politics'
        WHEN '语文' THEN 'chinese' WHEN '英语' THEN 'english' WHEN '物理' THEN 'physics'
        WHEN '化学' THEN 'chemistry' WHEN '生物' THEN 'biology' WHEN '历史' THEN 'history' WHEN '地理' THEN 'geography'
        ELSE lower(btrim(e.subject)) END AS canonical_subject
    FROM question q
    JOIN exam e ON e.tenant_id=q.tenant_id AND e.id=q.exam_id AND e.deleted_at IS NULL
    WHERE q.tenant_id=p_tenant_id AND q.exam_id=p_exam_id AND q.id=p_question_id
      AND q.deleted_at IS NULL AND q.status<>'deleted'
  )
  SELECT profile.id, archetype.code,
    COALESCE(config.allowed_evidence_types,(
      SELECT COALESCE(jsonb_agg(value ORDER BY value),'[]'::jsonb)
      FROM jsonb_array_elements_text(archetype.evidence_types_json) evidence(value)
      WHERE (profile.evidence_policy_json->'allowed_types') ? evidence.value)),
    COALESCE(config.risk_tier,'R2'),
    COALESCE(config.scoring_policy_json,jsonb_build_object(
      'mode',CASE WHEN q.grading_mode IN ('double_mark','blind_double_mark') THEN 'DUAL_HUMAN' ELSE archetype.default_scoring_mode END,
      'require_evidence',true,'human_review_below_confidence',true)),
    jsonb_build_object('id',profile.id,'code',profile.code,'education_stage',profile.education_stage,
      'subject_code',profile.subject_code,'version',profile.version,'parser_policy',profile.parser_policy_json,
      'evidence_policy',profile.evidence_policy_json,'scoring_default',profile.scoring_default_json),
    jsonb_build_object('code',archetype.code,'response_schema',archetype.response_schema_json,
      'evidence_types',archetype.evidence_types_json,'default_scoring_mode',archetype.default_scoring_mode),
    CASE WHEN config.id IS NULL THEN 'default' ELSE 'saved' END,
    CASE WHEN config.id IS NULL AND stage.blocking_reason<>'' THEN stage.blocking_reason
      WHEN profile.id IS NULL THEN '未找到匹配的启用学科模板，请在题目页核对学科和模板'
      WHEN archetype.code IS NULL THEN '题型模板缺失，请在题目页核对题型和评分方案'
      ELSE '' END,
    CASE WHEN config.id IS NULL AND stage.blocking_reason<>'' THEN 'exam'
      WHEN profile.id IS NULL OR archetype.code IS NULL THEN 'questions' ELSE '' END
  FROM question_context q
  LEFT JOIN question_assessment_config config
    ON config.tenant_id=q.tenant_id AND config.exam_id=q.exam_id AND config.question_id=q.id
  LEFT JOIN LATERAL assessment_resolve_exam_stage(q.tenant_id,q.exam_id) stage ON config.id IS NULL
  LEFT JOIN question_archetype archetype ON archetype.code=COALESCE(config.archetype_code,CASE q.question_type
    WHEN 'single_choice' THEN 'selected_response' WHEN 'multiple_choice' THEN 'selected_response' WHEN 'true_false' THEN 'selected_response'
    WHEN 'fill_blank' THEN 'exact_text' WHEN 'numeric' THEN 'numeric_expression' WHEN 'formula' THEN 'numeric_expression'
    WHEN 'calculation' THEN 'structured_steps' WHEN 'short_answer' THEN 'short_constructed'
    WHEN 'essay' THEN 'extended_response' WHEN 'discussion' THEN 'extended_response' ELSE 'structured_steps' END)
  LEFT JOIN LATERAL (
    SELECT candidate.* FROM subject_profile candidate
    WHERE candidate.tenant_id=q.tenant_id
      AND ((config.id IS NOT NULL AND candidate.id=config.subject_profile_id)
        OR (config.id IS NULL AND candidate.education_stage=stage.education_stage
          AND candidate.subject_code=q.canonical_subject AND candidate.status='active'))
    ORDER BY candidate.version DESC,candidate.id LIMIT 1
  ) profile ON true
$$;

CREATE OR REPLACE FUNCTION assessment_apply_default_question_config(p_tenant_id UUID, p_exam_id UUID, p_question_id UUID)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE effective RECORD;
BEGIN
  SELECT * INTO effective FROM assessment_effective_question_config(p_tenant_id,p_exam_id,p_question_id);
  IF NOT FOUND OR effective.source='saved' OR effective.blocking_reason<>'' THEN
    RETURN;
  END IF;
  INSERT INTO question_assessment_config (
    tenant_id,exam_id,question_id,subject_profile_id,archetype_code,
    allowed_evidence_types,risk_tier,scoring_policy_json
  ) VALUES (
    p_tenant_id,p_exam_id,p_question_id,effective.subject_profile_id,effective.archetype_code,
    effective.allowed_evidence_types,effective.risk_tier,effective.scoring_policy_json
  ) ON CONFLICT (tenant_id,exam_id,question_id) DO NOTHING;
END
$$;
