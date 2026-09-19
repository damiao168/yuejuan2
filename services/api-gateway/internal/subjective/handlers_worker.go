package subjective

import (
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

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
