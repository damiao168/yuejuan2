package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) RecordAnswer(ctx context.Context, tenantID string, segmentID string, actorID string, input RecordAnswerInput) (SegmentAnswer, error) {
	input = normalizeAnswerInput(input)
	if err := validateAnswerInput(input); err != nil {
		return SegmentAnswer{}, err
	}
	if err := ensureSegmentExists(ctx, s.db, tenantID, segmentID); err != nil {
		return SegmentAnswer{}, err
	}
	payload, _ := json.Marshal(input.AnswerPayload)
	var confidence any
	if input.Confidence != nil {
		confidence = *input.Confidence
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO answer_segment_answer (
  tenant_id, answer_segment_id, answer_text, answer_payload, source, confidence, recorded_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id::text, tenant_id::text, answer_segment_id::text, answer_text, answer_payload, source, confidence::float8, recorded_by::text, created_at
`, tenantID, segmentID, input.AnswerText, payload, input.Source, confidence, actorID)
	var out SegmentAnswer
	if err := scanAnswer(row, &out); err != nil {
		return SegmentAnswer{}, err
	}
	return out, nil
}

// LoadContext 使用最新记录的答案；若有已发布评分规则，其配置整体优先于答案键 tolerance，不逐字段合并。
func (s *PostgresStore) LoadContext(ctx context.Context, tenantID string, segmentID string) (Context, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT
  seg.id::text,
  to_jsonb(eqs),
  q.id::text, q.tenant_id::text, q.exam_id::text, COALESCE(q.exam_paper_id::text, ''),
  q.question_no, q.question_type, q.score::float8, COALESCE(q.stem, ''),
  q.knowledge_points, q.answer_area, q.sort_order, q.status,
  COALESCE(ak.id::text, ''), COALESCE(ak.answer_version, ''),
  COALESCE(ak.standard_answer, 'null'::jsonb),
  COALESCE(ak.equivalent_answers, '[]'::jsonb),
  COALESCE(sr.config, ak.tolerance, '{}'::jsonb),
  COALESCE(ans.id::text, ''), COALESCE(ans.answer_text, ''),
  COALESCE(ans.answer_payload, '{}'::jsonb), COALESCE(ans.source, ''),
  ans.confidence::float8, COALESCE(ans.recorded_by::text, ''), ans.created_at
FROM answer_segment seg
JOIN question q ON q.tenant_id = seg.tenant_id AND q.id = seg.question_id
JOIN exam_question_snapshot eqs ON eqs.tenant_id = q.tenant_id AND eqs.exam_id = q.exam_id AND eqs.question_id = q.id
LEFT JOIN LATERAL (
  SELECT id, answer_version, standard_answer, equivalent_answers, tolerance
  FROM question_answer_key
  WHERE tenant_id = q.tenant_id AND question_id = q.id AND deleted_at IS NULL
  ORDER BY created_at DESC
  LIMIT 1
) ak ON true
LEFT JOIN LATERAL (
  SELECT config
  FROM scoring_rule
  WHERE tenant_id = q.tenant_id AND question_id = q.id AND status = 'published' AND deleted_at IS NULL
  ORDER BY version DESC
  LIMIT 1
) sr ON true
LEFT JOIN LATERAL (
  SELECT id, answer_text, answer_payload, source, confidence, recorded_by, created_at
  FROM answer_segment_answer
  WHERE tenant_id = seg.tenant_id AND answer_segment_id = seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC
  LIMIT 1
) ans ON true
WHERE seg.tenant_id = $1 AND seg.id::text = $2 AND seg.deleted_at IS NULL
`, tenantID, segmentID)
	var segmentIDOut string
	var snapshotRaw []byte
	var result Context
	var question paper.Question
	var kpRaw, areaRaw []byte
	var keyID, keyVersion string
	var standardRaw, equivRaw, toleranceRaw []byte
	var answer SegmentAnswer
	var payloadRaw []byte
	var confidence sql.NullFloat64
	var answerCreated sql.NullTime
	if err := row.Scan(
		&segmentIDOut,
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
		&keyID,
		&keyVersion,
		&standardRaw,
		&equivRaw,
		&toleranceRaw,
		&answer.ID,
		&answer.AnswerText,
		&payloadRaw,
		&answer.Source,
		&confidence,
		&answer.RecordedBy,
		&answerCreated,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Context{}, ErrNotFound
		}
		return Context{}, err
	}
	if err := json.Unmarshal(snapshotRaw, &result.AssessmentSnapshot); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(kpRaw, &question.KnowledgePoints, "question.knowledge_points"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(areaRaw, &question.AnswerArea, "question.answer_area"); err != nil {
		return Context{}, err
	}
	if keyID == "" {
		return Context{}, ErrAnswerKeyMissing
	}
	answerKey := paper.AnswerKey{ID: keyID, QuestionID: question.ID, AnswerVersion: keyVersion}
	if err := decodeJSONB(standardRaw, &answerKey.StandardAnswer, "question_answer_key.standard_answer"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(equivRaw, &answerKey.EquivalentAnswers, "question_answer_key.equivalent_answers"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(toleranceRaw, &answerKey.Tolerance, "question_answer_key.tolerance"); err != nil {
		return Context{}, err
	}
	question.AnswerKey = &answerKey
	if answer.ID == "" {
		return Context{}, ErrAnswerMissing
	}
	answer.TenantID = tenantID
	answer.AnswerSegmentID = segmentIDOut
	answer.AnswerPayload = map[string]any{}
	if err := decodeJSONB(payloadRaw, &answer.AnswerPayload, "answer_segment_answer.answer_payload"); err != nil {
		return Context{}, err
	}
	if confidence.Valid {
		value := confidence.Float64
		answer.Confidence = &value
	}
	if answerCreated.Valid {
		answer.CreatedAt = answerCreated.Time.UTC()
	}
	result.SegmentID, result.Question, result.AnswerKey, result.Answer = segmentIDOut, question, answerKey, answer
	return result, nil
}

func (s *PostgresStore) CreateGrade(ctx context.Context, tenantID string, actorID string, grade Grade) (Grade, error) {
	if grade.AnswerSegmentID == "" || grade.QuestionID == "" {
		return Grade{}, ErrInvalidInput
	}
	matched, _ := json.Marshal(grade.MatchedPoints)
	missing, _ := json.Marshal(grade.MissingPoints)
	evidence, _ := json.Marshal(grade.Evidence)
	risks, _ := json.Marshal(grade.RiskFlags)
	raw, _ := json.Marshal(cloneMap(grade.RawOutput))
	row := s.db.QueryRowContext(ctx, `
INSERT INTO ai_grade (
  tenant_id, answer_segment_id, question_id, question_no, question_type, answer_version,
  grader_type, rule_version, suggested_score, max_score, confidence,
  matched_points, missing_points, evidence, risk_flags,
  needs_human_review, auto_pass, mock, raw_output, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, false, $18, $19)
RETURNING id::text, tenant_id::text, answer_segment_id::text, question_id::text, question_no, question_type,
  answer_version, grader_type, rule_version, suggested_score::float8, max_score::float8, confidence::float8,
  matched_points, missing_points, evidence, risk_flags, needs_human_review, auto_pass, mock, raw_output,
  created_by::text, created_at
`, tenantID, grade.AnswerSegmentID, grade.QuestionID, grade.QuestionNo, grade.QuestionType, grade.AnswerVersion,
		grade.GraderType, grade.RuleVersion, grade.SuggestedScore, grade.MaxScore, grade.Confidence,
		matched, missing, evidence, risks, grade.NeedsHumanReview, grade.AutoPass, raw, actorID)
	var out Grade
	if err := scanGrade(row, &out); err != nil {
		return Grade{}, err
	}
	return out, nil
}

func (s *PostgresStore) ListGrades(ctx context.Context, tenantID string, segmentID string) ([]Grade, error) {
	if err := ensureSegmentExists(ctx, s.db, tenantID, segmentID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, answer_segment_id::text, question_id::text, question_no, question_type,
  answer_version, grader_type, rule_version, suggested_score::float8, max_score::float8, confidence::float8,
  matched_points, missing_points, evidence, risk_flags, needs_human_review, auto_pass, mock, raw_output,
  created_by::text, created_at
FROM ai_grade
WHERE tenant_id = $1 AND answer_segment_id::text = $2 AND deleted_at IS NULL
ORDER BY created_at DESC
`, tenantID, segmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grade{}
	for rows.Next() {
		var grade Grade
		if err := scanGrade(rows, &grade); err != nil {
			return nil, err
		}
		out = append(out, grade)
	}
	return out, rows.Err()
}

func ensureSegmentExists(ctx context.Context, db *sql.DB, tenantID string, segmentID string) error {
	var exists int
	err := db.QueryRowContext(ctx, `
SELECT 1 FROM answer_segment WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, segmentID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

type answerScanner interface {
	Scan(dest ...any) error
}

func scanAnswer(row answerScanner, out *SegmentAnswer) error {
	var payloadRaw []byte
	var confidence sql.NullFloat64
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.AnswerSegmentID,
		&out.AnswerText,
		&payloadRaw,
		&out.Source,
		&confidence,
		&out.RecordedBy,
		&out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	out.AnswerPayload = map[string]any{}
	if err := decodeJSONB(payloadRaw, &out.AnswerPayload, "answer_segment_answer.answer_payload"); err != nil {
		return err
	}
	if confidence.Valid {
		value := confidence.Float64
		out.Confidence = &value
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return nil
}

type gradeScanner interface {
	Scan(dest ...any) error
}

func scanGrade(row gradeScanner, out *Grade) error {
	var matchedRaw, missingRaw, evidenceRaw, risksRaw, rawOutput []byte
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.AnswerSegmentID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.QuestionType,
		&out.AnswerVersion,
		&out.GraderType,
		&out.RuleVersion,
		&out.SuggestedScore,
		&out.MaxScore,
		&out.Confidence,
		&matchedRaw,
		&missingRaw,
		&evidenceRaw,
		&risksRaw,
		&out.NeedsHumanReview,
		&out.AutoPass,
		&out.Mock,
		&rawOutput,
		&out.CreatedBy,
		&out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := decodeJSONB(matchedRaw, &out.MatchedPoints, "ai_grade.matched_points"); err != nil {
		return err
	}
	if err := decodeJSONB(missingRaw, &out.MissingPoints, "ai_grade.missing_points"); err != nil {
		return err
	}
	if err := decodeJSONB(evidenceRaw, &out.Evidence, "ai_grade.evidence"); err != nil {
		return err
	}
	if err := decodeJSONB(risksRaw, &out.RiskFlags, "ai_grade.risk_flags"); err != nil {
		return err
	}
	out.RawOutput = map[string]any{}
	if err := decodeJSONB(rawOutput, &out.RawOutput, "ai_grade.raw_output"); err != nil {
		return err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return nil
}
