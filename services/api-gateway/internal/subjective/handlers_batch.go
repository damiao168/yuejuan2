package subjective

import (
	"context"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

// 批次创建先按幂等键恢复已有命令，再校验题目上下文，避免重复请求产生第二批运行。
func (h *Handler) CreateBatch(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input CreateBatchInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		writeStoreError(w, r, ErrInvalidInput)
		return
	}
	segments, err := normalizeBatchSegments(input.SegmentIDs)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	recovered, recoveryErr := h.store.RecoverBatchCommand(r.Context(), user.TenantID, user.ID, input.IdempotencyKey)
	if recoveryErr != nil {
		writeStoreError(w, r, recoveryErr)
		return
	}
	if recovered.Batch != nil {
		if !sameStringSlice(recovered.Batch.SegmentIDs, segments) || recovered.Batch.ScoringRunID != input.ScoringRunID {
			writeStoreError(w, r, ErrIdempotencyConflict)
			return
		}
		httpx.JSON(w, http.StatusCreated, map[string]any{"batch": recovered.Batch})
		return
	}
	contexts, err := loadBatchContexts(r.Context(), h.store, user.TenantID, segments)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	for _, ctx := range contexts {
		if !IsSupportedQuestionType(ctx.Question.QuestionType) {
			writeStoreError(w, r, ErrUnsupportedQuestionType)
			return
		}
	}
	batch, err := h.store.CreateBatch(r.Context(), user.TenantID, user.ID, CreateBatchInput{IdempotencyKey: input.IdempotencyKey, SegmentIDs: segments, ScoringRunID: input.ScoringRunID})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "subjective.batch_created", "subjective_grading_batch", batch.ID, "create subjective grading batch")
	httpx.JSON(w, http.StatusCreated, map[string]any{"batch": batch})
}

func (h *Handler) RecoverBatchCommand(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	result, err := h.store.RecoverBatchCommand(r.Context(), user.TenantID, user.ID, r.PathValue("commandId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (h *Handler) GetBatch(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	batch, err := h.store.GetBatch(r.Context(), user.TenantID, r.PathValue("batchId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if batch.Status != "planned" && batch.Status != "cancelled" {
		batch, err = h.store.RefreshBatch(r.Context(), user.TenantID, batch.ID)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"batch": batch})
}

func (h *Handler) ListScoringRunBatches(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	batches, err := h.store.ListScoringRunBatches(r.Context(), user.TenantID, r.PathValue("runId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"batches": batches})
}

func (h *Handler) GetFailedBatchSegments(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	segments, err := h.store.FailedBatchSegments(r.Context(), user.TenantID, r.PathValue("batchId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"segment_ids": segments})
}

// 入队计划先持久化再逐项创建任务；部分失败会保留计划，让客户端重试时沿用同一批运行身份。
func (h *Handler) EnqueueBatch(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.requireAvailable(w, r) {
		return
	}
	if h.runtime == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "subjective_worker_unavailable", "subjective worker runtime is unavailable")
		return
	}
	batch, err := h.store.GetBatch(r.Context(), user.TenantID, r.PathValue("batchId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if batch.Status == "cancelled" {
		writeStoreError(w, r, ErrBatchCancelled)
		return
	}
	plan, err := h.store.GetEnqueuePlan(r.Context(), user.TenantID, user.ID, batch.ID)
	if errors.Is(err, ErrNotFound) {
		plan, err = h.newBatchEnqueuePlan(r.Context(), user.TenantID, batch)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		plan, err = h.store.SaveEnqueuePlan(r.Context(), user.TenantID, user.ID, plan)
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	// 保存时可能取回并发请求的旧计划，后续只使用持久化返回的冻结运行输入。
	tasks, failures, counts := h.enqueueBatchRuns(r.Context(), user.TenantID, user.ID, batch.ID, plan.Runs)
	status := "processing"
	if counts.SucceededCount+counts.FailedCount == batch.TotalCount {
		if counts.FailedCount > 0 {
			status = "failed"
		} else {
			status = "completed"
		}
	}
	counts.Status = status
	updated, err := h.store.UpdateBatch(r.Context(), user.TenantID, batch.ID, counts)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if len(failures) == 0 {
		if err := h.store.CompleteEnqueuePlan(r.Context(), user.TenantID, user.ID, batch.ID); err != nil {
			writeStoreError(w, r, err)
			return
		}
	}
	result := BatchEnqueueResult{
		RequestedCount: len(batch.SegmentIDs),
		AcceptedCount:  len(batch.SegmentIDs) - len(failures),
		TaskCount:      len(tasks),
		FailedCount:    len(failures),
		PartialSuccess: len(failures) > 0 && len(failures) < len(batch.SegmentIDs),
		Failures:       failures,
	}
	auditAction := "subjective.batch_enqueued"
	auditReason := "enqueue subjective grading worker tasks"
	if len(failures) > 0 {
		auditAction = "subjective.batch_enqueue_partial"
		auditReason = "subjective grading batch requires an idempotent enqueue retry"
	}
	h.auditAction(r, auditAction, "subjective_grading_batch", batch.ID, auditReason)
	httpx.JSON(w, http.StatusOK, map[string]any{"batch": updated, "tasks": tasks, "enqueue_result": result})
}

func (h *Handler) newBatchEnqueuePlan(ctx context.Context, tenantID string, batch GradingBatch) (BatchEnqueuePlan, error) {
	policy := NormalizePolicy(ModelPolicy{})
	if governed, ok := h.adapter.(GovernedPolicyProvider); ok {
		policy = governed.Policy()
	}
	if err := ValidatePolicy(policy); err != nil {
		return BatchEnqueuePlan{}, err
	}
	contexts, err := loadBatchContexts(ctx, h.store, tenantID, batch.SegmentIDs)
	if err != nil {
		return BatchEnqueuePlan{}, err
	}
	plan := BatchEnqueuePlan{CommandID: "enqueue:" + batch.ID, BatchID: batch.ID, Runs: make([]CreateRunInput, 0, len(contexts))}
	for index := range contexts {
		value := &contexts[index]
		if err := h.prepareMathEvidence(ctx, tenantID, value); err != nil {
			return BatchEnqueuePlan{}, err
		}
		requestID, err := stableRequestID(tenantID, *value, policy, batch.IdempotencyKey+":"+value.SegmentID)
		if err != nil {
			return BatchEnqueuePlan{}, err
		}
		plan.Runs = append(plan.Runs, runInputFor(*value, batch.ID, policy, requestID))
	}
	return plan, nil
}

// 单项失败只记录可重试项；成功或失败的运行不再创建任务，其他项继续按原计划顺序入队。
func (h *Handler) enqueueBatchRuns(ctx context.Context, tenantID, actorID, batchID string, runs []CreateRunInput) ([]workerruntime.Task, []BatchEnqueueFailure, UpdateBatchInput) {
	tasks := make([]workerruntime.Task, 0, len(runs))
	failures := make([]BatchEnqueueFailure, 0)
	counts := UpdateBatchInput{}
	for _, input := range runs {
		segmentID := input.AnswerSegmentID
		requestID := input.RequestID
		run, runErr := h.store.GetOrCreateRun(ctx, tenantID, actorID, input)
		if runErr != nil {
			failures = append(failures, BatchEnqueueFailure{SegmentID: segmentID, Code: "run_creation_failed"})
			continue
		}
		if run.Status == RunSucceeded {
			counts.SucceededCount++
			continue
		}
		if run.Status == RunFailed {
			counts.FailedCount++
			continue
		}
		task, taskErr := h.runtime.CreateTask(ctx, tenantID, actorID, workerruntime.CreateTaskInput{TaskType: "ai_grade", QueueName: "subjective-grading", SourceType: "subjective_grading_run", SourceID: run.ID, Priority: 100, Payload: map[string]any{"run_id": run.ID, "answer_segment_id": input.AnswerSegmentID, "answer_version": input.AnswerVersion, "question_id": input.QuestionID, "rubric_version": input.RubricVersion, "model_version": input.ModelVersion, "prompt_version": input.PromptVersion}, PayloadSchemaVersion: "subjective-grade-v1", IdempotencyKey: requestID, DedupeKey: "subjective:" + batchID + ":" + input.AnswerSegmentID, MaxAttempts: 3, RetryBackoffSeconds: 30})
		if taskErr != nil {
			failures = append(failures, BatchEnqueueFailure{SegmentID: segmentID, Code: "task_creation_failed"})
			continue
		}
		tasks = append(tasks, task)
		switch task.Status {
		case workerruntime.StatusQueued:
			counts.QueuedCount++
		case workerruntime.StatusLeased, workerruntime.StatusRunning:
			counts.ProcessingCount++
		case workerruntime.StatusSucceeded:
			counts.SucceededCount++
		case workerruntime.StatusFailed, workerruntime.StatusDeadLetter:
			counts.FailedCount++
		}
	}
	return tasks, failures, counts
}

func loadBatchContexts(ctx context.Context, store Store, tenantID string, segmentIDs []string) ([]Context, error) {
	if bulkStore, ok := store.(BatchContextStore); ok {
		return bulkStore.LoadContexts(ctx, tenantID, segmentIDs)
	}
	contexts := make([]Context, 0, len(segmentIDs))
	for _, segmentID := range segmentIDs {
		value, err := store.LoadContext(ctx, tenantID, segmentID)
		if err != nil {
			return nil, err
		}
		contexts = append(contexts, value)
	}
	return contexts, nil
}

func (h *Handler) RecoverEnqueueCommand(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	batchID := r.PathValue("batchId")
	plan, err := h.store.GetEnqueuePlan(r.Context(), user.TenantID, user.ID, batchID)
	status := "processing"
	ids := []string{}
	if errors.Is(err, ErrNotFound) {
		status = "not_accepted"
	} else if err != nil {
		writeStoreError(w, r, err)
		return
	} else {
		if plan.Completed {
			status = "succeeded"
		}
		for _, input := range plan.Runs {
			ids = append(ids, input.RequestID)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"command_id": "enqueue:" + batchID, "batch_id": batchID, "status": status, "run_request_ids": ids})
}
