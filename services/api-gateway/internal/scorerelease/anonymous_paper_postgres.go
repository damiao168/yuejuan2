package scorerelease

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
)

// Validate again under the publish transaction. A page, registration run, or
// template changed after derivation must make publication fail closed.
func (s *PostgresStore) anonymousPaperReadyTx(ctx context.Context, tx *sql.Tx, tenantID, releaseID string) (bool, error) {
	var accepted, expected, matched, derived int
	err := tx.QueryRowContext(ctx, `
WITH release AS (
  SELECT exam_id FROM score_release WHERE tenant_id=$1::uuid AND id=$2::uuid
), highest AS (
  SELECT submission_id FROM score_release_item
  WHERE tenant_id=$1::uuid AND release_id=$2::uuid AND student_id IS NOT NULL
  ORDER BY total_score DESC,submission_id LIMIT 1
), expected AS (
  SELECT sp.id,sp.page_no,source.hash_sha256 AS source_hash,r.template_id,r.template_content_hash
  FROM highest h
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
  (SELECT count(*) FROM highest h JOIN submission_page sp ON sp.tenant_id=$1::uuid AND sp.submission_id=h.submission_id WHERE sp.status='accepted' AND sp.deleted_at IS NULL),
  (SELECT count(*) FROM expected),
  (SELECT count(*) FROM matched),
  (SELECT count(*) FROM score_release_anonymous_page WHERE tenant_id=$1::uuid AND release_id=$2::uuid AND revoked_at IS NULL)
`, tenantID, releaseID).Scan(&accepted, &expected, &matched, &derived)
	if err != nil {
		return false, err
	}
	// 原始页、可配准页、校验通过的匿名页和全部匿名页数量都要相等，缺页或多余旧页都会阻止发布。
	return accepted > 0 && accepted == expected && expected == matched && matched == derived, nil
}

func (s *PostgresStore) studentHighScorePaper(ctx context.Context, tenantID, releaseID string) (*StudentHighScorePaper, error) {
	var submissionID string
	var total, maxScore float64
	err := s.db.QueryRowContext(ctx, `SELECT submission_id::text,total_score::float8,max_score::float8
FROM score_release_item WHERE tenant_id=$1::uuid AND release_id=$2::uuid AND student_id IS NOT NULL
ORDER BY total_score DESC,submission_id LIMIT 1`, tenantID, releaseID).Scan(&submissionID, &total, &maxScore)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var accepted, shared int
	if err := s.db.QueryRowContext(ctx, `SELECT
  (SELECT count(*) FROM submission_page WHERE tenant_id=$1::uuid AND submission_id=$2::uuid AND status='accepted' AND deleted_at IS NULL),
	  (SELECT count(*) FROM score_release_anonymous_page ap
	   JOIN file_asset asset ON asset.tenant_id=ap.tenant_id AND asset.id=ap.file_asset_id
	     AND asset.hash_sha256=ap.derived_sha256 AND asset.owner_type='score_release_anonymous_page'
	     AND asset.owner_id=ap.id AND asset.lifecycle_status='active' AND asset.deleted_at IS NULL AND asset.legal_hold
	   WHERE ap.tenant_id=$1::uuid AND ap.release_id=$3::uuid AND ap.revoked_at IS NULL)`,
		tenantID, submissionID, releaseID).Scan(&accepted, &shared); err != nil {
		return nil, err
	}
	if accepted == 0 || accepted != shared {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT ap.page_no,q.question_id::text
FROM score_release_anonymous_page ap
JOIN LATERAL (
  SELECT rq.question_id FROM score_release_question rq
  JOIN answer_segment seg ON seg.tenant_id=rq.tenant_id AND seg.submission_id=rq.submission_id
    AND seg.question_id=rq.question_id AND seg.submission_page_id=ap.source_submission_page_id
    AND seg.deleted_at IS NULL
  WHERE rq.tenant_id=ap.tenant_id AND rq.release_id=ap.release_id AND rq.submission_id=$3::uuid
  ORDER BY rq.question_no,rq.question_id LIMIT 1
) q ON true
WHERE ap.tenant_id=$1::uuid AND ap.release_id=$2::uuid AND ap.revoked_at IS NULL
ORDER BY ap.page_no`, tenantID, releaseID, submissionID)
	if err != nil {
		return nil, err
	}
	pages := []StudentPaperPage{}
	for rows.Next() {
		var page StudentPaperPage
		if err := rows.Scan(&page.PageNo, &page.QuestionID); err != nil {
			rows.Close()
			return nil, err
		}
		pages = append(pages, page)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(pages) != accepted {
		return nil, nil
	}
	marksRows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT ON(q.question_id) q.question_id::text,q.question_no,q.score::float8,q.max_score::float8,
       sp.page_no,COALESCE(seg.normalized_bbox,'{}'::jsonb)
FROM score_release_question q
JOIN answer_segment seg ON seg.tenant_id=q.tenant_id AND seg.submission_id=q.submission_id
  AND seg.question_id=q.question_id AND seg.deleted_at IS NULL
JOIN submission_page sp ON sp.tenant_id=seg.tenant_id AND sp.id=seg.submission_page_id AND sp.deleted_at IS NULL
JOIN score_release_anonymous_page ap ON ap.tenant_id=q.tenant_id AND ap.release_id=q.release_id
  AND ap.source_submission_page_id=sp.id AND ap.revoked_at IS NULL
WHERE q.tenant_id=$1::uuid AND q.release_id=$2::uuid AND q.submission_id=$3::uuid
ORDER BY q.question_id,seg.updated_at DESC,seg.id DESC`, tenantID, releaseID, submissionID)
	if err != nil {
		return nil, err
	}
	marks := []StudentPaperScoreMark{}
	for marksRows.Next() {
		var mark StudentPaperScoreMark
		var raw []byte
		if err := marksRows.Scan(&mark.QuestionID, &mark.QuestionNo, &mark.Score, &mark.MaxScore, &mark.PageNo, &raw); err != nil {
			marksRows.Close()
			return nil, err
		}
		var values map[string]float64
		_ = json.Unmarshal(raw, &values)
		if values["width"] > 0 && values["height"] > 0 {
			mark.AnswerGeometry = &StudentImageGeometry{X: values["x"], Y: values["y"], Width: values["width"], Height: values["height"]}
		}
		marks = append(marks, mark)
	}
	err = marksRows.Err()
	marksRows.Close()
	if err != nil {
		return nil, err
	}
	sort.Slice(marks, func(i, j int) bool { return marks[i].QuestionNo < marks[j].QuestionNo })
	return &StudentHighScorePaper{Available: true, TotalScore: total, MaxScore: maxScore, Pages: pages, ScoreMarks: marks}, nil
}
