package submission

// 加页与换页继续先锁父答卷，冻结后拒绝修改；页内容和答卷页数沿用同一事务。
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

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
