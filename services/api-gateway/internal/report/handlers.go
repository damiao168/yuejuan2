package report

import (
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"errors"
	"net/http"

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

func (h *Handler) StudentReport(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	studentID := r.PathValue("studentId")
	if !hasPermission(user, "report:read") {
		if !hasPermission(user, "student:report:read") {
			httpx.Error(w, r, http.StatusForbidden, "report_permission_required", "report permission is required")
			return
		}
		scoped, ok := scopedStudentID(user)
		if !ok || scoped != studentID {
			httpx.Error(w, r, http.StatusForbidden, "student_report_scope_violation", "student can only view own report")
			return
		}
	}
	report, err := h.store.StudentReport(r.Context(), user.TenantID, r.PathValue("examId"), studentID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "report.generated", "student_report", studentID, "generate student learning report")
	httpx.JSON(w, http.StatusOK, map[string]any{"report": report})
}

func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	report, err := h.store.Overview(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "report.generated", "exam", r.PathValue("examId"), "generate exam report overview")
	httpx.JSON(w, http.StatusOK, map[string]any{"overview": report})
}

func (h *Handler) Classes(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	reports, err := h.store.ClassReports(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "report.generated", "exam", r.PathValue("examId"), "generate class learning reports")
	httpx.JSON(w, http.StatusOK, map[string]any{"classes": reports})
}

func (h *Handler) Questions(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	reports, err := h.store.QuestionAnalysis(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "report.generated", "exam", r.PathValue("examId"), "generate question analysis report")
	httpx.JSON(w, http.StatusOK, map[string]any{"questions": reports})
}

func (h *Handler) GradingQuality(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	report, err := h.store.GradingQuality(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "report.generated", "exam", r.PathValue("examId"), "generate grading quality report")
	httpx.JSON(w, http.StatusOK, map[string]any{"grading_quality": report})
}

func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(commandreceipt.WithID(r.Context(), r.Header.Get("Idempotency-Key")))
	user := mustUser(r)
	result, err := h.store.Export(r.Context(), user.TenantID, r.PathValue("examId"), user.ID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "report.exported", "report", result.ReportID, "export learning report")
	w.Header().Set("Content-Type", result.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+result.Filename+`"`)
	w.Header().Set("X-EduGrade-Watermark", result.Watermark)
	if result.ReportID != "" {
		w.Header().Set("X-EduGrade-Report-ID", result.ReportID)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Content)
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, commandreceipt.ErrConflict) {
		httpx.Error(w, r, http.StatusConflict, "command_request_conflict", "command ID is associated with a different request")
		return
	}
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "report_resource_not_found", "report resource not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_report_input", "report input is invalid")
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, r, http.StatusForbidden, "report_action_forbidden", "report action is forbidden")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "report_operation_failed", "report operation failed")
	}
}

func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func hasPermission(user auth.User, permission string) bool {
	for _, item := range user.Permissions {
		if item == permission {
			return true
		}
	}
	return false
}

func scopedStudentID(user auth.User) (string, bool) {
	return auth.ScopedStudentID(user)
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
