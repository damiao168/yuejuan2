package scorerelease

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

func (s *PostgresStore) currentFactsTx(ctx context.Context, tx *sql.Tx, tenantID, examID string) ([]SubmissionFact, error) {
	// 单评反馈只采用终评分数、满分一致且时间来源唯一的记录；无法对应时留空，避免错贴评语。
	// 发布事实只收录考试准备时冻结的题目；后来新增的题目即使有终评，也不能进入学生可见快照。
	rows, err := tx.QueryContext(ctx, `WITH latest_final AS (
  SELECT DISTINCT ON (answer_segment_id) * FROM final_grade
  WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL
  ORDER BY answer_segment_id,created_at DESC,id DESC
)
SELECT sg.student_id::text, sg.submission_id::text, sg.total_score::float8, sg.max_score::float8, sg.status,
  fg.question_id::text, fg.question_no, fg.id::text, fg.score::float8, fg.max_score::float8, fg.source,
  CASE fg.source
    WHEN 'arbitration' THEN COALESCE(fg.arbitration_task_id::text, '')
    WHEN 'single_review' THEN COALESCE(human_source.id::text, '')
    WHEN 'double_mark_auto' THEN COALESCE(fg.double_mark_session_id::text, '')
    ELSE fg.id::text END,
  CASE fg.source
    WHEN 'arbitration' THEN COALESCE(arbitration_source.student_feedback, '')
    WHEN 'single_review' THEN COALESCE(human_source.student_feedback, '')
    ELSE '' END,
  COALESCE(q.question_type, ''), COALESCE(q.stem, ''), COALESCE(q.knowledge_points, '[]'::jsonb),
  COALESCE((SELECT ak.standard_answer::text FROM question_answer_key ak
    WHERE ak.tenant_id=q.tenant_id AND ak.question_id=q.id AND ak.deleted_at IS NULL
    ORDER BY ak.created_at DESC,ak.id DESC LIMIT 1), ''),
  COALESCE((SELECT NULLIF(asa.answer_text, '') FROM answer_segment_answer asa
    WHERE asa.tenant_id=fg.tenant_id AND asa.answer_segment_id=fg.answer_segment_id AND asa.deleted_at IS NULL
    ORDER BY asa.created_at DESC,asa.id DESC LIMIT 1), '')
FROM submission_grade sg
JOIN latest_final fg ON fg.tenant_id = sg.tenant_id AND fg.exam_id = sg.exam_id AND fg.submission_id = sg.submission_id
JOIN question q ON q.tenant_id = fg.tenant_id AND q.id = fg.question_id AND q.deleted_at IS NULL
LEFT JOIN LATERAL (
  SELECT at.id, at.student_feedback FROM arbitration_task at
  WHERE fg.source = 'arbitration' AND at.tenant_id = fg.tenant_id AND at.id = fg.arbitration_task_id
    AND at.status = 'submitted' AND at.final_score = fg.score AND at.deleted_at IS NULL
) arbitration_source ON true
LEFT JOIN LATERAL (
  SELECT hg.id, hg.student_feedback, hg.score, hg.max_score,
    COUNT(*) OVER (PARTITION BY hg.created_at) AS timestamp_peers
  FROM human_grade hg
  JOIN review_task rt ON rt.tenant_id=hg.tenant_id AND rt.id=hg.review_task_id
  WHERE fg.source = 'single_review' AND hg.tenant_id = fg.tenant_id AND hg.answer_segment_id = fg.answer_segment_id
    AND hg.grade_round = 'single' AND rt.grade_round = 'single' AND rt.status IN ('submitted','completed')
    AND hg.created_at <= fg.created_at AND hg.deleted_at IS NULL
    AND rt.deleted_at IS NULL
  ORDER BY hg.created_at DESC, hg.id DESC LIMIT 1
) human_source ON human_source.score = fg.score AND human_source.max_score = fg.max_score AND human_source.timestamp_peers = 1
WHERE sg.tenant_id = $1 AND sg.exam_id = $2::uuid AND sg.deleted_at IS NULL
  AND EXISTS (SELECT 1 FROM exam_question_snapshot eqs
    WHERE eqs.tenant_id=fg.tenant_id AND eqs.exam_id=fg.exam_id AND eqs.question_id=fg.question_id)
ORDER BY sg.submission_id, fg.question_no, fg.id
`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bySubmission := map[string]SubmissionFact{}
	order := []string{}
	for rows.Next() {
		var studentID sql.NullString
		var fact SubmissionFact
		var question QuestionFact
		var feedback, questionType, stem, correctAnswer, actualAnswer string
		var knowledgePoints []byte
		if err := rows.Scan(&studentID, &fact.SubmissionID, &fact.TotalScore, &fact.MaxScore, &fact.Status, &question.QuestionID, &question.QuestionNo, &question.FinalGradeID, &question.Score, &question.MaxScore, &question.SourceType, &question.SourceID, &feedback, &questionType, &stem, &knowledgePoints, &correctAnswer, &actualAnswer); err != nil {
			return nil, err
		}
		fact.StudentID, question.Explanation.Feedback = studentID.String, strings.TrimSpace(feedback)
		question.Explanation.QuestionType, question.Explanation.Stem = strings.TrimSpace(questionType), strings.TrimSpace(stem)
		question.Explanation.CorrectAnswer = displayAnswer(correctAnswer)
		question.Explanation.ActualAnswer = strings.TrimSpace(actualAnswer)
		if err := json.Unmarshal(knowledgePoints, &question.Explanation.KnowledgePoints); err != nil {
			return nil, err
		}
		if existing, ok := bySubmission[fact.SubmissionID]; ok {
			existing.Questions = append(existing.Questions, question)
			bySubmission[fact.SubmissionID] = existing
		} else {
			fact.Questions = []QuestionFact{question}
			bySubmission[fact.SubmissionID] = fact
			order = append(order, fact.SubmissionID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]SubmissionFact, 0, len(order))
	for _, id := range order {
		out = append(out, bySubmission[id])
	}
	return out, nil
}

func displayAnswer(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "null" {
		return ""
	}
	var scalar string
	if json.Unmarshal([]byte(value), &scalar) == nil {
		return strings.TrimSpace(scalar)
	}
	return value
}

// insertReleaseTx 只使用草稿创建事实；状态、ID 和时间以数据库返回值为准。
// 成绩快照由调用方在同一事务写入，任何一步失败都会一起回滚。
func (s *PostgresStore) insertReleaseTx(ctx context.Context, tx *sql.Tx, draft Release) (Release, error) {
	draft.VisibilityPolicy = normalizeReleaseVisibility(draft.VisibilityPolicy)
	visibilityJSON, err := json.Marshal(draft.VisibilityPolicy)
	if err != nil {
		return Release{}, err
	}
	windowJSON, err := json.Marshal(draft.AppealWindow)
	if err != nil {
		return Release{}, err
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO score_release (tenant_id, exam_id, version, source, reason, idempotency_key, visibility_policy, appeal_window, source_release_id, created_by)
VALUES ($1, $2::uuid, $3, $4, $5, $6, $7::jsonb, $8::jsonb, NULLIF($9, '')::uuid, $10::uuid)
RETURNING `+releaseReturning, draft.TenantID, draft.ExamID, draft.Version, draft.Source, draft.Reason, draft.IdempotencyKey, string(visibilityJSON), string(windowJSON), draft.SourceReleaseID, draft.CreatedBy)
	return scanRelease(row)
}

func (s *PostgresStore) insertFactsTx(ctx context.Context, tx *sql.Tx, tenantID, releaseID string, facts []SubmissionFact) error {
	for _, fact := range facts {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO score_release_item (tenant_id, release_id, student_id, submission_id, total_score, max_score, status, snapshot_hash)
VALUES ($1, $2::uuid, NULLIF($3, '')::uuid, $4::uuid, $5, $6, $7, $8)
`, tenantID, releaseID, fact.StudentID, fact.SubmissionID, fact.TotalScore, fact.MaxScore, fact.Status, snapshotHash(fact)); err != nil {
			return err
		}
		for _, question := range fact.Questions {
			explanation, err := json.Marshal(question.Explanation)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO score_release_question (tenant_id, release_id, submission_id, question_id, question_no, final_grade_id, score, max_score, source_type, source_id, student_explanation)
VALUES ($1, $2::uuid, $3::uuid, $4::uuid, $5, $6::uuid, $7, $8, $9, NULLIF($10, '')::uuid, $11::jsonb)
`, tenantID, releaseID, fact.SubmissionID, question.QuestionID, question.QuestionNo, question.FinalGradeID, question.Score, question.MaxScore, question.SourceType, question.SourceID, string(explanation)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *PostgresStore) releaseFactsTx(ctx context.Context, tx *sql.Tx, tenantID, releaseID string) ([]SubmissionFact, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT item.student_id::text, item.submission_id::text, item.total_score::float8, item.max_score::float8, item.status,
       question.question_id::text, question.question_no, question.final_grade_id::text,
       question.score::float8, question.max_score::float8, question.source_type,
       COALESCE(question.source_id::text, ''), question.student_explanation
FROM score_release_item item
JOIN score_release_question question
  ON question.tenant_id = item.tenant_id AND question.release_id = item.release_id AND question.submission_id = item.submission_id
WHERE item.tenant_id = $1 AND item.release_id = $2::uuid
ORDER BY item.submission_id, question.question_no, question.question_id
`, tenantID, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bySubmission := map[string]SubmissionFact{}
	order := []string{}
	for rows.Next() {
		var studentID sql.NullString
		var fact SubmissionFact
		var question QuestionFact
		var explanation []byte
		if err := rows.Scan(&studentID, &fact.SubmissionID, &fact.TotalScore, &fact.MaxScore, &fact.Status,
			&question.QuestionID, &question.QuestionNo, &question.FinalGradeID, &question.Score, &question.MaxScore,
			&question.SourceType, &question.SourceID, &explanation); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(explanation, &question.Explanation); err != nil {
			return nil, err
		}
		fact.StudentID = studentID.String
		if existing, exists := bySubmission[fact.SubmissionID]; exists {
			existing.Questions = append(existing.Questions, question)
			bySubmission[fact.SubmissionID] = existing
		} else {
			fact.Questions = []QuestionFact{question}
			bySubmission[fact.SubmissionID] = fact
			order = append(order, fact.SubmissionID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	facts := make([]SubmissionFact, 0, len(order))
	for _, submissionID := range order {
		facts = append(facts, bySubmission[submissionID])
	}
	return facts, nil
}
