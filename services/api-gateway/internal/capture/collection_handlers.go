package capture

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"net/http"
)

func (h *Handler) GetCollectionSummary(w http.ResponseWriter, r *http.Request) {
	store, ok := h.store.(CollectionStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "capture_collection_unavailable", "当前环境尚未启用考试名册导入核对")
		return
	}
	user := mustUser(r)
	out, err := store.GetCollectionSummary(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"summary": out})
}
func (h *Handler) CompleteCollection(w http.ResponseWriter, r *http.Request) {
	store, ok := h.store.(CollectionStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "capture_collection_unavailable", "当前环境尚未启用考试名册导入核对")
		return
	}
	var input CompleteCollectionInput
	if !decodeStrict(w, r, &input) {
		return
	}
	user := mustUser(r)
	out, err := store.CompleteCollection(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture_collection.completed", "exam", out.ExamID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"summary": out})
}
func (h *Handler) ExcludeFile(w http.ResponseWriter, r *http.Request) {
	store, ok := h.store.(CollectionStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "capture_collection_unavailable", "当前环境尚未启用导入材料处置")
		return
	}
	var input ExcludeFileInput
	if !decodeStrict(w, r, &input) {
		return
	}
	user := mustUser(r)
	examID, err := store.GetCollectionFileExam(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
		return
	}
	if _, err = h.exams.GetExam(r.Context(), scope, examID); err != nil {
		httpx.Error(w, r, http.StatusNotFound, "exam_not_found", "exam not found")
		return
	}
	out, err := store.ExcludeFile(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture_file.excluded", "capture_file", r.PathValue("id"), input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"batch": out})
}
