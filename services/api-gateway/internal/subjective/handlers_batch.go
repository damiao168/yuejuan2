package subjective

import (
	"context"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

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
		if !sameStringSlice(recovered.Batch.SegmentIDs, segments) {
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
	batch, err := h.store.CreateBatch(r.Context(), user.TenantID, user.ID, CreateBatchInput{IdempotencyKey: input.IdempotencyKey, SegmentIDs: segments})
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
	plan, err := h.store.GetEnqueuePlan(r.Context(), user.TenantID, user.ID, batch.ID)
	if errors.Is(err, ErrNotFound) {
		policy := NormalizePolicy(ModelPolicy{})
		if governed, ok := h.adapter.(GovernedPolicyProvider); ok {
			policy = governed.Policy()
		}
		if err := ValidatePolicy(policy); err != nil {
			writeStoreError(w, r, err)
			return
		}
		contexts, loadErr := loadBatchContexts(r.Context(), h.store, user.TenantID, batch.SegmentIDs)
		if loadErr != nil {
			writeStoreError(w, r, loadErr)
			return
		}
		plan = BatchEnqueuePlan{CommandID: "enqueue:" + batch.ID, BatchID: batch.ID, Runs: make([]CreateRunInput, 0, len(contexts))}
		for index := range contexts {
			ctx := &contexts[index]
			if prepErr := h.prepareMathEvidence(r.Context(), user.TenantID, ctx); prepErr != nil {
				writeStoreError(w, r, prepErr)
				return
			}
			requestID, idErr := stableRequestID(user.TenantID, *ctx, policy, batch.IdempotencyKey+":"+ctx.SegmentID)
			if idErr != nil {
				writeStoreError(w, r, idErr)
				return
			}
			plan.Runs = append(plan.Runs, runInputFor(*ctx, batch.ID, policy, requestID))
		}
		plan, err = h.store.SaveEnqueuePlan(r.Context(), user.TenantID, user.ID, plan)
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	tasks := make([]workerruntime.Task, 0, len(plan.Runs))
	failures := make([]BatchEnqueueFailure, 0)
	queued, processing, succeeded, failed := 0, 0, 0, 0
	for _, input := range plan.Runs {
		segmentID := input.AnswerSegmentID
		requestID := input.RequestID
		run, runErr := h.store.GetOrCreateRun(r.Context(), user.TenantID, user.ID, input)
		if runErr != nil {
			failures = append(failures, BatchEnqueueFailure{SegmentID: segmentID, Code: "run_creation_failed"})
			continue
		}
		if run.Status == RunSucceeded {
			succeeded++
			continue
		}
		if run.Status == RunFailed {
			failed++
			continue
		}
		task, taskErr := h.runtime.CreateTask(r.Context(), user.TenantID, user.ID, workerruntime.CreateTaskInput{TaskType: "ai_grade", QueueName: "subjective-grading", SourceType: "subjective_grading_run", SourceID: run.ID, Priority: 100, Payload: map[string]any{"run_id": run.ID, "answer_segment_id": input.AnswerSegmentID, "answer_version": input.AnswerVersion, "question_id": input.QuestionID, "rubric_version": input.RubricVersion, "model_version": input.ModelVersion, "prompt_version": input.PromptVersion}, PayloadSchemaVersion: "subjective-grade-v1", IdempotencyKey: requestID, DedupeKey: "subjective:" + batch.ID + ":" + input.AnswerSegmentID, MaxAttempts: 3, RetryBackoffSeconds: 30})
		if taskErr != nil {
			failures = append(failures, BatchEnqueueFailure{SegmentID: segmentID, Code: "task_creation_failed"})
			continue
		}
		tasks = append(tasks, task)
		switch task.Status {
		case workerruntime.StatusQueued:
			queued++
		case workerruntime.StatusLeased, workerruntime.StatusRunning:
			processing++
		case workerruntime.StatusSucceeded:
			succeeded++
		case workerruntime.StatusFailed, workerruntime.StatusDeadLetter:
			failed++
		}
	}
	status := "processing"
	if succeeded+failed == batch.TotalCount {
		if failed > 0 {
			status = "failed"
		} else {
			status = "completed"
		}
	}
	updated, err := h.store.UpdateBatch(r.Context(), user.TenantID, batch.ID, UpdateBatchInput{Status: status, QueuedCount: queued, ProcessingCount: processing, SucceededCount: succeeded, FailedCount: failed})
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
