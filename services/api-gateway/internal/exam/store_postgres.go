package exam

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) CreateExam(ctx context.Context, scope auth.AccessScope, createdBy string, input CreateInput) (Exam, error) {
	if !scopeAllowsRequestedClasses(scope, input.SchoolID, input.ClassIDs) {
		return Exam{}, ErrScopeForbidden
	}
	tenantID := scope.TenantID
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Exam{}, err
	}
	defer tx.Rollback()

	appealEnabled := true
	if input.AppealEnabled != nil {
		appealEnabled = *input.AppealEnabled
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO exam (tenant_id, school_id, name, subject, exam_type, total_score, status, grading_mode, appeal_enabled, publish_policy, created_by)
SELECT $1, school.id, $3, $4, $5, $6, 'draft', $7, $8, $9, $10
FROM school
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, school_id::text, name, subject, exam_type, total_score::float8, status, grading_mode, appeal_enabled, publish_policy, created_by::text, revision, created_at, updated_at
`, tenantID, input.SchoolID, input.Name, input.Subject, input.ExamType, input.TotalScore, input.GradingMode, appealEnabled, input.PublishPolicy, createdBy)
	var out Exam
	if err := scanExam(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Exam{}, ErrInvalidInput
		}
		return Exam{}, err
	}
	if err := s.replaceClasses(ctx, tx, scope, out.ID, input.ClassIDs); err != nil {
		return Exam{}, err
	}
	if err := s.refreshExamCandidateSnapshot(ctx, tx, tenantID, out.ID); err != nil {
		return Exam{}, err
	}
	out.ClassIDs = cloneStrings(input.ClassIDs)
	if err := tx.Commit(); err != nil {
		return Exam{}, err
	}
	return out, nil
}

func (s *PostgresStore) ListExams(ctx context.Context, scope auth.AccessScope, filter ListFilter) ([]Exam, error) {
	query := `
SELECT e.id::text, e.tenant_id::text, e.school_id::text, e.name, e.subject, e.exam_type,
       e.total_score::float8, e.status, e.grading_mode, e.appeal_enabled, e.publish_policy,
       e.created_by::text, e.revision, e.created_at, e.updated_at,
       COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER (WHERE ec.class_id IS NOT NULL), '{}'),
       COALESCE(es.id::text, ''), COALESCE(es.name, ''), COALESCE(es.grade_id::text, '')
FROM exam e
LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL
LEFT JOIN exam_session es ON es.tenant_id=e.tenant_id AND es.id=e.exam_session_id AND es.deleted_at IS NULL
WHERE e.tenant_id = $1 AND e.deleted_at IS NULL
  AND (` + examScopePredicate("e", "ec") + `)`
	args := scopeQueryArgs(scope)
	if filter.Status != "" {
		query += fmt.Sprintf(` AND e.status = $%d`, len(args)+1)
		args = append(args, filter.Status)
	}
	if filter.SchoolID != "" {
		query += fmt.Sprintf(` AND e.school_id::text = $%d`, len(args)+1)
		args = append(args, filter.SchoolID)
	}
	if !filter.CursorAt.IsZero() && filter.CursorID != "" {
		query += fmt.Sprintf(` AND (e.created_at, e.id) < ($%d, $%d::uuid)`, len(args)+1, len(args)+2)
		args = append(args, filter.CursorAt, filter.CursorID)
	}
	query += ` GROUP BY e.id, es.id ORDER BY e.created_at DESC, e.id DESC`
	if filter.Limit > 0 {
		query += fmt.Sprintf(` LIMIT $%d`, len(args)+1)
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Exam{}
	for rows.Next() {
		var item Exam
		if err := scanExamWithSession(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetExam(ctx context.Context, scope auth.AccessScope, id string) (Exam, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT e.id::text, e.tenant_id::text, e.school_id::text, e.name, e.subject, e.exam_type,
       e.total_score::float8, e.status, e.grading_mode, e.appeal_enabled, e.publish_policy,
       e.created_by::text, e.revision, e.created_at, e.updated_at,
       COALESCE(array_agg(DISTINCT ec.class_id::text) FILTER (WHERE ec.class_id IS NOT NULL), '{}'),
       COALESCE(es.id::text, ''), COALESCE(es.name, ''), COALESCE(es.grade_id::text, '')
FROM exam e
LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL
LEFT JOIN exam_session es ON es.tenant_id=e.tenant_id AND es.id=e.exam_session_id AND es.deleted_at IS NULL
WHERE e.tenant_id = $1 AND e.id::text = $6 AND e.deleted_at IS NULL
  AND (`+examScopePredicate("e", "ec")+`)
GROUP BY e.id, es.id
`, append(scopeQueryArgs(scope), id)...)
	var out Exam
	if err := scanExamWithSession(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Exam{}, ErrNotFound
		}
		return Exam{}, err
	}
	return out, nil
}

// ListSessionExams returns only accessible subject exams in the current session.
// It avoids a tenant-wide exam listing on every workspace navigation.
func (s *PostgresStore) ListSessionExams(ctx context.Context, scope auth.AccessScope, sessionID string) ([]Exam, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT e.id::text, e.subject, e.total_score::float8
FROM exam e
LEFT JOIN exam_class ec ON ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL
WHERE e.tenant_id=$1 AND e.exam_session_id::text=$6 AND e.deleted_at IS NULL
  AND (`+examScopePredicate("e", "ec")+`)
ORDER BY e.subject, e.id::text
`, append(scopeQueryArgs(scope), sessionID)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Exam{}
	for rows.Next() {
		var item Exam
		if err := rows.Scan(&item.ID, &item.Subject, &item.TotalScore); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) UpdateExam(ctx context.Context, scope auth.AccessScope, id string, input UpdateInput) (Exam, error) {
	if input.ExpectedRevision <= 0 {
		return Exam{}, ErrRevisionConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Exam{}, err
	}
	defer tx.Rollback()

	current, err := s.getExamForUpdate(ctx, tx, scope, id)
	if err != nil {
		return Exam{}, err
	}
	if current.Revision != input.ExpectedRevision {
		return Exam{}, ErrRevisionConflict
	}
	if IsCoreLocked(current.Status) {
		return Exam{}, ErrLocked
	}
	merged := current
	if input.SchoolID != nil {
		merged.SchoolID = *input.SchoolID
	}
	if input.Name != nil {
		merged.Name = *input.Name
	}
	if input.Subject != nil {
		merged.Subject = *input.Subject
	}
	if input.ExamType != nil {
		merged.ExamType = *input.ExamType
	}
	if input.TotalScore != nil {
		merged.TotalScore = *input.TotalScore
	}
	if input.GradingMode != nil {
		merged.GradingMode = *input.GradingMode
	}
	if input.AppealEnabled != nil {
		merged.AppealEnabled = *input.AppealEnabled
	}
	if input.PublishPolicy != nil {
		merged.PublishPolicy = *input.PublishPolicy
	}
	classIDs := current.ClassIDs
	if input.ClassIDs != nil {
		classIDs = *input.ClassIDs
	}
	if !scopeAllowsRequestedClasses(scope, merged.SchoolID, classIDs) {
		return Exam{}, ErrScopeForbidden
	}
	if err := s.requireSchoolInTenant(ctx, tx, scope.TenantID, merged.SchoolID); err != nil {
		return Exam{}, err
	}
	row := tx.QueryRowContext(ctx, `
UPDATE exam
SET school_id = $3, name = $4, subject = $5, exam_type = $6, total_score = $7,
    grading_mode = $8, appeal_enabled = $9, publish_policy = $10,
    revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $11 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, school_id::text, name, subject, exam_type,
          total_score::float8, status, grading_mode, appeal_enabled, publish_policy,
          created_by::text, revision, created_at, updated_at
`, scope.TenantID, id, merged.SchoolID, merged.Name, merged.Subject, merged.ExamType, merged.TotalScore, merged.GradingMode, merged.AppealEnabled, merged.PublishPolicy, input.ExpectedRevision)
	var out Exam
	if err := scanExam(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Exam{}, ErrRevisionConflict
		}
		return Exam{}, err
	}
	if input.ClassIDs != nil {
		if err := s.replaceClasses(ctx, tx, scope, id, *input.ClassIDs); err != nil {
			return Exam{}, err
		}
		if err := s.refreshExamCandidateSnapshot(ctx, tx, scope.TenantID, id); err != nil {
			return Exam{}, err
		}
		out.ClassIDs = cloneStrings(*input.ClassIDs)
	} else {
		out.ClassIDs = current.ClassIDs
	}
	if err := tx.Commit(); err != nil {
		return Exam{}, err
	}
	return out, nil
}

func (s *PostgresStore) UpdateStatus(ctx context.Context, scope auth.AccessScope, id string, status string, expectedRevision int64) (Exam, error) {
	if expectedRevision <= 0 {
		return Exam{}, ErrRevisionConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Exam{}, err
	}
	defer tx.Rollback()
	current, err := s.getExamForUpdate(ctx, tx, scope, id)
	if err != nil {
		return Exam{}, err
	}
	if current.Revision != expectedRevision {
		return Exam{}, ErrRevisionConflict
	}
	if !CanTransition(current.Status, status) {
		return Exam{}, ErrInvalidTransition
	}
	row := tx.QueryRowContext(ctx, `
UPDATE exam
SET status = $3, revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $4 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, school_id::text, name, subject, exam_type,
          total_score::float8, status, grading_mode, appeal_enabled, publish_policy,
          created_by::text, revision, created_at, updated_at
`, scope.TenantID, id, status, expectedRevision)
	var out Exam
	if err := scanExam(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Exam{}, ErrRevisionConflict
		}
		return Exam{}, err
	}
	out.ClassIDs = current.ClassIDs
	if err := tx.Commit(); err != nil {
		return Exam{}, err
	}
	return out, nil
}

func (s *PostgresStore) replaceClasses(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, examID string, classIDs []string) error {
	tenantID := scope.TenantID
	if _, err := tx.ExecContext(ctx, `DELETE FROM exam_class WHERE tenant_id = $1 AND exam_id = $2`, tenantID, examID); err != nil {
		return err
	}
	for _, classID := range classIDs {
		if err := s.requireClassInScope(ctx, tx, scope, classID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO exam_class (tenant_id, exam_id, class_id) VALUES ($1, $2, $3) ON CONFLICT (tenant_id, exam_id, class_id) DO NOTHING`, tenantID, examID, classID); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) requireSchoolInTenant(ctx context.Context, tx *sql.Tx, tenantID string, schoolID string) error {
	var id string
	err := tx.QueryRowContext(ctx, `
SELECT id::text
FROM school
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, schoolID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidInput
	}
	return err
}

func (s *PostgresStore) requireClassInScope(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, classID string) error {
	var id, schoolID string
	err := tx.QueryRowContext(ctx, `
SELECT id::text, school_id::text
FROM school_class
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, scope.TenantID, classID).Scan(&id, &schoolID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidInput
	}
	if err != nil {
		return err
	}
	if !scope.TenantWide && !scope.AllowsClass(id) && !scope.AllowsSchool(schoolID) {
		return ErrScopeForbidden
	}
	return nil
}

func (s *PostgresStore) classIDsTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT class_id::text FROM exam_class WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL ORDER BY created_at`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *PostgresStore) getExamForUpdate(ctx context.Context, tx *sql.Tx, scope auth.AccessScope, id string) (Exam, error) {
	row := tx.QueryRowContext(ctx, `
SELECT e.id::text, e.tenant_id::text, e.school_id::text, e.name, e.subject, e.exam_type,
       e.total_score::float8, e.status, e.grading_mode, e.appeal_enabled, e.publish_policy,
       e.created_by::text, e.revision, e.created_at, e.updated_at
FROM exam e
WHERE e.tenant_id=$1 AND e.id::text=$6 AND e.deleted_at IS NULL
  AND (
    $2::boolean
    OR e.school_id::text IN (SELECT jsonb_array_elements_text($3::jsonb))
    OR e.id::text IN (SELECT jsonb_array_elements_text($4::jsonb))
    OR EXISTS (
      SELECT 1 FROM exam_class scoped_ec
      WHERE scoped_ec.tenant_id=e.tenant_id AND scoped_ec.exam_id=e.id AND scoped_ec.deleted_at IS NULL
        AND scoped_ec.class_id::text IN (SELECT jsonb_array_elements_text($5::jsonb))
    )
  )
FOR UPDATE
`, append(scopeQueryArgs(scope), id)...)
	var out Exam
	if err := scanExam(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Exam{}, ErrNotFound
		}
		return Exam{}, err
	}
	classIDs, err := s.classIDsTx(ctx, tx, scope.TenantID, id)
	out.ClassIDs = classIDs
	return out, err
}

func examScopePredicate(examAlias, classAlias string) string {
	return `$2::boolean
    OR ` + examAlias + `.school_id::text IN (SELECT jsonb_array_elements_text($3::jsonb))
    OR ` + examAlias + `.id::text IN (SELECT jsonb_array_elements_text($4::jsonb))
    OR ` + classAlias + `.class_id::text IN (SELECT jsonb_array_elements_text($5::jsonb))`
}

func scopeQueryArgs(scope auth.AccessScope) []any {
	return []any{
		scope.TenantID,
		scope.TenantWide,
		scopeIDsJSON(scope.SchoolIDs),
		scopeIDsJSON(scope.ExamIDs),
		scopeIDsJSON(scope.ClassIDs),
	}
}

func scopeIDsJSON(ids []string) string {
	raw, _ := json.Marshal(ids)
	return string(raw)
}

func scopeAllowsRequestedClasses(scope auth.AccessScope, schoolID string, classIDs []string) bool {
	if scope.TenantWide || scope.AllowsSchool(schoolID) {
		return true
	}
	if len(classIDs) == 0 {
		return false
	}
	for _, classID := range classIDs {
		if !scope.AllowsClass(classID) {
			return false
		}
	}
	return true
}

type scanner interface {
	Scan(dest ...any) error
}

func scanExam(row scanner, out *Exam) error {
	return row.Scan(&out.ID, &out.TenantID, &out.SchoolID, &out.Name, &out.Subject, &out.ExamType, &out.TotalScore, &out.Status, &out.GradingMode, &out.AppealEnabled, &out.PublishPolicy, &out.CreatedBy, &out.Revision, &out.CreatedAt, &out.UpdatedAt)
}

func scanExamWithClasses(row scanner, out *Exam) error {
	return row.Scan(&out.ID, &out.TenantID, &out.SchoolID, &out.Name, &out.Subject, &out.ExamType, &out.TotalScore, &out.Status, &out.GradingMode, &out.AppealEnabled, &out.PublishPolicy, &out.CreatedBy, &out.Revision, &out.CreatedAt, &out.UpdatedAt, examTextArray(&out.ClassIDs))
}

func scanExamWithSession(row scanner, out *Exam) error {
	return row.Scan(&out.ID, &out.TenantID, &out.SchoolID, &out.Name, &out.Subject, &out.ExamType, &out.TotalScore, &out.Status, &out.GradingMode, &out.AppealEnabled, &out.PublishPolicy, &out.CreatedBy, &out.Revision, &out.CreatedAt, &out.UpdatedAt, examTextArray(&out.ClassIDs), &out.SessionID, &out.SessionName, &out.SessionGradeID)
}

func examTextArray(target *[]string) any {
	return examScannerFunc(func(src any) error {
		if src == nil {
			*target = nil
			return nil
		}
		var text string
		switch value := src.(type) {
		case string:
			text = value
		case []byte:
			text = string(value)
		default:
			return fmt.Errorf("unsupported PostgreSQL array type %T", src)
		}
		text = strings.Trim(text, "{}")
		if text == "" {
			*target = []string{}
			return nil
		}
		parts := strings.Split(text, ",")
		for i := range parts {
			parts[i] = strings.Trim(parts[i], `"`)
		}
		*target = parts
		return nil
	})
}

type examScannerFunc func(any) error

func (f examScannerFunc) Scan(src any) error { return f(src) }
