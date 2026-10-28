package calibration

import (
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"github.com/google/uuid"
)

type templateCapabilities struct {
	CanApprove  bool `json:"can_approve"`
	CanWithdraw bool `json:"can_withdraw"`
}

func schoolTemplateCapability(user auth.User, scope auth.AccessScope, school string) bool {
	if scope.ActorID != user.ID || scope.TenantID != user.TenantID || !auth.HasPermission(user, reviewManagePermission) {
		return false
	}
	if scope.TenantWide {
		return true
	}
	if scope.IsSchoolWide() {
		for _, id := range scope.ManagedSchoolIDs() {
			if id == school {
				return true
			}
		}
	}
	return false
}

func (h *Handler) schoolTemplateContext(w http.ResponseWriter, r *http.Request) (auth.User, SchoolTemplateRepository, SchoolTemplateTarget, bool) {
	user, ok := currentUser(w, r)
	if !ok {
		return user, nil, SchoolTemplateTarget{}, false
	}
	repository, ok := h.service.store.(SchoolTemplateRepository)
	if !ok {
		httpx.Error(w, r, 503, "calibration_operation_failed", "school calibration templates are unavailable")
		return user, nil, SchoolTemplateTarget{}, false
	}
	for _, id := range []string{r.PathValue("examId"), r.PathValue("questionId")} {
		if _, err := uuid.Parse(id); err != nil {
			writeHandlerError(w, r, ErrInvalidInput)
			return user, nil, SchoolTemplateTarget{}, false
		}
	}
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok || scope.ActorID != user.ID || scope.TenantID != user.TenantID || !auth.HasPermission(user, reviewManagePermission) {
		httpx.Error(w, r, 403, "calibration_template_forbidden", "current account cannot manage this question calibration standard")
		return user, nil, SchoolTemplateTarget{}, false
	}
	target, err := repository.SchoolTemplateTarget(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"))
	if err != nil {
		writeHandlerError(w, r, err)
		return user, nil, target, false
	}
	if !scope.AllowsExam(r.PathValue("examId")) && !schoolTemplateCapability(user, scope, target.SchoolID) {
		httpx.Error(w, r, 403, "calibration_template_forbidden", "current account cannot manage this question calibration standard")
		return user, nil, target, false
	}
	return user, repository, target, true
}

func (h *Handler) ListSchoolTemplates(w http.ResponseWriter, r *http.Request) {
	user, repository, target, ok := h.schoolTemplateContext(w, r)
	if !ok {
		return
	}
	items, err := repository.ListSchoolTemplates(r.Context(), user.TenantID, target)
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	scope, _ := auth.AccessScopeFromContext(r.Context())
	canApprove := schoolTemplateCapability(user, scope, target.SchoolID)
	httpx.JSON(w, 200, map[string]any{"templates": items, "question": target, "capabilities": templateCapabilities{CanApprove: canApprove, CanWithdraw: canApprove}})
}
func (h *Handler) ApproveSchoolTemplate(w http.ResponseWriter, r *http.Request) {
	user, repository, target, ok := h.schoolTemplateContext(w, r)
	if !ok {
		return
	}
	scope, _ := auth.AccessScopeFromContext(r.Context())
	// 题目策略管理权不等于学校审批权；只有该校的可信管理范围或租户管理范围可以批准。
	if !schoolTemplateCapability(user, scope, target.SchoolID) {
		httpx.Error(w, r, 403, "calibration_template_forbidden", "school management approval permission is required")
		return
	}
	var input ApproveSchoolTemplateInput
	if !decodeBody(w, r, &input) {
		return
	}
	ctx := withExpectedTemplateTarget(r.Context(), r.PathValue("examId"), r.PathValue("questionId"), target)
	item, err := repository.ApproveSchoolTemplate(ctx, user.TenantID, user.ID, r.PathValue("examId"), r.PathValue("questionId"), input)
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	h.record(r, user, "grader_calibration.school_template_approved", item.ID)
	httpx.JSON(w, 200, map[string]any{"template": item})
}
func (h *Handler) WithdrawSchoolTemplate(w http.ResponseWriter, r *http.Request) {
	user, repository, target, ok := h.schoolTemplateContext(w, r)
	if !ok {
		return
	}
	scope, _ := auth.AccessScopeFromContext(r.Context())
	if !schoolTemplateCapability(user, scope, target.SchoolID) {
		httpx.Error(w, r, 403, "calibration_template_forbidden", "school management approval permission is required")
		return
	}
	id := r.PathValue("templateId")
	if _, err := uuid.Parse(id); err != nil {
		writeHandlerError(w, r, ErrInvalidInput)
		return
	}
	var input WithdrawSchoolTemplateInput
	if !decodeBody(w, r, &input) {
		return
	}
	ctx := withExpectedTemplateTarget(r.Context(), r.PathValue("examId"), r.PathValue("questionId"), target)
	item, err := repository.WithdrawSchoolTemplate(ctx, user.TenantID, user.ID, target, id, input)
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	h.record(r, user, "grader_calibration.school_template_withdrawn", item.ID)
	httpx.JSON(w, 200, map[string]any{"template": item})
}
