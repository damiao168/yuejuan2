package subjective

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
)

// 批次幂等键冲突时返回原批次并核对题目列表、评分运行和创建人，防止把不同请求当作同一批。
func (s *PostgresStore) CreateBatch(ctx context.Context, tenantID string, actorID string, input CreateBatchInput) (GradingBatch, error) {
	segments, err := normalizeBatchSegments(input.SegmentIDs)
	if err != nil || tenantID == "" || input.IdempotencyKey == "" || actorID == "" {
		return GradingBatch{}, ErrInvalidInput
	}
	raw, err := json.Marshal(segments)
	if err != nil {
		return GradingBatch{}, err
	}
	var requiresTrial bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM scoring_run sr WHERE sr.tenant_id=$1::uuid AND sr.id=NULLIF($2,'')::uuid AND sr.ai_trial_required
 UNION ALL
 SELECT 1 FROM jsonb_array_elements_text($3::jsonb) requested(id)
 JOIN review_task task ON task.tenant_id=$1::uuid AND task.answer_segment_id=requested.id::uuid AND task.deleted_at IS NULL
 JOIN scoring_run sr ON sr.tenant_id=task.tenant_id AND sr.id=task.scoring_run_id
 WHERE sr.ai_trial_required AND sr.status IN ('queued','processing','needs_review','failed') AND sr.deleted_at IS NULL
)`, tenantID, input.ScoringRunID, raw).Scan(&requiresTrial); err != nil {
		return GradingBatch{}, err
	}
	if requiresTrial {
		return GradingBatch{}, ErrTrialConflict
	}
	if input.ScoringRunID != "" {
		if _, parseErr := uuid.Parse(input.ScoringRunID); parseErr != nil {
			return GradingBatch{}, ErrInvalidInput
		}
		for _, segmentID := range segments {
			if _, parseErr := uuid.Parse(segmentID); parseErr != nil {
				return GradingBatch{}, ErrInvalidInput
			}
		}
		var eligible int
		err = s.db.QueryRowContext(ctx, `SELECT count(DISTINCT seg.id) FROM jsonb_array_elements_text($3::jsonb) requested(id)
JOIN answer_segment seg ON seg.tenant_id=$1::uuid AND seg.id=requested.id::uuid AND seg.deleted_at IS NULL
JOIN review_task task ON task.tenant_id=seg.tenant_id AND task.answer_segment_id=seg.id AND task.scoring_run_id=$2::uuid
  AND task.question_id=seg.question_id AND task.status IN ('pending','assigned','in_progress','returned') AND task.deleted_at IS NULL
JOIN scoring_run sr ON sr.tenant_id=task.tenant_id AND sr.id=task.scoring_run_id AND sr.status IN ('queued','processing','needs_review','failed') AND sr.deleted_at IS NULL
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.exam_id=sr.exam_id AND sub.deleted_at IS NULL
JOIN exam_question_snapshot snapshot ON snapshot.tenant_id=seg.tenant_id AND snapshot.exam_id=sr.exam_id AND snapshot.question_id=seg.question_id
  AND snapshot.scoring_policy_snapshot_json->>'mode' IN ('AI_ASSIST','AI_FAST_CONFIRM')`, tenantID, input.ScoringRunID, raw).Scan(&eligible)
		if err != nil {
			return GradingBatch{}, err
		}
		if eligible != len(segments) {
			return GradingBatch{}, ErrInvalidInput
		}
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO subjective_grading_batch (tenant_id, idempotency_key, scoring_run_id, status, segment_ids, total_count, created_by,command_request_hash)
VALUES ($1::uuid, $2, NULLIF($7,'')::uuid, 'planned', $3::jsonb, $4, $5::uuid,$6)
ON CONFLICT (tenant_id, idempotency_key) DO UPDATE SET updated_at = subjective_grading_batch.updated_at
RETURNING id::text, tenant_id::text, idempotency_key, COALESCE(scoring_run_id::text,''), status, segment_ids,
  total_count, queued_count, processing_count, succeeded_count, failed_count,
  created_by::text, created_at, updated_at
`, tenantID, input.IdempotencyKey, raw, len(segments), actorID, batchRequestHash(segments), input.ScoringRunID)
	batch, err := scanBatch(row)
	if err != nil {
		return GradingBatch{}, err
	}
	if batch.CreatedBy != actorID || batch.ScoringRunID != input.ScoringRunID || !sameStringSlice(batch.SegmentIDs, segments) {
		return GradingBatch{}, ErrIdempotencyConflict
	}
	return batch, nil
}

func (s *PostgresStore) GetBatch(ctx context.Context, tenantID string, batchID string) (GradingBatch, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, idempotency_key, COALESCE(scoring_run_id::text,''), status, segment_ids,
  total_count, queued_count, processing_count, succeeded_count, failed_count,
  created_by::text, created_at, updated_at
FROM subjective_grading_batch
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, batchID)
	return scanBatch(row)
}

func (s *PostgresStore) ListScoringRunBatches(ctx context.Context, tenantID, scoringRunID string) ([]GradingBatch, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL)`, tenantID, scoringRunID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, batchCommandSelect+` WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND deleted_at IS NULL ORDER BY created_at,id`, tenantID, scoringRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	batches := []GradingBatch{}
	for rows.Next() {
		batch, scanErr := scanBatch(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		batches = append(batches, batch)
	}
	return batches, rows.Err()
}

// FailedBatchSegments 只返回失败或冲突、且在当前评分运行中仍有开放人工任务的模型运行。
// 重试使用新的批次和请求身份，使历史 AI 证据保持不可变。
func (s *PostgresStore) FailedBatchSegments(ctx context.Context, tenantID, batchID string) ([]string, error) {
	batch, err := s.GetBatch(ctx, tenantID, batchID)
	if err != nil {
		return nil, err
	}
	if batch.ScoringRunID == "" || batch.Status == "cancelled" {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT requested.id
FROM subjective_grading_batch batch
JOIN scoring_run sr ON sr.tenant_id=batch.tenant_id AND sr.id=batch.scoring_run_id
  AND sr.status IN ('queued','processing','needs_review','failed') AND sr.deleted_at IS NULL
CROSS JOIN LATERAL jsonb_array_elements_text(batch.segment_ids) requested(id)
JOIN LATERAL (
  SELECT status FROM subjective_grading_run ai_run
  WHERE ai_run.tenant_id=batch.tenant_id AND ai_run.batch_id=batch.id
    AND ai_run.answer_segment_id=requested.id::uuid AND ai_run.deleted_at IS NULL
  ORDER BY created_at DESC,id DESC LIMIT 1
) latest ON latest.status IN ('failed','conflict')
WHERE batch.tenant_id=$1::uuid AND batch.id=$2::uuid AND batch.deleted_at IS NULL
  AND EXISTS (SELECT 1 FROM review_task task WHERE task.tenant_id=batch.tenant_id
    AND task.scoring_run_id=batch.scoring_run_id AND task.answer_segment_id=requested.id::uuid
    AND task.status IN ('pending','assigned','in_progress','returned') AND task.deleted_at IS NULL)
ORDER BY requested.id`, tenantID, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	segments := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		segments = append(segments, id)
	}
	return segments, rows.Err()
}

func (s *PostgresStore) RefreshBatch(ctx context.Context, tenantID string, batchID string) (GradingBatch, error) {
	return refreshBatchInDB(ctx, s.db, tenantID, batchID)
}

func refreshBatchInDB(ctx context.Context, query rowQueryer, tenantID string, batchID string) (GradingBatch, error) {
	row := query.QueryRowContext(ctx, `
WITH counts AS (
  SELECT
    count(*) FILTER (WHERE status = 'queued')::int AS queued_count,
    count(*) FILTER (WHERE status = 'processing')::int AS processing_count,
    count(*) FILTER (WHERE status = 'succeeded')::int AS succeeded_count,
    count(*) FILTER (WHERE status IN ('failed', 'conflict'))::int AS failed_count
  FROM subjective_grading_run
  WHERE tenant_id=$1::uuid AND batch_id=$2::uuid AND deleted_at IS NULL
)
UPDATE subjective_grading_batch AS batch
SET queued_count=counts.queued_count,
  processing_count=counts.processing_count,
  succeeded_count=counts.succeeded_count,
  failed_count=counts.failed_count,
  status=CASE
    WHEN batch.status IN ('planned', 'cancelled') THEN batch.status
    WHEN counts.succeeded_count + counts.failed_count = batch.total_count AND counts.failed_count > 0 THEN 'failed'
    WHEN counts.succeeded_count = batch.total_count THEN 'completed'
    ELSE 'processing'
  END,
  updated_at=now()
FROM counts
WHERE batch.tenant_id=$1::uuid AND batch.id=$2::uuid AND batch.deleted_at IS NULL
RETURNING batch.id::text, batch.tenant_id::text, batch.idempotency_key, COALESCE(batch.scoring_run_id::text,''), batch.status, batch.segment_ids,
  batch.total_count, batch.queued_count, batch.processing_count, batch.succeeded_count, batch.failed_count,
  batch.created_by::text, batch.created_at, batch.updated_at
`, tenantID, batchID)
	return scanBatch(row)
}

func (s *PostgresStore) UpdateBatch(ctx context.Context, tenantID string, batchID string, input UpdateBatchInput) (GradingBatch, error) {
	if input.Status != "planned" && input.Status != "processing" && input.Status != "completed" && input.Status != "failed" && input.Status != "cancelled" {
		return GradingBatch{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE subjective_grading_batch
SET status=$3, queued_count=$4, processing_count=$5, succeeded_count=$6, failed_count=$7, updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL AND (status <> 'cancelled' OR $3='cancelled')
RETURNING id::text, tenant_id::text, idempotency_key, COALESCE(scoring_run_id::text,''), status, segment_ids,
  total_count, queued_count, processing_count, succeeded_count, failed_count,
  created_by::text, created_at, updated_at
`, tenantID, batchID, input.Status, input.QueuedCount, input.ProcessingCount, input.SucceededCount, input.FailedCount)
	batch, err := scanBatch(row)
	if errors.Is(err, ErrNotFound) {
		current, lookupErr := s.GetBatch(ctx, tenantID, batchID)
		if lookupErr == nil && current.Status == "cancelled" {
			return GradingBatch{}, ErrBatchCancelled
		}
	}
	return batch, err
}

func scanBatch(row gradeScanner) (GradingBatch, error) {
	var out GradingBatch
	var segmentRaw []byte
	if err := row.Scan(&out.ID, &out.TenantID, &out.IdempotencyKey, &out.ScoringRunID, &out.Status, &segmentRaw, &out.TotalCount, &out.QueuedCount, &out.ProcessingCount, &out.SucceededCount, &out.FailedCount, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GradingBatch{}, ErrNotFound
		}
		return GradingBatch{}, err
	}
	if err := json.Unmarshal(segmentRaw, &out.SegmentIDs); err != nil {
		return GradingBatch{}, err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}
