package review

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"net/http"
)

func (h *Handler) WithReferenceMaterials(s *paper.ReferenceMaterialsService) *Handler {
	h.referenceMaterials = s
	return h
}
func (h *Handler) GetReferenceMaterials(w http.ResponseWriter, r *http.Request) {
	h.serveReferenceMaterials(w, r, false)
}
func (h *Handler) GetReferenceMaterialImage(w http.ResponseWriter, r *http.Request) {
	h.serveReferenceMaterials(w, r, true)
}
func (h *Handler) serveReferenceMaterials(w http.ResponseWriter, r *http.Request, image bool) {
	user := mustUser(r)
	if !h.authorizeTaskWorker(w, r, user) {
		return
	}
	if h.referenceMaterials == nil {
		httpx.Error(w, r, 503, "reference_material_unavailable", "参考材料服务尚未配置")
		return
	}
	contextStore, ok := h.store.(TaskContextStore)
	if !ok {
		writeStoreError(w, r, ErrNotFound)
		return
	}
	value, err := contextStore.GetTaskContext(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	task, err := h.store.GetTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if image {
		data, err := h.referenceMaterials.FrozenImage(r.Context(), user.TenantID, task.ExamID, task.QuestionID, value.QuestionSnapshot.ID, r.PathValue("itemId"))
		if err != nil {
			httpx.Error(w, r, 409, "reference_material_unavailable", "冻结参考图当前不可用，人工阅卷可继续")
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(200)
		_, _ = w.Write(data)
		return
	}
	materials, err := h.referenceMaterials.ForSnapshot(r.Context(), user.TenantID, task.ExamID, task.QuestionID, value.QuestionSnapshot.ID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	for i := range materials.Items {
		materials.Items[i].Source.Name = ""
		materials.Items[i].Source.PageRef = ""
	}
	httpx.JSON(w, 200, map[string]any{"materials": materials})
}
