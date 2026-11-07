package submission

// 质量结果继续先锁答卷再锁页面，旧源图、冻结状态和相同运行重放不覆盖当前投影。
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return QualityResult{}, err
	}
	defer tx.Rollback()
	// 与换页、加页及质量回调共用父锁；等锁后才读取页面，不能把旧图的通过状态写回新图。
	if _, err = lockWritableSubmissionTx(ctx, tx, tenantID, submissionID); err != nil {
		return QualityResult{}, err
	}
	// 采集完整性只是第一道门禁。只有每页都通过图像质量检查或获得有效覆盖批准后，聚合结果才可通过。
	if err = aggregateQualityTx(ctx, tx, tenantID, submissionID); err != nil {
		return QualityResult{}, err
	}
	var issuesJSON []byte
	if err = tx.QueryRowContext(ctx, `SELECT quality_issues FROM submission WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, submissionID).Scan(&issuesJSON); err != nil {
		return QualityResult{}, err
	}
	issues := []QualityIssue{}
	if err = json.Unmarshal(issuesJSON, &issues); err != nil {
		return QualityResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return QualityResult{}, err
	}
	return QualityResult{Valid: len(issues) == 0, Issues: issues}, nil
}

// RefreshQualityInTx 在页面编号或归属变化后重算答卷完整性与图像质量。
// 调用方须先锁住父答卷，再修改页面；与页面回写共用事务，不能把临时页号留下的缺页结论继续作为发布事实。
// 已冻结或拒收的答卷沿用原有状态保护，不会因重算自动恢复为可处理状态。
func RefreshQualityInTx(ctx context.Context, tx *sql.Tx, tenantID, submissionID string) error {
	if tx == nil || tenantID == "" || submissionID == "" {
		return ErrInvalidInput
	}
	return aggregateQualityTx(ctx, tx, tenantID, submissionID)
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
