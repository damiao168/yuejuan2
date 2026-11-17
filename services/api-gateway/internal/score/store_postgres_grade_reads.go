package score

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func (s *PostgresStore) ListExamGrades(ctx context.Context, tenantID string, examID string, filter GradeListFilter) (GradeListResult, error) {
	result := GradeListResult{Grades: []SubmissionGrade{}}
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(BOOL_AND(locked OR status IN ('published', 'locked')), false)
FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
`, tenantID, examID).Scan(&result.Total, &result.AllLocked); err != nil {
		return GradeListResult{}, err
	}

	where := "sg.tenant_id = $1 AND sg.exam_id::text = $2 AND sg.deleted_at IS NULL"
	args := []any{tenantID, examID}
	if filter.Status != "" {
		args = append(args, filter.Status)
		where += fmt.Sprintf(" AND sg.status = $%d", len(args))
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		args = append(args, "%"+query+"%")
		placeholder := fmt.Sprintf("$%d", len(args))
		where += " AND (sg.anonymous_code ILIKE " + placeholder +
			" OR sg.submission_id::text ILIKE " + placeholder +
			" OR COALESCE(candidate.student_name_snapshot, '') ILIKE " + placeholder +
			" OR COALESCE(candidate.student_no_snapshot, '') ILIKE " + placeholder +
			" OR COALESCE(candidate.class_name_snapshot, '') ILIKE " + placeholder + ")"
	}
	countQuery := `
SELECT COUNT(*)
FROM submission_grade sg
LEFT JOIN exam_candidate_snapshot candidate
  ON candidate.tenant_id=sg.tenant_id AND candidate.exam_id=sg.exam_id AND candidate.student_id=sg.student_id
WHERE ` + where
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&result.FilteredTotal); err != nil {
		return GradeListResult{}, err
	}

	pageWhere := where
	pageArgs := append([]any(nil), args...)
	if filter.CursorAnonymousCode != "" && filter.CursorID != "" {
		pageArgs = append(pageArgs, filter.CursorAnonymousCode, filter.CursorID)
		codeArg, idArg := len(pageArgs)-1, len(pageArgs)
		pageWhere += fmt.Sprintf(" AND (sg.anonymous_code > $%d OR (sg.anonymous_code = $%d AND sg.id::text > $%d))", codeArg, codeArg, idArg)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 51
	}
	pageArgs = append(pageArgs, limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT sg.id::text, sg.tenant_id::text, sg.exam_id::text, sg.submission_id::text, COALESCE(sg.student_id::text, ''),
  sg.anonymous_code, sg.total_score::float8, sg.max_score::float8, sg.status, sg.locked,
  COALESCE(sg.confirmed_by::text, ''), sg.confirmed_at, COALESCE(sg.published_by::text, ''), sg.published_at,
  sg.revision, sg.created_by::text, sg.created_at, sg.updated_at
FROM submission_grade sg
LEFT JOIN exam_candidate_snapshot candidate
  ON candidate.tenant_id=sg.tenant_id AND candidate.exam_id=sg.exam_id AND candidate.student_id=sg.student_id
WHERE `+pageWhere+`
ORDER BY sg.anonymous_code, sg.id
LIMIT $`+fmt.Sprint(len(pageArgs)), pageArgs...)
	if err != nil {
		return GradeListResult{}, err
	}
	defer rows.Close()
	for rows.Next() {
		grade, scanErr := scanSubmissionGrade(rows)
		if scanErr != nil {
			return GradeListResult{}, scanErr
		}
		result.Grades = append(result.Grades, grade)
	}
	if err := rows.Err(); err != nil {
		return GradeListResult{}, err
	}
	if err := rows.Close(); err != nil {
		return GradeListResult{}, err
	}
	if len(result.Grades) > 0 {
		items, err := s.listFinalGradesForSubmissions(ctx, tenantID, result.Grades)
		if err != nil {
			return GradeListResult{}, err
		}
		bySubmission := make(map[string][]FinalGrade, len(result.Grades))
		for _, item := range items {
			bySubmission[item.SubmissionID] = append(bySubmission[item.SubmissionID], item)
		}
		for i := range result.Grades {
			result.Grades[i].Items = bySubmission[result.Grades[i].SubmissionID]
		}
	}
	return result, nil
}

// GetStudentGrade 优先使用当前正式快照；没有正式指针的历史考试仍可查询升级前已公开并锁定的成绩。
func (s *PostgresStore) GetStudentGrade(ctx context.Context, tenantID, studentID, examID string) (SubmissionGrade, error) {
	grade, err := s.publications.studentGrade(ctx, tenantID, studentID, examID)
	if !errors.Is(err, ErrNotFound) {
		return grade, err
	}
	// 同一快照检查发布指针、历史总分和锁定题分；正式版本缺少某名学生时也不能回退到旧分数。
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return SubmissionGrade{}, err
	}
	defer tx.Rollback()
	grade, err = scanSubmissionGrade(tx.QueryRowContext(ctx, `
SELECT sg.id::text, sg.tenant_id::text, sg.exam_id::text, sg.submission_id::text, sg.student_id::text,
  sg.anonymous_code, sg.total_score::float8, sg.max_score::float8, sg.status, sg.locked,
  COALESCE(sg.confirmed_by::text, ''), sg.confirmed_at, COALESCE(sg.published_by::text, ''), sg.published_at,
  sg.revision, sg.created_by::text, sg.created_at, sg.updated_at
FROM submission_grade sg
WHERE sg.tenant_id=$1 AND sg.exam_id::text=$2 AND sg.student_id::text=$3
  AND sg.status='published' AND sg.locked AND sg.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM score_release_current c WHERE c.tenant_id=sg.tenant_id AND c.exam_id=sg.exam_id)
`, tenantID, examID, studentID))
	if err != nil {
		return SubmissionGrade{}, err
	}
	items, err := s.listFinalGradesWithQueryer(ctx, tx, tenantID, grade.SubmissionID)
	if err != nil {
		return SubmissionGrade{}, err
	}
	for _, item := range items {
		if item.ExamID == examID && item.Locked && item.Status == "locked" {
			grade.Items = append(grade.Items, item)
		}
	}
	if err := tx.Commit(); err != nil {
		return SubmissionGrade{}, err
	}
	return grade, nil
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
  revision, created_by::text, created_at, updated_at
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
	if withItems && len(out) > 0 {
		items, err := s.listFinalGradesForExamWithQueryer(ctx, q, tenantID, examID)
		if err != nil {
			return nil, err
		}
		bySubmission := make(map[string][]FinalGrade, len(out))
		for _, item := range items {
			bySubmission[item.SubmissionID] = append(bySubmission[item.SubmissionID], item)
		}
		for i := range out {
			out[i].Items = bySubmission[out[i].SubmissionID]
		}
	}
	return out, nil
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

func (s *PostgresStore) listFinalGradesForExamWithQueryer(ctx context.Context, q queryer, tenantID string, examID string) ([]FinalGrade, error) {
	rows, err := q.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, score::float8, max_score::float8,
  source, status, locked, created_by::text, created_at, updated_at
FROM final_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
ORDER BY submission_id, question_no, created_at DESC
`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FinalGrade, 0)
	for rows.Next() {
		grade, scanErr := scanFinalGrade(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, grade)
	}
	return out, rows.Err()
}

func (s *PostgresStore) listFinalGradesForSubmissions(ctx context.Context, tenantID string, grades []SubmissionGrade) ([]FinalGrade, error) {
	if len(grades) == 0 {
		return []FinalGrade{}, nil
	}
	args := make([]any, 0, len(grades)+1)
	args = append(args, tenantID)
	placeholders := make([]string, 0, len(grades))
	seen := make(map[string]struct{}, len(grades))
	for _, grade := range grades {
		if _, exists := seen[grade.SubmissionID]; exists {
			continue
		}
		seen[grade.SubmissionID] = struct{}{}
		args = append(args, grade.SubmissionID)
		placeholders = append(placeholders, fmt.Sprintf("$%d::uuid", len(args)))
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, score::float8, max_score::float8,
  source, status, locked, created_by::text, created_at, updated_at
FROM final_grade
WHERE tenant_id = $1 AND submission_id IN (`+strings.Join(placeholders, ",")+`) AND deleted_at IS NULL
ORDER BY submission_id, question_no, created_at DESC
`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FinalGrade, 0)
	for rows.Next() {
		grade, scanErr := scanFinalGrade(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, grade)
	}
	return out, rows.Err()
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
		&out.Revision,
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
