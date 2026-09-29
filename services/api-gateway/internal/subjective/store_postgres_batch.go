package subjective

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

// LoadContexts resolves an immutable batch input in one database round trip.
// Ordering follows segmentIDs so request identities and response failures remain
// stable across safe enqueue retries.
func (s *PostgresStore) LoadContexts(ctx context.Context, tenantID string, segmentIDs []string) ([]Context, error) {
	if tenantID == "" || len(segmentIDs) == 0 {
		return nil, ErrInvalidInput
	}
	rawSegmentIDs, err := json.Marshal(segmentIDs)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
WITH requested AS (
  SELECT CASE
    WHEN value ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
      THEN value::uuid
    ELSE NULL
  END AS segment_id, ordinality
  FROM jsonb_array_elements_text($2::jsonb) WITH ORDINALITY
)
SELECT
  seg.id::text, to_jsonb(eqs), seg.submission_page_id::text, seg.bbox,
  e.subject, COALESCE(cohort.grade_level, ''),
  q.id::text, q.tenant_id::text, q.exam_id::text, COALESCE(q.exam_paper_id::text, ''),
  q.question_no, q.question_type, q.score::float8, COALESCE(q.stem, ''),
  q.knowledge_points, q.answer_area, q.sort_order, q.status,
  COALESCE(qr.id::text, ''), COALESCE(qr.question_id::text, ''), COALESCE(rv.version, ''),
  COALESCE(qr.status, ''), COALESCE(qr.max_score::float8, 0), COALESCE(qr.points, '[]'::jsonb),
  COALESCE(qr.deductions, '[]'::jsonb), COALESCE(qr.examples, '[]'::jsonb),
  COALESCE(ans.id::text, ''), COALESCE(ans.answer_text, ''), ans.confidence::float8, ans.created_at,
  ref.question_snapshot, COALESCE(ref.import_snapshot_hash, '')
FROM requested
JOIN answer_segment seg ON seg.id = requested.segment_id
JOIN question q ON q.tenant_id = seg.tenant_id AND q.id = seg.question_id
JOIN LATERAL (
  SELECT * FROM exam_question_snapshot snapshot
  WHERE snapshot.tenant_id = q.tenant_id AND snapshot.exam_id = q.exam_id AND snapshot.question_id = q.id
  ORDER BY snapshot.snapshot_version DESC
  LIMIT 1
) eqs ON true
JOIN exam e ON e.tenant_id = q.tenant_id AND e.id = q.exam_id AND e.deleted_at IS NULL
LEFT JOIN LATERAL (
  SELECT CASE
    WHEN COUNT(DISTINCT g.level_no) = 1 AND MIN(g.level_no) BETWEEN 7 AND 9 THEN 'junior'
    WHEN COUNT(DISTINCT g.level_no) = 1 AND MIN(g.level_no) BETWEEN 10 AND 12 THEN 'senior'
    ELSE ''
  END AS grade_level
  FROM exam_class ec
  JOIN school_class sc ON sc.tenant_id = ec.tenant_id AND sc.id = ec.class_id AND sc.deleted_at IS NULL
  JOIN grade g ON g.tenant_id = sc.tenant_id AND g.id = sc.grade_id AND g.deleted_at IS NULL
  WHERE ec.tenant_id = q.tenant_id AND ec.exam_id = q.exam_id AND ec.deleted_at IS NULL
) cohort ON true
LEFT JOIN LATERAL (
  SELECT qr.id, qr.question_id, qr.status, qr.max_score, qr.points, qr.deductions, qr.examples, qr.rubric_version_id
  FROM question_rubric qr
  WHERE qr.tenant_id = q.tenant_id AND qr.question_id = q.id AND qr.deleted_at IS NULL
  ORDER BY qr.created_at DESC
  LIMIT 1
) qr ON true
LEFT JOIN rubric_version rv ON rv.tenant_id = q.tenant_id AND rv.id = qr.rubric_version_id
LEFT JOIN LATERAL (
  SELECT id, answer_text, confidence, created_at
  FROM answer_segment_answer
  WHERE tenant_id = seg.tenant_id AND answer_segment_id = seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC
  LIMIT 1
) ans ON true
LEFT JOIN LATERAL (
  SELECT item.value AS question_snapshot, r.import_snapshot_hash
  FROM exam_readiness_snapshot r
  CROSS JOIN LATERAL jsonb_array_elements(r.import_snapshot_json->'questions') item(value)
  WHERE r.tenant_id = q.tenant_id AND r.exam_id = q.exam_id AND r.status = 'passed'
    AND item.value->>'id' = q.id::text
    AND item.value->>'assessment_snapshot_id' = eqs.id::text
    AND item.value->>'assessment_snapshot_hash' = eqs.content_hash
  ORDER BY r.confirmed_at DESC
  LIMIT 1
) ref ON true
WHERE seg.tenant_id = $1 AND seg.deleted_at IS NULL
ORDER BY requested.ordinality
`, tenantID, rawSegmentIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	contexts := make([]Context, 0, len(segmentIDs))
	for rows.Next() {
		value, scanErr := scanBatchContext(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		contexts = append(contexts, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(contexts) != len(segmentIDs) {
		return nil, ErrNotFound
	}
	return contexts, nil
}

type contextRowsScanner interface {
	Scan(dest ...any) error
}

func scanBatchContext(row contextRowsScanner) (Context, error) {
	var out Context
	var snapshotRaw []byte
	var submissionPageID string
	var bboxRaw, knowledgePointsRaw, answerAreaRaw []byte
	var question paper.Question
	var rubric paper.Rubric
	var pointsRaw, deductionsRaw, examplesRaw []byte
	var answerID string
	var ocrConfidence sql.NullFloat64
	var answerCreated sql.NullTime
	var referenceRaw []byte
	var referenceHash string
	if err := row.Scan(
		&out.SegmentID,
		&snapshotRaw,
		&submissionPageID,
		&bboxRaw,
		&out.Subject,
		&out.GradeLevel,
		&question.ID,
		&question.TenantID,
		&question.ExamID,
		&question.ExamPaperID,
		&question.QuestionNo,
		&question.QuestionType,
		&question.Score,
		&question.Stem,
		&knowledgePointsRaw,
		&answerAreaRaw,
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
		&answerID,
		&out.AnswerText,
		&ocrConfidence,
		&answerCreated,
		&referenceRaw,
		&referenceHash,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Context{}, ErrNotFound
		}
		return Context{}, err
	}
	if err := json.Unmarshal(snapshotRaw, &out.AssessmentSnapshot); err != nil {
		return Context{}, err
	}
	reference, err := referenceContextFromSnapshot(referenceRaw, referenceHash)
	if err != nil {
		return Context{}, err
	}
	out.ReferenceContext = reference
	out.Subject = string(out.AssessmentSnapshot.SubjectCode)
	out.GradeLevel = string(out.AssessmentSnapshot.EducationStage)
	if !IsSupportedQuestionType(question.QuestionType) {
		return Context{}, ErrUnsupportedQuestionType
	}
	_ = json.Unmarshal(knowledgePointsRaw, &question.KnowledgePoints)
	if len(answerAreaRaw) > 0 {
		_ = json.Unmarshal(answerAreaRaw, &question.AnswerArea)
	}
	if rubric.ID == "" {
		return Context{}, ErrRubricMissing
	}
	_ = json.Unmarshal(pointsRaw, &rubric.Points)
	_ = json.Unmarshal(deductionsRaw, &rubric.Deductions)
	_ = json.Unmarshal(examplesRaw, &rubric.Examples)
	if frozen, ok := rubricFromAssessmentSnapshot(out.AssessmentSnapshot.RubricSnapshot, question.ID); ok {
		rubric = frozen
	}
	if answerID == "" {
		return Context{}, ErrAnswerMissing
	}
	out.AnswerVersion = answerID
	var bbox []float64
	_ = json.Unmarshal(bboxRaw, &bbox)
	out.Question = question
	out.Rubric = rubric
	out.AnswerImageRef = map[string]any{"submission_page_id": submissionPageID, "bbox": bbox}
	if ocrConfidence.Valid {
		value := ocrConfidence.Float64
		out.OCRConfidence = &value
	}
	if answerCreated.Valid {
		out.AnswerCreatedAt = answerCreated.Time.UTC()
	}
	return out, nil
}
