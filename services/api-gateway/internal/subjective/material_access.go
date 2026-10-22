package subjective

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"net/http"
)

func (h *Handler) GetScoringReferenceMaterials(w http.ResponseWriter, r *http.Request) {
	h.serveScoringReference(w, r, false)
}
func (h *Handler) GetScoringReferenceImage(w http.ResponseWriter, r *http.Request) {
	h.serveScoringReference(w, r, true)
}
func (h *Handler) serveScoringReference(w http.ResponseWriter, r *http.Request, image bool) {
	if h.referenceMaterials == nil {
		httpx.Error(w, r, 503, "reference_material_unavailable", "参考材料服务尚未配置")
		return
	}
	scope, _ := auth.AccessScopeFromContext(r.Context())
	question := r.PathValue("questionId")
	exam, snapshot, err := h.referenceMaterials.ScoringRunSnapshot(r.Context(), scope, r.PathValue("runId"), question)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if image {
		data, err := h.referenceMaterials.FrozenImage(r.Context(), scope.TenantID, exam, question, snapshot, r.PathValue("itemId"))
		if err != nil {
			httpx.Error(w, r, 409, "reference_material_unavailable", "冻结参考图当前不可用")
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(200)
		_, _ = w.Write(data)
		return
	}
	m, err := h.referenceMaterials.ForSnapshot(r.Context(), scope.TenantID, exam, question, snapshot)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"materials": m})
}
