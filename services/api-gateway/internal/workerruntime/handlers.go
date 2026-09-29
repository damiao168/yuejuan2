package workerruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type Handler struct {
	store              Store
	audit              auth.AuditRecorder
	sourceLeaseRenewer SourceLeaseRenewer
}

type SourceLeaseRenewer interface {
	RenewSourceLease(ctx context.Context, task Task) error
}

func NewHandler(store Store, audit auth.AuditRecorder, renewers ...SourceLeaseRenewer) *Handler {
	handler := &Handler{store: store, audit: audit}
	if len(renewers) > 0 {
		handler.sourceLeaseRenewer = renewers[0]
	}
	return handler
}

func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	user := runtimeUser(r)
	var input CreateTaskInput
	if !decodeRuntimeJSON(w, r, &input) {
		return
	}
	task, err := h.store.CreateTask(r.Context(), user.TenantID, user.ID, input)
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	h.auditTask(r, "worker.task_created", task, "create worker runtime task")
	httpx.JSON(w, http.StatusCreated, map[string]any{"task": task})
}

func (h *Handler) Claim(w http.ResponseWriter, r *http.Request) {
	user := runtimeUser(r)
	var input ClaimInput
	if !decodeRuntimeJSON(w, r, &input) {
		return
	}
	if !workerIdentityAllows(user, input.QueueName, input.WorkerService) {
		httpx.Error(w, r, http.StatusForbidden, "worker_service_forbidden", "authenticated worker service cannot claim the requested queue")
		return
	}
	var tasks []Task
	var err error
	if auth.IsPlatformWorker(user) {
		tasks, err = h.store.ClaimAcrossTenants(r.Context(), user.TenantID, input)
	} else {
		tasks, err = h.store.Claim(r.Context(), user.TenantID, input)
	}
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	for _, task := range tasks {
		h.auditTask(r, "worker.task_claimed", task, "claim worker runtime task")
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (h *Handler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	user := runtimeUser(r)
	var input HeartbeatInput
	if !decodeRuntimeJSON(w, r, &input) {
		return
	}
	task, err := h.store.Heartbeat(r.Context(), user.TenantID, r.PathValue("taskId"), input)
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	if h.sourceLeaseRenewer != nil {
		if err := h.sourceLeaseRenewer.RenewSourceLease(r.Context(), task); err != nil {
			writeRuntimeError(w, r, err)
			return
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	user := runtimeUser(r)
	var input CompleteInput
	if !decodeRuntimeJSON(w, r, &input) {
		return
	}
	existing, err := h.store.Get(r.Context(), user.TenantID, r.PathValue("taskId"))
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	if requiresSourceActivation(existing.SourceType) {
		writeRuntimeError(w, r, ErrSourceActivation)
		return
	}
	task, err := h.store.Complete(r.Context(), user.TenantID, r.PathValue("taskId"), input)
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	h.auditTask(r, "worker.task_completed", task, "complete worker runtime task")
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

func (h *Handler) Fail(w http.ResponseWriter, r *http.Request) {
	user := runtimeUser(r)
	var input FailInput
	if !decodeRuntimeJSON(w, r, &input) {
		return
	}
	existing, err := h.store.Get(r.Context(), user.TenantID, r.PathValue("taskId"))
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	if requiresSourceActivation(existing.SourceType) {
		writeRuntimeError(w, r, ErrSourceActivation)
		return
	}
	task, err := h.store.Fail(r.Context(), user.TenantID, r.PathValue("taskId"), input)
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	action := "worker.task_failed"
	if task.Status == StatusQueued {
		action = "worker.task_retried"
	} else if task.Status == StatusDeadLetter {
		action = "worker.task_dead_lettered"
	}
	h.auditTask(r, action, task, "fail worker runtime task")
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	user := runtimeUser(r)
	task, err := h.store.Cancel(r.Context(), user.TenantID, r.PathValue("taskId"))
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	h.auditTask(r, "worker.task_cancelled", task, "cancel worker runtime task")
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

func (h *Handler) Requeue(w http.ResponseWriter, r *http.Request) {
	user := runtimeUser(r)
	task, err := h.store.Requeue(r.Context(), user.TenantID, r.PathValue("taskId"))
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	h.auditTask(r, "worker.task_requeued", task, "requeue worker runtime task")
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	user := runtimeUser(r)
	task, err := h.store.Get(r.Context(), user.TenantID, r.PathValue("taskId"))
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	user := runtimeUser(r)
	var metrics Metrics
	var err error
	if user.TenantID == auth.PlatformTenantID {
		metrics, err = h.store.MetricsAcrossTenants(r.Context(), user.TenantID)
	} else {
		metrics, err = h.store.Metrics(r.Context(), user.TenantID)
	}
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, metrics)
}

func decodeRuntimeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_worker_task_input", "worker task input is invalid")
		return false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_worker_task_input", "worker task input is invalid")
		return false
	}
	return true
}

func writeRuntimeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "worker_task_not_found", "worker task not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_worker_task_input", "worker task input is invalid")
	case errors.Is(err, ErrLeaseExpired):
		httpx.Error(w, r, http.StatusConflict, "worker_task_lease_expired", "worker task lease expired")
	case errors.Is(err, ErrLeaseMismatch):
		httpx.Error(w, r, http.StatusConflict, "worker_task_lease_mismatch", "worker task lease mismatch")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "worker_task_result_conflict", "worker task result conflicts with the accepted result")
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "invalid_worker_task_transition", "worker task transition is not allowed")
	case errors.Is(err, ErrSourceActivation):
		httpx.Error(w, r, http.StatusConflict, "worker_source_activation_required", "worker task must complete through its source adapter")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "worker_task_operation_failed", "worker task operation failed")
	}
}

// 这些任务的结果还会改变来源业务数据，必须走来源适配器，将业务事实与任务状态一起落库。
// 通用完成或失败接口不能只改任务状态，否则会留下来源数据与队列状态不一致的记录。
func requiresSourceActivation(sourceType string) bool {
	return sourceType == "image_quality_run" || sourceType == "ocr_task" || sourceType == "capture_file" || sourceType == "omr_run" || sourceType == "subjective_grading_run" || sourceType == "math_understanding_artifact"
}

func runtimeUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func (h *Handler) auditTask(r *http.Request, action string, task Task, reason string) {
	user := runtimeUser(r)
	actorID := user.ID
	if auth.IsPlatformWorker(user) && task.TenantID != user.TenantID {
		actorID = ""
	}
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID: task.TenantID, ActorID: actorID, Action: action, TargetType: "agent_worker_task",
		TargetID: task.ID, AfterValue: map[string]any{"status": task.Status, "task_type": task.TaskType, "source_type": task.SourceType, "source_id": task.SourceID, "attempt_count": task.AttemptCount},
		Reason: reason, IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
}
