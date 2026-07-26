package score

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"time"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) FinalizeExam(ctx context.Context, tenantID string, examID string, actorID string) (FinalizeResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FinalizeResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockExamTx(ctx, tx, tenantID, examID); err != nil {
		return FinalizeResult{}, err
	}
	if locked, err := s.hasLockedGradesTx(ctx, tx, tenantID, examID); err != nil {
		return FinalizeResult{}, err
	} else if locked {
		return FinalizeResult{}, ErrInvalidTransition
	}
	if invalid, err := s.countUnexpectedSubmissionStatusesTx(ctx, tx, tenantID, examID, "pending_confirmation"); err != nil {
		return FinalizeResult{}, err
	} else if invalid > 0 {
		return FinalizeResult{}, ErrInvalidTransition
	}
	createdHuman, err := s.createFinalsFromHumanTx(ctx, tx, tenantID, examID, actorID)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("create finals from human: %w", err)
	}
	createdRule, err := s.createFinalsFromRuleTx(ctx, tx, tenantID, examID, actorID)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("create finals from rule: %w", err)
	}
	if err := s.recalculateSubmissionGradesTx(ctx, tx, tenantID, examID, actorID); err != nil {
		return FinalizeResult{}, fmt.Errorf("recalculate submission grades: %w", err)
	}
	quality, err := s.qualityReportTx(ctx, tx, tenantID, examID, false)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("quality report: %w", err)
	}
	grades, err := s.listGradesTx(ctx, tx, tenantID, examID, true)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("list grades: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return FinalizeResult{}, err
	}
	return FinalizeResult{
		Status:            "pending_confirmation",
		CreatedFinals:     int(createdHuman + createdRule),
		SubmissionGrades:  grades,
		Quality:           quality,
		AvailableStatuses: Statuses(),
	}, nil
}

func (s *PostgresStore) ListExamGrades(ctx context.Context, tenantID string, examID string) ([]SubmissionGrade, error) {
	return s.listGrades(ctx, tenantID, examID, true)
}

func (s *PostgresStore) CheckQuality(ctx context.Context, tenantID string, examID string, requirePendingPublish bool) (QualityReport, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return QualityReport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	quality, err := s.qualityReportTx(ctx, tx, tenantID, examID, requirePendingPublish)
	if err != nil {
		return QualityReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return QualityReport{}, err
	}
	return quality, nil
}

func (s *PostgresStore) ConfirmGrades(ctx context.Context, tenantID string, examID string, actorID string, _ ConfirmInput) ([]SubmissionGrade, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockExamTx(ctx, tx, tenantID, examID); err != nil {
		return nil, err
	}
	quality, err := s.qualityReportTx(ctx, tx, tenantID, examID, false)
	if err != nil {
		return nil, err
	}
	if !quality.Passed {
		return nil, ErrQualityGateFailed
	}
	count, err := s.countSubmissionGradesTx(ctx, tx, tenantID, examID)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, ErrNotFound
	}
	if invalid, err := s.countUnexpectedSubmissionStatusesTx(ctx, tx, tenantID, examID, "pending_confirmation"); err != nil {
		return nil, err
	} else if invalid > 0 {
		return nil, ErrInvalidTransition
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE submission_grade
SET status = 'confirmed', confirmed_by = $3::uuid, confirmed_at = now(), updated_at = now()
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND locked = false AND status = 'pending_confirmation'
`, tenantID, examID, actorID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE final_grade
SET status = 'confirmed', updated_at = now()
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND locked = false AND status = 'pending_confirmation'
`, tenantID, examID); err != nil {
		return nil, err
	}
	grades, err := s.listGradesTx(ctx, tx, tenantID, examID, true)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return grades, nil
}

func (s *PostgresStore) PublishGrades(ctx context.Context, tenantID string, examID string, actorID string, _ PublishInput) (PublishResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockExamTx(ctx, tx, tenantID, examID); err != nil {
		return PublishResult{}, err
	}
	if locked, err := s.hasLockedGradesTx(ctx, tx, tenantID, examID); err != nil {
		return PublishResult{}, err
	} else if locked {
		return PublishResult{}, ErrInvalidTransition
	}
	quality, err := s.qualityReportTx(ctx, tx, tenantID, examID, true)
	if err != nil {
		return PublishResult{}, err
	}
	if !quality.Passed {
		return PublishResult{Status: "blocked", Quality: quality}, ErrQualityGateFailed
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
UPDATE submission_grade
SET status = 'published', locked = true, published_by = $3::uuid, published_at = now(), updated_at = now()
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
`, tenantID, examID, actorID); err != nil {
		return PublishResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE final_grade
SET status = 'locked', locked = true, updated_at = now()
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
`, tenantID, examID); err != nil {
		return PublishResult{}, err
	}
	if err := publishExamTx(ctx, tx, tenantID, examID); err != nil {
		return PublishResult{}, err
	}
	grades, err := s.listGradesTx(ctx, tx, tenantID, examID, true)
	if err != nil {
		return PublishResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishResult{}, err
	}
	return PublishResult{
		Status:           "published",
		SubmissionGrades: grades,
		Quality:          QualityReport{Passed: true},
		PublishedAt:      now,
	}, nil
}

func (s *PostgresStore) GetStudentGrade(ctx context.Context, tenantID string, studentID string, examID string) (SubmissionGrade, error) {
	grade, err := scanSubmissionGrade(s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, submission_id::text, COALESCE(student_id::text, ''),
  anonymous_code, total_score::float8, max_score::float8, status, locked,
  COALESCE(confirmed_by::text, ''), confirmed_at, COALESCE(published_by::text, ''), published_at,
  created_by::text, created_at, updated_at
FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND student_id::text = $3 AND status = 'published' AND locked = true AND deleted_at IS NULL
`, tenantID, examID, studentID))
	if err != nil {
		return SubmissionGrade{}, err
	}
	items, err := s.listFinalGrades(ctx, tenantID, grade.SubmissionID)
	if err != nil {
		return SubmissionGrade{}, err
	}
	grade.Items = items
	return grade, nil
}

func (s *PostgresStore) ExportGradesCSV(ctx context.Context, tenantID string, examID string, actorID string) (ExportResult, error) {
	grades, err := s.listGrades(ctx, tenantID, examID, false)
	if err != nil {
		return ExportResult{}, err
	}
	if len(grades) == 0 {
		return ExportResult{}, ErrNotFound
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	exportedAt := time.Now().UTC().Format(time.RFC3339)
	watermark := fmt.Sprintf("EduGrade export tenant=%s exam=%s actor=%s at=%s", tenantID, examID, actorID, exportedAt)
	_ = writer.Write([]string{"submission_id", "student_id", "anonymous_code", "total_score", "max_score", "status", "locked", "exported_by", "exported_at", "watermark"})
	for _, grade := range grades {
		_ = writer.Write([]string{
			grade.SubmissionID,
			grade.StudentID,
			grade.AnonymousCode,
			fmt.Sprintf("%.2f", grade.TotalScore),
			fmt.Sprintf("%.2f", grade.MaxScore),
			grade.Status,
			fmt.Sprintf("%t", grade.Locked),
			actorID,
			exportedAt,
			watermark,
		})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return ExportResult{}, err
	}
	return ExportResult{
		Filename:    fmt.Sprintf("exam-%s-grades.csv", examID),
		ContentType: "text/csv; charset=utf-8",
		Content:     buf.Bytes(),
		RowCount:    len(grades),
		Watermark:   watermark,
	}, nil
}

func (s *PostgresStore) createFinalsFromHumanTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, actorID string) (int64, error) {
	result, err := tx.ExecContext(ctx, `
WITH latest_human AS (
  SELECT DISTINCT ON (rt.answer_segment_id)
    rt.tenant_id, rt.exam_id, rt.question_id, rt.question_no, rt.answer_segment_id, rt.submission_id,
    rt.anonymous_code, hg.score, hg.max_score
  FROM review_task rt
  JOIN human_grade hg ON hg.tenant_id = rt.tenant_id AND hg.review_task_id = rt.id AND hg.deleted_at IS NULL
  WHERE rt.tenant_id = $1
    AND rt.exam_id::text = $2
    AND rt.deleted_at IS NULL
    AND rt.grade_round = 'single'
    AND rt.status IN ('submitted', 'completed')
  ORDER BY rt.answer_segment_id, hg.created_at DESC
)
INSERT INTO final_grade (
  tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  score, max_score, source, status, locked, created_by
)
SELECT tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  score, max_score, 'single_review', 'pending_confirmation', false, $3::uuid
FROM latest_human lh
WHERE NOT EXISTS (
  SELECT 1 FROM final_grade fg
  WHERE fg.tenant_id = lh.tenant_id AND fg.answer_segment_id = lh.answer_segment_id AND fg.deleted_at IS NULL
)
`, tenantID, examID, actorID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *PostgresStore) createFinalsFromRuleTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, actorID string) (int64, error) {
	result, err := tx.ExecContext(ctx, `
WITH latest_rule AS (
  SELECT DISTINCT ON (ag.answer_segment_id)
    ag.tenant_id, sub.exam_id, ag.question_id, ag.question_no, ag.answer_segment_id,
    seg.submission_id, COALESCE(NULLIF(sub.candidate_no, ''), seg.id::text) AS anonymous_code,
    ag.suggested_score AS score, ag.max_score
  FROM ai_grade ag
  JOIN answer_segment seg ON seg.tenant_id = ag.tenant_id AND seg.id = ag.answer_segment_id AND seg.deleted_at IS NULL
  JOIN submission sub ON sub.tenant_id = seg.tenant_id AND sub.id = seg.submission_id AND sub.deleted_at IS NULL
  WHERE ag.tenant_id = $1
    AND sub.exam_id::text = $2
    AND ag.deleted_at IS NULL
    AND ag.grader_type = 'rule_based_objective'
    AND ag.auto_pass = true
    AND ag.needs_human_review = false
    AND ag.mock = false
    AND ag.status = 'succeeded'
  ORDER BY ag.answer_segment_id, ag.created_at DESC
)
INSERT INTO final_grade (
  tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  score, max_score, source, status, locked, created_by
)
SELECT tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  score, max_score, 'rule_auto', 'pending_confirmation', false, $3::uuid
FROM latest_rule lr
WHERE NOT EXISTS (
  SELECT 1 FROM final_grade fg
  WHERE fg.tenant_id = lr.tenant_id AND fg.answer_segment_id = lr.answer_segment_id AND fg.deleted_at IS NULL
)
`, tenantID, examID, actorID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *PostgresStore) recalculateSubmissionGradesTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, actorID string) error {
	_, err := tx.ExecContext(ctx, `
WITH latest_final AS (
  SELECT DISTINCT ON (answer_segment_id)
    tenant_id, exam_id, submission_id, anonymous_code, score, max_score
  FROM final_grade
  WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
  ORDER BY answer_segment_id, created_at DESC
),
summed AS (
  SELECT
    lf.tenant_id,
    lf.exam_id,
    lf.submission_id,
    COALESCE(sub.student_id, NULL) AS student_id,
    COALESCE(NULLIF(sub.candidate_no, ''), MIN(lf.anonymous_code)) AS anonymous_code,
    SUM(lf.score) AS total_score,
    SUM(lf.max_score) AS max_score
  FROM latest_final lf
  JOIN submission sub ON sub.tenant_id = lf.tenant_id AND sub.id = lf.submission_id AND sub.deleted_at IS NULL
  GROUP BY lf.tenant_id, lf.exam_id, lf.submission_id, sub.student_id, sub.candidate_no
)
INSERT INTO submission_grade (
  tenant_id, exam_id, submission_id, student_id, anonymous_code, total_score, max_score, status, locked, created_by
)
SELECT tenant_id, exam_id, submission_id, student_id, anonymous_code, total_score, max_score, 'pending_confirmation', false, $3::uuid
FROM summed
ON CONFLICT (tenant_id, exam_id, submission_id) DO UPDATE
SET total_score = EXCLUDED.total_score,
    max_score = EXCLUDED.max_score,
    anonymous_code = EXCLUDED.anonymous_code,
    student_id = EXCLUDED.student_id,
    status = CASE WHEN submission_grade.locked THEN submission_grade.status ELSE 'pending_confirmation' END,
    updated_at = now()
`, tenantID, examID, actorID)
	return err
}

func (s *PostgresStore) qualityReportTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, requirePendingPublish bool) (QualityReport, error) {
	issues := []QualityIssue{}
	checks := []struct {
		code    string
		message string
		query   string
	}{
		{
			code:    "unfinished_review_tasks",
			message: "there are unfinished review tasks",
			query: `SELECT COUNT(*) FROM review_task
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND status NOT IN ('submitted', 'completed')`,
		},
		{
			code:    "unfinished_arbitration_tasks",
			message: "there are unfinished arbitration tasks",
			query: `SELECT COUNT(*) FROM arbitration_task
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND status <> 'submitted'`,
		},
		{
			code:    "ocr_failed_unhandled",
			message: "there are failed OCR tasks",
			query: `SELECT COUNT(*) FROM ocr_task ot
JOIN submission sub ON sub.tenant_id = ot.tenant_id AND sub.id = ot.submission_id AND sub.deleted_at IS NULL
WHERE ot.tenant_id = $1 AND sub.exam_id::text = $2 AND ot.deleted_at IS NULL AND ot.status = 'failed'`,
		},
		{
			code:    "missing_final_grades",
			message: "there are answer segments without final grades",
			query: `SELECT COUNT(*) FROM answer_segment seg
WHERE seg.tenant_id = $1 AND EXISTS (
  SELECT 1 FROM submission sub WHERE sub.tenant_id = seg.tenant_id AND sub.id = seg.submission_id AND sub.exam_id::text = $2 AND sub.deleted_at IS NULL
)
AND seg.deleted_at IS NULL
AND NOT EXISTS (
  SELECT 1 FROM final_grade fg WHERE fg.tenant_id = seg.tenant_id AND fg.answer_segment_id = seg.id AND fg.deleted_at IS NULL
)`,
		},
	}
	for _, check := range checks {
		count, err := scalarCountTx(ctx, tx, check.query, tenantID, examID)
		if err != nil {
			return QualityReport{}, err
		}
		if count > 0 {
			issues = append(issues, QualityIssue{Code: check.code, Message: check.message, Blocking: true, Count: count})
		}
	}
	if requirePendingPublish {
		total, err := s.countSubmissionGradesTx(ctx, tx, tenantID, examID)
		if err != nil {
			return QualityReport{}, err
		}
		if total == 0 {
			issues = append(issues, QualityIssue{Code: "no_submission_grades", Message: "there are no submission grades to publish", Blocking: true, Count: 1})
		}
		unconfirmed, err := scalarCountTx(ctx, tx, `SELECT COUNT(*) FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND status NOT IN ('confirmed', 'pending_publish', 'published', 'locked')`, tenantID, examID)
		if err != nil {
			return QualityReport{}, err
		}
		if unconfirmed > 0 {
			issues = append(issues, QualityIssue{Code: "grades_not_confirmed", Message: "there are grades not ready for publish", Blocking: true, Count: unconfirmed})
		}
	}
	return QualityReport{Passed: len(issues) == 0, Issues: issues}, nil
}

func (s *PostgresStore) listGrades(ctx context.Context, tenantID string, examID string, withItems bool) ([]SubmissionGrade, error) {
	return s.listGradesWithQueryer(ctx, s.db, tenantID, examID, withItems)
}

func (s *PostgresStore) listGradesTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, withItems bool) ([]SubmissionGrade, error) {
	return s.listGradesWithQueryer(ctx, tx, tenantID, examID, withItems)
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *PostgresStore) listGradesWithQueryer(ctx context.Context, q queryer, tenantID string, examID string, withItems bool) ([]SubmissionGrade, error) {
	rows, err := q.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, submission_id::text, COALESCE(student_id::text, ''),
  anonymous_code, total_score::float8, max_score::float8, status, locked,
  COALESCE(confirmed_by::text, ''), confirmed_at, COALESCE(published_by::text, ''), published_at,
  created_by::text, created_at, updated_at
FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
ORDER BY anonymous_code, created_at
`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubmissionGrade{}
	for rows.Next() {
		grade, err := scanSubmissionGrade(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, grade)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if withItems {
		for i := range out {
			items, err := s.listFinalGradesWithQueryer(ctx, q, tenantID, out[i].SubmissionID)
			if err != nil {
				return nil, err
			}
			out[i].Items = items
		}
	}
	return out, nil
}

func (s *PostgresStore) listFinalGrades(ctx context.Context, tenantID string, submissionID string) ([]FinalGrade, error) {
	return s.listFinalGradesWithQueryer(ctx, s.db, tenantID, submissionID)
}

func (s *PostgresStore) listFinalGradesWithQueryer(ctx context.Context, q queryer, tenantID string, submissionID string) ([]FinalGrade, error) {
	rows, err := q.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, score::float8, max_score::float8,
  source, status, locked, created_by::text, created_at, updated_at
FROM final_grade
WHERE tenant_id = $1 AND submission_id::text = $2 AND deleted_at IS NULL
ORDER BY question_no, created_at DESC
`, tenantID, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FinalGrade{}
	for rows.Next() {
		grade, err := scanFinalGrade(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, grade)
	}
	return out, rows.Err()
}

func (s *PostgresStore) hasLockedGradesTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string) (bool, error) {
	count, err := scalarCountTx(ctx, tx, `SELECT COUNT(*) FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND (locked = true OR status IN ('published', 'locked'))`, tenantID, examID)
	return count > 0, err
}

func (s *PostgresStore) countSubmissionGradesTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string) (int, error) {
	return scalarCountTx(ctx, tx, `SELECT COUNT(*) FROM submission_grade WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL`, tenantID, examID)
}

func (s *PostgresStore) countUnexpectedSubmissionStatusesTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, allowedStatus string) (int, error) {
	return scalarCountTx(ctx, tx, `SELECT COUNT(*) FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
  AND (locked = true OR status <> $3)`, tenantID, examID, allowedStatus)
}

func lockExamTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, tenantID, examID)
	return err
}

func publishExamTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string) error {
	result, err := tx.ExecContext(ctx, `
UPDATE exam
SET status = 'published', updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
  AND status IN ('draft', 'configured', 'ready', 'collecting', 'grading', 'reviewing', 'finalized')
`, tenantID, examID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 1 {
		return nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM exam WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
)
`, tenantID, examID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return ErrInvalidTransition
}

func scalarCountTx(ctx context.Context, tx *sql.Tx, query string, args ...any) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

type submissionGradeScanner interface {
	Scan(dest ...any) error
}

func scanSubmissionGrade(row submissionGradeScanner) (SubmissionGrade, error) {
	var out SubmissionGrade
	var confirmedAt sql.NullTime
	var publishedAt sql.NullTime
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ExamID,
		&out.SubmissionID,
		&out.StudentID,
		&out.AnonymousCode,
		&out.TotalScore,
		&out.MaxScore,
		&out.Status,
		&out.Locked,
		&out.ConfirmedBy,
		&confirmedAt,
		&out.PublishedBy,
		&publishedAt,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SubmissionGrade{}, ErrNotFound
		}
		return SubmissionGrade{}, err
	}
	if confirmedAt.Valid {
		out.ConfirmedAt = &confirmedAt.Time
	}
	if publishedAt.Valid {
		out.PublishedAt = &publishedAt.Time
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}

type finalGradeScanner interface {
	Scan(dest ...any) error
}

func scanFinalGrade(row finalGradeScanner) (FinalGrade, error) {
	var out FinalGrade
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ExamID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.AnswerSegmentID,
		&out.SubmissionID,
		&out.AnonymousCode,
		&out.Score,
		&out.MaxScore,
		&out.Source,
		&out.Status,
		&out.Locked,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FinalGrade{}, ErrNotFound
		}
		return FinalGrade{}, err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}
