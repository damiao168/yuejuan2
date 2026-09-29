package mathunderstanding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

// 激活验证结果与完成 worker 租约必须一起成功。
// 验证期间新增了教师修订或识别版本时，过期任务只记录已完成，不把结果设为当前证据。
func (h *Handler) completeVerifiedRuntime(ctx context.Context, task workerruntime.Task, parent Artifact, revision int64, input CreateArtifactInput, quality map[string]any, lease string, duration int) (Artifact, bool, error) {
	switch store := h.artifacts.(type) {
	case *PostgresStore:
		return store.completeVerifiedRuntime(ctx, task, parent, revision, input, quality, lease, duration)
	case *MemoryStore:
		runtime, runtimeOK := h.runtime.(*workerruntime.MemoryStore)
		corrections, correctionOK := h.corrections.(*MemoryCorrectionStore)
		if !runtimeOK || !correctionOK || corrections.artifacts != store {
			return Artifact{}, false, ErrInvalidInput
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		corrections.mu.RLock()
		defer corrections.mu.RUnlock()
		items := corrections.items[task.TenantID+":"+parent.ID]
		latestRevision := int64(len(items))
		storedParent, ok := store.artifacts[task.TenantID+":"+parent.ID]
		if !ok {
			return Artifact{}, false, ErrNotFound
		}
		var existing Artifact
		for _, item := range store.artifacts {
			if item.TenantID == task.TenantID && item.ParentArtifactID == parent.ID && item.CorrectionRevision == revision {
				existing = item
			}
		}
		superseded := latestRevision != revision || (!storedParent.IsCurrent && existing.ID == "")
		previousCurrent, previousNext := store.current[task.TenantID+":"+parent.AnswerSegmentID], store.next
		var derived Artifact
		err := runtime.RunAtomic(func(tx *workerruntime.MemoryTx) error {
			if !superseded {
				var err error
				derived, err = store.createDerivedArtifactLocked(task.TenantID, parent.ID, revision, input, quality)
				if err != nil {
					return err
				}
				if !sameDerivedResult(derived, input, quality) {
					return workerruntime.ErrConflict
				}
			}
			_, err := tx.Complete(ctx, task.TenantID, task.ID, verificationCompleteInput(parent, derived, revision, superseded, lease, duration))
			return err
		})
		if err != nil && existing.ID == "" && derived.ID != "" {
			delete(store.artifacts, task.TenantID+":"+derived.ID)
			store.artifacts[task.TenantID+":"+parent.ID] = storedParent
			store.current[task.TenantID+":"+parent.AnswerSegmentID] = previousCurrent
			store.next = previousNext
		}
		return cloneArtifact(derived), superseded, err
	default:
		return Artifact{}, false, ErrInvalidInput
	}
}

func (s *PostgresStore) completeVerifiedRuntime(ctx context.Context, task workerruntime.Task, parent Artifact, revision int64, input CreateArtifactInput, quality map[string]any, lease string, duration int) (Artifact, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Artifact{}, false, err
	}
	defer tx.Rollback()
	var segmentID string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM answer_segment WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, task.TenantID, parent.AnswerSegmentID).Scan(&segmentID); err != nil {
		return Artifact{}, false, err
	}
	var current bool
	if err = tx.QueryRowContext(ctx, `SELECT is_current FROM math_understanding_artifact WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, task.TenantID, parent.ID).Scan(&current); err != nil {
		return Artifact{}, false, err
	}
	var latestRevision int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(revision),0) FROM math_understanding_correction WHERE tenant_id=$1::uuid AND artifact_id=$2::uuid`, task.TenantID, parent.ID).Scan(&latestRevision); err != nil {
		return Artifact{}, false, err
	}
	var existing bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM math_understanding_artifact WHERE tenant_id=$1::uuid AND parent_artifact_id=$2::uuid AND correction_revision=$3 AND stage='verified')`, task.TenantID, parent.ID, revision).Scan(&existing); err != nil {
		return Artifact{}, false, err
	}
	// 已有同版本结果允许幂等重试；否则父记录已被替代或修订号改变，都不能再激活本次结果。
	superseded := latestRevision != revision || (!current && !existing)
	var derived Artifact
	if !superseded {
		derived, err = s.createDerivedArtifactInTx(ctx, tx, task.TenantID, parent.ID, revision, input, quality)
		if err != nil {
			return Artifact{}, false, err
		}
		if !sameDerivedResult(derived, input, quality) {
			return Artifact{}, false, workerruntime.ErrConflict
		}
	}
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, task.TenantID, task.ID, verificationCompleteInput(parent, derived, revision, superseded, lease, duration)); err != nil {
		return Artifact{}, false, err
	}
	return derived, superseded, tx.Commit()
}

func sameDerivedResult(derived Artifact, input CreateArtifactInput, quality map[string]any) bool {
	existing, err := json.Marshal(derived.CreateArtifactInput)
	incoming, inputErr := json.Marshal(input)
	existingQuality, qualityErr := json.Marshal(derived.QualitySummary)
	incomingQuality, incomingErr := json.Marshal(cloneMap(quality))
	return errors.Join(err, inputErr, qualityErr, incomingErr) == nil && bytes.Equal(existing, incoming) && bytes.Equal(existingQuality, incomingQuality)
}

func verificationCompleteInput(parent, derived Artifact, revision int64, superseded bool, lease string, duration int) workerruntime.CompleteInput {
	result := map[string]any{"correction_revision": revision, "superseded": superseded}
	if superseded {
		result["artifact_id"], result["artifact_version"] = parent.ID, parent.Version
	} else {
		result["artifact_id"], result["artifact_version"], result["parent_artifact_id"] = derived.ID, derived.Version, parent.ID
	}
	return workerruntime.CompleteInput{LeaseToken: lease, ResultSchemaVersion: "math-verification-result-v1", Result: result, DurationMS: duration}
}
