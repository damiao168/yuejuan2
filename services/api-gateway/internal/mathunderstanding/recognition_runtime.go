package mathunderstanding

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type recognitionRuntimeCompletion struct {
	Task       workerruntime.Task
	ActorID    string
	Input      CreateArtifactInput
	LeaseToken string
	DurationMS int
}

// completeRuntimeArtifact 把识别产物、核验任务和原始租约完成放进同一事务。
// 最后接受结果时校验有效租约；失败时识别产物和后续核验任务都不能生效。
func (h *Handler) completeRuntimeArtifact(ctx context.Context, completion recognitionRuntimeCompletion) (Artifact, string, error) {
	switch store := h.artifacts.(type) {
	case *PostgresStore:
		return store.completeRuntimeArtifact(ctx, completion)
	case *MemoryStore:
		runtime, ok := h.runtime.(*workerruntime.MemoryStore)
		if !ok {
			return Artifact{}, "", ErrInvalidInput
		}
		return store.completeRuntimeArtifact(ctx, runtime, completion)
	default:
		return Artifact{}, "", ErrInvalidInput
	}
}

type memoryRecognitionSnapshot struct {
	current   map[string]Artifact
	artifacts map[string]Artifact
	next      int64
}

func (s *MemoryStore) recognitionSnapshotLocked() memoryRecognitionSnapshot {
	current := make(map[string]Artifact, len(s.current))
	for key, item := range s.current {
		current[key] = cloneArtifact(item)
	}
	artifacts := make(map[string]Artifact, len(s.artifacts))
	for key, item := range s.artifacts {
		artifacts[key] = cloneArtifact(item)
	}
	return memoryRecognitionSnapshot{current: current, artifacts: artifacts, next: s.next}
}

func (s *MemoryStore) restoreRecognitionSnapshotLocked(snapshot memoryRecognitionSnapshot) {
	s.current = snapshot.current
	s.artifacts = snapshot.artifacts
	s.next = snapshot.next
}

func (s *MemoryStore) completeRuntimeArtifact(ctx context.Context, runtime *workerruntime.MemoryStore, completion recognitionRuntimeCompletion) (Artifact, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := s.recognitionSnapshotLocked()
	committed := false
	defer func() {
		if !committed {
			s.restoreRecognitionSnapshotLocked(snapshot)
		}
	}()
	var artifact Artifact
	verificationTaskID := ""
	err := runtime.RunAtomic(func(tx *workerruntime.MemoryTx) error {
		var err error
		artifact, err = s.createArtifactLocked(completion.Task.TenantID, completion.Input)
		if err != nil {
			return err
		}
		if !sameRecognitionResult(artifact, completion.Input) {
			return workerruntime.ErrConflict
		}
		if len(artifact.Formulas) > 0 {
			key := "math-verification:" + artifact.ID + ":" + strconv.FormatInt(artifact.Version, 10) + ":0"
			verificationTask, taskErr := tx.CreateTask(ctx, artifact.TenantID, completion.ActorID, workerruntime.CreateTaskInput{
				TaskType: "evidence_verify", QueueName: "math-verification", SourceType: "math_understanding_artifact", SourceID: artifact.ID,
				Priority: 75, PayloadSchemaVersion: "math-verification-task-v1", IdempotencyKey: key, DedupeKey: key,
				MaxAttempts: 3, RetryBackoffSeconds: 30,
				Payload: map[string]any{"artifact_id": artifact.ID, "artifact_version": artifact.Version, "input_hash": artifact.InputHash, "correction_revision": 0},
			})
			if taskErr != nil {
				return taskErr
			}
			verificationTaskID = verificationTask.ID
		}
		_, err = tx.Complete(ctx, completion.Task.TenantID, completion.Task.ID, workerruntime.CompleteInput{
			LeaseToken: completion.LeaseToken, ResultSchemaVersion: "math-understanding-result-v2",
			Result:     map[string]any{"artifact_id": artifact.ID, "artifact_version": artifact.Version, "input_hash": artifact.InputHash, "verification_task_id": verificationTaskID},
			DurationMS: completion.DurationMS,
		})
		return err
	})
	if err != nil {
		return Artifact{}, "", err
	}
	committed = true
	return cloneArtifact(artifact), verificationTaskID, nil
}

func (s *PostgresStore) completeRuntimeArtifact(ctx context.Context, completion recognitionRuntimeCompletion) (Artifact, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Artifact{}, "", err
	}
	defer tx.Rollback()
	// 与人工纠正、符号核验保持题块在前、运行时任务在后的锁顺序；
	// 租约在最后完成处按数据库实际时间校验，任何失败都会回滚产物和触发器创建的核验任务。
	artifact, err := s.createArtifactInTx(ctx, tx, completion.Task.TenantID, completion.Input)
	if err != nil {
		return Artifact{}, "", err
	}
	if !sameRecognitionResult(artifact, completion.Input) {
		return Artifact{}, "", workerruntime.ErrConflict
	}
	verificationTaskID := ""
	if len(artifact.Formulas) > 0 {
		key := "math-verification:" + artifact.ID + ":" + strconv.FormatInt(artifact.Version, 10) + ":0"
		verificationTask, taskErr := workerruntime.CreateTaskInTx(ctx, tx, artifact.TenantID, completion.ActorID, workerruntime.CreateTaskInput{
			TaskType: "evidence_verify", QueueName: "math-verification", SourceType: "math_understanding_artifact", SourceID: artifact.ID,
			Priority: 75, PayloadSchemaVersion: "math-verification-task-v1", IdempotencyKey: key, DedupeKey: key,
			MaxAttempts: 3, RetryBackoffSeconds: 30,
			Payload: map[string]any{"artifact_id": artifact.ID, "artifact_version": artifact.Version, "input_hash": artifact.InputHash, "correction_revision": 0},
		})
		if taskErr != nil {
			return Artifact{}, "", taskErr
		}
		verificationTaskID = verificationTask.ID
	}
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, completion.Task.TenantID, completion.Task.ID, workerruntime.CompleteInput{
		LeaseToken: completion.LeaseToken, ResultSchemaVersion: "math-understanding-result-v2",
		Result:     map[string]any{"artifact_id": artifact.ID, "artifact_version": artifact.Version, "input_hash": artifact.InputHash, "verification_task_id": verificationTaskID},
		DurationMS: completion.DurationMS,
	}); err != nil {
		return Artifact{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return Artifact{}, "", err
	}
	return artifact, verificationTaskID, nil
}

// 同一输入哈希只标识图片来源；重放还必须与已接受的识别内容相同，不能把冲突公式伪装成原成功回执。
func sameRecognitionResult(artifact Artifact, input CreateArtifactInput) bool {
	existing, existingErr := json.Marshal(artifact.CreateArtifactInput)
	incoming, incomingErr := json.Marshal(input)
	return existingErr == nil && incomingErr == nil && bytes.Equal(existing, incoming)
}
