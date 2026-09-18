package subjective

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/modelcalibration"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type Handler struct {
	store         Store
	adapter       LLMGradingAdapter
	audit         auth.Store
	runtime       workerruntime.Store
	eligibility   EligibilityGate
	evaluation    EvaluationEvidenceProvider
	calibration   CalibrationEvidenceProvider
	parserQuality ParserQualityProvider
	mathV2Enabled bool
	mathAdapter   LLMGradingAdapter
	mathEvidence  MathEvidenceSource
	activeCrops   *ActiveCropResolver
}

func NewHandler(store Store, adapter LLMGradingAdapter, audit auth.Store) *Handler {
	return &Handler{store: store, adapter: adapter, audit: audit}
}

func (h *Handler) WithWorkerRuntimeStore(runtime workerruntime.Store) *Handler {
	h.runtime = runtime
	return h
}

func (h *Handler) WithEligibilityGate(gate EligibilityGate) *Handler {
	h.eligibility = gate
	return h
}

// EvaluationEvidenceProvider supplies only the aggregate, aligned offline
// evidence needed by A14. It cannot expose answers or make a model eligible
// by itself; the admission policy remains the final gate.
type EvaluationEvidenceProvider interface {
	AdmissionEvidenceFor(context.Context, string, string, string, string, assessment.SubjectCode, string) (gradingevaluation.AdmissionEvidence, error)
}

func (h *Handler) WithEvaluationEvidence(provider EvaluationEvidenceProvider) *Handler {
	h.evaluation = provider
	return h
}

type CalibrationEvidenceProvider interface {
	Approved(context.Context, string, modelcalibration.Axis) (modelcalibration.ApprovedEvidence, error)
	RecordCandidate(context.Context, string, modelcalibration.RecordCandidateInput) (modelcalibration.Candidate, error)
}

func (h *Handler) WithCalibrationEvidence(provider CalibrationEvidenceProvider) *Handler {
	h.calibration = provider
	return h
}

// ParserQualityProvider supplies specialised parser quality only for the
// answer segment being considered. It must return nil when that evidence is
// absent so the AI admission policy can abstain rather than treat OCR text
// quality as a mathematical, chemical, diagram, or table parse.
type ParserQualityProvider interface {
	ParserQualityForSegment(context.Context, string, string, assessment.SubjectCode, string) (*float64, error)
}

func (h *Handler) WithParserQuality(provider ParserQualityProvider) *Handler {
	h.parserQuality = provider
	return h
}

func (h *Handler) workerExecutionService() *WorkerExecutionService {
	return NewWorkerExecutionService(h.store, h.runtime, h.prepareMathEvidence, h.decideEligibility)
}

func (h *Handler) gradeSettlementPipeline() *GradeSettlementPipeline {
	return NewGradeSettlementPipeline(h.settleMathOutput, h.recordCalibrationCandidate)
}

func (h *Handler) workerCompletionService() *WorkerCompletionService {
	return NewWorkerCompletionService(h.store, h.runtime)
}

func (h *Handler) writeWorkerPrepareError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrWorkerTaskMismatch):
		httpx.Error(w, r, http.StatusConflict, "subjective_task_mismatch", "worker task does not belong to this subjective grading run")
	case errors.Is(err, ErrWorkerLeaseMismatch):
		httpx.Error(w, r, http.StatusConflict, "subjective_task_lease_mismatch", "subjective worker lease is missing, expired, or no longer active")
	case errors.Is(err, ErrWorkerResultVersionConflict):
		var versionErr *WorkerResultVersionError
		if errors.As(err, &versionErr) && versionErr.MathSchema {
			httpx.Error(w, r, http.StatusConflict, "subjective_result_version_conflict", "math worker result must use the server-settled v2 schema")
			return
		}
		httpx.Error(w, r, http.StatusConflict, "subjective_result_version_conflict", "worker result does not match the requested grading versions")
	case errors.Is(err, ErrWorkerMathBindingConflict):
		httpx.Error(w, r, http.StatusConflict, "math_evidence_version_conflict", "math evidence no longer matches this grading run")
	case errors.Is(err, ErrAIEligibilityAbstained):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "ai_eligibility_abstained", "AI grading is not admitted for this frozen question context")
	default:
		writeStoreError(w, r, err)
	}
}

func (h *Handler) Grade(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.requireAvailable(w, r) {
		return
	}
	var input GradeRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		writeStoreError(w, r, err)
		return
	}
	policy := NormalizePolicy(input.ModelPolicy)
	if governed, ok := h.adapter.(GovernedPolicyProvider); ok {
		policy = governed.Policy()
	}
	if err := ValidatePolicy(policy); err != nil {
		writeStoreError(w, r, err)
		return
	}
	ctx, err := h.store.LoadContext(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if err := h.prepareMathEvidence(r.Context(), user.TenantID, &ctx); err != nil {
		writeStoreError(w, r, err)
		return
	}
	if !IsSupportedQuestionType(ctx.Question.QuestionType) {
		writeStoreError(w, r, ErrUnsupportedQuestionType)
		return
	}
	requestID, err := stableRequestID(user.TenantID, ctx, policy, input.IdempotencyKey)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if input.IdempotencyKey != "" {
		if existing, lookupErr := h.store.GetGradeByAdapterRequestID(r.Context(), user.TenantID, requestID); lookupErr == nil {
			httpx.JSON(w, http.StatusOK, map[string]any{"grade": existing, "idempotent_replay": true})
			return
		} else if !errors.Is(lookupErr, ErrNotFound) {
			writeStoreError(w, r, lookupErr)
			return
		}
	}
	runID := ""
	if run, runErr := h.store.GetOrCreateRun(r.Context(), user.TenantID, user.ID, runInputFor(ctx, "", policy, requestID)); runErr != nil {
		writeStoreError(w, r, runErr)
		return
	} else {
		runID = run.ID
	}
	finishRun := func(status string, grade Grade, errorCode string) error {
		_, updateErr := h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: status, GradeID: grade.ID, ErrorCode: errorCode})
		return updateErr
	}
	decision, allowed, decisionErr := h.decideEligibility(r.Context(), user.TenantID, runID, ctx, policy)
	if decisionErr != nil {
		writeStoreError(w, r, decisionErr)
		return
	}
	if !allowed {
		grade, createErr := h.store.CreateGrade(r.Context(), user.TenantID, user.ID, failedGrade(ctx, policy, runID, "ai_eligibility_abstained", eligibilityAbstentionOutput(policy, decision)))
		if createErr != nil {
			writeStoreError(w, r, createErr)
			return
		}
		if updateErr := finishRun(RunFailed, grade, "ai_eligibility_abstained"); updateErr != nil {
			writeStoreError(w, r, updateErr)
			return
		}
		h.auditAction(r, "subjective.ai_eligibility_abstained", "subjective_grading_run", runID, "external AI was not admitted")
		httpx.JSON(w, http.StatusCreated, map[string]any{"grade": grade, "eligibility_decision": decision})
		return
	}
	promptGuard := InspectPromptInjection(ctx.AnswerText)
	adapterInput, activeAdapter, usedMathV2, err := h.buildAdapterInput(r.Context(), user.TenantID, requestID, ctx, policy, promptGuard, decision.OutputConstraint)
	if err != nil {
		if isMathRevisionConflict(err) {
			_, _ = h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: RunConflict, ErrorCode: "math_evidence_version_conflict"})
			writeStoreError(w, r, err)
			return
		}
		_, _ = h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: RunFailed, ErrorCode: "math_evidence_unavailable"})
		h.auditAction(r, "subjective.math_human_review_required", "subjective_grading_run", runID, "math evidence or active crop is unavailable")
		httpx.JSON(w, http.StatusUnprocessableEntity, mathReviewPayload(ctx, AdapterOutput{}))
		return
	}
	output, err := activeAdapter.Grade(r.Context(), adapterInput)
	output.RequestID = requestID
	if err != nil {
		var agentErr *GradingAgentError
		if errors.As(err, &agentErr) {
			code := "ai_service_unavailable"
			if agentErr.Code == "adapter_not_configured" {
				code = "ai_service_not_configured"
			}
			if _, updateErr := h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: RunFailed, ErrorCode: code}); updateErr != nil {
				writeStoreError(w, r, updateErr)
				return
			}
			h.auditAction(r, "subjective.ai_service_unavailable", "subjective_grading_run", runID, code)
			httpx.Error(w, r, http.StatusServiceUnavailable, code, "AI grading service is unavailable; use manual review")
			return
		}
		ApplyPromptGuard(&output, promptGuard)
		grade, createErr := h.store.CreateGrade(r.Context(), user.TenantID, user.ID, failedGrade(ctx, policy, runID, err.Error(), output))
		if createErr != nil {
			writeStoreError(w, r, createErr)
			return
		}
		if updateErr := finishRun(RunFailed, grade, "adapter_failed"); updateErr != nil {
			writeStoreError(w, r, updateErr)
			return
		}
		h.auditAction(r, "subjective.ai_grade_failed", "ai_grade", grade.ID, "subjective adapter failed")
		httpx.JSON(w, http.StatusCreated, map[string]any{"grade": grade})
		return
	}
	settlement := GradeSettlementInput{TenantID: user.TenantID, RunID: runID, Context: ctx, Policy: policy, Decision: decision, MathRun: usedMathV2}
	if settleErr := h.gradeSettlementPipeline().Settle(r.Context(), settlement, &output); settleErr != nil {
		if usedMathV2 {
			if errors.Is(settleErr, ErrMathHumanReviewRequired) {
				_, _ = h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: RunFailed, ErrorCode: "math_human_review_required"})
				h.auditAction(r, "subjective.math_human_review_required", "subjective_grading_run", runID, "math scoring remained unresolved")
				httpx.JSON(w, http.StatusUnprocessableEntity, mathReviewPayload(ctx, output))
				return
			}
			if isMathRevisionConflict(settleErr) {
				_, _ = h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: RunConflict, ErrorCode: "math_evidence_version_conflict"})
				httpx.Error(w, r, http.StatusConflict, "math_evidence_version_conflict", "math evidence changed during grading")
				return
			}
			_, _ = h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: RunFailed, ErrorCode: "invalid_model_output"})
			writeStoreError(w, r, settleErr)
			return
		}
		if errors.Is(settleErr, ErrGradeCalibration) {
			writeStoreError(w, r, settleErr)
			return
		}
		grade, createErr := h.store.CreateGrade(r.Context(), user.TenantID, user.ID, failedGrade(ctx, policy, runID, settleErr.Error(), output))
		if createErr != nil {
			writeStoreError(w, r, createErr)
			return
		}
		if updateErr := finishRun(RunFailed, grade, "invalid_model_output"); updateErr != nil {
			writeStoreError(w, r, updateErr)
			return
		}
		h.auditAction(r, "subjective.ai_grade_failed", "ai_grade", grade.ID, "subjective adapter output failed schema validation")
		httpx.JSON(w, http.StatusCreated, map[string]any{"grade": grade})
		return
	}
	grade, err := h.store.CreateGrade(r.Context(), user.TenantID, user.ID, successfulGrade(ctx, policy, runID, output))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if updateErr := finishRun(RunSucceeded, grade, ""); updateErr != nil {
		writeStoreError(w, r, updateErr)
		return
	}
	h.auditAction(r, "subjective.ai_grade_created", "ai_grade", grade.ID, "create subjective ai grade")
	httpx.JSON(w, http.StatusCreated, map[string]any{"grade": grade})
}

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

func (h *Handler) CompleteWorker(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if h.runtime == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "subjective_worker_unavailable", "subjective worker runtime is unavailable")
		return
	}
	var input WorkerResultInput
	if !decodeJSON(w, r, &input) {
		return
	}
	prepared, err := h.workerExecutionService().Prepare(r.Context(), WorkerPrepareInput{
		TenantID: user.TenantID, RunID: r.PathValue("runId"), TaskID: input.TaskID,
		LeaseToken: input.LeaseToken, DurationMS: input.DurationMS, Result: &input,
	})
	if err != nil {
		h.writeWorkerPrepareError(w, r, err)
		return
	}
	output := input.Output
	settlement := GradeSettlementInput{TenantID: user.TenantID, RunID: prepared.Run.ID, Context: prepared.Context, Policy: prepared.Policy, Decision: prepared.Decision, MathRun: prepared.MathRun}
	if settleErr := h.gradeSettlementPipeline().Settle(r.Context(), settlement, &output); settleErr != nil {
		if prepared.MathRun {
			code := "invalid_model_output"
			status := http.StatusBadRequest
			runStatus := RunFailed
			if errors.Is(settleErr, ErrMathHumanReviewRequired) {
				code, status = "math_human_review_required", http.StatusUnprocessableEntity
			}
			if isMathRevisionConflict(settleErr) {
				code, status = "math_evidence_version_conflict", http.StatusConflict
				runStatus = RunConflict
			}
			h.workerCompletionService().Reject(r.Context(), user.TenantID, prepared, input, runStatus, code, settleErr)
			if code == "math_human_review_required" {
				httpx.JSON(w, status, mathReviewPayload(prepared.Context, output))
			} else {
				httpx.Error(w, r, status, code, "math worker result could not be settled")
			}
			return
		}
		if errors.Is(settleErr, ErrGradeCalibration) {
			writeStoreError(w, r, settleErr)
			return
		}
		h.workerCompletionService().Reject(r.Context(), user.TenantID, prepared, input, RunFailed, "invalid_model_output", settleErr)
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_output", "worker result failed schema validation")
		return
	}
	completed, err := h.workerCompletionService().Complete(r.Context(), user.TenantID, user.ID, prepared, input, output)
	if err != nil {
		if errors.Is(err, ErrWorkerCompletionRejected) {
			httpx.Error(w, r, http.StatusConflict, "subjective_task_completion_rejected", "subjective worker lease or result is no longer valid")
		} else {
			writeStoreError(w, r, err)
		}
		return
	}
	h.auditAction(r, "subjective.worker_completed", "subjective_grading_run", prepared.Run.ID, "complete subjective worker result")
	httpx.JSON(w, http.StatusOK, map[string]any{"run": completed.Run, "grade": completed.Grade, "task": completed.Task})
}

func (h *Handler) ExecuteWorker(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.requireAvailable(w, r) {
		return
	}
	if h.runtime == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "subjective_worker_unavailable", "subjective worker runtime is unavailable")
		return
	}
	var input struct {
		TaskID     string `json:"task_id"`
		LeaseToken string `json:"lease_token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	prepared, err := h.workerExecutionService().Prepare(r.Context(), WorkerPrepareInput{TenantID: user.TenantID, RunID: r.PathValue("runId"), TaskID: input.TaskID, LeaseToken: input.LeaseToken})
	if err != nil {
		h.writeWorkerPrepareError(w, r, err)
		return
	}
	promptGuard := InspectPromptInjection(prepared.Context.AnswerText)
	adapterInput, activeAdapter, usedMathV2, err := h.buildAdapterInput(r.Context(), user.TenantID, prepared.Run.RequestID, prepared.Context, prepared.Policy, promptGuard, prepared.Decision.OutputConstraint)
	if err != nil {
		if isMathRevisionConflict(err) {
			h.workerExecutionService().failMathEvidence(r.Context(), WorkerPrepareInput{TenantID: user.TenantID, TaskID: input.TaskID, LeaseToken: input.LeaseToken}, prepared.Run, prepared.Task, err)
			writeStoreError(w, r, err)
			return
		}
		_, _ = h.runtime.Fail(r.Context(), user.TenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: false, ErrorCode: "math_evidence_unavailable"})
		_, _ = h.store.UpdateRun(r.Context(), user.TenantID, prepared.Run.ID, UpdateRunInput{Status: RunFailed, ErrorCode: "math_evidence_unavailable", AttemptCount: prepared.Task.AttemptCount})
		httpx.JSON(w, http.StatusUnprocessableEntity, mathReviewPayload(prepared.Context, AdapterOutput{}))
		return
	}
	output, err := activeAdapter.Grade(r.Context(), adapterInput)
	output.RequestID = prepared.Run.RequestID
	if err != nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "ai_service_unavailable", "AI grading service is unavailable; use manual review")
		return
	}
	resultSchemaVersion := "subjective-grade-v1"
	if usedMathV2 {
		resultSchemaVersion = "math-grade-v2"
		if settleErr := h.settleMathOutput(r.Context(), user.TenantID, prepared.Context, prepared.Policy, &output); settleErr != nil {
			if errors.Is(settleErr, ErrMathHumanReviewRequired) {
				_, _ = h.runtime.Fail(r.Context(), user.TenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: false, ErrorCode: "math_human_review_required"})
				_, _ = h.store.UpdateRun(r.Context(), user.TenantID, prepared.Run.ID, UpdateRunInput{Status: RunFailed, ErrorCode: "math_human_review_required", AttemptCount: prepared.Task.AttemptCount})
				httpx.JSON(w, http.StatusUnprocessableEntity, mathReviewPayload(prepared.Context, output))
				return
			}
			code := "invalid_model_output"
			status := http.StatusBadRequest
			runStatus := RunFailed
			message := "math model candidates could not be settled"
			if isMathRevisionConflict(settleErr) {
				code, status, runStatus = "math_evidence_version_conflict", http.StatusConflict, RunConflict
				message = "math evidence changed during grading"
			}
			_, _ = h.runtime.Fail(r.Context(), user.TenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: false, ErrorCode: code})
			_, _ = h.store.UpdateRun(r.Context(), user.TenantID, prepared.Run.ID, UpdateRunInput{Status: runStatus, ErrorCode: code, AttemptCount: prepared.Task.AttemptCount})
			httpx.Error(w, r, status, code, message)
			return
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"run_id": prepared.Run.ID, "task_id": prepared.Task.ID, "result_schema_version": resultSchemaVersion, "output": output})
}

func (h *Handler) FailWorker(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if h.runtime == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "subjective_worker_unavailable", "subjective worker runtime is unavailable")
		return
	}
	var input WorkerFailureInput
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := h.workerCompletionService().Fail(r.Context(), user.TenantID, r.PathValue("runId"), input)
	if err != nil {
		if errors.Is(err, ErrWorkerTaskMismatch) {
			httpx.Error(w, r, http.StatusConflict, "subjective_task_mismatch", "worker task does not belong to this subjective grading run")
		} else if errors.Is(err, ErrWorkerFailureRejected) {
			httpx.Error(w, r, http.StatusConflict, "subjective_task_failure_rejected", "subjective worker lease or failure is no longer valid")
		} else {
			writeStoreError(w, r, err)
		}
		return
	}
	h.auditAction(r, "subjective.worker_failed", "subjective_grading_run", result.Run.ID, input.ErrorCode)
	httpx.JSON(w, http.StatusOK, map[string]any{"run": result.Run, "task": result.Task})
}

func successfulGrade(ctx Context, policy ModelPolicy, runID string, output AdapterOutput) Grade {
	graderType := LLMGraderType
	if output.Mock {
		graderType = MockGraderType
	}
	grade := Grade{
		AnswerSegmentID:        ctx.SegmentID,
		QuestionID:             ctx.Question.ID,
		QuestionNo:             ctx.Question.QuestionNo,
		QuestionType:           ctx.Question.QuestionType,
		AnswerVersion:          ctx.AnswerVersion,
		GraderType:             graderType,
		ModelVersion:           firstNonEmpty(output.ModelVersion, policy.ModelVersion),
		PromptVersion:          firstNonEmpty(output.PromptVersion, policy.PromptVersion),
		RubricVersion:          firstNonEmpty(output.RubricVersion, ctx.Rubric.Version),
		DeliveryMode:           firstNonEmpty(output.DeliveryMode, "teacher_review"),
		CapabilityProfile:      output.CapabilityProfile,
		AdapterRequestID:       output.RequestID,
		RunID:                  runID,
		AdapterName:            output.Telemetry.Adapter,
		ProviderKey:            output.Telemetry.Provider,
		DeploymentKey:          output.Telemetry.Deployment,
		DeploymentRegion:       output.Telemetry.Region,
		AdapterAttempts:        output.Telemetry.Attempts,
		AdapterLatencyMS:       output.Telemetry.ElapsedMS,
		AdapterRepairAttempted: output.Telemetry.RepairAttempted,
		SuggestedScore:         output.SuggestedScore,
		MaxScore:               ctx.Question.Score,
		Confidence:             output.Confidence,
		MatchedPoints:          output.MatchedPoints,
		MissingPoints:          output.MissingPoints,
		Evidence:               output.Evidence,
		RiskFlags:              output.RiskFlags,
		NeedsHumanReview:       output.NeedsHumanReview,
		StudentFeedback:        output.StudentFeedback,
		TeacherNote:            output.TeacherNote,
		Mock:                   output.Mock,
		Status:                 "succeeded",
		RawOutput:              output.RawOutput,
	}
	if ctx.MathEvidence != nil {
		grade.MathArtifactID = ctx.MathEvidence.ArtifactID
		grade.MathArtifactVersion = ctx.MathEvidence.ArtifactVersion
		grade.MathCorrectionRevision = ctx.MathEvidence.CorrectionRevision
		grade.MathScoringVersion = ctx.MathEvidence.ScoringVersion
	}
	return grade
}

func failedGrade(ctx Context, policy ModelPolicy, runID string, reason string, output AdapterOutput) Grade {
	riskFlags := appendFlag(output.RiskFlags, "invalid_model_output")
	if output.Mock {
		riskFlags = appendFlag(riskFlags, "mock_llm_output")
	}
	graderType := LLMGraderType
	if output.Mock {
		graderType = MockGraderType
	}
	raw := output.RawOutput
	if raw == nil {
		raw = map[string]any{}
	}
	raw["failure_reason"] = reason
	grade := Grade{
		AnswerSegmentID:        ctx.SegmentID,
		QuestionID:             ctx.Question.ID,
		QuestionNo:             ctx.Question.QuestionNo,
		QuestionType:           ctx.Question.QuestionType,
		AnswerVersion:          ctx.AnswerVersion,
		GraderType:             graderType,
		ModelVersion:           firstNonEmpty(output.ModelVersion, policy.ModelVersion),
		PromptVersion:          firstNonEmpty(output.PromptVersion, policy.PromptVersion),
		RubricVersion:          firstNonEmpty(output.RubricVersion, ctx.Rubric.Version),
		DeliveryMode:           "teacher_review",
		CapabilityProfile:      output.CapabilityProfile,
		AdapterRequestID:       output.RequestID,
		RunID:                  runID,
		AdapterName:            output.Telemetry.Adapter,
		ProviderKey:            output.Telemetry.Provider,
		DeploymentKey:          output.Telemetry.Deployment,
		DeploymentRegion:       output.Telemetry.Region,
		AdapterAttempts:        output.Telemetry.Attempts,
		AdapterLatencyMS:       output.Telemetry.ElapsedMS,
		AdapterRepairAttempted: output.Telemetry.RepairAttempted,
		SuggestedScore:         0,
		MaxScore:               ctx.Question.Score,
		Confidence:             0,
		MatchedPoints:          []grading.PointResult{},
		MissingPoints:          []grading.PointResult{},
		Evidence:               []grading.Evidence{},
		RiskFlags:              riskFlags,
		NeedsHumanReview:       true,
		StudentFeedback:        "",
		TeacherNote:            "Adapter output failed schema validation; no valid subjective score was produced.",
		Mock:                   output.Mock,
		Status:                 "failed",
		FailureReason:          reason,
		RawOutput:              raw,
	}
	if ctx.MathEvidence != nil {
		grade.MathArtifactID = ctx.MathEvidence.ArtifactID
		grade.MathArtifactVersion = ctx.MathEvidence.ArtifactVersion
		grade.MathCorrectionRevision = ctx.MathEvidence.CorrectionRevision
		grade.MathScoringVersion = ctx.MathEvidence.ScoringVersion
	}
	return grade
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (h *Handler) Availability(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"ai_grading": h.runtimeStatus()})
}

func (h *Handler) runtimeStatus() RuntimeStatus {
	if provider, ok := h.adapter.(RuntimeStatusProvider); ok {
		return provider.RuntimeStatus()
	}
	return RuntimeStatus{Enabled: true, Available: true, Mode: "custom"}
}

func (h *Handler) requireAvailable(w http.ResponseWriter, r *http.Request) bool {
	status := h.runtimeStatus()
	if status.Available {
		return true
	}
	code := status.ErrorCode
	if code == "" {
		code = "ai_service_unavailable"
	}
	message := "AI grading service is unavailable; manual review remains available"
	if code == "ai_grading_disabled" {
		message = "AI grading is disabled; use manual review"
	}
	httpx.Error(w, r, http.StatusServiceUnavailable, code, message)
	return false
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		return true
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case isMathRevisionConflict(err):
		httpx.Error(w, r, http.StatusConflict, "math_evidence_version_conflict", "math evidence no longer matches the current crop or artifact revision")
	case errors.Is(err, ErrActiveCropUnavailable):
		httpx.JSON(w, http.StatusUnprocessableEntity, mathReviewPayload(Context{}, AdapterOutput{}))
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "subjective_grading_resource_not_found", "subjective grading resource not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_subjective_grading_input", "subjective grading input is invalid")
	case errors.Is(err, ErrUnsupportedQuestionType):
		httpx.Error(w, r, http.StatusBadRequest, "unsupported_subjective_question_type", "question type is not supported by subjective ai grading")
	case errors.Is(err, ErrAnswerMissing):
		httpx.Error(w, r, http.StatusConflict, "answer_segment_answer_missing", "answer segment has no recorded answer")
	case errors.Is(err, ErrRubricMissing):
		httpx.Error(w, r, http.StatusConflict, "question_rubric_missing", "question has no rubric for subjective grading")
	case errors.Is(err, ErrInvalidModelOutput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_output", "model output failed schema validation")
	case errors.Is(err, ErrIdempotencyConflict):
		httpx.Error(w, r, http.StatusConflict, "subjective_grade_idempotency_conflict", "same idempotency request produced a different grading fact")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "subjective_grading_operation_failed", "subjective grading operation failed")
	}
}

func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func (h *Handler) auditAction(r *http.Request, action string, targetType string, targetID string, reason string) {
	user := mustUser(r)
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID:   user.TenantID,
		ActorID:    user.ID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Reason:     reason,
		IPAddress:  r.RemoteAddr,
		UserAgent:  r.UserAgent(),
		RequestID:  logger.RequestID(r.Context()),
	})
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
