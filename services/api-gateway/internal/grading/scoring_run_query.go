package grading

import (
	"context"
	"database/sql"
	"errors"
)

const scoringRunSelect = `SELECT id::text,tenant_id::text,exam_id::text,idempotency_key,status,total_count,queued_count,auto_confirmed_count,human_confirmed_count,review_count,failed_count,started_by::text,started_at,completed_at,created_at,updated_at FROM scoring_run`

func scanScoringRun(row ruleScanner) (ScoringRun, error) {
	var out ScoringRun
	var started, completed sql.NullTime
	err := row.Scan(&out.ID, &out.TenantID, &out.ExamID, &out.IdempotencyKey, &out.Status, &out.TotalCount, &out.QueuedCount, &out.AutoConfirmedCount, &out.HumanConfirmedCount, &out.ReviewCount, &out.FailedCount, &out.StartedBy, &started, &completed, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ScoringRun{}, ErrNotFound
	}
	if err != nil {
		return ScoringRun{}, err
	}
	if started.Valid {
		v := started.Time.UTC()
		out.StartedAt = &v
	}
	if completed.Valid {
		v := completed.Time.UTC()
		out.CompletedAt = &v
	}
	return out, nil
}

func (s *PostgresStore) GetScoringSummary(ctx context.Context, tenantID, examID string) (ScoringSummary, error) {
	// 概要以该考试最近一次未删除的运行作为计数口径；每题的排队、确认和复核数都限定在这次运行。
	var summary ScoringSummary
	summary.Questions = []ScoringQuestionSummary{}
	run, err := scanScoringRun(s.db.QueryRowContext(ctx, scoringRunSelect+` WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1`, tenantID, examID))
	if err == nil {
		summary.Run = &run
	} else if !errors.Is(err, ErrNotFound) {
		return summary, err
	}
	runID := ""
	if summary.Run != nil {
		runID = summary.Run.ID
	}
	rows, err := s.db.QueryContext(ctx, `SELECT q.id::text,q.question_no,q.question_type,count(seg.id)::int,
count(seg.id) FILTER(WHERE EXISTS(SELECT 1 FROM omr_run o WHERE o.tenant_id=seg.tenant_id AND o.answer_segment_id=seg.id AND o.scoring_run_id=NULLIF($3,'')::uuid AND o.status IN ('queued','processing') AND o.deleted_at IS NULL))::int,
count(seg.id) FILTER(WHERE EXISTS(SELECT 1 FROM question_grade g WHERE g.tenant_id=seg.tenant_id AND g.answer_segment_id=seg.id AND g.scoring_run_id=NULLIF($3,'')::uuid AND g.is_current AND g.deleted_at IS NULL))::int,
count(seg.id) FILTER(WHERE EXISTS(SELECT 1 FROM review_task rt WHERE rt.tenant_id=seg.tenant_id AND rt.answer_segment_id=seg.id AND rt.scoring_run_id=NULLIF($3,'')::uuid AND rt.status IN ('pending','assigned','in_progress','returned') AND rt.deleted_at IS NULL))::int,
count(seg.id) FILTER(WHERE EXISTS(SELECT 1 FROM omr_run o WHERE o.tenant_id=seg.tenant_id AND o.answer_segment_id=seg.id AND o.scoring_run_id=NULLIF($3,'')::uuid AND o.status='terminal_error' AND o.deleted_at IS NULL))::int
FROM question q LEFT JOIN answer_segment seg ON seg.tenant_id=q.tenant_id AND seg.question_id=q.id AND seg.deleted_at IS NULL
WHERE q.tenant_id=$1::uuid AND q.exam_id=$2::uuid AND q.deleted_at IS NULL GROUP BY q.id ORDER BY q.sort_order`, tenantID, examID, runID)
	if err != nil {
		return summary, err
	}
	defer rows.Close()
	for rows.Next() {
		var q ScoringQuestionSummary
		if err := rows.Scan(&q.QuestionID, &q.QuestionNo, &q.QuestionType, &q.Total, &q.Queued, &q.Confirmed, &q.Review, &q.Failed); err != nil {
			return summary, err
		}
		summary.Questions = append(summary.Questions, q)
	}
	return summary, rows.Err()
}

func (s *PostgresStore) GetScoringRunDetail(ctx context.Context, tenantID, runID string) (ScoringRunDetail, error) {
	run, err := scanScoringRun(s.db.QueryRowContext(ctx, scoringRunSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, runID))
	if err != nil {
		return ScoringRunDetail{}, err
	}
	detail := ScoringRunDetail{Run: run, Items: []ScoringRunItem{}}
	rows, err := s.db.QueryContext(ctx, `
SELECT seg.id::text,seg.submission_id::text,seg.submission_page_id::text,sp.page_no,
  COALESCE(pr.registered_file_asset_id,sp.normalized_file_asset_id,sp.file_asset_id)::text,
  q.id::text,q.question_no,q.question_type,eqs.id::text,COALESCE(NULLIF(sub.candidate_no,''),seg.id::text),seg.normalized_bbox,
  COALESCE(ac.source,''),COALESCE(ac.display_text,''),COALESCE(ac.decision,''),ac.confidence::float8,
  COALESCE(ak.standard_answer,'null'::jsonb),COALESCE(sr.rule_type,''),g.score::float8,g.max_score::float8,COALESCE(g.source,''),
  COALESCE(o.id::text,''),COALESCE(o.status,''),COALESCE(o.runtime_task_id::text,''),COALESCE(wt.status,''),COALESCE(o.error_code,''),
  COALESCE(rt.id::text,''),CASE WHEN pending_arb.id IS NOT NULL THEN 'needs_arbitration' ELSE COALESCE(rt.status,'') END,
  CASE WHEN pending_arb.id IS NOT NULL THEN 'double_mark_disagreement' ELSE COALESCE(rt.reason_code,'') END,COALESCE(g.id::text,'')
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
JOIN submission_page sp ON sp.tenant_id=seg.tenant_id AND sp.id=seg.submission_page_id AND sp.deleted_at IS NULL
LEFT JOIN page_registration_run pr ON pr.tenant_id=seg.tenant_id AND pr.id=seg.registration_run_id AND pr.deleted_at IS NULL
JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
JOIN exam_question_snapshot eqs ON eqs.tenant_id=q.tenant_id AND eqs.exam_id=q.exam_id AND eqs.question_id=q.id
LEFT JOIN omr_run o ON o.tenant_id=seg.tenant_id AND o.scoring_run_id=$3::uuid AND o.answer_segment_id=seg.id AND o.deleted_at IS NULL
LEFT JOIN agent_worker_task wt ON wt.tenant_id=seg.tenant_id AND wt.id=o.runtime_task_id
LEFT JOIN LATERAL (
  SELECT source,display_text,decision,confidence FROM answer_candidate
  WHERE tenant_id=seg.tenant_id AND scoring_run_id=$3::uuid AND answer_segment_id=seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) ac ON true
LEFT JOIN LATERAL (
  SELECT standard_answer FROM question_answer_key
  WHERE tenant_id=q.tenant_id AND question_id=q.id AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) ak ON true
LEFT JOIN LATERAL (
  SELECT rule_type FROM scoring_rule
  WHERE tenant_id=q.tenant_id AND question_id=q.id AND status='published' AND deleted_at IS NULL
  ORDER BY version DESC LIMIT 1
) sr ON true
LEFT JOIN LATERAL (
  SELECT id::text,status,reason_code FROM review_task
  WHERE tenant_id=seg.tenant_id AND scoring_run_id=$3::uuid AND answer_segment_id=seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) rt ON true
LEFT JOIN LATERAL (
  SELECT arb.id FROM double_mark_session dm
  JOIN review_task first_task ON first_task.tenant_id=dm.tenant_id AND first_task.id=dm.first_review_task_id
  JOIN arbitration_task arb ON arb.tenant_id=dm.tenant_id AND arb.double_mark_session_id=dm.id
  WHERE dm.tenant_id=seg.tenant_id AND dm.answer_segment_id=seg.id AND first_task.scoring_run_id=$3::uuid
    AND arb.status IN ('pending','assigned') AND arb.deleted_at IS NULL
  LIMIT 1
) pending_arb ON true
LEFT JOIN LATERAL (
  SELECT id::text,source,score,max_score FROM question_grade
  WHERE tenant_id=seg.tenant_id AND scoring_run_id=$3::uuid AND answer_segment_id=seg.id AND is_current AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) g ON true
WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND seg.deleted_at IS NULL
  AND (
    EXISTS (SELECT 1 FROM omr_run run_omr WHERE run_omr.tenant_id=seg.tenant_id AND run_omr.scoring_run_id=$3::uuid AND run_omr.answer_segment_id=seg.id AND run_omr.deleted_at IS NULL)
    OR EXISTS (SELECT 1 FROM answer_candidate run_candidate WHERE run_candidate.tenant_id=seg.tenant_id AND run_candidate.scoring_run_id=$3::uuid AND run_candidate.answer_segment_id=seg.id AND run_candidate.deleted_at IS NULL)
    OR EXISTS (SELECT 1 FROM review_task run_review WHERE run_review.tenant_id=seg.tenant_id AND run_review.scoring_run_id=$3::uuid AND run_review.answer_segment_id=seg.id AND run_review.deleted_at IS NULL)
    OR EXISTS (SELECT 1 FROM question_grade run_grade WHERE run_grade.tenant_id=seg.tenant_id AND run_grade.scoring_run_id=$3::uuid AND run_grade.answer_segment_id=seg.id AND run_grade.deleted_at IS NULL)
  )
ORDER BY q.sort_order,seg.created_at`, tenantID, run.ExamID, runID)
	if err != nil {
		return ScoringRunDetail{}, err
	}
	defer rows.Close()
	for rows.Next() {
		item, scanErr := scanScoringRunItem(rows, run.Status)
		if scanErr != nil {
			return ScoringRunDetail{}, scanErr
		}
		detail.Items = append(detail.Items, item)
	}
	return detail, rows.Err()
}

// GetExamAutomationResults returns the current, exam-wide objective grading
// facts independently of a particular orchestration run. This is the operator
// view: a rerun is process history, while the latest recognition and grade are
// the facts that must be auditable during live marking.
func (s *PostgresStore) GetExamAutomationResults(ctx context.Context, tenantID, examID string) (ExamAutomationResults, error) {
	out := ExamAutomationResults{Items: []ScoringRunItem{}}
	rows, err := s.db.QueryContext(ctx, `
SELECT seg.id::text,seg.submission_id::text,seg.submission_page_id::text,sp.page_no,
  COALESCE(pr.registered_file_asset_id,sp.normalized_file_asset_id,sp.file_asset_id)::text,
  q.id::text,q.question_no,q.question_type,eqs.id::text,COALESCE(NULLIF(sub.candidate_no,''),seg.id::text),seg.normalized_bbox,
  COALESCE(NULLIF(ac.source,''),ans.source,''),COALESCE(NULLIF(ac.display_text,''),ans.answer_text,''),
  COALESCE(NULLIF(ac.decision,''),CASE WHEN ans.id IS NOT NULL THEN 'confirmed' ELSE '' END),COALESCE(ac.confidence,ans.confidence)::float8,
  COALESCE(ak.standard_answer,'null'::jsonb),COALESCE(sr.rule_type,q.question_type),
  COALESCE(g.score,ag.suggested_score)::float8,COALESCE(g.max_score,ag.max_score)::float8,
  COALESCE(g.source,CASE WHEN ag.id IS NOT NULL THEN 'ai_suggestion' ELSE '' END),
  COALESCE(o.id::text,''),COALESCE(o.status,CASE WHEN ag.status='failed' THEN 'terminal_error' ELSE '' END),
  COALESCE(o.runtime_task_id::text,''),COALESCE(wt.status,''),
  COALESCE(o.error_code,CASE WHEN ag.status='failed' THEN ag.failure_reason ELSE '' END,''),
  COALESCE(rt.id::text,''),COALESCE(rt.status,''),COALESCE(rt.reason_code,''),COALESCE(g.id::text,'')
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
JOIN submission_page sp ON sp.tenant_id=seg.tenant_id AND sp.id=seg.submission_page_id AND sp.deleted_at IS NULL
LEFT JOIN page_registration_run pr ON pr.tenant_id=seg.tenant_id AND pr.id=seg.registration_run_id AND pr.deleted_at IS NULL
JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
JOIN exam_question_snapshot eqs ON eqs.tenant_id=q.tenant_id AND eqs.exam_id=q.exam_id AND eqs.question_id=q.id
LEFT JOIN LATERAL (
  SELECT id,source,display_text,decision,confidence FROM answer_candidate
  WHERE tenant_id=seg.tenant_id AND answer_segment_id=seg.id AND is_current AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) ac ON true
LEFT JOIN LATERAL (
  SELECT id,source,answer_text,confidence FROM answer_segment_answer
  WHERE tenant_id=seg.tenant_id AND answer_segment_id=seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) ans ON true
LEFT JOIN LATERAL (
  SELECT standard_answer FROM question_answer_key
  WHERE tenant_id=q.tenant_id AND question_id=q.id AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) ak ON true
LEFT JOIN LATERAL (
  SELECT rule_type FROM scoring_rule
  WHERE tenant_id=q.tenant_id AND question_id=q.id AND status='published' AND deleted_at IS NULL
  ORDER BY version DESC LIMIT 1
) sr ON true
LEFT JOIN LATERAL (
  SELECT id,source,score,max_score FROM question_grade
  WHERE tenant_id=seg.tenant_id AND answer_segment_id=seg.id AND is_current AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) g ON true
LEFT JOIN LATERAL (
  SELECT id,suggested_score,max_score,status,failure_reason FROM ai_grade
  WHERE tenant_id=seg.tenant_id AND answer_segment_id=seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) ag ON true
LEFT JOIN LATERAL (
  SELECT id,status,runtime_task_id,error_code FROM omr_run
  WHERE tenant_id=seg.tenant_id AND answer_segment_id=seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) o ON true
LEFT JOIN agent_worker_task wt ON wt.tenant_id=seg.tenant_id AND wt.id=o.runtime_task_id
LEFT JOIN LATERAL (
  SELECT id,status,reason_code FROM review_task
  WHERE tenant_id=seg.tenant_id AND answer_segment_id=seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) rt ON true
WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND seg.deleted_at IS NULL
ORDER BY COALESCE(NULLIF(sub.candidate_no,''),seg.id::text),q.sort_order,seg.created_at`, tenantID, examID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		item, scanErr := scanScoringRunItem(rows, "")
		if scanErr != nil {
			return ExamAutomationResults{}, scanErr
		}
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}

func scanScoringRunItem(row ruleScanner, runStatus string) (ScoringRunItem, error) {
	var item ScoringRunItem
	var recognitionConfidence, score, maxScore sql.NullFloat64
	var normalizedBBoxRaw, standardAnswerRaw []byte
	var gradeID string
	if err := row.Scan(&item.AnswerSegmentID, &item.SubmissionID, &item.SubmissionPageID, &item.PageNo, &item.PageFileAssetID,
		&item.QuestionID, &item.QuestionNo, &item.QuestionType, &item.AssessmentSnapshotID, &item.AnonymousCode, &normalizedBBoxRaw,
		&item.RecognitionSource, &item.RecognizedAnswer, &item.RecognitionDecision, &recognitionConfidence,
		&standardAnswerRaw, &item.RuleType, &score, &maxScore, &item.GradeSource,
		&item.OMRRunID, &item.State, &item.RuntimeTaskID, &item.RuntimeStatus, &item.ErrorCode,
		&item.ReviewTaskID, &item.ReviewStatus, &item.ReasonCode, &gradeID); err != nil {
		return ScoringRunItem{}, err
	}
	if recognitionConfidence.Valid {
		value := recognitionConfidence.Float64
		item.RecognitionConfidence = &value
	}
	if score.Valid {
		value := score.Float64
		item.Score = &value
	}
	if maxScore.Valid {
		value := maxScore.Float64
		item.MaxScore = &value
	}
	// 展示坐标采用宽松解析，避免一个异常字段让整份列表失败；解析失败时可能保留部分字段。
	// 此处不保证返回完整、有效的几何区域。
	decodeJSONBLenient(normalizedBBoxRaw, &item.NormalizedBBox)
	if err := decodeJSONB(standardAnswerRaw, &item.StandardAnswer, "question_answer_key.standard_answer"); err != nil {
		return ScoringRunItem{}, err
	}
	item.State = scoringItemState(runStatus, item.State, item.ReviewStatus, gradeID)
	return item, nil
}

// 先展示取消与失败，再展示待复核；仍有人工任务时，已有分数不能把题块显示为已确认。
func scoringItemState(runStatus, omrStatus, reviewStatus, gradeID string) string {
	if runStatus == "cancelled" || runStatus == "cancelling" {
		return runStatus
	}
	if omrStatus == "terminal_error" || (runStatus == "failed" && gradeID == "" && reviewStatus == "") {
		return "failed"
	}
	if reviewStatus == "pending" || reviewStatus == "assigned" || reviewStatus == "in_progress" || reviewStatus == "returned" || reviewStatus == "needs_arbitration" {
		return "review"
	}
	if gradeID != "" {
		return "confirmed"
	}
	if omrStatus == "queued" || omrStatus == "processing" || omrStatus == "retryable_error" {
		return "processing"
	}
	return "pending"
}
