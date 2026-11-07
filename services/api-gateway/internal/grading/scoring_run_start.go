package grading

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"errors"
	"strings"
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

// validateScoringSegmentReprocessTx 只允许尚未进入人工提交或终评的片段重算。
// 已提交的人工分和终评必须走复评或更正，通用重算不能撤掉人工保护后保留旧终评。
func validateScoringSegmentReprocessTx(ctx context.Context, tx *sql.Tx, tenantID, examID, segmentID string) error {
	// 与成绩汇总共用考试锁，并先于片段行锁取得；汇总不能在检查到失效期间插入终评。
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, tenantID, examID); err != nil {
		return err
	}
	var exists int
	err := tx.QueryRowContext(ctx, `
SELECT 1
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND seg.id=$3::uuid
  AND seg.deleted_at IS NULL AND seg.processing_status='completed' AND seg.crop_file_asset_id IS NOT NULL
FOR UPDATE OF seg
`, tenantID, examID, segmentID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// 提交会先锁任务再通过外键读取片段；此处只读任务，避免与片段锁形成反向等待。
	// 所有非取消任务都阻止重算，取消任务不能正常恢复提交；片段锁同时阻止新任务和成绩的外键插入。
	var protectedGrade bool
	if err = tx.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM review_task WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND status<>'cancelled' AND deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND source='human' AND deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM human_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL)
 OR EXISTS(SELECT 1 FROM final_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL)`, tenantID, segmentID).Scan(&protectedGrade); err != nil {
		return err
	}
	var activeOMR int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM omr_run WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND status IN ('queued','processing','retryable_error') AND deleted_at IS NULL`, tenantID, segmentID).Scan(&activeOMR); err != nil {
		return err
	}
	if protectedGrade || activeOMR > 0 {
		return ErrInvalidTransition
	}
	return nil
}

// invalidateScoringSegmentCurrentTx 只在已通过准入的新重处理运行内执行，先失效候选再失效成绩。
// 普通整场启动不替换片段事实；只失效自动题分，两次更新由启动事务一起提交或回滚。
func invalidateScoringSegmentCurrentTx(ctx context.Context, tx *sql.Tx, tenantID, segmentID string) error {
	if segmentID == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE answer_candidate SET is_current=false WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current`, tenantID, segmentID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE question_grade SET is_current=false,status='invalidated' WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND source='rule_confirmed' AND deleted_at IS NULL`, tenantID, segmentID); err != nil {
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
