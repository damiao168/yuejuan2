package captureupload

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type Handler struct {
	service *Service
	audit   auth.AuditRecorder
}

func NewHandler(service *Service, audit auth.AuditRecorder) *Handler {
	return &Handler{service: service, audit: audit}
}

// RegisterRoutes uses segment routes rather than the legacy ":complete"
// syntax, which net/http ServeMux cannot safely bind as a path wildcard.
func RegisterRoutes(mux *http.ServeMux, handler *Handler, requireCaptureManage func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /api/v1/capture/uploads:init", requireCaptureManage(handler.Init))
	mux.Handle("GET /api/v1/capture/uploads/{id}", requireCaptureManage(handler.Get))
	mux.Handle("PUT /api/v1/capture/uploads/{id}/chunks", requireCaptureManage(handler.PutChunk))
	mux.Handle("POST /api/v1/capture/uploads/{id}/complete", requireCaptureManage(handler.Complete))
}

// Get recovers transfer progress without reinitializing or advancing it.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	session, err := h.service.Get(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !h.allowsExam(r, session.ExamID) {
		httpx.Error(w, r, http.StatusForbidden, "capture_upload_forbidden", "capture upload is outside the current data scope")
		return
	}
	status := "processing"
	if session.Status == "completed" {
		status = "succeeded"
	}
	if session.Status == "failed" {
		status = "failed"
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"command_id": session.IdempotencyKey, "status": status, "upload": session})
}

func (h *Handler) Init(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input InitInput
	if !decodeStrict(w, r, &input) {
		return
	}
	if !h.allowsExam(r, input.ExamID) {
		httpx.Error(w, r, http.StatusForbidden, "capture_upload_forbidden", "capture upload is outside the current data scope")
		return
	}
	response, err := h.service.Init(r.Context(), user.TenantID, user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture_upload.initialized", response.RemoteUploadID, "initialize resumable scan upload")
	httpx.JSON(w, http.StatusOK, response)
}

func (h *Handler) PutChunk(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	session, err := h.service.Get(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !h.allowsExam(r, session.ExamID) {
		httpx.Error(w, r, http.StatusForbidden, "capture_upload_forbidden", "capture upload is outside the current data scope")
		return
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(r.Header.Get("Upload-Offset")), 10, 64)
	if err != nil || offset < 0 {
		httpx.Error(w, r, http.StatusBadRequest, "capture_upload_invalid_offset", "Upload-Offset must be a non-negative integer")
		return
	}
	chunkHash := strings.TrimSpace(r.Header.Get("X-Chunk-SHA256"))
	if chunkHash == "" {
		httpx.Error(w, r, http.StatusBadRequest, "capture_upload_missing_chunk_hash", "X-Chunk-SHA256 is required")
		return
	}
	if r.ContentLength > session.ChunkSize {
		httpx.Error(w, r, http.StatusRequestEntityTooLarge, "capture_upload_chunk_too_large", "chunk exceeds the negotiated chunk size")
		return
	}
	body := http.MaxBytesReader(w, r.Body, session.ChunkSize+1)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		httpx.Error(w, r, http.StatusRequestEntityTooLarge, "capture_upload_chunk_too_large", "chunk exceeds the negotiated chunk size")
		return
	}
	updated, err := h.service.AppendChunk(r.Context(), user.TenantID, session.ID, ChunkInput{Offset: offset, SHA256: chunkHash, Data: data})
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture_upload.chunk_confirmed", updated.ID, "confirm resumable scan upload chunk")
	httpx.JSON(w, http.StatusOK, map[string]any{"remote_upload_id": updated.ID, "confirmed_offset": updated.ConfirmedOffset, "status": updated.Status})
}

func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	session, err := h.service.Get(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !h.allowsExam(r, session.ExamID) {
		httpx.Error(w, r, http.StatusForbidden, "capture_upload_forbidden", "capture upload is outside the current data scope")
		return
	}
	var input CompleteInput
	if !decodeStrict(w, r, &input) {
		return
	}
	completed, err := h.service.Complete(r.Context(), user.TenantID, user.ID, session.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture_upload.completed", completed.ID, "verify and register offline scan upload")
	httpx.JSON(w, http.StatusOK, completed)
}

func (h *Handler) allowsExam(r *http.Request, examID string) bool {
	user := mustUser(r)
	scope, ok := auth.AccessScopeFromContext(r.Context())
	return ok && scope.TenantID == user.TenantID && (scope.IsPlatform || scope.AllowsExam(examID))
}

func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func decodeStrict(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid or contains unknown fields")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "capture_upload_not_found", "resumable scan upload was not found")
	case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrUnsupportedType):
		httpx.Error(w, r, http.StatusBadRequest, "capture_upload_invalid_input", "resumable scan upload input is invalid")
	case errors.Is(err, ErrHashMismatch):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "capture_upload_hash_mismatch", "uploaded bytes do not match the expected hash")
	case errors.Is(err, ErrIncomplete):
		httpx.Error(w, r, http.StatusConflict, "capture_upload_incomplete", "upload is not fully confirmed; resume from the confirmed offset")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "capture_upload_conflict", "resumable scan upload conflicts with saved state")
	case errors.Is(err, ErrStorage):
		httpx.Error(w, r, http.StatusBadGateway, "capture_upload_storage_failed", "scan upload storage is temporarily unavailable")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "capture_upload_failed", "resumable scan upload failed")
	}
}

func (h *Handler) auditAction(r *http.Request, action, targetID, reason string) {
	user := mustUser(r)
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: action, TargetType: "capture_upload_session", TargetID: targetID,
		Reason: reason, IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
}
