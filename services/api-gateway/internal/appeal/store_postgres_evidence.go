package appeal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) withDetails(ctx context.Context, item Appeal) (Appeal, error) {
	out := cloneAppeal(item)
	if err := s.db.QueryRowContext(ctx, `
SELECT e.name, e.subject, sg.anonymous_code
FROM exam e
JOIN submission_grade sg ON sg.tenant_id = e.tenant_id AND sg.exam_id = e.id
WHERE e.tenant_id = $1 AND e.id::text = $2 AND sg.id::text = $3
  AND e.deleted_at IS NULL AND sg.deleted_at IS NULL
`, item.TenantID, item.ExamID, item.SubmissionGradeID).Scan(&out.ExamName, &out.Subject, &out.AnonymousCode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Appeal{}, ErrNotFound
		}
		return Appeal{}, err
	}
	adjustments, err := s.listAdjustments(ctx, item.TenantID, item.ID)
	if err != nil {
		return Appeal{}, err
	}
	out.Adjustments = adjustments
	if item.FinalGradeID == "" {
		return out, nil
	}
	evidence, err := s.loadEvidence(ctx, item.TenantID, item.FinalGradeID)
	if err != nil {
		return Appeal{}, err
	}
	out.Evidence = &evidence
	return out, nil
}

// 旧流程展示当前成绩与最近批阅证据，不是成绩发布时冻结的证据快照。
func (s *PostgresStore) loadEvidence(ctx context.Context, tenantID string, finalGradeID string) (AppealEvidence, error) {
	var ev AppealEvidence
	var answerSegmentID string
	if err := s.db.QueryRowContext(ctx, `
SELECT answer_segment_id::text,
  jsonb_build_object('id', id::text, 'score', score, 'max_score', max_score, 'source', source, 'status', status, 'locked', locked, 'question_no', question_no)
FROM final_grade
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, finalGradeID).Scan(&answerSegmentID, jsonMapScanner(&ev.FinalGrade)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AppealEvidence{}, ErrNotFound
		}
		return AppealEvidence{}, err
	}
	var answerSource string
	err := s.db.QueryRowContext(ctx, `
SELECT answer_text, source
FROM answer_segment_answer
WHERE tenant_id = $1 AND answer_segment_id::text = $2 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT 1
`, tenantID, answerSegmentID).Scan(&ev.RawAnswer, &answerSource)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AppealEvidence{}, err
	}
	if answerSource == "ocr_text" {
		ev.OCRText = ev.RawAnswer
	}
	var ocrText sql.NullString
	if err = s.db.QueryRowContext(ctx, `
SELECT string_agg(orx.text, E'\n' ORDER BY orx.created_at)
FROM ocr_result orx
JOIN answer_segment seg ON seg.tenant_id = orx.tenant_id AND seg.submission_page_id = orx.submission_page_id
WHERE orx.tenant_id = $1 AND seg.id::text = $2 AND orx.deleted_at IS NULL
`, tenantID, answerSegmentID).Scan(&ocrText); err != nil {
		return AppealEvidence{}, err
	}
	if ocrText.Valid && ocrText.String != "" {
		ev.OCRText = ocrText.String
	}
	aiGrades, err := queryJSONMaps(ctx, s.db, `
SELECT jsonb_build_object(
  'id', id::text,
  'grader_type', grader_type,
  'suggested_score', suggested_score,
  'max_score', max_score,
  'confidence', confidence,
  'risk_flags', risk_flags,
  'mock', mock,
  'status', status
)
FROM ai_grade
WHERE tenant_id = $1 AND answer_segment_id::text = $2 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT 5
`, tenantID, answerSegmentID)
	if err != nil {
		return AppealEvidence{}, err
	}
	ev.AIGrades = aiGrades
	humanGrades, err := queryJSONMaps(ctx, s.db, `
SELECT jsonb_build_object(
  'id', hg.id::text,
  'review_task_id', hg.review_task_id::text,
  'reviewer_id', hg.reviewer_id::text,
  'score', hg.score,
  'max_score', hg.max_score,
  'grade_round', hg.grade_round,
  'comments', hg.comments
)
FROM human_grade hg
WHERE hg.tenant_id = $1 AND hg.answer_segment_id::text = $2 AND hg.deleted_at IS NULL
ORDER BY hg.created_at DESC
LIMIT 5
`, tenantID, answerSegmentID)
	if err != nil {
		return AppealEvidence{}, err
	}
	ev.HumanGrades = humanGrades
	err = s.db.QueryRowContext(ctx, `
SELECT jsonb_build_object(
  'id', qr.id::text,
  'question_id', qr.question_id::text,
  'status', qr.status,
  'max_score', qr.max_score,
  'points', qr.points,
  'deductions', qr.deductions
)
FROM question_rubric qr
JOIN final_grade fg ON fg.tenant_id = qr.tenant_id AND fg.question_id = qr.question_id
WHERE fg.tenant_id = $1 AND fg.id::text = $2 AND qr.deleted_at IS NULL
ORDER BY qr.created_at DESC
LIMIT 1
`, tenantID, finalGradeID).Scan(jsonMapScanner(&ev.Rubric))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AppealEvidence{}, err
	}
	return ev, nil
}

func queryJSONMaps(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string, args ...any) ([]map[string]any, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item map[string]any
		_ = json.Unmarshal(raw, &item)
		out = append(out, item)
	}
	return out, rows.Err()
}

func jsonMapScanner(target *map[string]any) sql.Scanner {
	return jsonMapScanFunc(func(raw []byte) error {
		if len(raw) == 0 {
			*target = nil
			return nil
		}
		return json.Unmarshal(raw, target)
	})
}

type jsonMapScanFunc func([]byte) error

func (f jsonMapScanFunc) Scan(src any) error {
	switch value := src.(type) {
	case nil:
		return f(nil)
	case []byte:
		return f(value)
	case string:
		return f([]byte(value))
	default:
		return nil
	}
}
