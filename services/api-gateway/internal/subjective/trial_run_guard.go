package subjective

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// 批次锁覆盖运行插入，停止先拿同一批次锁再关闭已有运行；两者交错也不能留下新的可执行运行。
// 直接评分和面板评分同样经过此处，不能省略批次身份来绕过新评分运行的试改闸门。
func validateTrialRunInTx(ctx context.Context, tx *sql.Tx, tenantID string, input CreateRunInput) error {
	if input.BatchID != "" {
		var status string
		var member bool
		err := tx.QueryRowContext(ctx, `SELECT status,segment_ids @> jsonb_build_array($3::text)
FROM subjective_grading_batch WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR SHARE`, tenantID, input.BatchID, input.AnswerSegmentID).Scan(&status, &member)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status == "cancelled" {
			return ErrBatchCancelled
		}
		if !member {
			return ErrInvalidInput
		}
	}
	var blocked bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM review_task task JOIN scoring_run sr ON sr.tenant_id=task.tenant_id AND sr.id=task.scoring_run_id
 WHERE task.tenant_id=$1::uuid AND task.answer_segment_id=$2::uuid AND task.deleted_at IS NULL
 AND sr.ai_trial_required AND sr.deleted_at IS NULL AND sr.status IN ('queued','processing','needs_review','failed')
 AND NOT EXISTS(SELECT 1 FROM scoring_ai_trial trial
   WHERE trial.tenant_id=sr.tenant_id AND trial.scoring_run_id=sr.id AND trial.question_id=task.question_id
   AND trial.status NOT IN ('stopped','invalidated')
   AND (trial.sample_batch_id=NULLIF($3,'')::uuid OR (trial.expansion_batch_id=NULLIF($3,'')::uuid AND trial.status='expanded')))
)`, tenantID, input.AnswerSegmentID, input.BatchID).Scan(&blocked)
	if err != nil {
		return err
	}
	if blocked {
		return ErrTrialConflict
	}
	return nil
}

func (s *MemoryStore) validateTrialRunLocked(tenantID string, input CreateRunInput) error {
	if input.BatchID != "" {
		var found bool
		for _, batch := range s.batches {
			if batch.TenantID != tenantID || batch.ID != input.BatchID {
				continue
			}
			if batch.Status == "cancelled" {
				return ErrBatchCancelled
			}
			for _, segmentID := range batch.SegmentIDs {
				found = found || segmentID == input.AnswerSegmentID
			}
		}
		if !found {
			return ErrInvalidInput
		}
	}
	for runKey, segmentIDs := range s.trialRunSegments {
		if !strings.HasPrefix(runKey, key(tenantID, "")) {
			continue
		}
		for _, segmentID := range segmentIDs {
			if segmentID != input.AnswerSegmentID {
				continue
			}
			var allowed bool
			for _, trial := range s.trials {
				if trial.TenantID == tenantID && key(tenantID, trial.RunID) == runKey && trial.Status != "stopped" && trial.Status != "invalidated" &&
					input.BatchID != "" && (trial.SampleBatchID == input.BatchID || (trial.ExpansionBatchID == input.BatchID && trial.Status == "expanded")) {
					allowed = true
				}
			}
			if !allowed {
				return ErrTrialConflict
			}
		}
	}
	return nil
}
