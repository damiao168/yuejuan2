package scorerelease

import (
	"context"
	"database/sql"
)

// 在发布事务内再次校验。派生后若页面、配准运行或模板发生改变，必须拒绝发布。
func (s *PostgresStore) anonymousPaperReadyTx(ctx context.Context, tx *sql.Tx, tenantID, releaseID string) (bool, error) {
	var accepted, expected, matched, derived, selected, withPages int
	err := tx.QueryRowContext(ctx, `
WITH release AS (
  SELECT id,tenant_id,exam_id,visibility_policy FROM score_release WHERE tenant_id=$1::uuid AND id=$2::uuid
)`+excellentPaperCandidatesCTE+`, expected AS (
  SELECT sp.id,sp.page_no,source.hash_sha256 AS source_hash,r.template_id,r.template_content_hash
  FROM selected_papers h
  JOIN submission_page sp ON sp.tenant_id=$1::uuid AND sp.submission_id=h.submission_id
    AND sp.status='accepted' AND sp.deleted_at IS NULL
  JOIN LATERAL (
    SELECT run.* FROM page_registration_run run
    WHERE run.tenant_id=sp.tenant_id AND run.submission_page_id=sp.id AND run.deleted_at IS NULL
    ORDER BY run.created_at DESC,run.id DESC LIMIT 1
  ) r ON r.processing_status='completed' AND r.match_status='matched'
      AND r.registered_file_asset_id IS NOT NULL AND r.page_no=sp.page_no
  JOIN answer_sheet_template template ON template.tenant_id=r.tenant_id AND template.id=r.template_id
    AND template.content_hash=r.template_content_hash AND template.status='locked' AND template.deleted_at IS NULL
  JOIN file_asset source ON source.tenant_id=r.tenant_id AND source.id=r.registered_file_asset_id
    AND source.exam_id=(SELECT exam_id FROM release)
    AND source.lifecycle_status='active' AND source.deleted_at IS NULL
), matched AS (
  SELECT ap.id
  FROM expected e
  JOIN score_release_anonymous_page ap ON ap.tenant_id=$1::uuid AND ap.release_id=$2::uuid
    AND ap.source_submission_page_id=e.id AND ap.page_no=e.page_no
    AND ap.source_sha256=e.source_hash AND ap.template_id=e.template_id
    AND ap.template_content_hash=e.template_content_hash
    AND ap.redaction_version='identity-regions-v1' AND ap.revoked_at IS NULL
  JOIN file_asset asset ON asset.tenant_id=ap.tenant_id AND asset.id=ap.file_asset_id
    AND asset.owner_type='score_release_anonymous_page' AND asset.owner_id=ap.id
    AND asset.hash_sha256=ap.derived_sha256 AND asset.content_type='image/png'
    AND asset.lifecycle_status='active' AND asset.deleted_at IS NULL AND asset.legal_hold
)
SELECT
  (SELECT count(*) FROM selected_papers h JOIN submission_page sp ON sp.tenant_id=$1::uuid AND sp.submission_id=h.submission_id WHERE sp.status='accepted' AND sp.deleted_at IS NULL),
  (SELECT count(*) FROM expected),
  (SELECT count(*) FROM matched),
  (SELECT count(*) FROM score_release_anonymous_page WHERE tenant_id=$1::uuid AND release_id=$2::uuid AND revoked_at IS NULL),
  (SELECT count(*) FROM selected_papers),
  (SELECT count(DISTINCT h.submission_id) FROM selected_papers h JOIN submission_page sp
    ON sp.tenant_id=$1::uuid AND sp.submission_id=h.submission_id WHERE sp.status='accepted' AND sp.deleted_at IS NULL)
`, tenantID, releaseID).Scan(&accepted, &expected, &matched, &derived, &selected, &withPages)
	if err != nil {
		return false, err
	}
	// 每份选中答卷都须有有效页；只看总页数，会把“有卷完全没页”的情况漏掉。
	// 原始页、可配准页、匿名页和全部派生页数量仍须相等，缺页或多余旧页都会阻止发布。
	return selected > 0 && selected == withPages && accepted > 0 && accepted == expected && expected == matched && matched == derived, nil
}
