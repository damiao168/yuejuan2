package submission

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
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

func (s *PostgresStore) AddPage(ctx context.Context, tenantID string, submissionID string, _ string, input AddPageInput) (SubmissionPage, error) {
	if err := ValidatePageInput(input); err != nil {
		return SubmissionPage{}, err
	}
	if _, err := s.pageByNo(ctx, tenantID, submissionID, input.PageNo); err == nil {
		return SubmissionPage{}, ErrDuplicatePage
	} else if !errors.Is(err, ErrNotFound) {
		return SubmissionPage{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubmissionPage{}, err
	}
	defer tx.Rollback()
	// 答卷状态和页面变更使用同一父行锁；等锁期间冻结后不能继续写页。
	if _, err = lockWritableSubmissionTx(ctx, tx, tenantID, submissionID); err != nil {
		return SubmissionPage{}, err
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO submission_page (tenant_id, submission_id, file_asset_id, page_no, status, quality_status, quality_override, quality_issues)
VALUES ($1, $2, $3, $4, 'uploaded', 'unchecked', '{}', '[]')
RETURNING id::text, tenant_id::text, submission_id::text, file_asset_id::text, page_no, status,
  COALESCE(latest_quality_run_id::text, ''), COALESCE(normalized_file_asset_id::text, ''),
  quality_status, quality_override, quality_issues, created_at
`, tenantID, submissionID, input.FileAssetID, input.PageNo)
	var page SubmissionPage
	if err := scanPage(row, &page); err != nil {
		return SubmissionPage{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE submission
SET actual_page_count = (
    SELECT COUNT(*) FROM submission_page
    WHERE tenant_id = $1 AND submission_id = $2 AND deleted_at IS NULL
  ),
  status = 'pages_uploaded',
  quality_status = 'unchecked',
  quality_issues = '[]',
  revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
`, tenantID, submissionID); err != nil {
		return SubmissionPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubmissionPage{}, err
	}
	return page, nil
}

func (s *PostgresStore) ReplacePage(ctx context.Context, tenantID string, submissionID string, _ string, pageNo int, input AddPageInput) (SubmissionPage, error) {
	input.PageNo = pageNo
	if err := ValidatePageInput(input); err != nil {
		return SubmissionPage{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubmissionPage{}, err
	}
	defer tx.Rollback()
	if _, err = lockWritableSubmissionTx(ctx, tx, tenantID, submissionID); err != nil {
		return SubmissionPage{}, err
	}
	row := tx.QueryRowContext(ctx, `
UPDATE submission_page
SET file_asset_id = $4,
  status = 'uploaded',
  latest_quality_run_id = NULL,
  normalized_file_asset_id = NULL,
  quality_status = 'unchecked',
  quality_override = '{}',
  quality_issues = '[]',
  updated_at = now()
WHERE tenant_id = $1 AND submission_id = $2 AND page_no = $3 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, submission_id::text, file_asset_id::text, page_no, status,
  COALESCE(latest_quality_run_id::text, ''), COALESCE(normalized_file_asset_id::text, ''),
  quality_status, quality_override, quality_issues, created_at
`, tenantID, submissionID, pageNo, input.FileAssetID)
	var page SubmissionPage
	if err := scanPage(row, &page); err != nil {
		return SubmissionPage{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE submission
SET actual_page_count = (
    SELECT COUNT(*) FROM submission_page
    WHERE tenant_id = $1 AND submission_id = $2 AND deleted_at IS NULL
  ),
  status = 'pages_uploaded',
  quality_status = 'unchecked',
  quality_issues = '[]',
  revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
`, tenantID, submissionID); err != nil {
		return SubmissionPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubmissionPage{}, err
	}
	return page, nil
}

func (s *PostgresStore) ListPages(ctx context.Context, tenantID string, submissionID string) ([]SubmissionPage, error) {
	if _, err := s.getSubmission(ctx, tenantID, submissionID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, file_asset_id::text, page_no, status,
  COALESCE(latest_quality_run_id::text, ''), COALESCE(normalized_file_asset_id::text, ''),
  quality_status, quality_override, quality_issues, created_at
FROM submission_page
WHERE tenant_id = $1 AND submission_id = $2 AND deleted_at IS NULL
ORDER BY page_no
`, tenantID, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubmissionPage{}
	for rows.Next() {
		var page SubmissionPage
		if err := scanPage(rows, &page); err != nil {
			return nil, err
		}
		out = append(out, page)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListAnswerRegions(ctx context.Context, tenantID string, submissionID string) ([]AnswerRegion, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT seg.id::text, seg.question_id::text, COALESCE(seg.question_no::text,''),
	       COALESCE(q.question_type,''), COALESCE(NULLIF(seg.pixel_bbox,'{}'::jsonb),seg.bbox), seg.submission_page_id::text,
	       (seg.registration_run_id IS NOT NULL OR NULLIF(seg.normalized_bbox,'{}'::jsonb) IS NOT NULL
	        OR seg.status NOT IN ('generated','accepted'))
FROM answer_segment seg
JOIN submission_page sp ON sp.tenant_id=seg.tenant_id AND sp.id=seg.submission_page_id
  AND sp.submission_id=seg.submission_id AND sp.deleted_at IS NULL
LEFT JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
WHERE seg.tenant_id=$1 AND seg.submission_id=$2 AND seg.deleted_at IS NULL
ORDER BY seg.submission_page_id, seg.question_no, seg.id
`, tenantID, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AnswerRegion{}
	for rows.Next() {
		var region AnswerRegion
		var raw []byte
		if err := rows.Scan(&region.ID, &region.QuestionID, &region.QuestionNo, &region.QuestionType, &raw, &region.PageID, &region.RequiresFullPage); err != nil {
			return nil, err
		}
		region.BBox = decodeAnswerRegionBBox(raw)
		out = append(out, region)
	}
	return out, rows.Err()
}

func decodeAnswerRegionBBox(raw []byte) []float64 {
	var values []float64
	if json.Unmarshal(raw, &values) == nil && len(values) == 4 {
		return values
	}
	var object map[string]float64
	if json.Unmarshal(raw, &object) == nil {
		width, hasWidth := object["width"]
		height, hasHeight := object["height"]
		if !hasWidth {
			width = object["w"]
		}
		if !hasHeight {
			height = object["h"]
		}
		return []float64{object["x"], object["y"], width, height}
	}
	// 保留保守拒绝行为：空 bbox 仍序列化给 worker，
	// worker 将任务拒绝为 invalid_ocr_region，而不是静默使用整页回退。
	return []float64{}
}

func (s *PostgresStore) ApplyPageQualityResult(ctx context.Context, tenantID string, input ApplyPageQualityInput) (SubmissionPage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubmissionPage{}, err
	}
	defer tx.Rollback()
	page, err := ApplyPageQualityResultInTx(ctx, tx, tenantID, input)
	if err != nil {
		return SubmissionPage{}, err
	}
	return page, tx.Commit()
}

// ApplyPageQualityResultInTx 允许质量结果协调器将页面投影与提交聚合结果同来源和运行时结果一起提交。
func ApplyPageQualityResultInTx(ctx context.Context, tx *sql.Tx, tenantID string, input ApplyPageQualityInput) (SubmissionPage, error) {
	page, _, err := ApplyPageQualityProjectionInTx(ctx, tx, tenantID, input)
	return page, err
}

// ApplyPageQualityProjectionInTx 返回是否投影当前页面；旧源图、冻结答卷和同运行重放仅保留运行历史。
// 先锁答卷再锁页面，与普通换页保持同一锁序，不能把旧质检结果放行给新源图。
func ApplyPageQualityProjectionInTx(ctx context.Context, tx *sql.Tx, tenantID string, input ApplyPageQualityInput) (SubmissionPage, bool, error) {
	if input.QualityStatus != "passed" && input.QualityStatus != "review" && input.QualityStatus != "failed" {
		return SubmissionPage{}, false, ErrInvalidInput
	}
	if tx == nil {
		return SubmissionPage{}, false, ErrInvalidInput
	}
	status, lockErr := lockWritableSubmissionTx(ctx, tx, tenantID, input.SubmissionID)
	if lockErr != nil && !errors.Is(lockErr, ErrSubmissionLocked) {
		return SubmissionPage{}, false, lockErr
	}
	var current SubmissionPage
	if err := scanPage(tx.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,submission_id::text,file_asset_id::text,page_no,status,COALESCE(latest_quality_run_id::text,''),COALESCE(normalized_file_asset_id::text,''),quality_status,quality_override,quality_issues,created_at FROM submission_page WHERE tenant_id=$1 AND submission_id=$2 AND id=$3::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, input.SubmissionID, input.PageID), &current); err != nil {
		return SubmissionPage{}, false, err
	}
	if status == "ready_for_ocr" || status == "rejected" || input.SourceFileAssetID != "" && current.FileAssetID != input.SourceFileAssetID || input.LatestQualityRunID != "" && current.LatestQualityRunID == input.LatestQualityRunID {
		return current, false, nil
	}
	issuesJSON, _ := json.Marshal(input.QualityIssues)
	row := tx.QueryRowContext(ctx, `
UPDATE submission_page
SET latest_quality_run_id = NULLIF($4, '')::uuid,
  normalized_file_asset_id = NULLIF($5, '')::uuid,
  quality_status = $6,
  quality_issues = $7,
  updated_at = now()
WHERE tenant_id = $1 AND submission_id = $2 AND id::text = $3 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, submission_id::text, file_asset_id::text, page_no, status,
  COALESCE(latest_quality_run_id::text, ''), COALESCE(normalized_file_asset_id::text, ''),
  quality_status, quality_override, quality_issues, created_at
`, tenantID, input.SubmissionID, input.PageID, input.LatestQualityRunID, input.NormalizedFileAssetID, input.QualityStatus, issuesJSON)
	var page SubmissionPage
	if err := scanPage(row, &page); err != nil {
		return SubmissionPage{}, false, err
	}
	if err := aggregateQualityTx(ctx, tx, tenantID, input.SubmissionID); err != nil {
		return SubmissionPage{}, false, err
	}
	return page, true, nil
}

func (s *PostgresStore) OverridePageQuality(ctx context.Context, tenantID string, pageID string, actorID string, input OverridePageQualityInput) (SubmissionPage, error) {
	reason := strings.TrimSpace(input.Reason)
	if pageID == "" || actorID == "" || len([]rune(reason)) < 5 || len([]rune(reason)) > 500 {
		return SubmissionPage{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubmissionPage{}, err
	}
	defer tx.Rollback()
	var submissionID, currentStatus, normalizedAssetID, submissionStatus string
	var currentIssues, currentOverride []byte
	err = tx.QueryRowContext(ctx, `
SELECT sp.submission_id::text,sp.quality_status,COALESCE(sp.normalized_file_asset_id::text,''),
       sp.quality_issues,sp.quality_override,s.status
FROM submission_page sp
JOIN submission s ON s.tenant_id=sp.tenant_id AND s.id=sp.submission_id
WHERE sp.tenant_id=$1 AND sp.id=$2::uuid AND sp.deleted_at IS NULL AND s.deleted_at IS NULL
FOR UPDATE OF sp,s
`, tenantID, pageID).Scan(&submissionID, &currentStatus, &normalizedAssetID, &currentIssues, &currentOverride, &submissionStatus)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SubmissionPage{}, ErrNotFound
		}
		return SubmissionPage{}, err
	}
	if submissionStatus == "ready_for_ocr" || submissionStatus == "rejected" {
		return SubmissionPage{}, ErrSubmissionLocked
	}
	if currentStatus == "passed" {
		var existingOverride map[string]any
		if json.Unmarshal(currentOverride, &existingOverride) == nil && existingOverride["decision"] == "accepted" {
			var page SubmissionPage
			err = scanPage(tx.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, file_asset_id::text, page_no, status,
  COALESCE(latest_quality_run_id::text, ''), COALESCE(normalized_file_asset_id::text, ''),
  quality_status, quality_override, quality_issues, created_at
FROM submission_page
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, pageID), &page)
			if err != nil {
				return SubmissionPage{}, err
			}
			return page, tx.Commit()
		}
	}
	if currentStatus != "review" && currentStatus != "failed" {
		return SubmissionPage{}, ErrInvalidTransition
	}
	if normalizedAssetID == "" {
		return SubmissionPage{}, ErrInvalidTransition
	}
	overrideJSON, err := json.Marshal(map[string]any{
		"decision":                "accepted",
		"reason":                  reason,
		"actor_id":                actorID,
		"overridden_at":           time.Now().UTC(),
		"original_quality_status": currentStatus,
		"original_quality_issues": json.RawMessage(currentIssues),
	})
	if err != nil {
		return SubmissionPage{}, err
	}
	row := tx.QueryRowContext(ctx, `
UPDATE submission_page
SET quality_status='passed',quality_override=$3,updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, submission_id::text, file_asset_id::text, page_no, status,
  COALESCE(latest_quality_run_id::text, ''), COALESCE(normalized_file_asset_id::text, ''),
  quality_status, quality_override, quality_issues, created_at
`, tenantID, pageID, overrideJSON)
	var page SubmissionPage
	if err = scanPage(row, &page); err != nil {
		return SubmissionPage{}, err
	}
	if err = aggregateQualityTx(ctx, tx, tenantID, submissionID); err != nil {
		return SubmissionPage{}, err
	}
	if err = tx.Commit(); err != nil {
		return SubmissionPage{}, err
	}
	return page, nil
}

func (s *PostgresStore) RunQualityCheck(ctx context.Context, tenantID string, submissionID string, _ string) (QualityResult, error) {
	item, err := s.getSubmission(ctx, tenantID, submissionID)
	if err != nil {
		return QualityResult{}, err
	}
	if item.Status == "ready_for_ocr" || item.Status == "rejected" {
		return QualityResult{}, ErrSubmissionLocked
	}
	pages, err := s.ListPages(ctx, tenantID, submissionID)
	if err != nil {
		return QualityResult{}, err
	}
	issues := qualityIssues(item, pages)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return QualityResult{}, err
	}
	defer tx.Rollback()
	// 采集完整性只是第一道门禁。只有每页都通过图像质量检查或获得有效覆盖批准后，聚合结果才可通过。
	if err = aggregateQualityTx(ctx, tx, tenantID, submissionID); err != nil {
		return QualityResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return QualityResult{}, err
	}
	return QualityResult{Valid: len(issues) == 0, Issues: issues}, nil
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

func (s *PostgresStore) pageByNo(ctx context.Context, tenantID string, submissionID string, pageNo int) (SubmissionPage, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, file_asset_id::text, page_no, status,
  COALESCE(latest_quality_run_id::text, ''), COALESCE(normalized_file_asset_id::text, ''),
  quality_status, quality_override, quality_issues, created_at
FROM submission_page
WHERE tenant_id = $1 AND submission_id = $2 AND page_no = $3 AND deleted_at IS NULL
`, tenantID, submissionID, pageNo)
	var page SubmissionPage
	if err := scanPage(row, &page); err != nil {
		return SubmissionPage{}, err
	}
	return page, nil
}

func aggregateQualityTx(ctx context.Context, tx *sql.Tx, tenantID string, submissionID string) error {
	rows, err := tx.QueryContext(ctx, `
SELECT page_no, quality_status
FROM submission_page
WHERE tenant_id = $1 AND submission_id = $2 AND deleted_at IS NULL
`, tenantID, submissionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	pages := []SubmissionPage{}
	hasUnchecked := false
	hasReview := false
	hasFailed := false
	for rows.Next() {
		var page SubmissionPage
		var status string
		if err := rows.Scan(&page.PageNo, &status); err != nil {
			return err
		}
		pages = append(pages, page)
		switch status {
		case "failed":
			hasFailed = true
		case "review":
			hasReview = true
		case "passed":
		default:
			hasUnchecked = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	pageCount := len(pages)
	var expected int
	if err := tx.QueryRowContext(ctx, `
SELECT expected_page_count
FROM submission
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
`, tenantID, submissionID).Scan(&expected); err != nil {
		return err
	}
	issues := qualityIssues(Submission{ExpectedPageCount: expected}, pages)
	issuesJSON, err := json.Marshal(issues)
	if err != nil {
		return err
	}
	qualityStatus := "unchecked"
	submissionStatus := "pages_uploaded"
	if len(issues) > 0 {
		qualityStatus = "failed"
	} else if hasFailed {
		qualityStatus = "failed"
	} else if hasReview {
		qualityStatus = "review"
	} else if hasUnchecked || pageCount == 0 {
		qualityStatus = "unchecked"
	} else {
		qualityStatus = "passed"
		submissionStatus = "quality_checked"
	}
	_, err = tx.ExecContext(ctx, `
UPDATE submission
SET actual_page_count = $3,
  quality_status = $4,
  status = $5,
  quality_issues = $6,
  updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL AND status NOT IN ('ready_for_ocr','rejected')
`, tenantID, submissionID, pageCount, qualityStatus, submissionStatus, issuesJSON)
	return err
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

func scanPage(row submissionScanner, out *SubmissionPage) error {
	var issues []byte
	var override []byte
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.SubmissionID,
		&out.FileAssetID,
		&out.PageNo,
		&out.Status,
		&out.LatestQualityRunID,
		&out.NormalizedFileAssetID,
		&out.QualityStatus,
		&override,
		&issues,
		&out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_ = json.Unmarshal(override, &out.QualityOverride)
	if out.QualityOverride == nil {
		out.QualityOverride = map[string]any{}
	}
	_ = json.Unmarshal(issues, &out.QualityIssues)
	return nil
}
