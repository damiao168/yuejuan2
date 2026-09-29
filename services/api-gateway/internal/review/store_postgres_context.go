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
  COALESCE(ans.answer_text, ''), COALESCE(ans.source, ''),
  COALESCE(ag.ai_suggestion, 'null'::jsonb)
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id = seg.tenant_id AND sub.id = seg.submission_id
JOIN question q ON q.tenant_id = seg.tenant_id AND q.id = seg.question_id
JOIN exam_question_snapshot eqs ON eqs.tenant_id = q.tenant_id AND eqs.exam_id = q.exam_id AND eqs.question_id = q.id
LEFT JOIN LATERAL (
  SELECT id, question_id, status, max_score, points, deductions, examples, rubric_version_id
  FROM question_rubric
  WHERE tenant_id = q.tenant_id AND question_id = q.id AND deleted_at IS NULL
  ORDER BY created_at DESC
  LIMIT 1
) qr ON true
LEFT JOIN rubric_version rv ON rv.tenant_id = q.tenant_id AND rv.id = qr.rubric_version_id
LEFT JOIN LATERAL (
  SELECT answer_text, source
  FROM answer_segment_answer
  WHERE tenant_id = seg.tenant_id AND answer_segment_id = seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC
  LIMIT 1
) ans ON true
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
    'question_no', question_no,
    'question_type', question_type,
    'answer_version', answer_version,
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
    'matched_points', matched_points,
    'missing_points', missing_points,
    'evidence', evidence,
    'risk_flags', risk_flags,
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
  WHERE tenant_id = seg.tenant_id AND answer_segment_id = seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC
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
	if err := decodeJSONB(pointsRaw, &rubric.Points, "rubric.points"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(deductionsRaw, &rubric.Deductions, "rubric.deductions"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(examplesRaw, &rubric.Examples, "rubric.examples"); err != nil {
		return Context{}, err
	}
	// 优先用考试快照中的评分细则；快照无法还原细则时，保留前面查到的当前细则。
	if frozen, ok := frozenRubric(out.AssessmentSnapshot.RubricSnapshot, question.ID); ok {
		rubric = frozen
	}
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
