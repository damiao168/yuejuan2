package regrade

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/taskmaterials"
)

var _ ContextSource = (*PostgresStore)(nil)

// GetGraderContext 只读取已领取重评条目所需的证据。
// 查询不连接 score_release、final_grade、human_grade 或 review_task，避免这些事实影响独立重评。
func (s *PostgresStore) GetGraderContext(ctx context.Context, tenantID, itemID, graderID string) (GraderContext, error) {
	return s.loadGraderContext(ctx, tenantID, itemID, graderID, false)
}

func (s *PostgresStore) loadGraderContext(ctx context.Context, tenantID, itemID, graderID string, manage bool) (GraderContext, error) {
	assignment := ` AND item.assigned_to=$3::uuid AND item.claimed_by=$3::uuid AND item.status='claimed'`
	args := []any{tenantID, itemID, graderID}
	if manage {
		assignment = ` AND item.status='awaiting_review'`
		args = args[:2]
	}
	row := s.db.QueryRowContext(ctx, `
SELECT
  item.id::text, item.job_id::text, item.submission_id::text, item.status,
  item.max_score::float8, item.revision, item.created_at,
  q.id::text, q.question_no, q.question_type, q.score::float8,
  COALESCE(q.stem,''), q.knowledge_points,
  to_jsonb(snapshot), seg.id::text, seg.status,
  COALESCE(answer.answer_text,''), COALESCE(answer.source,'')
FROM regrade_item item
JOIN regrade_job job
  ON job.tenant_id=item.tenant_id AND job.id=item.job_id
JOIN answer_segment seg
  ON seg.tenant_id=item.tenant_id AND seg.submission_id=item.submission_id
 AND seg.question_id=job.question_id AND seg.deleted_at IS NULL
JOIN question q
  ON q.tenant_id=job.tenant_id AND q.id=job.question_id
JOIN exam_question_snapshot snapshot
  ON snapshot.tenant_id=job.tenant_id AND snapshot.id=job.new_rubric_snapshot_id
 AND snapshot.exam_id=job.exam_id AND snapshot.question_id=job.question_id
LEFT JOIN LATERAL (
  SELECT answer_text, source
  FROM answer_segment_answer
  WHERE tenant_id=seg.tenant_id AND answer_segment_id=seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC, id DESC
  LIMIT 1
) answer ON true
WHERE item.tenant_id=$1::uuid AND item.id=$2::uuid
`+assignment+` AND job.status IN ('running','diff_review')
`, args...)

	var item Item
	var question GraderQuestion
	var knowledgeRaw, snapshotRaw []byte
	var segmentID, segmentStatus, answerText, answerSource string
	if err := row.Scan(
		&item.ID, &item.JobID, &item.SubmissionID, &item.Status,
		&item.MaxScore, &item.Revision, &item.CreatedAt,
		&question.ID, &question.QuestionNo, &question.QuestionType, &question.Score,
		&question.Stem, &knowledgeRaw,
		&snapshotRaw, &segmentID, &segmentStatus, &answerText, &answerSource,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GraderContext{}, ErrNotFound
		}
		return GraderContext{}, err
	}
	if err := json.Unmarshal(knowledgeRaw, &question.KnowledgePoints); err != nil {
		return GraderContext{}, err
	}
	var snapshot assessment.ExamQuestionSnapshot
	if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
		return GraderContext{}, err
	}
	reference, err := (taskmaterials.PostgresReader{DB: s.db}).QuestionForSnapshot(ctx, tenantID, snapshot.ExamID, question.ID, snapshot.ID)
	if err != nil {
		return GraderContext{}, err
	}
	question.Options, question.AnswerKey, question.Solution = reference.Options, reference.AnswerKey, reference.Solution
	rubric, ok := frozenRubric(snapshot.RubricSnapshot, question.ID)
	if !ok {
		return GraderContext{}, ErrStateConflict
	}
	answer := GraderAnswer{SegmentStatus: segmentStatus, SegmentImageURL: "/api/v1/regrade-items/" + item.ID + "/segment-image"}
	if answerSource == "ocr_text" {
		answer.OCRText = answerText
	} else {
		answer.RawAnswer = answerText
	}
	return GraderContext{
		Item: workItem(item), ExpectedRevision: item.Revision,
		Question: question, FrozenRubric: rubric, Answer: answer,
	}, nil
}

func (s *PostgresStore) GetReviewContext(ctx context.Context, tenantID, itemID string) (ReviewContext, error) {
	item, err := scanItem(s.db.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM regrade_item WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='awaiting_review'`, tenantID, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewContext{}, ErrNotFound
	}
	if err != nil {
		return ReviewContext{}, err
	}
	value, err := s.loadGraderContext(ctx, tenantID, itemID, "", true)
	if err != nil {
		return ReviewContext{}, err
	}
	if item.Revision != value.ExpectedRevision {
		return ReviewContext{}, ErrRevisionConflict
	}
	value.Answer.SegmentImageURL = "/api/v1/regrade-items/" + item.ID + "/review-segment-image"
	return ReviewContext{Item: item, Context: value}, nil
}

func (s *PostgresStore) GetReviewSegmentID(ctx context.Context, tenantID, itemID string) (string, error) {
	var segmentID string
	err := s.db.QueryRowContext(ctx, `SELECT seg.id::text FROM regrade_item item
JOIN regrade_job job ON job.tenant_id=item.tenant_id AND job.id=item.job_id
JOIN answer_segment seg ON seg.tenant_id=item.tenant_id AND seg.submission_id=item.submission_id AND seg.question_id=job.question_id AND seg.deleted_at IS NULL
WHERE item.tenant_id=$1::uuid AND item.id=$2::uuid AND item.status='awaiting_review' AND job.status IN ('running','diff_review')`, tenantID, itemID).Scan(&segmentID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return segmentID, err
}

func (s *PostgresStore) GetSegmentID(ctx context.Context, tenantID, itemID, graderID string) (string, error) {
	var segmentID string
	err := s.db.QueryRowContext(ctx, `
SELECT seg.id::text
FROM regrade_item item
JOIN regrade_job job ON job.tenant_id=item.tenant_id AND job.id=item.job_id
JOIN answer_segment seg
  ON seg.tenant_id=item.tenant_id AND seg.submission_id=item.submission_id
 AND seg.question_id=job.question_id AND seg.deleted_at IS NULL
WHERE item.tenant_id=$1::uuid AND item.id=$2::uuid
  AND item.assigned_to=$3::uuid AND item.claimed_by=$3::uuid
  AND item.status='claimed' AND job.status IN ('running','diff_review')
`, tenantID, itemID, graderID).Scan(&segmentID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return segmentID, err
}

func frozenRubric(snapshot map[string]any, questionID string) (paper.Rubric, bool) {
	if len(snapshot) == 0 {
		return paper.Rubric{}, false
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return paper.Rubric{}, false
	}
	var rubric paper.Rubric
	if err := json.Unmarshal(raw, &rubric); err != nil {
		return paper.Rubric{}, false
	}
	rubric.QuestionID = questionID
	return rubric, rubric.ID != "" || rubric.Version != "" || len(rubric.Points) > 0
}
