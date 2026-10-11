package mathunderstanding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

// 一次完成命令绑定冻结输入、核验结果和租约元数据；跨后端传递时不能拆换其中某一版本。
type verifiedRuntimeCompletion struct {
	Task               workerruntime.Task
	Parent             Artifact
	CorrectionRevision int64
	Contract           CreateArtifactInput
	Quality            map[string]any
	LeaseToken         string
	DurationMS         int
}

// 激活验证结果与完成 worker 租约必须一起成功。
// 验证期间新增了教师修订或识别版本时，过期任务只记录已完成，不把结果设为当前证据。
func (h *Handler) completeVerifiedRuntime(ctx context.Context, completion verifiedRuntimeCompletion) (Artifact, bool, error) {
	switch store := h.artifacts.(type) {
	case *PostgresStore:
		return store.completeVerifiedRuntime(ctx, completion)
	case *MemoryStore:
		runtime, runtimeOK := h.runtime.(*workerruntime.MemoryStore)
		corrections, correctionOK := h.corrections.(*MemoryCorrectionStore)
		if !runtimeOK || !correctionOK || corrections.artifacts != store {
			return Artifact{}, false, ErrInvalidInput
		}
		return store.completeVerifiedRuntime(ctx, runtime, corrections, completion)
	default:
		return Artifact{}, false, ErrInvalidInput
	}
}

type memoryVerificationActivation struct {
	storedParent    Artifact
	existing        Artifact
	previousCurrent Artifact
	previousNext    int64
	superseded      bool
}

func (s *MemoryStore) completeVerifiedRuntime(ctx context.Context, runtime *workerruntime.MemoryStore, corrections *MemoryCorrectionStore, completion verifiedRuntimeCompletion) (Artifact, bool, error) {
	// 保持证据写锁、修订读锁、运行时原子锁的顺序，失败恢复也在前两把锁内。
	s.mu.Lock()
	defer s.mu.Unlock()
	corrections.mu.RLock()
	defer corrections.mu.RUnlock()
	activation, err := s.memoryVerificationActivationLocked(corrections, completion)
	if err != nil {
		return Artifact{}, false, err
	}
	var derived Artifact
	err = runtime.RunAtomic(func(tx *workerruntime.MemoryTx) error {
		var activationErr error
		derived, activationErr = s.activateMemoryVerificationLocked(completion, activation.superseded)
		if activationErr != nil {
			return activationErr
		}
		_, err := tx.Complete(ctx, completion.Task.TenantID, completion.Task.ID, verificationCompleteInput(completion.Parent, derived, completion.CorrectionRevision, activation.superseded, completion.LeaseToken, completion.DurationMS))
		return err
	})
	s.restoreMemoryVerificationActivationLocked(completion, activation, derived, err)
	return cloneArtifact(derived), activation.superseded, err
}

func (s *MemoryStore) memoryVerificationActivationLocked(corrections *MemoryCorrectionStore, completion verifiedRuntimeCompletion) (memoryVerificationActivation, error) {
	items := corrections.items[completion.Task.TenantID+":"+completion.Parent.ID]
	latestRevision := int64(len(items))
	storedParent, ok := s.artifacts[completion.Task.TenantID+":"+completion.Parent.ID]
	if !ok {
		return memoryVerificationActivation{}, ErrNotFound
	}
	var existing Artifact
	for _, item := range s.artifacts {
		if item.TenantID == completion.Task.TenantID && item.ParentArtifactID == completion.Parent.ID && item.CorrectionRevision == completion.CorrectionRevision {
			existing = item
		}
	}
	superseded := latestRevision != completion.CorrectionRevision || (!storedParent.IsCurrent && existing.ID == "")
	return memoryVerificationActivation{
		storedParent: storedParent, existing: existing, superseded: superseded,
		previousCurrent: s.current[completion.Task.TenantID+":"+completion.Parent.AnswerSegmentID], previousNext: s.next,
	}, nil
}

func (s *MemoryStore) activateMemoryVerificationLocked(completion verifiedRuntimeCompletion, superseded bool) (Artifact, error) {
	if superseded {
		return Artifact{}, nil
	}
	derived, err := s.createDerivedArtifactLocked(completion.Task.TenantID, completion.Parent.ID, completion.CorrectionRevision, completion.Contract, completion.Quality)
	if err != nil {
		return derived, err
	}
	if !sameDerivedResult(derived, completion.Contract, completion.Quality) {
		return derived, workerruntime.ErrConflict
	}
	return derived, nil
}

func (s *MemoryStore) restoreMemoryVerificationActivationLocked(completion verifiedRuntimeCompletion, activation memoryVerificationActivation, derived Artifact, err error) {
	// 仅撤销本次新建结果；失败重试不能删除之前已接受的派生证据，也不能消耗版本计数。
	if err != nil && activation.existing.ID == "" && derived.ID != "" {
		delete(s.artifacts, completion.Task.TenantID+":"+derived.ID)
		s.artifacts[completion.Task.TenantID+":"+completion.Parent.ID] = activation.storedParent
		s.current[completion.Task.TenantID+":"+completion.Parent.AnswerSegmentID] = activation.previousCurrent
		s.next = activation.previousNext
	}
}

func (s *PostgresStore) completeVerifiedRuntime(ctx context.Context, completion verifiedRuntimeCompletion) (Artifact, bool, error) {
	task, parent, revision := completion.Task, completion.Parent, completion.CorrectionRevision
	input, quality, lease, duration := completion.Contract, completion.Quality, completion.LeaseToken, completion.DurationMS
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
