package paper

import (
	"database/sql"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func (h *Handler) WithReferenceMaterials(service *ReferenceMaterialsService) *Handler {
	h.referenceMaterials = service
	return h
}
func (h *Handler) referenceService(w http.ResponseWriter, r *http.Request) bool {
	if h.referenceMaterials == nil {
		httpx.Error(w, r, 503, "reference_material_unavailable", "参考材料服务尚未配置")
		return false
	}
	return true
}
func referenceScope(r *http.Request) auth.AccessScope {
	scope, _ := auth.AccessScopeFromContext(r.Context())
	return scope
}
func referenceWriteError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrConflict), errors.Is(err, commandreceipt.ErrConflict):
		httpx.Error(w, r, 409, "reference_material_revision_conflict", "材料版本或原操作输入已变化，请核对后重试")
	case errors.Is(err, ErrExamFrozen):
		httpx.Error(w, r, 409, "exam_configuration_frozen", "本场已冻结，不能添加或替换图片")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, 400, "reference_material_incomplete", "请核对图片来源、裁图范围、角色和数量")
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, ErrNotFound), errors.Is(err, files.ErrNotFound):
		httpx.Error(w, r, 404, "reference_material_unavailable", "材料不存在或当前不可用")
	case errors.Is(err, files.ErrForbidden):
		httpx.Error(w, r, 403, "reference_material_forbidden", "没有使用这份材料的权限")
	default:
		httpx.Error(w, r, 500, "reference_material_operation_failed", "材料操作未完成，请核对原操作结果")
	}
}
func (h *Handler) ListReferenceMaterialSources(w http.ResponseWriter, r *http.Request) {
	if !h.referenceService(w, r) {
		return
	}
	items, e := h.referenceMaterials.Sources(r.Context(), referenceScope(r), r.PathValue("examId"))
	if e != nil {
		referenceWriteError(w, r, e)
		return
	}
	httpx.JSON(w, 200, map[string]any{"sources": items})
}
func referenceImage(w http.ResponseWriter, b []byte, kind string) {
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(200)
	_, _ = w.Write(b)
}
func (h *Handler) GetReferenceMaterialSourceImage(w http.ResponseWriter, r *http.Request) {
	if !h.referenceService(w, r) {
		return
	}
	b, kind, e := h.referenceMaterials.SourceImage(r.Context(), referenceScope(r), r.PathValue("examId"), r.PathValue("pageRef"))
	if e != nil {
		referenceWriteError(w, r, e)
		return
	}
	referenceImage(w, b, kind)
}
func (h *Handler) CreateReferenceMaterialCrop(w http.ResponseWriter, r *http.Request) {
	if !h.referenceService(w, r) {
		return
	}
	var input CreateReferenceCropInput
	if !decodeJSON(w, r, &input) {
		return
	}
	out, e := h.referenceMaterials.CreateCrop(r.Context(), referenceScope(r), r.PathValue("examId"), r.PathValue("questionId"), input)
	if e != nil {
		referenceWriteError(w, r, e)
		return
	}
	httpx.JSON(w, 201, map[string]any{"crop": out})
}
func (h *Handler) GetReferenceMaterialCropImage(w http.ResponseWriter, r *http.Request) {
	if !h.referenceService(w, r) {
		return
	}
	b, e := h.referenceMaterials.CropImage(r.Context(), referenceScope(r), r.PathValue("examId"), r.PathValue("questionId"), r.PathValue("cropRef"))
	if e != nil {
		referenceWriteError(w, r, e)
		return
	}
	referenceImage(w, b, "image/png")
}
func (h *Handler) GetReferenceMaterials(w http.ResponseWriter, r *http.Request) {
	if !h.referenceService(w, r) {
		return
	}
	out, e := h.referenceMaterials.Get(r.Context(), mustUser(r).TenantID, r.PathValue("examId"), r.PathValue("questionId"))
	if e != nil {
		referenceWriteError(w, r, e)
		return
	}
	httpx.JSON(w, 200, map[string]any{"materials": out})
}
func (h *Handler) SaveReferenceMaterials(w http.ResponseWriter, r *http.Request) {
	if !h.referenceService(w, r) {
		return
	}
	var input SaveReferenceMaterialsInput
	if !decodeJSON(w, r, &input) {
		return
	}
	out, e := h.referenceMaterials.Save(r.Context(), referenceScope(r), r.PathValue("examId"), r.PathValue("questionId"), input)
	if e != nil {
		referenceWriteError(w, r, e)
		return
	}
	h.auditAction(r, "reference.materials_saved", "question", r.PathValue("questionId"), "保存参考材料版本")
	httpx.JSON(w, 200, map[string]any{"materials": out})
}
func (h *Handler) WithdrawReferenceMaterials(w http.ResponseWriter, r *http.Request) {
	if !h.referenceService(w, r) {
		return
	}
	var input WithdrawReferenceMaterialsInput
	if !decodeJSON(w, r, &input) {
		return
	}
	out, e := h.referenceMaterials.Withdraw(r.Context(), referenceScope(r), r.PathValue("examId"), r.PathValue("questionId"), r.PathValue("manifestId"), input)
	if e != nil {
		referenceWriteError(w, r, e)
		return
	}
	h.auditAction(r, "reference.materials_withdrawn", "question", r.PathValue("questionId"), "撤回参考材料使用授权")
	httpx.JSON(w, 200, map[string]any{"materials": out})
}
func (h *Handler) RecoverReferenceMaterialCommand(w http.ResponseWriter, r *http.Request) {
	if !h.referenceService(w, r) {
		return
	}
	out, e := h.referenceMaterials.Recover(r.Context(), referenceScope(r), r.PathValue("examId"), r.PathValue("commandId"))
	if e != nil {
		referenceWriteError(w, r, e)
		return
	}
	httpx.JSON(w, 200, map[string]any{"command": out})
}

func (h *Handler) ResumeReferenceMaterialCommand(w http.ResponseWriter, r *http.Request) {
	if !h.referenceService(w, r) {
		return
	}
	out, e := h.referenceMaterials.ResumeCommand(r.Context(), referenceScope(r), r.PathValue("examId"), r.PathValue("questionId"), r.PathValue("commandId"))
	if e != nil {
		referenceWriteError(w, r, e)
		return
	}
	httpx.JSON(w, 200, map[string]any{"command": out})
}
