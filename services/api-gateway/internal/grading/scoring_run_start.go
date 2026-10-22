package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

// scoringRunSegment 保留同一事务读到的片段、答案和模板事实，供后续任务写入使用。
type scoringRunSegment struct {
	id, submissionID, questionID, no, kind, cropID, cropHash, templateID, templateHash, anonymous, contentType, ruleID, scoringMode string
	answerID, answerText, answerSource, templateStatus, currentTemplateHash                                                         string
	referenceAssetID, referenceHash, referenceContentType                                                                           string
	area, answerPayload, templateLayout                                                                                             []byte
	answerConfidence                                                                                                                sql.NullFloat64
	inputVersionID, planItemID, snapshotID                                                                                          string
}

type scoringStartReviewTask struct {
	segment scoringRunSegment
	cause   scoringReviewCause
}

type scoringSegmentTask struct {
	segment   scoringRunSegment
	reviewers [2]string
}

type scoringOMRSegment struct {
	segment scoringRunSegment
	options []any
}

func (s *PostgresStore) StartScoringRun(ctx context.Context, tenantID, examID, actorID string, input StartScoringRunInput) (ScoringRun, error) {
	return s.startScoringRun(ctx, tenantID, examID, actorID, input, "")
}

func (s *PostgresStore) ReprocessSegmentScore(ctx context.Context, tenantID, segmentID, actorID string, input StartScoringRunInput) (ScoringRun, error) {
	segmentID = strings.TrimSpace(segmentID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if segmentID == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 100 {
		return ScoringRun{}, ErrInvalidInput
	}
	var examID string
	err := s.db.QueryRowContext(ctx, `SELECT sub.exam_id::text FROM answer_segment seg JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid AND seg.deleted_at IS NULL`, tenantID, segmentID).Scan(&examID)
	if errors.Is(err, sql.ErrNoRows) {
		return ScoringRun{}, ErrNotFound
	}
	if err != nil {
		return ScoringRun{}, err
	}
	input.IdempotencyKey = "segment-reprocess:" + segmentID + ":" + input.IdempotencyKey
	return s.startScoringRun(ctx, tenantID, examID, actorID, input, segmentID)
}

func (s *PostgresStore) startScoringRun(ctx context.Context, tenantID, examID, actorID string, input StartScoringRunInput, segmentID string) (ScoringRun, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" || len(input.IdempotencyKey) > 160 {
		return ScoringRun{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScoringRun{}, err
	}
	defer tx.Rollback()
	run, requestHash, replayed, err := loadScoringCommandReplayTx(ctx, tx, tenantID, examID, actorID, input.IdempotencyKey, segmentID)
	if err != nil {
		return ScoringRun{}, err
	}
	if replayed {
		return run, tx.Commit()
	}
	// 已接受命令在前面直接重放；只有新启动才重新检查就绪条件，避免丢失响应后无法恢复。
	if err = validateNewScoringRunTx(ctx, tx, tenantID, examID, segmentID); err != nil {
		return ScoringRun{}, err
	}
	if err = validateGradingAIPlanForStartTx(ctx, tx, tenantID, examID, segmentID); err != nil {
		return ScoringRun{}, err
	}
	if input.ExpectedPlanHash != "" {
		hash, hashErr := scoringPlanHash(ctx, tx, tenantID, examID)
		if hashErr != nil {
			return ScoringRun{}, hashErr
		}
		if hash != input.ExpectedPlanHash {
			return ScoringRun{}, ErrScoringPlanChanged
		}
	}
	if err = paper.RepairConfirmedObjectiveRulesInTx(ctx, tx, tenantID, examID, actorID); err != nil {
		return ScoringRun{}, err
	}
	var runID string
	err = tx.QueryRowContext(ctx, `
INSERT INTO scoring_run (tenant_id, exam_id, idempotency_key, status, started_by, started_at, command_request_hash)
SELECT $1::uuid, e.id, $3, 'processing', $4::uuid, now(), $5
FROM exam e WHERE e.tenant_id=$1::uuid AND e.id=$2::uuid AND e.deleted_at IS NULL AND e.status IN ('collecting','processing','grading')
RETURNING id::text`, tenantID, examID, input.IdempotencyKey, actorID, requestHash).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return ScoringRun{}, ErrInvalidInput
	}
	if err != nil {
		return ScoringRun{}, err
	}
	if err = freezeScoringRunRulesTx(ctx, tx, tenantID, examID, runID); err != nil {
		return ScoringRun{}, err
	}
	if err = invalidateScoringSegmentCurrentTx(ctx, tx, tenantID, segmentID); err != nil {
		return ScoringRun{}, err
	}
	segments, err := loadScoringRunSegmentsTx(ctx, tx, tenantID, examID, segmentID)
	if err != nil {
		return ScoringRun{}, err
	}
	segments, err = prepareScoringPlanTx(ctx, tx, tenantID, runID, segments)
	if err != nil {
		return ScoringRun{}, err
	}
	queued, review, err := s.createScoringRunTasksTx(ctx, tx, tenantID, examID, runID, actorID, segments)
	if err != nil {
		return ScoringRun{}, err
	}
	status := scoringRunStatusForCounts(len(segments), queued, review)
	_, err = tx.ExecContext(ctx, `UPDATE scoring_run SET status=$3,total_count=$4,queued_count=$5,review_count=$6,failed_count=$7,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, runID, status, len(segments), queued, review, map[bool]int{true: 1, false: 0}[len(segments) == 0])
	if err != nil {
		return ScoringRun{}, err
	}
	run, err = scanScoringRun(tx.QueryRowContext(ctx, scoringRunSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, runID))
	if err != nil {
		return ScoringRun{}, err
	}
	return run, tx.Commit()
}

// loadScoringCommandReplayTx 先锁命令、检查跨考试冲突，再锁考试和读取已接受结果。
// 它只读取重放事实，提交或回滚仍由启动入口负责。
func loadScoringCommandReplayTx(ctx context.Context, tx *sql.Tx, tenantID, examID, actorID, idempotencyKey, segmentID string) (ScoringRun, string, bool, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenantID+":"+actorID+":"+idempotencyKey+":scoring-command"); err != nil {
		return ScoringRun{}, "", false, err
	}
	requestHash := scoringCommandHash(examID, segmentID)
	var otherID string
	err := tx.QueryRowContext(ctx, `SELECT id::text FROM scoring_run WHERE tenant_id=$1::uuid AND started_by=$2::uuid AND idempotency_key=$3 AND exam_id<>$4::uuid LIMIT 1`, tenantID, actorID, idempotencyKey, examID).Scan(&otherID)
	if err == nil {
		return ScoringRun{}, "", false, ErrCommandConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ScoringRun{}, "", false, err
	}
	// 同一考试的启动操作串行化，否则第二个未解决运行会重复排入 OMR 工作，并竞争替换同一批当前成绩。
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, examID+":scoring-run"); err != nil {
		return ScoringRun{}, "", false, err
	}
	var existing string
	var existingActor string
	var existingHash sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id::text,started_by::text,command_request_hash FROM scoring_run WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND idempotency_key=$3`, tenantID, examID, idempotencyKey).Scan(&existing, &existingActor, &existingHash)
	if err == nil {
		if existingActor != actorID || (existingHash.Valid && existingHash.String != requestHash) {
			return ScoringRun{}, "", false, ErrCommandConflict
		}
		run, getErr := scanScoringRun(tx.QueryRowContext(ctx, scoringRunSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, existing))
		if getErr != nil {
			return ScoringRun{}, "", false, getErr
		}
		return run, requestHash, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ScoringRun{}, "", false, err
	}
	return ScoringRun{}, requestHash, false, nil
}

// validateNewScoringRunTx 仅校验新命令；段重处理不重新要求整场考试就绪，但仍拒绝未完成的片段和活跃工作。
func validateNewScoringRunTx(ctx context.Context, tx *sql.Tx, tenantID, examID, segmentID string) error {
	if segmentID == "" {
		readiness, readinessErr := calculateScoringReadiness(ctx, tx, tenantID, examID)
		if readinessErr != nil {
			return readinessErr
		}
		if !readiness.Ready {
			return &ScoringReadinessError{Readiness: readiness}
		}
	}
	var unresolvedRunID string
	err := tx.QueryRowContext(ctx, `
SELECT id::text
FROM scoring_run
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL
  AND status IN ('queued','processing','needs_review','failed','cancelling')
ORDER BY created_at DESC
LIMIT 1
`, tenantID, examID).Scan(&unresolvedRunID)
	if err == nil {
		return ErrInvalidTransition
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if segmentID != "" {
		return validateScoringSegmentReprocessTx(ctx, tx, tenantID, examID, segmentID)
	}
	return nil
}

// validateScoringSegmentReprocessTx 在新运行冲突检查之后校验目标片段，再依次检查人工和 OMR 活跃工作。
// 重处理不要求整场考试就绪，但不能替换尚未完成或仍有人处理的片段事实。
func validateScoringSegmentReprocessTx(ctx context.Context, tx *sql.Tx, tenantID, examID, segmentID string) error {
	var exists int
	err := tx.QueryRowContext(ctx, `
SELECT 1
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND seg.id=$3::uuid
  AND seg.deleted_at IS NULL AND seg.processing_status='completed' AND seg.crop_file_asset_id IS NOT NULL
`, tenantID, examID, segmentID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var activeReview, activeOMR int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM review_task WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL`, tenantID, segmentID).Scan(&activeReview); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM omr_run WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND status IN ('queued','processing','retryable_error') AND deleted_at IS NULL`, tenantID, segmentID).Scan(&activeOMR); err != nil {
		return err
	}
	if activeReview > 0 || activeOMR > 0 {
		return ErrInvalidTransition
	}
	return nil
}

// invalidateScoringSegmentCurrentTx 只在已通过准入的新重处理运行内执行，先失效候选再失效成绩。
// 普通整场启动不替换片段事实；两次更新仍由启动事务一起提交或回滚。
func invalidateScoringSegmentCurrentTx(ctx context.Context, tx *sql.Tx, tenantID, segmentID string) error {
	if segmentID == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE answer_candidate SET is_current=false WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current`, tenantID, segmentID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE question_grade SET is_current=false,status='invalidated' WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND deleted_at IS NULL`, tenantID, segmentID); err != nil {
		return err
	}
	return nil
}

// loadScoringRunSegmentsTx 先完整读取并关闭结果集，再创建任务；扫描、遍历和关闭错误按首次出现顺序返回。
func loadScoringRunSegmentsTx(ctx context.Context, tx *sql.Tx, tenantID, examID, segmentID string) ([]scoringRunSegment, error) {
	rows, err := tx.QueryContext(ctx, `
 SELECT seg.id::text, seg.submission_id::text, q.id::text, q.question_no, q.question_type,
   seg.crop_file_asset_id::text, seg.crop_sha256, seg.template_id::text, seg.template_content_hash,
   COALESCE(q.answer_area,'{}'::jsonb) || jsonb_build_object('answer_scope',COALESCE(seg.normalized_bbox->>'answer_scope','question')), COALESCE(NULLIF(sub.candidate_no,''), sub.id::text), fa.content_type,
   COALESCE(sr.id::text,''), COALESCE(ans.id::text,''), COALESCE(ans.answer_text,''),
   COALESCE(ans.answer_payload,'{}'::jsonb), COALESCE(ans.source,''), ans.confidence::float8,
   COALESCE(ast.status,''), COALESCE(ast.content_hash,''), COALESCE(ast.layout,'{}'::jsonb),
   COALESCE(reference_file.id::text,''), COALESCE(reference_file.hash_sha256,''), COALESCE(reference_file.content_type,''),
   COALESCE(eqs.scoring_policy_snapshot_json->>'mode','')
 FROM answer_segment seg
 JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
 JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
 LEFT JOIN exam_question_snapshot eqs ON eqs.tenant_id=q.tenant_id AND eqs.exam_id=q.exam_id AND eqs.question_id=q.id
 JOIN file_asset fa ON fa.tenant_id=seg.tenant_id AND fa.id=seg.crop_file_asset_id AND fa.deleted_at IS NULL
	LEFT JOIN answer_sheet_template ast ON ast.tenant_id=seg.tenant_id AND ast.id=seg.template_id AND ast.deleted_at IS NULL
 LEFT JOIN exam_paper reference_paper ON reference_paper.tenant_id=ast.tenant_id AND reference_paper.id=ast.exam_paper_id AND reference_paper.deleted_at IS NULL
 LEFT JOIN file_asset reference_file ON reference_file.tenant_id=reference_paper.tenant_id AND reference_file.id=reference_paper.file_asset_id AND reference_file.deleted_at IS NULL
 LEFT JOIN scoring_rule sr ON sr.tenant_id=q.tenant_id AND sr.question_id=q.id AND sr.status='published' AND sr.deleted_at IS NULL
LEFT JOIN LATERAL (SELECT id,answer_text,answer_payload,source,confidence FROM answer_segment_answer WHERE tenant_id=seg.tenant_id AND answer_segment_id=seg.id AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1) ans ON true
WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND seg.deleted_at IS NULL
  AND seg.processing_status='completed' AND seg.crop_file_asset_id IS NOT NULL
  AND (NULLIF($3,'')::uuid IS NULL OR seg.id=NULLIF($3,'')::uuid)
ORDER BY q.sort_order, seg.created_at`, tenantID, examID, segmentID)
	if err != nil {
		return nil, err
	}
	segments := []scoringRunSegment{}
	for rows.Next() {
		var item scoringRunSegment
		if err := rows.Scan(&item.id, &item.submissionID, &item.questionID, &item.no, &item.kind, &item.cropID, &item.cropHash, &item.templateID, &item.templateHash, &item.area, &item.anonymous, &item.contentType, &item.ruleID, &item.answerID, &item.answerText, &item.answerPayload, &item.answerSource, &item.answerConfidence, &item.templateStatus, &item.currentTemplateHash, &item.templateLayout, &item.referenceAssetID, &item.referenceHash, &item.referenceContentType, &item.scoringMode); err != nil {
			rows.Close()
			return nil, err
		}
		segments = append(segments, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	return segments, nil
}

func loadScoringDualReviewersTx(ctx context.Context, tx *sql.Tx, tenantID, examID string, segments []scoringRunSegment) ([]string, error) {
	// 在与任务创建相同的事务中解析学校名单；DUAL_HUMAN 不得静默回退为单个未分配任务。
	dualNeeded := false
	for _, segment := range segments {
		if segment.scoringMode == "DUAL_HUMAN" {
			dualNeeded = true
			break
		}
	}
	dualReviewers := []string{}
	if dualNeeded {
		graderRows, graderErr := tx.QueryContext(ctx, `SELECT DISTINCT u.id::text FROM app_user u
JOIN exam e ON e.tenant_id=u.tenant_id AND e.school_id=u.school_id
JOIN user_role ur ON ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL
JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.code='grader' AND r.deleted_at IS NULL
WHERE e.tenant_id=$1::uuid AND e.id=$2::uuid AND u.status='active' AND u.deleted_at IS NULL
ORDER BY u.id::text`, tenantID, examID)
		if graderErr != nil {
			return nil, graderErr
		}
		for graderRows.Next() {
			var reviewerID string
			if err := graderRows.Scan(&reviewerID); err != nil {
				graderRows.Close()
				return nil, err
			}
			dualReviewers = append(dualReviewers, reviewerID)
		}
		if err := graderRows.Err(); err != nil {
			graderRows.Close()
			return nil, err
		}
		graderRows.Close()
		if len(dualReviewers) < 2 {
			return nil, ErrScoringNotReady
		}
	}
	return dualReviewers, nil
}

// insertScoringDualMarkTx 为一个片段同时写入两份指派和双评会话；外层事务负责一起提交。
func insertScoringDualMarkTx(ctx context.Context, tx *sql.Tx, tenantID, examID, runID, actorID string, task scoringSegmentTask) error {
	segment := task.segment
	first, second := task.reviewers[0], task.reviewers[1]
	ids := [2]string{}
	for i, reviewerID := range []string{first, second} {
		round := "first_mark"
		if i == 1 {
			round = "second_mark"
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO review_task
(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,priority,assigned_to,grade_round,reason_code,scoring_run_id,created_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7,'double_mark_required','assigned',50,$8::uuid,$9,'dual_human_policy',$10::uuid,$11::uuid)
RETURNING id::text`, tenantID, examID, segment.questionID, segment.no, segment.id, segment.submissionID, segment.anonymous, reviewerID, round, runID, actorID).Scan(&ids[i]); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO double_mark_session
(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,first_review_task_id,second_review_task_id,first_reviewer_id,second_reviewer_id,threshold,resolution_strategy,status,created_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7,$8::uuid,$9::uuid,$10::uuid,$11::uuid,0,'average','pending',$12::uuid)`, tenantID, examID, segment.questionID, segment.no, segment.id, segment.submissionID, segment.anonymous, ids[0], ids[1], first, second, actorID)
	return err
}

// insertScoringStartReviewTx 返回是否新建任务；冲突跳过时不计入本次运行的人工片段数。
func insertScoringStartReviewTx(ctx context.Context, tx *sql.Tx, tenantID, examID, runID, actorID string, task scoringStartReviewTask) (bool, error) {
	segment := task.segment
	source, reason := task.cause.source, task.cause.reason
	result, insertErr := tx.ExecContext(ctx, `INSERT INTO review_task (tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,priority,grade_round,reason_code,scoring_run_id,created_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7,$8,'pending',50,'single',$9,$10::uuid,$11::uuid)
ON CONFLICT (tenant_id,answer_segment_id,source,grade_round) WHERE status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL AND source <> 'ai_panel_disagreement' DO NOTHING`, tenantID, examID, segment.questionID, segment.no, segment.id, segment.submissionID, segment.anonymous, source, reason, runID, actorID)
	if insertErr != nil {
		return false, insertErr
	}
	count, _ := result.RowsAffected()
	return count == 1, nil
}

// createScoringRunTasksTx 按查询顺序逐段完成持久化；任一段失败立即返回，不创建后续片段任务。
// 双人阅卷每段产生两份任务，但运行 review 计数仍只增加一个片段。
func (s *PostgresStore) createScoringRunTasksTx(ctx context.Context, tx *sql.Tx, tenantID, examID, runID, actorID string, segments []scoringRunSegment) (int, int, error) {
	dualReviewers, err := loadScoringDualReviewersTx(ctx, tx, tenantID, examID, segments)
	if err != nil {
		return 0, 0, err
	}
	dualIndex := 0
	queued, review := 0, 0
	for _, segment := range segments {
		task := scoringSegmentTask{segment: segment}
		// 名单已在处理任何片段前验证；仅双评片段消耗轮转位置，其他路径不改变指派顺序。
		if segment.scoringMode == "DUAL_HUMAN" {
			task.reviewers = [2]string{dualReviewers[dualIndex%len(dualReviewers)], dualReviewers[(dualIndex+1)%len(dualReviewers)]}
			dualIndex++
		}
		queuedDelta, reviewDelta, err := s.createScoringSegmentTasksTx(ctx, tx, tenantID, examID, runID, actorID, task)
		if err != nil {
			return 0, 0, err
		}
		queued += queuedDelta
		review += reviewDelta
	}
	return queued, review, nil
}

// createScoringSegmentTasksTx 在同一事务内完成一个片段的区域解析、路径选择和全部写入，再返回运行计数增量。
// 双评任务使用外层已选好的两名阅卷人；每段仍只计一次人工片段，冲突跳过的单评任务不计数。
func (s *PostgresStore) createScoringSegmentTasksTx(ctx context.Context, tx *sql.Tx, tenantID, examID, runID, actorID string, task scoringSegmentTask) (int, int, error) {
	segment := task.segment
	var area map[string]any
	if err := decodeJSONB(segment.area, &area, "question.answer_area"); err != nil {
		return 0, 0, err
	}
	options, _ := area["option_regions"].([]any)
	options = cropRelativeOptionRegions(area, options)
	answerScope, _ := area["answer_scope"].(string)
	route := routeScoringSegmentForScope(segment.kind, segment.scoringMode, answerScope, segment.ruleID != "", segment.answerID != "", len(options))
	if _, err := tx.ExecContext(ctx, `UPDATE scoring_run_item SET route=$3,stage='score',processing_status='waiting_dependency',updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, segment.planItemID, route.Kind); err != nil {
		return 0, 0, err
	}
	if route.Kind == scoringRouteObserveRule || route.Kind == scoringRouteImageAI {
		if err := queueScoringObservationTx(ctx, tx, tenantID, runID, actorID, segment, route.Kind); err != nil {
			return 0, 0, err
		}
		return 1, 0, nil
	}
	if route.Kind == scoringRouteRuleInput {
		created, err := insertRuleScoringInputTx(ctx, tx, tenantID, examID, runID, actorID, segment)
		if err != nil {
			return 0, 0, err
		}
		lowConfidence := segment.answerSource != "manual_entry" && segment.answerSource != "imported_answer" && (!segment.answerConfidence.Valid || segment.answerConfidence.Float64 < 0.9)
		_, err = tx.ExecContext(ctx, `UPDATE scoring_run_item SET recognition_completed=true,stage=CASE WHEN $3 THEN 'done' ELSE 'score' END,processing_status=CASE WHEN $3 THEN 'succeeded' ELSE 'waiting_dependency' END,decision_status=CASE WHEN $3 THEN 'review_needed' ELSE 'undecided' END,reason_code=CASE WHEN $3 THEN 'answer_low_confidence' ELSE '' END,next_action=CASE WHEN $3 THEN 'teacher_review' ELSE 'wait' END WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, segment.planItemID, lowConfidence)
		return 0, created, err
	}
	if route.Kind == scoringRouteOMR {
		if err := s.queueScoringOMRTx(ctx, tx, tenantID, examID, runID, actorID, scoringOMRSegment{segment: segment, options: options}); err != nil {
			return 0, 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scoring_run_item item SET stage='observe',processing_status='queued',runtime_task_id=o.runtime_task_id FROM omr_run o WHERE item.tenant_id=$1::uuid AND item.id=$2::uuid AND o.tenant_id=item.tenant_id AND o.scoring_run_id=item.scoring_run_id AND o.answer_segment_id=item.answer_segment_id`, tenantID, segment.planItemID); err != nil {
			return 0, 0, err
		}
		return 1, 0, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scoring_run_item SET stage='done',processing_status='succeeded',decision_status='manual_only',reason_code=$3,next_action='teacher_review' WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, segment.planItemID, route.Reason); err != nil {
		return 0, 0, err
	}
	if segment.scoringMode == "DUAL_HUMAN" {
		if err := insertScoringDualMarkTx(ctx, tx, tenantID, examID, runID, actorID, task); err != nil {
			return 0, 0, err
		}
		return 0, 1, nil
	}
	created, err := insertScoringStartReviewTx(ctx, tx, tenantID, examID, runID, actorID, scoringStartReviewTask{segment: segment, cause: scoringReviewCause{source: route.Source, reason: route.Reason}})
	if err != nil {
		return 0, 0, err
	}
	if created {
		return 0, 1, nil
	}
	return 0, 0, nil
}

// insertRuleScoringInputTx 先替换当前候选，再为低置信答案创建复核任务；人工录入和导入答案沿用已确认来源规则。
func insertRuleScoringInputTx(ctx context.Context, tx *sql.Tx, tenantID, examID, runID, actorID string, segment scoringRunSegment) (int, error) {
	confidence := 0.0
	if segment.answerConfidence.Valid {
		confidence = segment.answerConfidence.Float64
	}
	confirmed := segment.answerSource == "manual_entry" || segment.answerSource == "imported_answer" || confidence >= 0.9
	decision := "ambiguous"
	if confirmed {
		decision = "confirmed"
	}
	source := "ocr"
	if segment.answerSource == "manual_entry" {
		source = "manual"
	}
	if segment.answerSource == "imported_answer" {
		source = "imported"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE answer_candidate SET is_current=false WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current`, tenantID, segment.id); err != nil {
		return 0, err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO answer_candidate(tenant_id,answer_segment_id,scoring_run_id,exam_question_snapshot_id,input_version_id,source,payload,display_text,confidence,decision,evidence,engine_version,profile_version,input_hash,is_current,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$12::uuid,$13::uuid,$4,$5::jsonb,$6,NULLIF($7,0),$8,jsonb_build_object('answer_segment_answer_id',$9::text),'rule-input-v1','rule-input-v1',$10,true,$11::uuid)`, tenantID, segment.id, runID, source, segment.answerPayload, segment.answerText, confidence, decision, segment.answerID, "sha256:"+strings.ReplaceAll(segment.answerID, "-", ""), actorID, segment.snapshotID, segment.inputVersionID)
	if err != nil {
		return 0, err
	}
	if !confirmed {
		created, insertErr := insertScoringStartReviewTx(ctx, tx, tenantID, examID, runID, actorID, scoringStartReviewTask{segment: segment, cause: scoringReviewCause{source: "rule_review_required", reason: "answer_low_confidence"}})
		if insertErr != nil {
			return 0, insertErr
		}
		if created {
			return 1, nil
		}
	}
	return 0, nil
}

// queueScoringOMRTx 先冻结服务端策略，再建立 Worker 任务和运行关联；外层事务失败时这些事实一起回滚。
func (s *PostgresStore) queueScoringOMRTx(ctx context.Context, tx *sql.Tx, tenantID, examID, runID, actorID string, item scoringOMRSegment) error {
	segment, options := item.segment, item.options
	var templateLayout paper.TemplateLayout
	if err := decodeJSONB(segment.templateLayout, &templateLayout, "answer_sheet_template.layout"); err != nil {
		return err
	}
	currentReference := paper.TemplateOMRReference{
		Source:      paper.OMRReferenceSourceExamPaper,
		FileAssetID: segment.referenceAssetID,
		HashSHA256:  segment.referenceHash,
		ContentType: segment.referenceContentType,
	}
	basePolicy := paper.OMRAutoConfirmPolicyForTemplateReference(templateLayout, segment.templateStatus, segment.currentTemplateHash, segment.templateHash, currentReference, segment.questionID)
	var calibration *paper.OMRCalibrationApproval
	if basePolicy.Reference != nil && basePolicy.RuntimeProfile.Mode == paper.OMRProfileModeTemplateDifference {
		scope := omrCalibrationScope{
			templateID:           segment.templateID,
			templateContentHash:  segment.templateHash,
			profileVersion:       basePolicy.RuntimeProfile.Version,
			profileHash:          basePolicy.ProfileHash,
			referenceFileAssetID: basePolicy.Reference.FileAssetID,
			referenceSHA256:      basePolicy.Reference.HashSHA256,
		}
		var err error
		calibration, err = s.loadApprovedOMRCalibrationTx(ctx, tx, tenantID, scope, segment.questionID)
		if err != nil {
			return err
		}
	}
	// 冻结服务端校准资格、参考图和阈值到 OMR 运行；Worker 只接收识别配置，不能自行授予自动确认。
	template := paper.AnswerSheetTemplate{Layout: templateLayout, Status: segment.templateStatus, ContentHash: segment.currentTemplateHash, ID: segment.templateID}
	policy := paper.OMRAutoConfirmPolicyForTemplateReferenceAndCalibration(template, segment.templateHash, currentReference, segment.questionID, calibration)
	referenceAssetID, referenceSHA256 := "", ""
	if policy.Reference != nil {
		referenceAssetID, referenceSHA256 = policy.Reference.FileAssetID, policy.Reference.HashSHA256
	}
	calibrationID, calibrationEvidenceHash := "", ""
	if policy.Calibration != nil {
		calibrationID, calibrationEvidenceHash = policy.Calibration.ID, policy.Calibration.EvidenceHash
	}
	var omrID string
	err := tx.QueryRowContext(ctx, `INSERT INTO omr_run (tenant_id,scoring_run_id,answer_segment_id,crop_file_asset_id,crop_sha256,template_id,template_content_hash,profile_version,profile_hash,reference_file_asset_id,reference_sha256,calibration_session_id,calibration_evidence_hash,auto_confirm_min_confidence,auto_confirm_eligible,auto_confirm_reason)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,$8,$9,NULLIF($10,'')::uuid,NULLIF($11,''),NULLIF($12,'')::uuid,NULLIF($13,''),$14,$15,$16) RETURNING id::text`, tenantID, runID, segment.id, segment.cropID, segment.cropHash, segment.templateID, segment.templateHash, policy.RuntimeProfile.Version, policy.ProfileHash, referenceAssetID, referenceSHA256, calibrationID, calibrationEvidenceHash, policy.MinimumConfidence, policy.AutoConfirmEligible, policy.Reason).Scan(&omrID)
	if err != nil {
		return err
	}
	taskPayload := map[string]any{"exam_id": examID, "answer_segment_id": segment.id, "omr_run_id": omrID, "source_download_url": "/api/v1/internal/answer-segments/" + segment.id + "/image", "source_content_type": segment.contentType, "option_regions": options, "multiple": segment.kind == "multiple_choice", "profile": policy.RuntimeProfile, "profile_hash": policy.ProfileHash}
	if policy.Reference != nil {
		pageWidth, pageHeight := templatePageDimensions(templateLayout, segment.questionID)
		taskPayload["reference"] = map[string]any{
			"file_asset_id": policy.Reference.FileAssetID,
			"download_url":  "/api/v1/files/" + policy.Reference.FileAssetID + "/download",
			"sha256":        policy.Reference.HashSHA256,
			"content_type":  policy.Reference.ContentType,
			"page_no":       policy.Reference.PageNo,
			"page_width":    pageWidth,
			"page_height":   pageHeight,
			"question_region": map[string]any{
				"x": policy.Reference.X, "y": policy.Reference.Y,
				"width": policy.Reference.Width, "height": policy.Reference.Height,
			},
		}
	}
	payload, err := json.Marshal(taskPayload)
	if err != nil {
		return fmt.Errorf("encode omr worker task payload: %w", err)
	}
	var taskID string
	err = tx.QueryRowContext(ctx, `INSERT INTO agent_worker_task (tenant_id,task_type,queue_name,source_type,source_id,priority,payload,payload_schema_version,idempotency_key,dedupe_key,max_attempts,retry_backoff_seconds,created_by)
VALUES ($1::uuid,'omr_extract','page-processing','omr_run',$2::uuid,50,$3::jsonb,'omr-task-v2',$4,$4,3,5,$5::uuid)
ON CONFLICT (tenant_id,task_type,idempotency_key) DO UPDATE SET idempotency_key=EXCLUDED.idempotency_key RETURNING id::text`, tenantID, omrID, payload, "omr:"+omrID+":"+segment.cropHash, actorID).Scan(&taskID)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE omr_run SET runtime_task_id=$3::uuid WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, omrID, taskID); err != nil {
		return err
	}
	return nil
}

func cropRelativeOptionRegions(area map[string]any, options []any) []any {
	// 题区内的坐标有时恰好也落在页面题区范围；显式标记优先，避免旧启发式再次转换。
	if area["option_coordinate_space"] == "question_relative" {
		return options
	}
	areaX, xOK := area["x"].(float64)
	areaY, yOK := area["y"].(float64)
	areaWidth, widthOK := area["width"].(float64)
	areaHeight, heightOK := area["height"].(float64)
	if !xOK || !yOK || !widthOK || !heightOK || areaWidth <= 0 || areaHeight <= 0 {
		return options
	}
	normalized := make([]any, 0, len(options))
	for _, raw := range options {
		option, ok := raw.(map[string]any)
		if !ok {
			return options
		}
		x, xOK := option["x"].(float64)
		y, yOK := option["y"].(float64)
		width, widthOK := option["width"].(float64)
		height, heightOK := option["height"].(float64)
		if !xOK || !yOK || !widthOK || !heightOK ||
			x < areaX || y < areaY || x+width > areaX+areaWidth+0.000001 || y+height > areaY+areaHeight+0.000001 {
			// 部分集成已提供相对裁剪图的区域；保留这些值，避免重复转换。
			return options
		}
		relative := make(map[string]any, len(option))
		for key, value := range option {
			relative[key] = value
		}
		relative["x"] = (x - areaX) / areaWidth
		relative["y"] = (y - areaY) / areaHeight
		relative["width"] = width / areaWidth
		relative["height"] = height / areaHeight
		normalized = append(normalized, relative)
	}
	return normalized
}

func templatePageDimensions(layout paper.TemplateLayout, questionID string) (int, int) {
	for _, page := range layout.Pages {
		for _, region := range page.QuestionRegions {
			if region.QuestionID == questionID {
				return page.Width, page.Height
			}
		}
	}
	return 0, 0
}
