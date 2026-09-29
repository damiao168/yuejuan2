package appeal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) CreateAppeal(ctx context.Context, tenantID string, actorID string, input CreateAppealInput) (Appeal, error) {
	input = normalizeCreateInput(input)
	if err := validateCreateInput(input); err != nil {
		return Appeal{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Appeal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var submissionGradeID, submissionID string
	if err := tx.QueryRowContext(ctx, `
SELECT id::text, submission_id::text
FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND student_id::text = $3
  AND status = 'published' AND locked = true AND deleted_at IS NULL
`, tenantID, input.ExamID, input.StudentID).Scan(&submissionGradeID, &submissionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Appeal{}, ErrUnpublishedGrade
		}
		return Appeal{}, err
	}
	var final finalGradeRef
	if input.TargetType != "exam" {
		var err error
		final, err = s.getFinalGradeRefTx(ctx, tx, tenantID, input.FinalGradeID, submissionID)
		if err != nil {
			return Appeal{}, err
		}
	}
	attachment, _ := json.Marshal(input.Attachment)
	item, err := scanAppeal(tx.QueryRowContext(ctx, `
INSERT INTO appeal (
  tenant_id, exam_id, submission_id, submission_grade_id, student_id, target_type,
  final_grade_id, question_id, question_no, deduction_point_id, reason, attachment, status, created_by
)
VALUES ($1, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, NULLIF($7, '')::uuid, NULLIF($8, '')::uuid, $9, $10, $11, $12, 'submitted', $13::uuid)
RETURNING `+appealColumns()+`
`, tenantID, input.ExamID, submissionID, submissionGradeID, input.StudentID, input.TargetType,
		input.FinalGradeID, final.QuestionID, final.QuestionNo, input.DeductionPointID, input.Reason, attachment, actorID))
	if err != nil {
		return Appeal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Appeal{}, err
	}
	return s.withDetails(ctx, item)
}

func (s *PostgresStore) ListAppeals(ctx context.Context, tenantID string, filter ListFilter) ([]Appeal, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT `+appealColumns("a")+`, e.name, e.subject, sg.anonymous_code
FROM appeal a
JOIN exam e ON e.tenant_id = a.tenant_id AND e.id = a.exam_id
JOIN submission_grade sg ON sg.tenant_id = a.tenant_id AND sg.id = a.submission_grade_id
WHERE a.tenant_id = $1 AND a.deleted_at IS NULL
  AND ($2 = '' OR a.exam_id::text = $2)
  AND ($3 = '' OR a.student_id::text = $3)
  AND ($4 = '' OR a.status = $4)
  AND ($5 = '' OR a.assigned_to::text = $5)
  AND ($7 = '' OR a.created_at < $6 OR (a.created_at = $6 AND a.id::text < $7))
ORDER BY a.created_at DESC, a.id::text DESC
LIMIT NULLIF($8, 0)
`, tenantID, filter.ExamID, filter.StudentID, filter.Status, filter.AssignedTo,
		filter.CursorCreatedAt, filter.CursorID, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Appeal{}
	for rows.Next() {
		item, err := scanAppealSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetAppeal(ctx context.Context, tenantID string, id string) (Appeal, error) {
	item, err := scanAppeal(s.db.QueryRowContext(ctx, `
SELECT `+appealColumns()+`
FROM appeal
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, id))
	if err != nil {
		return Appeal{}, err
	}
	return s.withDetails(ctx, item)
}

func (s *PostgresStore) AssignAppeal(ctx context.Context, tenantID string, id string, _ string, input AssignAppealInput) (Appeal, error) {
	input.AssignedTo = strings.TrimSpace(input.AssignedTo)
	if input.AssignedTo == "" || input.ExpectedRevision <= 0 {
		return Appeal{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Appeal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var eligible bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM app_user u
  JOIN user_role ur ON ur.tenant_id = u.tenant_id AND ur.user_id = u.id AND ur.deleted_at IS NULL
  JOIN role_permission rp ON rp.tenant_id = ur.tenant_id AND rp.role_id = ur.role_id AND rp.deleted_at IS NULL
  JOIN permission p ON p.tenant_id = rp.tenant_id AND p.id = rp.permission_id AND p.deleted_at IS NULL
  WHERE u.tenant_id = $1 AND u.id::text = $2 AND u.status = 'active' AND u.deleted_at IS NULL
    AND p.code = 'appeal:work'
)
`, tenantID, input.AssignedTo).Scan(&eligible); err != nil {
		return Appeal{}, err
	}
	if !eligible {
		return Appeal{}, ErrForbidden
	}
	item, err := scanAppeal(tx.QueryRowContext(ctx, `
SELECT `+appealColumns()+`
FROM appeal
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id))
	if err != nil {
		return Appeal{}, err
	}
	if isTerminalStatus(item.Status) {
		return Appeal{}, ErrInvalidTransition
	}
	if item.Revision != input.ExpectedRevision {
		return Appeal{}, ErrRevisionConflict
	}
	// 已参与这道题原始人工批阅的教师不能再受派复核同一成绩。
	if item.FinalGradeID != "" {
		var participated bool
		if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM human_grade hg
  JOIN final_grade fg ON fg.tenant_id = hg.tenant_id AND fg.answer_segment_id = hg.answer_segment_id
  WHERE fg.tenant_id = $1 AND fg.id::text = $2 AND hg.reviewer_id::text = $3
    AND fg.deleted_at IS NULL AND hg.deleted_at IS NULL
)
`, tenantID, item.FinalGradeID, input.AssignedTo).Scan(&participated); err != nil {
			return Appeal{}, err
		}
		if participated {
			return Appeal{}, ErrForbidden
		}
	}
	item, err = scanAppeal(tx.QueryRowContext(ctx, `
UPDATE appeal
SET assigned_to = $3::uuid, status = 'under_review', revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $4 AND deleted_at IS NULL
RETURNING `+appealColumns()+`
`, tenantID, id, input.AssignedTo, input.ExpectedRevision))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Appeal{}, ErrRevisionConflict
		}
		return Appeal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Appeal{}, err
	}
	return s.withDetails(ctx, item)
}

func (s *PostgresStore) SubmitRecommendation(ctx context.Context, tenantID string, id string, actorID string, input SubmitRecommendationInput) (Appeal, error) {
	input = normalizeRecommendationInput(input)
	if err := validateRecommendationInput(input); err != nil {
		return Appeal{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Appeal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := scanAppeal(tx.QueryRowContext(ctx, `
SELECT `+appealColumns()+`
FROM appeal
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id))
	if err != nil {
		return Appeal{}, err
	}
	if item.AssignedTo != actorID {
		return Appeal{}, ErrForbidden
	}
	if isTerminalStatus(item.Status) {
		return Appeal{}, ErrInvalidTransition
	}
	if input.ExpectedRevision <= 0 || item.Revision != input.ExpectedRevision {
		return Appeal{}, ErrRevisionConflict
	}
	if input.RecommendedScore != nil {
		if item.FinalGradeID == "" {
			return Appeal{}, ErrInvalidInput
		}
		var maxScore float64
		if err := tx.QueryRowContext(ctx, `
SELECT max_score::float8
FROM final_grade
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
		`, tenantID, item.FinalGradeID).Scan(&maxScore); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Appeal{}, ErrInvalidInput
			}
			return Appeal{}, err
		}
		if *input.RecommendedScore > maxScore {
			return Appeal{}, ErrInvalidInput
		}
	}
	item, err = scanAppeal(tx.QueryRowContext(ctx, `
UPDATE appeal
SET status = 'under_review',
    teacher_recommendation = $3,
    teacher_recommendation_reason = $4,
    teacher_recommended_score = $5,
    teacher_recommendation_by = $6::uuid,
    teacher_recommendation_at = now(),
    revision = revision + 1,
    updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $7 AND deleted_at IS NULL
RETURNING `+appealColumns()+`
`, tenantID, id, input.Recommendation, input.Reason, input.RecommendedScore, actorID, input.ExpectedRevision))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Appeal{}, ErrRevisionConflict
		}
		return Appeal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Appeal{}, err
	}
	return s.withDetails(ctx, item)
}

func (s *PostgresStore) ReviewAppeal(ctx context.Context, tenantID string, id string, actorID string, input ReviewAppealInput) (Appeal, *ScoreAdjustment, error) {
	input = normalizeReviewInput(input)
	if err := validateReviewInput(input); err != nil {
		return Appeal{}, nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Appeal{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := scanAppeal(tx.QueryRowContext(ctx, `
SELECT `+appealColumns()+`
FROM appeal
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id))
	if err != nil {
		return Appeal{}, nil, err
	}
	if item.Status == "closed" {
		return Appeal{}, nil, ErrInvalidTransition
	}
	if input.ExpectedRevision <= 0 || item.Revision != input.ExpectedRevision {
		return Appeal{}, nil, ErrRevisionConflict
	}
	if !canReviewTransition(item.Status, input.Status) {
		return Appeal{}, nil, ErrInvalidTransition
	}
	var adjustment *ScoreAdjustment
	if input.Status == "score_adjusted" {
		finalID := input.FinalGradeID
		if finalID == "" {
			finalID = item.FinalGradeID
		}
		if finalID == "" || input.AdjustedScore == nil {
			return Appeal{}, nil, ErrInvalidInput
		}
		final, err := s.getFinalGradeRefTx(ctx, tx, tenantID, finalID, item.SubmissionID)
		if err != nil {
			return Appeal{}, nil, err
		}
		if *input.AdjustedScore < 0 || *input.AdjustedScore > final.MaxScore {
			return Appeal{}, nil, ErrInvalidInput
		}
		adj, err := s.applyAdjustmentTx(ctx, tx, tenantID, item, final, actorID, input.Reason, *input.AdjustedScore)
		if err != nil {
			return Appeal{}, nil, err
		}
		adjustment = &adj
		item.FinalGradeID = final.ID
		item.QuestionID = final.QuestionID
		item.QuestionNo = final.QuestionNo
	}
	item, err = scanAppeal(tx.QueryRowContext(ctx, `
UPDATE appeal
SET status = $3,
    result_reason = $4,
    assigned_to = NULLIF($5, '')::uuid,
    reviewed_by = $6::uuid,
    reviewed_at = now(),
    final_grade_id = NULLIF($7, '')::uuid,
    question_id = NULLIF($8, '')::uuid,
    question_no = $9,
    revision = revision + 1,
    updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $10
RETURNING `+appealColumns()+`
`, tenantID, id, input.Status, input.Reason, input.AssignedTo, actorID, item.FinalGradeID, item.QuestionID, item.QuestionNo, input.ExpectedRevision))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Appeal{}, nil, ErrRevisionConflict
		}
		return Appeal{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Appeal{}, nil, err
	}
	item, err = s.withDetails(ctx, item)
	return item, adjustment, err
}

func (s *PostgresStore) CloseAppeal(ctx context.Context, tenantID string, id string, actorID string, input CloseAppealInput) (Appeal, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" || input.ExpectedRevision <= 0 {
		return Appeal{}, ErrInvalidInput
	}
	item, err := scanAppeal(s.db.QueryRowContext(ctx, `
UPDATE appeal
SET status = 'closed', result_reason = $3, closed_by = $4::uuid, closed_at = now(),
    revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $5 AND status <> 'closed' AND deleted_at IS NULL
RETURNING `+appealColumns()+`
`, tenantID, id, input.Reason, actorID, input.ExpectedRevision))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			var currentStatus string
			var currentRevision int64
			lookupErr := s.db.QueryRowContext(ctx, `SELECT status, revision FROM appeal WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL`, tenantID, id).Scan(&currentStatus, &currentRevision)
			if errors.Is(lookupErr, sql.ErrNoRows) {
				return Appeal{}, ErrNotFound
			}
			if lookupErr != nil {
				return Appeal{}, lookupErr
			}
			if currentRevision != input.ExpectedRevision {
				return Appeal{}, ErrRevisionConflict
			}
			return Appeal{}, ErrInvalidTransition
		}
		return Appeal{}, err
	}
	return s.withDetails(ctx, item)
}

func (s *PostgresStore) Statistics(ctx context.Context, tenantID string, filter StatisticsFilter) (AppealStatistics, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT status, COUNT(*)
FROM appeal
WHERE tenant_id = $1 AND deleted_at IS NULL AND ($2 = '' OR exam_id::text = $2)
GROUP BY status
`, tenantID, filter.ExamID)
	if err != nil {
		return AppealStatistics{}, err
	}
	defer rows.Close()
	out := AppealStatistics{ByStatus: map[string]int{}}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return AppealStatistics{}, err
		}
		out.ByStatus[status] = count
		out.Total += count
	}
	if err := rows.Err(); err != nil {
		return AppealStatistics{}, err
	}
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM score_adjustment sa
JOIN appeal a ON a.tenant_id = sa.tenant_id AND a.id = sa.appeal_id
WHERE sa.tenant_id = $1 AND sa.deleted_at IS NULL AND a.deleted_at IS NULL AND ($2 = '' OR a.exam_id::text = $2)
`, tenantID, filter.ExamID).Scan(&out.ScoreAdjustedCount); err != nil {
		return AppealStatistics{}, err
	}
	var avg sql.NullFloat64
	if err := s.db.QueryRowContext(ctx, `
SELECT AVG(EXTRACT(EPOCH FROM (reviewed_at - created_at)) / 3600.0)
FROM appeal
WHERE tenant_id = $1 AND deleted_at IS NULL AND reviewed_at IS NOT NULL AND ($2 = '' OR exam_id::text = $2)
`, tenantID, filter.ExamID).Scan(&avg); err != nil {
		return AppealStatistics{}, err
	}
	if avg.Valid {
		out.AverageHandleHours = avg.Float64
	}
	return out, nil
}

type finalGradeRef struct {
	ID              string
	QuestionID      string
	QuestionNo      string
	AnswerSegmentID string
	SubmissionID    string
	Score           float64
	MaxScore        float64
}

func (s *PostgresStore) getFinalGradeRefTx(ctx context.Context, tx *sql.Tx, tenantID string, finalGradeID string, submissionID string) (finalGradeRef, error) {
	var out finalGradeRef
	if err := tx.QueryRowContext(ctx, `
SELECT id::text, question_id::text, question_no, answer_segment_id::text, submission_id::text, score::float8, max_score::float8
FROM final_grade
WHERE tenant_id = $1 AND id::text = $2 AND submission_id::text = $3 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, finalGradeID, submissionID).Scan(
		&out.ID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.AnswerSegmentID,
		&out.SubmissionID,
		&out.Score,
		&out.MaxScore,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finalGradeRef{}, ErrInvalidInput
		}
		return finalGradeRef{}, err
	}
	return out, nil
}

// 调分流水、单题分数和总分增量共用裁决事务；任何一步失败都不能留下部分调分。
func (s *PostgresStore) applyAdjustmentTx(ctx context.Context, tx *sql.Tx, tenantID string, item Appeal, final finalGradeRef, actorID string, reason string, adjustedScore float64) (ScoreAdjustment, error) {
	previous := final.Score
	delta := adjustedScore - previous
	adjustment, err := scanAdjustment(tx.QueryRowContext(ctx, `
INSERT INTO score_adjustment (
  tenant_id, appeal_id, exam_id, submission_id, submission_grade_id, final_grade_id,
  question_id, question_no, previous_score, adjusted_score, delta, reason, adjusted_by
)
VALUES ($1, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7::uuid, $8, $9, $10, $11, $12, $13::uuid)
RETURNING id::text, tenant_id::text, appeal_id::text, exam_id::text, submission_id::text,
  submission_grade_id::text, final_grade_id::text, question_id::text, question_no,
  previous_score::float8, adjusted_score::float8, delta::float8, reason, adjusted_by::text, created_at
`, tenantID, item.ID, item.ExamID, item.SubmissionID, item.SubmissionGradeID, final.ID,
		final.QuestionID, final.QuestionNo, previous, adjustedScore, delta, reason, actorID))
	if err != nil {
		return ScoreAdjustment{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE final_grade
SET score = $4, status = 'locked', locked = true, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND submission_id::text = $3
`, tenantID, final.ID, item.SubmissionID, adjustedScore); err != nil {
		return ScoreAdjustment{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE submission_grade
SET total_score = total_score + $4, status = 'published', locked = true, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND submission_id::text = $3
`, tenantID, item.SubmissionGradeID, item.SubmissionID, delta); err != nil {
		return ScoreAdjustment{}, err
	}
	return adjustment, nil
}

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

func (s *PostgresStore) listAdjustments(ctx context.Context, tenantID string, appealID string) ([]ScoreAdjustment, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, appeal_id::text, exam_id::text, submission_id::text,
  submission_grade_id::text, final_grade_id::text, question_id::text, question_no,
  previous_score::float8, adjusted_score::float8, delta::float8, reason, adjusted_by::text, created_at
FROM score_adjustment
WHERE tenant_id = $1 AND appeal_id::text = $2 AND deleted_at IS NULL
ORDER BY created_at DESC
`, tenantID, appealID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScoreAdjustment{}
	for rows.Next() {
		item, err := scanAdjustment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func appealColumns(aliases ...string) string {
	prefix := ""
	if len(aliases) > 0 && aliases[0] != "" {
		prefix = aliases[0] + "."
	}
	return prefix + `id::text, ` + prefix + `tenant_id::text, ` + prefix + `exam_id::text, ` + prefix + `submission_id::text, ` + prefix + `submission_grade_id::text,
  ` + prefix + `student_id::text, ` + prefix + `target_type, COALESCE(` + prefix + `final_grade_id::text, ''), COALESCE(` + prefix + `question_id::text, ''),
  ` + prefix + `question_no, ` + prefix + `deduction_point_id, ` + prefix + `reason, ` + prefix + `attachment, ` + prefix + `status, ` + prefix + `result_reason,
  COALESCE(` + prefix + `assigned_to::text, ''), ` + prefix + `teacher_recommendation, ` + prefix + `teacher_recommendation_reason,
  ` + prefix + `teacher_recommended_score::float8, COALESCE(` + prefix + `teacher_recommendation_by::text, ''), ` + prefix + `teacher_recommendation_at,
  COALESCE(` + prefix + `reviewed_by::text, ''), ` + prefix + `reviewed_at,
  COALESCE(` + prefix + `closed_by::text, ''), ` + prefix + `closed_at, ` + prefix + `revision, ` + prefix + `created_by::text, ` + prefix + `created_at, ` + prefix + `updated_at`
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAppeal(row scanner) (Appeal, error) {
	var out Appeal
	var attachmentRaw []byte
	var recommendedScore sql.NullFloat64
	var recommendationAt sql.NullTime
	var reviewedAt sql.NullTime
	var closedAt sql.NullTime
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ExamID,
		&out.SubmissionID,
		&out.SubmissionGradeID,
		&out.StudentID,
		&out.TargetType,
		&out.FinalGradeID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.DeductionPointID,
		&out.Reason,
		&attachmentRaw,
		&out.Status,
		&out.ResultReason,
		&out.AssignedTo,
		&out.Recommendation,
		&out.RecommendationReason,
		&recommendedScore,
		&out.RecommendationBy,
		&recommendationAt,
		&out.ReviewedBy,
		&reviewedAt,
		&out.ClosedBy,
		&closedAt,
		&out.Revision,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Appeal{}, ErrNotFound
		}
		return Appeal{}, err
	}
	_ = json.Unmarshal(attachmentRaw, &out.Attachment)
	if recommendedScore.Valid {
		out.RecommendedScore = &recommendedScore.Float64
	}
	if recommendationAt.Valid {
		out.RecommendationAt = &recommendationAt.Time
	}
	if reviewedAt.Valid {
		out.ReviewedAt = &reviewedAt.Time
	}
	if closedAt.Valid {
		out.ClosedAt = &closedAt.Time
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}

func scanAppealSummary(row scanner) (Appeal, error) {
	var out Appeal
	var attachmentRaw []byte
	var recommendedScore sql.NullFloat64
	var recommendationAt sql.NullTime
	var reviewedAt sql.NullTime
	var closedAt sql.NullTime
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ExamID,
		&out.SubmissionID,
		&out.SubmissionGradeID,
		&out.StudentID,
		&out.TargetType,
		&out.FinalGradeID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.DeductionPointID,
		&out.Reason,
		&attachmentRaw,
		&out.Status,
		&out.ResultReason,
		&out.AssignedTo,
		&out.Recommendation,
		&out.RecommendationReason,
		&recommendedScore,
		&out.RecommendationBy,
		&recommendationAt,
		&out.ReviewedBy,
		&reviewedAt,
		&out.ClosedBy,
		&closedAt,
		&out.Revision,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.UpdatedAt,
		&out.ExamName,
		&out.Subject,
		&out.AnonymousCode,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Appeal{}, ErrNotFound
		}
		return Appeal{}, err
	}
	_ = json.Unmarshal(attachmentRaw, &out.Attachment)
	if recommendedScore.Valid {
		out.RecommendedScore = &recommendedScore.Float64
	}
	if recommendationAt.Valid {
		out.RecommendationAt = &recommendationAt.Time
	}
	if reviewedAt.Valid {
		out.ReviewedAt = &reviewedAt.Time
	}
	if closedAt.Valid {
		out.ClosedAt = &closedAt.Time
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}

func scanAdjustment(row scanner) (ScoreAdjustment, error) {
	var out ScoreAdjustment
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.AppealID,
		&out.ExamID,
		&out.SubmissionID,
		&out.SubmissionGradeID,
		&out.FinalGradeID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.PreviousScore,
		&out.AdjustedScore,
		&out.Delta,
		&out.Reason,
		&out.AdjustedBy,
		&out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ScoreAdjustment{}, ErrNotFound
		}
		return ScoreAdjustment{}, err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return out, nil
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
