package paper

import (
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func (h *Handler) PreviewPaperStructure(w http.ResponseWriter, r *http.Request) {
	store, ok := h.imports.(PaperStructureRepository)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "structure_unavailable", "试卷结构预览暂不可用")
		return
	}
	user := mustUser(r)
	out, err := store.PreviewPaperStructure(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}
func (h *Handler) AdoptPaperStructure(w http.ResponseWriter, r *http.Request) {
	store, ok := h.imports.(PaperStructureRepository)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "structure_unavailable", "试卷结构调整暂不可用")
		return
	}
	user := mustUser(r)
	var input AdoptPaperStructureInput
	if !decodeJSON(w, r, &input) {
		return
	}
	out, err := store.AdoptPaperStructure(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.structure_adopted", "paper_import", out.ImportID, "confirm complete paper and adopt recognized structure")
	httpx.JSON(w, http.StatusOK, out)
}
