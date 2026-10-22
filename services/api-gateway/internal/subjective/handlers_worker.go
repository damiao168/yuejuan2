package subjective

import (
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

const (
	subjectiveWorkerUnavailable = "subjective worker runtime is unavailable"
)

func (h *Handler) CompleteWorker(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if h.runtime == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "subjective_worker_unavailable", subjectiveWorkerUnavailable)
		return
	}
	var input WorkerResultInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.resultHash, _ = workerResultHash(input)
	// 成功回调重发只核对原租约和原请求；不得重新把运行改成 processing 或重新结算新版本证据。
	if completed, replay, err := h.workerCompletionService().Replay(r.Context(), user.TenantID, user.ID, r.PathValue("runId"), input); replay || err != nil {
		if err != nil {
			if errors.Is(err, ErrWorkerCompletionRejected) {
				httpx.Error(w, r, http.StatusConflict, "subjective_task_completion_rejected", "subjective worker lease or result is no longer valid")
			} else {
				writeStoreError(w, r, err)
			}
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"run": completed.Run, "grade": completed.Grade, "task": completed.Task})
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
	if err := h.bindWorkerImageGradeModel(r.Context(), user.TenantID, &prepared.Context, prepared.Policy); err != nil {
		h.writeWorkerPrepareError(w, r, err)
		return
	}
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
			h.writeWorkerSettlementFailure(w, r, user.TenantID, prepared, input, runStatus, code, status, settleErr)
			return
		}
		if errors.Is(settleErr, ErrGradeCalibration) {
			writeStoreError(w, r, settleErr)
			return
		}
		h.writeWorkerSettlementFailure(w, r, user.TenantID, prepared, input, RunFailed, "invalid_model_output", http.StatusBadRequest, settleErr)
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
		httpx.Error(w, r, http.StatusServiceUnavailable, "subjective_worker_unavailable", subjectiveWorkerUnavailable)
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
			h.writeWorkerSettlementFailure(w, r, user.TenantID, prepared, WorkerResultInput{TaskID: input.TaskID, LeaseToken: input.LeaseToken}, RunConflict, "math_evidence_version_conflict", http.StatusConflict, err)
			return
		}
		h.writeWorkerSettlementFailure(w, r, user.TenantID, prepared, WorkerResultInput{TaskID: input.TaskID, LeaseToken: input.LeaseToken}, RunFailed, "math_evidence_unavailable", http.StatusUnprocessableEntity, err)
		return
	}
	var output AdapterOutput
	if prepared.Context.InputVersionID != "" && prepared.Context.AIPlan != nil {
		output, err = h.executeImagePlan(r.Context(), user.TenantID, prepared, adapterInput)
	} else {
		output, err = activeAdapter.Grade(r.Context(), adapterInput)
	}
	output.RequestID = prepared.Run.RequestID
	if err != nil {
		agentErr := &GradingAgentError{Code: "adapter_error", ExecutionDiagnostics: diagnosticsFromOutput(output, "")}
		_ = errors.As(err, &agentErr)
		code, _ := agentFailureHTTP(agentErr)
		failureDetail := executionFailureDetail(agentErr.ExecutionDiagnostics)
		if output.RawOutput["panel_execution"] != nil {
			if failureDetail == nil {
				failureDetail = map[string]any{}
			}
			failureDetail["panel_execution"] = output.RawOutput["panel_execution"]
		}
		// Execute 已持有租约；失败事实必须与租约关闭一同提交，再通知 worker 避免重复 Fail。
		_, failErr := h.workerCompletionService().Fail(r.Context(), user.TenantID, prepared.Run.ID, WorkerFailureInput{
			TaskID: input.TaskID, LeaseToken: input.LeaseToken, ErrorCode: code, Retryable: agentErrorRetryable(err), ErrorDetail: failureDetail})
		if failErr != nil {
			writeStoreError(w, r, failErr)
			return
		}
		writeAgentFailureResponse(w, r, agentErr, true)
		return
	}
	resultSchemaVersion := "subjective-grade-v1"
	if usedMathV2 {
		resultSchemaVersion = "math-grade-v2"
		if settleErr := h.settleMathOutput(r.Context(), user.TenantID, prepared.Context, prepared.Policy, &output); settleErr != nil {
			if errors.Is(settleErr, ErrMathHumanReviewRequired) {
				h.writeWorkerSettlementFailure(w, r, user.TenantID, prepared, WorkerResultInput{TaskID: input.TaskID, LeaseToken: input.LeaseToken, Output: output}, RunFailed, "math_human_review_required", http.StatusUnprocessableEntity, settleErr)
				return
			}
			code := "invalid_model_output"
			status := http.StatusBadRequest
			runStatus := RunFailed
			if isMathRevisionConflict(settleErr) {
				code, status, runStatus = "math_evidence_version_conflict", http.StatusConflict, RunConflict
			}
			h.writeWorkerSettlementFailure(w, r, user.TenantID, prepared, WorkerResultInput{TaskID: input.TaskID, LeaseToken: input.LeaseToken, Output: output}, runStatus, code, status, settleErr)
			return
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"run_id": prepared.Run.ID, "task_id": prepared.Task.ID, "result_schema_version": resultSchemaVersion, "output": output})
}

// 原子拒绝成功后才标记 task_failure_recorded，worker 才能跳过自己的失败回调。
// 若租约过期或事务失败，返回存储错误；不能让调用方把未提交的失败当成已经关闭任务。
func (h *Handler) writeWorkerSettlementFailure(w http.ResponseWriter, r *http.Request, tenantID string, prepared WorkerExecutionContext, input WorkerResultInput, runStatus, code string, status int, cause error) {
	if err := h.workerCompletionService().Reject(r.Context(), tenantID, prepared, input, runStatus, code, cause); err != nil {
		writeStoreError(w, r, err)
		return
	}
	payload := map[string]any{}
	if code == "math_human_review_required" || code == "math_evidence_unavailable" {
		payload = mathReviewPayload(prepared.Context, input.Output)
	}
	kind := "schema_invalid"
	if errors.Is(cause, ErrMathHumanReviewRequired) || isMathRevisionConflict(cause) {
		kind = "evidence_invalid"
	}
	payload["error"] = httpx.ErrorBody{Code: code, Message: "AI 建议未完成，请继续人工评分"}
	payload["execution_diagnostics"] = diagnosticsFromOutput(input.Output, kind)
	payload["task_failure_recorded"] = true
	payload["retryable"] = false
	w.Header().Set(httpx.ErrorCodeHeader, code)
	httpx.JSON(w, status, payload)
}

func (h *Handler) FailWorker(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if h.runtime == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "subjective_worker_unavailable", subjectiveWorkerUnavailable)
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
