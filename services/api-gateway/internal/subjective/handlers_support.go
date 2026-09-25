package subjective

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

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
	case errors.Is(err, ErrBatchCancelled):
		httpx.Error(w, r, http.StatusConflict, "subjective_grading_batch_cancelled", "subjective grading batch was cancelled")
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
