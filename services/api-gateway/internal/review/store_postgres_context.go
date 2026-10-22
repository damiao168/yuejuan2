package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func (s *PostgresStore) loadContext(ctx context.Context, tenantID string, segmentID string) (Context, error) {
	return s.loadContextScanner(ctx, s.db, tenantID, segmentID)
}

func (s *PostgresStore) loadContextTx(ctx context.Context, tx *sql.Tx, tenantID string, segmentID string) (Context, error) {
	return s.loadContextScanner(ctx, tx, tenantID, segmentID)
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// 当前可采纳的建议及最多 20 条历史只取同一答案、快照和冻结量规；旧版本仍保存在成绩管理历史中。
// 图像链以当前有效计划的 input_version 为答案身份，不能拿旧 OCR 答案 ID 过滤或展示它的建议。
// 旧建议的 nil 列表曾保存为 JSON null；读取时仅将 null 转为空数组，其它错误类型保留给契约校验拒绝。
// 历史单模型没有 panel_execution；只在原记录存在时投影，避免补成 null 后被误判为汇总失败。
func (s *PostgresStore) loadContextScanner(ctx context.Context, q queryer, tenantID string, segmentID string) (Context, error) {
	row := q.QueryRowContext(ctx, `
SELECT
  sub.exam_id::text,
  sub.id::text,
  COALESCE(NULLIF(sub.candidate_no, ''), seg.id::text),
  seg.id::text,
  to_jsonb(eqs),
  q.id::text, q.tenant_id::text, q.exam_id::text, COALESCE(q.exam_paper_id::text, ''),
  q.question_no, q.question_type, q.score::float8, COALESCE(q.stem, ''),
  q.knowledge_points, q.answer_area, q.sort_order, q.status,
  COALESCE(qr.id::text, ''), COALESCE(qr.question_id::text, ''), COALESCE(rv.version, ''),
  COALESCE(qr.status, ''), COALESCE(qr.max_score::float8, 0), COALESCE(qr.points, '[]'::jsonb),
  COALESCE(qr.deductions, '[]'::jsonb), COALESCE(qr.examples, '[]'::jsonb),
  CASE WHEN image_input.input_version_id IS NOT NULL THEN '' ELSE COALESCE(ans.answer_text, '') END,
  CASE WHEN image_input.input_version_id IS NOT NULL THEN 'image' ELSE COALESCE(ans.source, '') END,
  COALESCE(ag.ai_suggestion, 'null'::jsonb),
  q.options, to_jsonb(reference_key), to_jsonb(reference_solution), COALESCE(image_input.input_version_id::text, ans.id::text, '')
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id = seg.tenant_id AND sub.id = seg.submission_id
JOIN question q ON q.tenant_id = seg.tenant_id AND q.id = seg.question_id
JOIN LATERAL (
  SELECT * FROM exam_question_snapshot snapshot
  WHERE snapshot.tenant_id=q.tenant_id AND snapshot.exam_id=q.exam_id AND snapshot.question_id=q.id
  ORDER BY snapshot.snapshot_version DESC, snapshot.id DESC LIMIT 1
) eqs ON true
LEFT JOIN LATERAL (
  SELECT id,question_id,answer_version,standard_answer,equivalent_answers,tolerance
  FROM question_answer_key
  WHERE tenant_id=q.tenant_id AND question_id=q.id AND deleted_at IS NULL AND created_at<=eqs.created_at
  ORDER BY created_at DESC,id DESC LIMIT 1
) reference_key ON true
LEFT JOIN LATERAL (
  SELECT id,question_id,solution_version,raw_text,steps,source_refs,verification_status
  FROM question_solution
  WHERE tenant_id=q.tenant_id AND question_id=q.id AND deleted_at IS NULL AND created_at<=eqs.created_at
  ORDER BY created_at DESC,id DESC LIMIT 1
) reference_solution ON true
LEFT JOIN LATERAL (
  SELECT id, question_id, status, max_score, points, deductions, examples, rubric_version_id
  FROM question_rubric
  WHERE tenant_id = q.tenant_id AND question_id = q.id AND deleted_at IS NULL
  ORDER BY created_at DESC
  LIMIT 1
) qr ON true
LEFT JOIN rubric_version rv ON rv.tenant_id = q.tenant_id AND rv.id = qr.rubric_version_id
LEFT JOIN LATERAL (
  SELECT id, answer_text, source
  FROM answer_segment_answer
  WHERE tenant_id = seg.tenant_id AND answer_segment_id = seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC, id DESC
  LIMIT 1
) ans ON true
LEFT JOIN LATERAL (
  SELECT item.input_version_id,item.subjective_run_id,iv.crop_sha256,ai.input_protocol,ai.status AS run_status
  FROM scoring_run_item item
  JOIN scoring_run run ON run.tenant_id=item.tenant_id AND run.id=item.scoring_run_id
    AND run.status NOT IN ('cancelling','cancelled') AND run.deleted_at IS NULL
  JOIN answer_input_version iv ON iv.tenant_id=item.tenant_id AND iv.id=item.input_version_id
    AND iv.crop_file_asset_id=seg.crop_file_asset_id AND iv.crop_sha256=seg.crop_sha256
  JOIN subjective_grading_run ai ON ai.tenant_id=item.tenant_id AND ai.id=item.subjective_run_id
    AND ai.input_mode='image' AND ai.input_version_id=item.input_version_id
    AND ai.answer_segment_id=seg.id AND ai.question_id=q.id AND ai.exam_question_snapshot_id=eqs.id
    AND ai.deleted_at IS NULL
  WHERE item.tenant_id=seg.tenant_id AND item.answer_segment_id=seg.id
    AND item.route='image_ai' AND item.assessment_snapshot_id=eqs.id
    AND item.processing_status NOT IN ('superseded','cancelled')
  ORDER BY run.created_at DESC,item.plan_generation DESC LIMIT 1
) image_input ON true
LEFT JOIN LATERAL (
  SELECT (array_agg(ai_suggestion ORDER BY created_at DESC, id DESC))[1]
    || jsonb_build_object('suggestion_history',jsonb_agg(ai_suggestion ORDER BY created_at DESC, id DESC)) AS ai_suggestion
  FROM (
  SELECT id,created_at,jsonb_build_object(
    'id', id::text,
    'ai_grade_id', id::text,
    'tenant_id', tenant_id::text,
    'answer_segment_id', answer_segment_id::text,
    'question_id', question_id::text,
    'exam_question_snapshot_id', exam_question_snapshot_id::text,
    'question_no', question_no,
    'question_type', question_type,
    'answer_version', answer_version,
    'input_binding', CASE WHEN image_input.input_version_id IS NOT NULL THEN jsonb_build_object(
      'input_mode','image','input_protocol',image_input.input_protocol,
      'input_version_id',image_input.input_version_id::text,'crop_sha256',image_input.crop_sha256,
      'assessment_snapshot_id',eqs.id::text,'answer_segment_id',seg.id::text,'question_id',q.id::text,
      'subjective_run_id',image_input.subjective_run_id::text
    ) ELSE NULL END,
    'raw_output', CASE WHEN image_input.input_version_id IS NOT NULL THEN jsonb_build_object(
      'schema_version',raw_output->'schema_version','input_version_id',raw_output->'input_version_id',
      'assessment_snapshot_id',raw_output->'assessment_snapshot_id',
      'observation',raw_output->'observation','judgments',raw_output->'judgments',
      'score_prefill_allowed',raw_output->'score_prefill_allowed',
      'scoring_basis',COALESCE(raw_output->'scoring_basis',(
        SELECT jsonb_build_object(
          'standard_answer',basis_run.image_context_json->'ReferenceContext'->'standard_answer',
          'solution_text',basis_run.image_context_json->'ReferenceContext'->'solution_text',
          'rubric_points',basis_run.image_context_json->'Rubric'->'points')
        FROM subjective_grading_run basis_run
        WHERE basis_run.tenant_id=ai_grade.tenant_id AND basis_run.id=ai_grade.subjective_grading_run_id
          AND basis_run.input_version_id::text=ai_grade.answer_version AND basis_run.deleted_at IS NULL
      ))
    ) || CASE WHEN raw_output ? 'panel_execution' THEN jsonb_build_object('panel_execution',raw_output->'panel_execution') ELSE '{}'::jsonb END ELSE NULL END,
    'grader_type', grader_type,
    'rule_version', rule_version,
    'model_version', model_version,
    'prompt_version', prompt_version,
    'rubric_version', rubric_version,
    'math_artifact_id', COALESCE(math_artifact_id::text,''),
    'math_artifact_version', math_artifact_version,
    'math_correction_revision', math_correction_revision,
    'math_scoring_version', math_scoring_version,
    'delivery_mode', delivery_mode,
    'suggested_score', suggested_score,
    'max_score', max_score,
    'confidence', confidence,
    'matched_points', CASE WHEN matched_points='null'::jsonb THEN '[]'::jsonb ELSE matched_points END,
    'missing_points', CASE WHEN missing_points='null'::jsonb THEN '[]'::jsonb ELSE missing_points END,
    'evidence', CASE WHEN evidence='null'::jsonb THEN '[]'::jsonb ELSE evidence END,
    'risk_flags', CASE WHEN risk_flags='null'::jsonb THEN '[]'::jsonb ELSE risk_flags END,
    'needs_human_review', needs_human_review,
    'auto_pass', auto_pass,
    'mock', mock,
    'status', status,
    'failure_reason', COALESCE(failure_reason, ''),
    'student_feedback', student_feedback,
    'teacher_note', teacher_note,
    'created_by', created_by::text,
    'created_at', created_at
  ) AS ai_suggestion
  FROM ai_grade
  WHERE tenant_id = seg.tenant_id AND answer_segment_id = seg.id AND question_id=q.id AND deleted_at IS NULL
    AND answer_version=COALESCE(image_input.input_version_id::text, ans.id::text) AND exam_question_snapshot_id=eqs.id
    AND rubric_version=eqs.rubric_snapshot_json->>'version'
    AND (image_input.input_version_id IS NULL OR (subjective_grading_run_id=image_input.subjective_run_id AND status='succeeded' AND image_input.run_status='succeeded'))
  ORDER BY created_at DESC, id DESC
  LIMIT 20
  ) suggestions
) ag ON true
WHERE seg.tenant_id = $1 AND seg.id::text = $2 AND seg.deleted_at IS NULL AND sub.deleted_at IS NULL
`, tenantID, segmentID)
	var out Context
	var snapshotRaw []byte
	var question paper.Question
	var kpRaw, areaRaw []byte
	var rubric paper.Rubric
	var pointsRaw, deductionsRaw, examplesRaw []byte
	var answerSource string
	var aiSuggestionRaw []byte
	var optionsRaw, referenceKeyRaw, referenceSolutionRaw []byte
	if err := row.Scan(
		&out.ExamID,
		&out.SubmissionID,
		&out.AnonymousCode,
		&out.AnswerSegmentID,
		&snapshotRaw,
		&question.ID,
		&question.TenantID,
		&question.ExamID,
		&question.ExamPaperID,
		&question.QuestionNo,
		&question.QuestionType,
		&question.Score,
		&question.Stem,
		&kpRaw,
		&areaRaw,
		&question.SortOrder,
		&question.Status,
		&rubric.ID,
		&rubric.QuestionID,
		&rubric.Version,
		&rubric.Status,
		&rubric.MaxScore,
		&pointsRaw,
		&deductionsRaw,
		&examplesRaw,
		&out.RawAnswer,
		&answerSource,
		&aiSuggestionRaw,
		&optionsRaw, &referenceKeyRaw, &referenceSolutionRaw, &out.AnswerVersion,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Context{}, ErrNotFound
		}
		return Context{}, err
	}
	if err := decodeJSONB(snapshotRaw, &out.AssessmentSnapshot, "exam_question_snapshot"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(kpRaw, &question.KnowledgePoints, "question.knowledge_points"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(areaRaw, &question.AnswerArea, "question.answer_area"); err != nil {
		return Context{}, err
	}
	// 题干和选项来自 ready 后不可编辑的考试题目；参考答案和解析只取快照形成前的历史版本。
	// 后续更正生成的新资料不能悄悄替换当前任务的阅卷依据；旧资料缺失时如实返回空值。
	if err := decodeJSONB(optionsRaw, &question.Options, "question.options"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(referenceKeyRaw, &question.AnswerKey, "question.answer_key"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(referenceSolutionRaw, &question.Solution, "question.solution"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(pointsRaw, &rubric.Points, "rubric.points"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(deductionsRaw, &rubric.Deductions, "rubric.deductions"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(examplesRaw, &rubric.Examples, "rubric.examples"); err != nil {
		return Context{}, err
	}
	// 工作台与评分服务都选当前最新冻结快照；量规仍从该快照读取，不能换成后来编辑的题库细则。
	rubric = taskFrozenRubric(out.AssessmentSnapshot.RubricSnapshot, question.ID, question.Score)
	if answerSource == "ocr_text" {
		out.OCRText = out.RawAnswer
	}
	if err := decodeJSONB(aiSuggestionRaw, &out.AISuggestion, "ai_grade.ai_suggestion"); err != nil {
		return Context{}, err
	}
	if automationResult, loadErr := loadAutomationResult(ctx, q, tenantID, segmentID); loadErr != nil {
		return Context{}, loadErr
	} else {
		out.AutomationResult = automationResult
	}
	out.Question = question
	out.Rubric = rubric
	return out, nil
}

func frozenRubric(snapshot map[string]any, questionID string) (paper.Rubric, bool) {
	if len(snapshot) == 0 {
		return paper.Rubric{}, false
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return paper.Rubric{}, false
	}
	var rubric paper.Rubric
	if err := json.Unmarshal(raw, &rubric); err != nil {
		return paper.Rubric{}, false
	}
	rubric.QuestionID = questionID
	return rubric, rubric.ID != "" || rubric.Version != "" || len(rubric.Points) > 0
}

func taskFrozenRubric(snapshot map[string]any, questionID string, maxScore float64) paper.Rubric {
	if rubric, ok := frozenRubric(snapshot, questionID); ok {
		return rubric
	}
	return paper.Rubric{QuestionID: questionID, Status: "missing", MaxScore: maxScore, Points: []paper.RubricPoint{}, Deductions: []any{}, Examples: []any{}}
}

// 这里汇集当前识别候选、标准答案、已发布规则和当前成绩，供异常复核展示；不是一份历史评分快照。
func loadAutomationResult(ctx context.Context, q queryer, tenantID, segmentID string) (*AutomationResult, error) {
	row := q.QueryRowContext(ctx, `
SELECT
  COALESCE(NULLIF(ac.source,''),ans.source,''),
  COALESCE(NULLIF(ac.display_text,''),ans.answer_text,''),
  COALESCE(ac.confidence,ans.confidence)::float8,
  COALESCE(ac.decision,''),
  COALESCE(ak.id::text,''),COALESCE(ak.standard_answer,'null'::jsonb),
  COALESCE(sr.rule_type,''),g.score::float8,g.max_score::float8,COALESCE(g.source,'')
FROM (SELECT 1) seed
LEFT JOIN LATERAL (
  SELECT source,display_text,confidence,decision
  FROM answer_candidate
  WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) ac ON true
LEFT JOIN LATERAL (
  SELECT source,answer_text,confidence
  FROM answer_segment_answer
  WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) ans ON true
LEFT JOIN LATERAL (
  SELECT id,standard_answer
  FROM question_answer_key
  WHERE tenant_id=$1::uuid AND question_id=(
    SELECT question_id FROM answer_segment WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
  ) AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) ak ON true
LEFT JOIN LATERAL (
  SELECT rule_type
  FROM scoring_rule
  WHERE tenant_id=$1::uuid AND question_id=(
    SELECT question_id FROM answer_segment WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
  ) AND status='published' AND deleted_at IS NULL
  ORDER BY version DESC LIMIT 1
) sr ON true
LEFT JOIN LATERAL (
  SELECT score,max_score,source
  FROM question_grade
  WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) g ON true`, tenantID, segmentID)

	var out AutomationResult
	var confidence, score, maxScore sql.NullFloat64
	var answerKeyID string
	var standardAnswerRaw []byte
	if err := row.Scan(
		&out.Source, &out.RecognizedAnswer, &confidence, &out.Decision,
		&answerKeyID, &standardAnswerRaw, &out.RuleType, &score, &maxScore, &out.GradeSource,
	); err != nil {
		return nil, err
	}
	if confidence.Valid {
		value := confidence.Float64
		out.Confidence = &value
	}
	if score.Valid {
		value := score.Float64
		out.Score = &value
	}
	if maxScore.Valid {
		value := maxScore.Float64
		out.MaxScore = &value
	}
	if answerKeyID != "" {
		if err := decodeJSONB(standardAnswerRaw, &out.StandardAnswer, "question_answer_key.standard_answer"); err != nil {
			return nil, err
		}
	}
	if out.Source == "" && out.RecognizedAnswer == "" && out.RuleType == "" && answerKeyID == "" {
		return nil, nil
	}
	return &out, nil
}
