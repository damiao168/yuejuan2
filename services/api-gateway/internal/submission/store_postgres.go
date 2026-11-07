package submission

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) Create(ctx context.Context, tenantID string, examID string, actorID string, input CreateSubmissionInput) (Submission, error) {
	if err := ValidateCreateInput(input); err != nil {
		return Submission{}, err
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO submission (
  tenant_id, exam_id, student_id, candidate_no, source_type, status,
  expected_page_count, actual_page_count, quality_status, quality_issues, collected_by
)
VALUES (
  $1, $2, NULLIF($3, '')::uuid, NULLIF($4, ''), $5, 'created',
  $6, 0, 'unchecked', '[]', $7
)
RETURNING id::text, tenant_id::text, exam_id::text, COALESCE(student_id::text, ''),
  COALESCE(candidate_no, ''), source_type, status, expected_page_count, actual_page_count,
  quality_status, quality_issues, collected_by::text, revision, created_at
`, tenantID, examID, input.StudentID, input.CandidateNo, input.SourceType, input.ExpectedPageCount, actorID)
	var out Submission
	if err := scanSubmission(row, &out); err != nil {
		return Submission{}, err
	}
	return out, nil
}

func (s *PostgresStore) ListByExam(ctx context.Context, tenantID string, examID string, filter ListFilter) ([]Submission, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT s.id::text, s.tenant_id::text, s.exam_id::text, COALESCE(s.student_id::text, ''),
  COALESCE(s.candidate_no, ''), s.source_type, s.status, s.expected_page_count, s.actual_page_count,
  s.quality_status, s.quality_issues, s.collected_by::text, s.revision, s.created_at,
  jsonb_build_object(
    'latest_ocr_status', COALESCE((SELECT ot.status FROM ocr_task ot WHERE ot.tenant_id=s.tenant_id AND ot.submission_id=s.id AND ot.deleted_at IS NULL ORDER BY ot.created_at DESC, ot.id DESC LIMIT 1), ''),
    'latest_ocr_requires_human_review', COALESCE((SELECT ot.requires_human_review FROM ocr_task ot WHERE ot.tenant_id=s.tenant_id AND ot.submission_id=s.id AND ot.deleted_at IS NULL ORDER BY ot.created_at DESC, ot.id DESC LIMIT 1), false),
    'ocr_failed_count', (SELECT count(*) FROM ocr_task ot WHERE ot.tenant_id=s.tenant_id AND ot.submission_id=s.id AND ot.deleted_at IS NULL AND ot.status='failed'),
    'segment_count', (SELECT count(*) FROM answer_segment aseg WHERE aseg.tenant_id=s.tenant_id AND aseg.submission_id=s.id AND aseg.deleted_at IS NULL),
    'manual_segment_count', (SELECT count(*) FROM answer_segment aseg WHERE aseg.tenant_id=s.tenant_id AND aseg.submission_id=s.id AND aseg.deleted_at IS NULL AND aseg.status IN ('needs_manual_review', 'rejected'))
  )
FROM submission s
WHERE s.tenant_id = $1 AND s.exam_id = $2 AND s.deleted_at IS NULL
  AND ($4 = '' OR s.created_at < $3 OR (s.created_at = $3 AND s.id::text < $4))
ORDER BY s.created_at DESC, s.id::text DESC
LIMIT NULLIF($5, 0)
`, tenantID, examID, filter.CursorCreatedAt, filter.CursorID, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Submission{}
	for rows.Next() {
		var item Submission
		if err := scanSubmissionSummary(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListByExams(ctx context.Context, tenantID string, examIDs []string) (map[string][]Submission, error) {
	out := make(map[string][]Submission, len(examIDs))
	if len(examIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(examIDs))
	args := make([]any, 0, len(examIDs)+1)
	args = append(args, tenantID)
	for index, examID := range examIDs {
		placeholders[index] = fmt.Sprintf("$%d::uuid", index+2)
		args = append(args, examID)
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, COALESCE(student_id::text, ''),
  COALESCE(candidate_no, ''), source_type, status, expected_page_count, actual_page_count,
  quality_status, quality_issues, collected_by::text, revision, created_at
FROM submission
WHERE tenant_id=$1 AND exam_id IN (`+strings.Join(placeholders, ",")+`) AND deleted_at IS NULL
ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item Submission
		if scanErr := scanSubmission(rows, &item); scanErr != nil {
			return nil, scanErr
		}
		out[item.ExamID] = append(out[item.ExamID], item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) Get(ctx context.Context, tenantID string, id string) (Submission, error) {
	item, err := s.getSubmission(ctx, tenantID, id)
	if err != nil {
		return Submission{}, err
	}
	pages, err := s.ListPages(ctx, tenantID, id)
	if err != nil {
		return Submission{}, err
	}
	item.Pages = pages
	return item, nil
}

func (s *PostgresStore) UpdateStatus(ctx context.Context, tenantID string, submissionID string, _ string, status string, expectedRevision int64) (Submission, error) {
	if expectedRevision <= 0 {
		return Submission{}, ErrInvalidInput
	}
	item, err := s.getSubmission(ctx, tenantID, submissionID)
	if err != nil {
		return Submission{}, err
	}
	if !CanTransition(item.Status, status, item.QualityStatus) {
		return Submission{}, ErrInvalidTransition
	}
	if item.Revision != expectedRevision {
		return Submission{}, ErrRevisionConflict
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE submission
SET status = $3, revision=revision+1, updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND status=$4 AND revision=$5 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, exam_id::text, COALESCE(student_id::text, ''),
  COALESCE(candidate_no, ''), source_type, status, expected_page_count, actual_page_count,
  quality_status, quality_issues, collected_by::text, revision, created_at
`, tenantID, submissionID, status, item.Status, expectedRevision)
	var out Submission
	if err := scanSubmission(row, &out); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Submission{}, ErrRevisionConflict
		}
		return Submission{}, err
	}
	return out, nil
}

func (s *PostgresStore) getSubmission(ctx context.Context, tenantID string, id string) (Submission, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, COALESCE(student_id::text, ''),
  COALESCE(candidate_no, ''), source_type, status, expected_page_count, actual_page_count,
  quality_status, quality_issues, collected_by::text, revision, created_at
FROM submission
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, id)
	var out Submission
	if err := scanSubmission(row, &out); err != nil {
		return Submission{}, err
	}
	return out, nil
}

// 父答卷先于页面取锁；普通页面写入在锁内拒绝冻结状态。
func lockWritableSubmissionTx(ctx context.Context, tx *sql.Tx, tenantID, submissionID string) (string, error) {
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM submission WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, submissionID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	if status == "ready_for_ocr" || status == "rejected" {
		return status, ErrSubmissionLocked
	}
	return status, nil
}

type submissionScanner interface {
	Scan(dest ...any) error
}

func scanSubmission(row submissionScanner, out *Submission) error {
	var issues []byte
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ExamID,
		&out.StudentID,
		&out.CandidateNo,
		&out.SourceType,
		&out.Status,
		&out.ExpectedPageCount,
		&out.ActualPageCount,
		&out.QualityStatus,
		&issues,
		&out.CollectedBy,
		&out.Revision,
		&out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_ = json.Unmarshal(issues, &out.QualityIssues)
	return nil
}

func scanSubmissionSummary(row submissionScanner, out *Submission) error {
	var issues []byte
	var summary []byte
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ExamID,
		&out.StudentID,
		&out.CandidateNo,
		&out.SourceType,
		&out.Status,
		&out.ExpectedPageCount,
		&out.ActualPageCount,
		&out.QualityStatus,
		&issues,
		&out.CollectedBy,
		&out.Revision,
		&out.CreatedAt,
		&summary,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_ = json.Unmarshal(issues, &out.QualityIssues)
	_ = json.Unmarshal(summary, &out.Summary)
	return nil
}
