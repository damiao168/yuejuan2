package capture

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type Handler struct {
	store         Store
	files         files.Store
	exams         exam.Store
	runtime       workerruntime.Store
	audit         auth.Store
	scanTemplates ScanTemplateReader
}

// WithScanTemplates 只用于采集任务的模板投影，不开放模板配置或文件管理能力。
func (h *Handler) WithScanTemplates(reader ScanTemplateReader) *Handler {
	h.scanTemplates = reader
	return h
}

func NewHandler(store Store, fileStore files.Store, examStore exam.Store, runtime workerruntime.Store, audit auth.Store) *Handler {
	return &Handler{store: store, files: fileStore, exams: examStore, runtime: runtime, audit: audit}
}

func writeCaptureCompletionError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, workerruntime.ErrLeaseExpired) || errors.Is(err, workerruntime.ErrLeaseMismatch) || errors.Is(err, workerruntime.ErrConflict) || errors.Is(err, workerruntime.ErrInvalidTransition) {
		writeRuntimeError(w, r, err)
		return
	}
	writeError(w, r, err)
}

// 请求体限制为 1 MiB、禁止未知字段且只能包含一个 JSON 值，避免 worker 回调夹带未审计输入。
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
func mustUser(r *http.Request) auth.User { user, _ := auth.UserFromContext(r.Context()); return user }
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var printInput printPackageInputError
	switch {
	case errors.Is(err, ErrPrintLayoutConfirmationRequired):
		httpx.Error(w, r, http.StatusConflict, "student_print_layout_confirmation_required", "请先查看原答题卡，并逐页确认可放置二维码的位置。")
	case errors.Is(err, ErrFileHasGrades):
		httpx.Error(w, r, http.StatusConflict, "capture_file_has_grades", "这份答卷已进入评分，请通过答卷修订与重新阅卷处理，不能直接移除")
	case strings.Contains(err.Error(), "capture_collection_closed"):
		httpx.Error(w, r, http.StatusConflict, "capture_collection_closed", "本场导入已结束，请使用补传答卷入口")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "capture_not_found", "capture resource not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "capture_invalid_input", "capture input is invalid")
	case errors.As(err, &printInput):
		httpx.Error(w, r, http.StatusConflict, "student_print_layout_unavailable", printInput.Error())
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "capture_invalid_transition", "capture state does not allow this action")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "capture_revision_conflict", "capture resource changed; refresh and retry")
	case errors.Is(err, ErrDuplicateFile):
		httpx.Error(w, r, http.StatusConflict, "capture_duplicate_file", "file is already registered in this batch")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "capture_operation_failed", "capture operation failed")
	}
}
func writeRuntimeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, workerruntime.ErrLeaseExpired), errors.Is(err, workerruntime.ErrLeaseMismatch):
		httpx.Error(w, r, http.StatusConflict, "capture_worker_lease_invalid", "worker lease expired or no longer owns this task")
	case errors.Is(err, workerruntime.ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "capture_worker_result_conflict", "worker result conflicts with the saved result")
	default:
		httpx.Error(w, r, http.StatusConflict, "capture_worker_result_rejected", "worker result could not be accepted")
	}
}
func writeTemplateMatchCommandError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, workerruntime.ErrNotFound),
		errors.Is(err, workerruntime.ErrInvalidInput),
		errors.Is(err, workerruntime.ErrInvalidTransition),
		errors.Is(err, workerruntime.ErrLeaseMismatch),
		errors.Is(err, workerruntime.ErrLeaseExpired),
		errors.Is(err, workerruntime.ErrConflict),
		errors.Is(err, workerruntime.ErrSourceActivation):
		writeRuntimeError(w, r, err)
	default:
		writeError(w, r, err)
	}
}
func (h *Handler) auditAction(r *http.Request, action, targetType, targetID, reason string) {
	user := mustUser(r)
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{TenantID: user.TenantID, ActorID: user.ID, Action: action, TargetType: targetType, TargetID: targetID, Reason: strings.TrimSpace(reason), IPAddress: httpx.ClientIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context())})
}
