package scorerelease

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
)

func (s *PostgresStore) studentPaperPresentation(ctx context.Context, tenantID, submissionID string) (map[string]studentQuestionPresentation, []StudentPaperPage, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT ON(seg.question_id) seg.question_id::text,sp.page_no,sp.id::text,COALESCE(seg.normalized_bbox,'{}'::jsonb)
FROM answer_segment seg JOIN submission_page sp ON sp.tenant_id=seg.tenant_id AND sp.id=seg.submission_page_id AND sp.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND seg.submission_id=$2::uuid AND seg.deleted_at IS NULL
ORDER BY seg.question_id,seg.updated_at DESC,seg.id DESC
`, tenantID, submissionID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := map[string]studentQuestionPresentation{}
	pageByNo := map[int]StudentPaperPage{}
	for rows.Next() {
		var id, pageID string
		var pageNo int
		var raw []byte
		if err := rows.Scan(&id, &pageNo, &pageID, &raw); err != nil {
			return nil, nil, err
		}
		var values map[string]float64
		_ = json.Unmarshal(raw, &values)
		var geometry *StudentImageGeometry
		if values["width"] > 0 && values["height"] > 0 {
			geometry = &StudentImageGeometry{X: values["x"], Y: values["y"], Width: values["width"], Height: values["height"]}
		}
		out[id] = studentQuestionPresentation{PageNo: pageNo, SubmissionPageID: pageID, Geometry: geometry}
		if _, exists := pageByNo[pageNo]; !exists {
			pageByNo[pageNo] = StudentPaperPage{PageNo: pageNo, QuestionID: id, SubmissionPageID: pageID}
		}
	}
	pages := make([]StudentPaperPage, 0, len(pageByNo))
	for _, page := range pageByNo {
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].PageNo < pages[j].PageNo })
	return out, pages, rows.Err()
}

func (s *PostgresStore) StudentQuestion(ctx context.Context, tenantID, examID, studentID, questionID string, expectedReleaseID ...string) (StudentQuestion, error) {
	result, err := s.StudentResult(ctx, tenantID, examID, studentID)
	if err != nil {
		return StudentQuestion{}, err
	}
	// 比较本次实际读取的不可变发布，而不是另外预读当前指针；发布恰好切换时不能拼接新题目和旧申请。
	if err := requireStudentRelease(result.ReleaseID, expectedReleaseID); err != nil {
		return StudentQuestion{}, err
	}
	if !result.QuestionsIsVisible() {
		return StudentQuestion{}, ErrForbidden
	}
	for _, question := range result.Questions {
		if question.QuestionID == questionID {
			return question, nil
		}
	}
	return StudentQuestion{}, ErrNotFound
}

func (s *PostgresStore) StudentQuestionImage(ctx context.Context, tenantID, examID, studentID, questionID string, expectedReleaseID ...string) (StudentQuestionImageSource, error) {
	var source StudentQuestionImageSource
	err := s.db.QueryRowContext(ctx, `
SELECT answer_segment.id::text, release.id::text
FROM score_release_current current_release
JOIN score_release release
  ON release.tenant_id = current_release.tenant_id
 AND release.id = current_release.release_id
 AND release.status = 'published'
JOIN score_release_item item
  ON item.tenant_id = release.tenant_id
 AND item.release_id = release.id
 AND item.student_id = $3::uuid
JOIN score_release_question release_question
  ON release_question.tenant_id = item.tenant_id
 AND release_question.release_id = item.release_id
 AND release_question.submission_id = item.submission_id
 AND release_question.question_id = $4::uuid
JOIN answer_segment
  ON answer_segment.tenant_id = release_question.tenant_id
 AND answer_segment.submission_id = release_question.submission_id
 AND answer_segment.question_id = release_question.question_id
 AND answer_segment.deleted_at IS NULL
WHERE current_release.tenant_id = $1
  AND current_release.exam_id = $2::uuid
  AND COALESCE((release.visibility_policy ->> 'show_question_scores')::boolean, false)
ORDER BY answer_segment.updated_at DESC, answer_segment.id
LIMIT 1
`, tenantID, examID, studentID, questionID).Scan(&source.AnswerSegmentID, &source.ReleaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return StudentQuestionImageSource{}, ErrNotFound
	}
	if err != nil {
		return StudentQuestionImageSource{}, err
	}
	// 图片来源与发布 ID 由同一条查询取得，比较后才交给资源读取器。
	if err := requireStudentRelease(source.ReleaseID, expectedReleaseID); err != nil {
		return StudentQuestionImageSource{}, err
	}
	return source, nil
}

func (s *PostgresStore) StudentPaperPageImage(ctx context.Context, tenantID, examID, studentID, questionID string, highScore bool, expectedReleaseID ...string) (StudentQuestionImageSource, error) {
	if highScore {
		return s.studentAnonymousPageImage(ctx, tenantID, examID, studentID, questionID, "", expectedReleaseID)
	}
	// 整页图片同样以发布题目清单为范围；仅有答题分段不能证明题目已发布。
	var source StudentQuestionImageSource
	err := s.db.QueryRowContext(ctx, `
SELECT seg.id::text, release.id::text FROM score_release_current current_release
  JOIN score_release release ON release.tenant_id=current_release.tenant_id AND release.id=current_release.release_id AND release.status='published'
JOIN score_release_item item ON item.tenant_id=release.tenant_id AND item.release_id=release.id AND item.student_id=$3::uuid
JOIN score_release_question release_question
  ON release_question.tenant_id=item.tenant_id
 AND release_question.release_id=item.release_id
 AND release_question.submission_id=item.submission_id
 AND release_question.question_id=$4::uuid
JOIN answer_segment seg ON seg.tenant_id=item.tenant_id AND seg.submission_id=item.submission_id AND seg.question_id=$4::uuid AND seg.deleted_at IS NULL
WHERE current_release.tenant_id=$1::uuid AND current_release.exam_id=$2::uuid
  AND COALESCE((release.visibility_policy->>'show_question_scores')::boolean,false)
ORDER BY seg.updated_at DESC,seg.id DESC LIMIT 1
`, tenantID, examID, studentID, questionID).Scan(&source.AnswerSegmentID, &source.ReleaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return StudentQuestionImageSource{}, ErrNotFound
	}
	if err != nil {
		return StudentQuestionImageSource{}, err
	}
	if err := requireStudentRelease(source.ReleaseID, expectedReleaseID); err != nil {
		return StudentQuestionImageSource{}, err
	}
	return source, nil
}
