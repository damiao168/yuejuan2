package evidence

import (
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type Handler struct {
	store  Store
	engine *Engine
	audit  auth.AuditRecorder
}

func NewHandler(store Store, engine *Engine, audit auth.AuditRecorder) *Handler {
	return &Handler{store: store, engine: engine, audit: audit}
}

func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	ctx, err := h.store.LoadContext(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	result := h.engine.Verify(ctx)
	job, err := h.store.CreateJob(r.Context(), user.TenantID, user.ID, ctx.Grade.ID, result)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "evidence.checked", "ai_grade", ctx.Grade.ID, "verify ai grade evidence")
	httpx.JSON(w, http.StatusCreated, map[string]any{"job": job})
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "evidence_resource_not_found", "evidence resource not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_evidence_input", "evidence input is invalid")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "evidence_operation_failed", "evidence operation failed")
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
