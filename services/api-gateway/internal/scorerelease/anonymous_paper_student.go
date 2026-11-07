package scorerelease

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
)

// StudentSelectedHighScorePaperPageImage 将匿名卷、题目和当前发布一起解析；匿名 ID 不是原始答卷或学生 ID。
func (s *PostgresStore) StudentSelectedHighScorePaperPageImage(ctx context.Context, tenantID, examID, studentID, questionID, paperID, expectedReleaseID string) (StudentQuestionImageSource, error) {
	return s.studentAnonymousPageImage(ctx, tenantID, examID, studentID, questionID, paperID, []string{expectedReleaseID})
}

func (s *PostgresStore) studentAnonymousPageImage(ctx context.Context, tenantID, examID, studentID, questionID, paperID string, expectedReleaseID []string) (StudentQuestionImageSource, error) {
	var source StudentQuestionImageSource
	// 即使请求的匿名卷已经不在新发布中，也先返回本人当前发布标识，供客户端识别换版。
	// 只在选中卷的有效匿名页中解析题目；原始答卷 ID 和图像对象均不走公开参数。
	err := s.db.QueryRowContext(ctx, `WITH release AS (
  SELECT r.id,r.tenant_id,r.exam_id,r.visibility_policy
  FROM score_release_current current_release
  JOIN score_release r ON r.tenant_id=current_release.tenant_id AND r.id=current_release.release_id
    AND r.exam_id=current_release.exam_id AND r.status='published'
  WHERE current_release.tenant_id=$1::uuid AND current_release.exam_id=$2::uuid
    AND EXISTS (SELECT 1 FROM score_release_item mine WHERE mine.tenant_id=r.tenant_id
      AND mine.release_id=r.id AND mine.student_id=$3::uuid)
)`+excellentPaperCandidatesCTE+`, valid_pages AS (
  SELECT ap.id,ap.file_asset_id,ap.page_no,ap.source_submission_page_id,sp.submission_id
  FROM release
  JOIN score_release_anonymous_page ap ON ap.tenant_id=release.tenant_id AND ap.release_id=release.id AND ap.revoked_at IS NULL
  JOIN submission_page sp ON sp.tenant_id=ap.tenant_id AND sp.id=ap.source_submission_page_id
    AND sp.status='accepted' AND sp.deleted_at IS NULL
  JOIN selected_papers selected ON selected.submission_id=sp.submission_id
  JOIN file_asset asset ON asset.tenant_id=ap.tenant_id AND asset.id=ap.file_asset_id
    AND asset.hash_sha256=ap.derived_sha256 AND asset.owner_type='score_release_anonymous_page'
    AND asset.owner_id=ap.id AND asset.lifecycle_status='active' AND asset.deleted_at IS NULL
    AND asset.content_type='image/png' AND asset.legal_hold
), anonymous_papers AS (
  SELECT selected.submission_id,selected.source_order,
         (array_agg(valid.id ORDER BY valid.page_no,valid.id))[1] AS paper_id
  FROM selected_papers selected CROSS JOIN release
  JOIN valid_pages valid ON valid.submission_id=selected.submission_id
  GROUP BY selected.submission_id,selected.source_order,release.tenant_id
  HAVING count(*)=(SELECT count(*) FROM submission_page sp WHERE sp.tenant_id=release.tenant_id
    AND sp.submission_id=selected.submission_id AND sp.status='accepted' AND sp.deleted_at IS NULL)
)
SELECT release.id::text,COALESCE(page.id::text,''),COALESCE(page.file_asset_id::text,'')
FROM release
LEFT JOIN LATERAL (
  SELECT valid.id,valid.file_asset_id
  FROM anonymous_papers chosen
  JOIN score_release_question q ON q.tenant_id=release.tenant_id AND q.release_id=release.id
    AND q.submission_id=chosen.submission_id AND q.question_id=$4::uuid
  JOIN answer_segment seg ON seg.tenant_id=q.tenant_id AND seg.submission_id=q.submission_id
    AND seg.question_id=q.question_id AND seg.deleted_at IS NULL
  JOIN valid_pages valid ON valid.submission_id=chosen.submission_id AND valid.source_submission_page_id=seg.submission_page_id
  WHERE CASE WHEN $5='' THEN chosen.source_order=1 ELSE chosen.paper_id=NULLIF($5,'')::uuid END
    AND COALESCE((release.visibility_policy->>'show_question_scores')::boolean,false)
    AND COALESCE((release.visibility_policy->>'show_high_score_paper')::boolean,false)
    AND (SELECT count(*) FROM anonymous_papers)=(SELECT count(*) FROM selected_papers)
  ORDER BY seg.updated_at DESC,seg.id DESC LIMIT 1
) page ON true`, tenantID, examID, studentID, questionID, paperID).Scan(&source.ReleaseID, &source.AnonymousPageID, &source.AnonymousFileAssetID)
	if errors.Is(err, sql.ErrNoRows) {
		return StudentQuestionImageSource{}, ErrNotFound
	}
	if err != nil {
		return StudentQuestionImageSource{}, err
	}
	if err := requireStudentRelease(source.ReleaseID, expectedReleaseID); err != nil {
		return StudentQuestionImageSource{}, err
	}
	if source.AnonymousPageID == "" {
		return StudentQuestionImageSource{}, ErrNotFound
	}
	return source, nil
}

// 优秀答卷只暴露独立匿名标识；内部 submission ID 仅用于将本次冻结分数、匿名页和批注位置分组。
type studentAnonymousPaper struct {
	accepted int
	paper    StudentHighScorePaper
}

func (s *PostgresStore) studentHighScorePapers(ctx context.Context, tenantID, releaseID string) ([]StudentHighScorePaper, error) {
	rows, err := s.db.QueryContext(ctx, `WITH release AS (
  SELECT id,tenant_id,exam_id,visibility_policy FROM score_release WHERE tenant_id=$1::uuid AND id=$2::uuid
)`+excellentPaperCandidatesCTE+`
SELECT selected.submission_id::text,selected.total_score::float8,selected.max_score::float8,
       COALESCE(shared.paper_id,''),accepted.total,shared.total
FROM selected_papers selected CROSS JOIN release
CROSS JOIN LATERAL (
  SELECT count(*) AS total FROM submission_page sp
  WHERE sp.tenant_id=release.tenant_id AND sp.submission_id=selected.submission_id
    AND sp.status='accepted' AND sp.deleted_at IS NULL
) accepted
CROSS JOIN LATERAL (
  SELECT count(*) AS total,(array_agg(ap.id::text ORDER BY ap.page_no,ap.id))[1] AS paper_id
  FROM score_release_anonymous_page ap
  JOIN submission_page sp ON sp.tenant_id=ap.tenant_id AND sp.id=ap.source_submission_page_id
    AND sp.submission_id=selected.submission_id AND sp.status='accepted' AND sp.deleted_at IS NULL
  JOIN file_asset asset ON asset.tenant_id=ap.tenant_id AND asset.id=ap.file_asset_id
    AND asset.hash_sha256=ap.derived_sha256 AND asset.owner_type='score_release_anonymous_page'
    AND asset.owner_id=ap.id AND asset.lifecycle_status='active' AND asset.deleted_at IS NULL
    AND asset.content_type='image/png' AND asset.legal_hold
  WHERE ap.tenant_id=release.tenant_id AND ap.release_id=release.id AND ap.revoked_at IS NULL
) shared
ORDER BY selected.total_score DESC,selected.submission_id`, tenantID, releaseID)
	if err != nil {
		return nil, err
	}
	bySubmission := map[string]*studentAnonymousPaper{}
	order := []string{}
	complete := true
	for rows.Next() {
		var submissionID string
		var item studentAnonymousPaper
		var shared int
		if err := rows.Scan(&submissionID, &item.paper.TotalScore, &item.paper.MaxScore, &item.paper.PaperID, &item.accepted, &shared); err != nil {
			rows.Close()
			return nil, err
		}
		if item.accepted <= 0 || item.accepted != shared || item.paper.PaperID == "" {
			complete = false
		}
		item.paper.Available = true
		bySubmission[submissionID] = &item
		order = append(order, submissionID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// 生成和发布都按完整候选集合校验；读取也不把失效或缺页的卷静默剔除后改称原来的名次范围。
	if !complete || len(order) == 0 {
		return nil, nil
	}
	pageRows, err := s.db.QueryContext(ctx, `WITH release AS (
  SELECT id,tenant_id,exam_id,visibility_policy FROM score_release WHERE tenant_id=$1::uuid AND id=$2::uuid
)`+excellentPaperCandidatesCTE+`
SELECT selected.submission_id::text,ap.page_no,q.question_id::text
FROM selected_papers selected CROSS JOIN release
JOIN submission_page sp ON sp.tenant_id=release.tenant_id AND sp.submission_id=selected.submission_id
  AND sp.status='accepted' AND sp.deleted_at IS NULL
JOIN score_release_anonymous_page ap ON ap.tenant_id=release.tenant_id AND ap.release_id=release.id
  AND ap.source_submission_page_id=sp.id AND ap.revoked_at IS NULL
JOIN LATERAL (
  SELECT rq.question_id FROM score_release_question rq
  JOIN answer_segment seg ON seg.tenant_id=rq.tenant_id AND seg.submission_id=rq.submission_id
    AND seg.question_id=rq.question_id AND seg.submission_page_id=sp.id AND seg.deleted_at IS NULL
  WHERE rq.tenant_id=release.tenant_id AND rq.release_id=release.id AND rq.submission_id=selected.submission_id
  ORDER BY rq.question_no,rq.question_id LIMIT 1
) q ON true
ORDER BY selected.total_score DESC,selected.submission_id,ap.page_no`, tenantID, releaseID)
	if err != nil {
		return nil, err
	}
	for pageRows.Next() {
		var submissionID string
		var page StudentPaperPage
		if err := pageRows.Scan(&submissionID, &page.PageNo, &page.QuestionID); err != nil {
			pageRows.Close()
			return nil, err
		}
		if item := bySubmission[submissionID]; item != nil {
			item.paper.Pages = append(item.paper.Pages, page)
		}
	}
	err = pageRows.Err()
	pageRows.Close()
	if err != nil {
		return nil, err
	}
	for _, item := range bySubmission {
		if len(item.paper.Pages) != item.accepted {
			return nil, nil
		}
	}
	markRows, err := s.db.QueryContext(ctx, `WITH release AS (
  SELECT id,tenant_id,exam_id,visibility_policy FROM score_release WHERE tenant_id=$1::uuid AND id=$2::uuid
)`+excellentPaperCandidatesCTE+`
SELECT DISTINCT ON(q.submission_id,q.question_id) q.submission_id::text,q.question_id::text,q.question_no,q.score::float8,q.max_score::float8,
       sp.page_no,COALESCE(seg.normalized_bbox,'{}'::jsonb)
FROM selected_papers selected CROSS JOIN release
JOIN score_release_question q ON q.tenant_id=release.tenant_id AND q.release_id=release.id AND q.submission_id=selected.submission_id
JOIN answer_segment seg ON seg.tenant_id=q.tenant_id AND seg.submission_id=q.submission_id
  AND seg.question_id=q.question_id AND seg.deleted_at IS NULL
JOIN submission_page sp ON sp.tenant_id=seg.tenant_id AND sp.id=seg.submission_page_id
  AND sp.status='accepted' AND sp.deleted_at IS NULL
JOIN score_release_anonymous_page ap ON ap.tenant_id=q.tenant_id AND ap.release_id=q.release_id
  AND ap.source_submission_page_id=sp.id AND ap.revoked_at IS NULL
ORDER BY q.submission_id,q.question_id,seg.updated_at DESC,seg.id DESC`, tenantID, releaseID)
	if err != nil {
		return nil, err
	}
	for markRows.Next() {
		var submissionID string
		var mark StudentPaperScoreMark
		var raw []byte
		if err := markRows.Scan(&submissionID, &mark.QuestionID, &mark.QuestionNo, &mark.Score, &mark.MaxScore, &mark.PageNo, &raw); err != nil {
			markRows.Close()
			return nil, err
		}
		var values map[string]float64
		_ = json.Unmarshal(raw, &values)
		if values["width"] > 0 && values["height"] > 0 {
			mark.AnswerGeometry = &StudentImageGeometry{X: values["x"], Y: values["y"], Width: values["width"], Height: values["height"]}
		}
		if item := bySubmission[submissionID]; item != nil {
			item.paper.ScoreMarks = append(item.paper.ScoreMarks, mark)
		}
	}
	err = markRows.Err()
	markRows.Close()
	if err != nil {
		return nil, err
	}
	papers := make([]StudentHighScorePaper, 0, len(order))
	for _, submissionID := range order {
		item := bySubmission[submissionID]
		sort.Slice(item.paper.ScoreMarks, func(i, j int) bool { return item.paper.ScoreMarks[i].QuestionNo < item.paper.ScoreMarks[j].QuestionNo })
		papers = append(papers, item.paper)
	}
	return papers, nil
}
