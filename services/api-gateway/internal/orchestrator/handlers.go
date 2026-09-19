package orchestrator

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type Handler struct {
	store Store
	audit auth.AuditRecorder
}

func NewHandler(store Store, audit auth.AuditRecorder) *Handler {
	return &Handler{store: store, audit: audit}
}

func (h *Handler) CreateRun(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input CreateRunInput
	if !decodeJSON(w, r, &input) {
		return
	}
	run, err := h.store.CreateRun(r.Context(), user.TenantID, user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "orchestrator.run_created", "orchestration_run", run.ID, "create orchestration run")
	httpx.JSON(w, http.StatusCreated, map[string]any{"run": run})
}

func (h *Handler) GetRun(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	run, err := h.store.GetRun(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"run": run})
}

func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	tasks, err := h.store.ListTasks(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input CreateTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	task, err := h.store.CreateTask(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "orchestrator.task_created", "agent_task", task.ID, "create agent task")
	httpx.JSON(w, http.StatusCreated, map[string]any{"task": task})
}

func (h *Handler) StartTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	task, err := h.store.StartTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "orchestrator.task_started", "agent_task", task.ID, "start agent task")
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

func (h *Handler) CompleteTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input CompleteTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	task, err := h.store.CompleteTask(r.Context(), user.TenantID, r.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	reason := "complete agent task"
	if task.Status == "requires_human_review" {
		reason = "complete agent task and trigger human review"
	}
	h.auditAction(r, "orchestrator.task_completed", "agent_task", task.ID, reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

func (h *Handler) FailTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input FailTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ErrorMessage = strings.TrimSpace(input.ErrorMessage)
	if input.ErrorMessage == "" {
		writeStoreError(w, r, ErrInvalidInput)
		return
	}
	task, err := h.store.FailTask(r.Context(), user.TenantID, r.PathValue("id"), input.ErrorMessage)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "orchestrator.task_failed", "agent_task", task.ID, "fail agent task")
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

func (h *Handler) RetryTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	task, err := h.store.RetryTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "orchestrator.task_retried", "agent_task", task.ID, "retry agent task")
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		return true
	}
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "orchestrator_resource_not_found", "orchestrator resource not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_orchestrator_input", "orchestrator input is invalid")
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "invalid_orchestrator_status_transition", "orchestrator status transition is not allowed")
	case errors.Is(err, ErrRetryExhausted):
		httpx.Error(w, r, http.StatusConflict, "agent_task_retry_exhausted", "agent task retry limit has been reached")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "orchestrator_operation_failed", "orchestrator operation failed")
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
