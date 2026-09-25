package subjective

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

type BatchCommandRecovery struct {
	CommandID string        `json:"command_id"`
	Status    string        `json:"status"`
	Batch     *GradingBatch `json:"batch,omitempty"`
}
type BatchEnqueuePlan struct {
	Completed bool             `json:"completed"`
	CommandID string           `json:"command_id"`
	BatchID   string           `json:"batch_id"`
	Runs      []CreateRunInput `json:"runs"`
}

func batchRequestHash(segments []string) string {
	raw, _ := json.Marshal(segments)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

const batchCommandSelect = `SELECT id::text,tenant_id::text,idempotency_key,COALESCE(scoring_run_id::text,''),status,segment_ids,total_count,queued_count,processing_count,succeeded_count,failed_count,created_by::text,created_at,updated_at FROM subjective_grading_batch`

func (s *PostgresStore) RecoverBatchCommand(ctx context.Context, tenantID, actorID, commandID string) (BatchCommandRecovery, error) {
	result := BatchCommandRecovery{CommandID: commandID, Status: "not_accepted"}
	if commandID == "" || validateIdempotencyKey(commandID) != nil {
		return result, ErrInvalidInput
	}
	batch, err := scanBatch(s.db.QueryRowContext(ctx, batchCommandSelect+` WHERE tenant_id=$1::uuid AND created_by=$2::uuid AND idempotency_key=$3`, tenantID, actorID, commandID))
	if errors.Is(err, ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Status = "succeeded"
	result.Batch = &batch
	return result, nil
}
func (s *PostgresStore) GetEnqueuePlan(ctx context.Context, tenantID, actorID, batchID string) (BatchEnqueuePlan, error) {
	var plan BatchEnqueuePlan
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT command_id,batch_id::text,runs,completed_at IS NOT NULL FROM subjective_batch_enqueue_plan WHERE tenant_id=$1::uuid AND batch_id=$2::uuid AND actor_id=$3::uuid`, tenantID, batchID, actorID).Scan(&plan.CommandID, &plan.BatchID, &raw, &plan.Completed)
	if errors.Is(err, sql.ErrNoRows) {
		return plan, ErrNotFound
	}
	if err != nil {
		return plan, err
	}
	err = json.Unmarshal(raw, &plan.Runs)
	return plan, err
}
func (s *PostgresStore) SaveEnqueuePlan(ctx context.Context, tenantID, actorID string, plan BatchEnqueuePlan) (BatchEnqueuePlan, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return plan, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenantID+":"+plan.BatchID+":subjective-plan"); err != nil {
		return plan, err
	}
	var existingActor string
	var existingRaw []byte
	var existingCommand string
	err = tx.QueryRowContext(ctx, `SELECT actor_id::text,command_id,runs FROM subjective_batch_enqueue_plan WHERE tenant_id=$1::uuid AND batch_id=$2::uuid`, tenantID, plan.BatchID).Scan(&existingActor, &existingCommand, &existingRaw)
	if err == nil {
		if existingActor != actorID || existingCommand != plan.CommandID {
			return plan, ErrIdempotencyConflict
		}
		if err = json.Unmarshal(existingRaw, &plan.Runs); err != nil {
			return plan, err
		}
		return plan, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return plan, err
	}
	// Preserve pre-migration runs instead of deriving new IDs from today's policy.
	for i := range plan.Runs {
		input := &plan.Runs[i]
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM subjective_grading_run WHERE tenant_id=$1::uuid AND batch_id=$2::uuid AND answer_segment_id=$3::uuid`, tenantID, plan.BatchID, input.AnswerSegmentID).Scan(&count); err != nil {
			return plan, err
		}
		if count > 1 {
			return plan, ErrIdempotencyConflict
		}
		if count == 1 {
			err = tx.QueryRowContext(ctx, `SELECT answer_version,question_id::text,rubric_version,model_version,prompt_version,min_confidence::float8,request_id,COALESCE(math_artifact_id::text,''),math_artifact_version,math_correction_revision,math_scoring_version FROM subjective_grading_run WHERE tenant_id=$1::uuid AND batch_id=$2::uuid AND answer_segment_id=$3::uuid`, tenantID, plan.BatchID, input.AnswerSegmentID).Scan(&input.AnswerVersion, &input.QuestionID, &input.RubricVersion, &input.ModelVersion, &input.PromptVersion, &input.MinConfidence, &input.RequestID, &input.MathArtifactID, &input.MathArtifactVersion, &input.MathCorrectionRevision, &input.MathScoringVersion)
			if err != nil {
				return plan, err
			}
		}
	}
	raw, err := json.Marshal(plan.Runs)
	if err != nil {
		return plan, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO subjective_batch_enqueue_plan(tenant_id,batch_id,actor_id,command_id,request_hash,runs) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::jsonb)`, tenantID, plan.BatchID, actorID, plan.CommandID, batchRequestHash([]string{plan.BatchID}), raw)
	if err != nil {
		return plan, err
	}
	return plan, tx.Commit()
}

func (s *MemoryStore) RecoverBatchCommand(_ context.Context, tenantID, actorID, commandID string) (BatchCommandRecovery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := BatchCommandRecovery{CommandID: commandID, Status: "not_accepted"}
	if commandID == "" || validateIdempotencyKey(commandID) != nil {
		return result, ErrInvalidInput
	}
	for _, batch := range s.batches {
		if batch.TenantID == tenantID && batch.CreatedBy == actorID && batch.IdempotencyKey == commandID {
			batch.SegmentIDs = cloneStrings(batch.SegmentIDs)
			result.Status = "succeeded"
			result.Batch = &batch
			break
		}
	}
	return result, nil
}
func (s *MemoryStore) GetEnqueuePlan(_ context.Context, tenantID, actorID, batchID string) (BatchEnqueuePlan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.enqueuePlans[key(tenantID, batchID)]
	if !ok || entry.actor != actorID {
		return BatchEnqueuePlan{}, ErrNotFound
	}
	plan := entry.plan
	plan.Runs = append([]CreateRunInput(nil), plan.Runs...)
	return plan, nil
}
func (s *MemoryStore) SaveEnqueuePlan(_ context.Context, tenantID, actorID string, plan BatchEnqueuePlan) (BatchEnqueuePlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enqueuePlans == nil {
		s.enqueuePlans = map[string]memoryEnqueuePlan{}
	}
	k := key(tenantID, plan.BatchID)
	if entry, ok := s.enqueuePlans[k]; ok {
		if entry.actor != actorID || entry.plan.CommandID != plan.CommandID {
			return plan, ErrIdempotencyConflict
		}
		plan = entry.plan
	} else {
		s.enqueuePlans[k] = memoryEnqueuePlan{actor: actorID, plan: plan}
	}
	plan.Runs = append([]CreateRunInput(nil), plan.Runs...)
	return plan, nil
}

type memoryEnqueuePlan struct {
	actor string
	plan  BatchEnqueuePlan
}

func (s *PostgresStore) CompleteEnqueuePlan(ctx context.Context, tenantID, actorID, batchID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE subjective_batch_enqueue_plan SET completed_at=COALESCE(completed_at,now()) WHERE tenant_id=$1::uuid AND batch_id=$2::uuid AND actor_id=$3::uuid`, tenantID, batchID, actorID)
	return err
}
func (s *MemoryStore) CompleteEnqueuePlan(_ context.Context, tenantID, actorID, batchID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(tenantID, batchID)
	entry, ok := s.enqueuePlans[k]
	if !ok || entry.actor != actorID {
		return ErrNotFound
	}
	entry.plan.Completed = true
	s.enqueuePlans[k] = entry
	return nil
}
