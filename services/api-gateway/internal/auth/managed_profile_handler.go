package auth

import (
	"errors"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type managedProfileRequest struct {
	DisplayName         *string `json:"display_name"`
	EmployeeNo          *string `json:"employee_no"`
	Phone               *string `json:"phone,omitempty"`
	ExpectedDisplayName *string `json:"expected_display_name"`
	ExpectedEmployeeNo  *string `json:"expected_employee_no"`
	ExpectedPhoneMasked *string `json:"expected_phone_masked"`
}

// UpdateManagedUserProfile 只更新同校可管理人员；登录身份变更会撤销旧会话和一次性链接。
func (h *Handler) UpdateManagedUserProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, _ := UserFromContext(r.Context())
	var request managedProfileRequest
	if err := decodeAuthJSON(w, r, &request, true); err != nil {
		if authRequestBodyTooLarge(err) {
			httpx.Error(w, r, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
			return
		}
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid profile fields")
		return
	}
	// 空工号和空旧值是有效值，但必须显式提交，避免省略字段绕过并发核对。
	if request.DisplayName == nil || request.EmployeeNo == nil || request.ExpectedDisplayName == nil || request.ExpectedEmployeeNo == nil || request.ExpectedPhoneMasked == nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "display_name, employee_no and expected profile fields are required")
		return
	}
	input := UpdateManagedUserProfileInput{
		DisplayName: *request.DisplayName, EmployeeNo: *request.EmployeeNo, Phone: request.Phone,
		ExpectedDisplayName: *request.ExpectedDisplayName, ExpectedEmployeeNo: *request.ExpectedEmployeeNo, ExpectedPhoneMasked: *request.ExpectedPhoneMasked,
	}
	input, err := normalizeManagedProfileInput(input)
	if err != nil {
		writeManagedProfileError(w, r, err)
		return
	}
	scope, ok := AccessScopeFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
		return
	}
	user, identityChanged, err := h.store.UpdateManagedUserProfile(r.Context(), actor, scope, strings.TrimSpace(r.PathValue("id")), input)
	if err != nil {
		writeManagedProfileError(w, r, err)
		return
	}
	// 审计只保存可见资料和脱敏手机号；完整号码不能进入审计载荷或响应。
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID: actor.TenantID, ActorID: actor.ID, Action: "auth.user_profile_updated", TargetType: "user", TargetID: user.ID,
		BeforeValue: map[string]any{"display_name": input.ExpectedDisplayName, "employee_no": input.ExpectedEmployeeNo, "phone_masked": input.ExpectedPhoneMasked},
		AfterValue:  map[string]any{"display_name": user.DisplayName, "employee_no": user.EmployeeNo, "phone_masked": user.PhoneMasked, "login_identity_changed": identityChanged},
		Reason:      "update organization user profile", IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"user": user, "login_identity_changed": identityChanged})
}

func writeManagedProfileError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errInvalidProfilePhone):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_phone", "phone must be a valid mobile number and cannot be cleared")
	case errors.Is(err, errInvalidManagedProfile):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "profile fields are invalid or exceed supported size limits")
	case errors.Is(err, ErrManagedUserNotFound):
		httpx.Error(w, r, http.StatusNotFound, "managed_user_not_found", "managed user not found")
	case errors.Is(err, ErrManagedUserProfileConflict):
		httpx.Error(w, r, http.StatusConflict, "managed_user_profile_conflict", "profile changed; reload before editing")
	case errors.Is(err, ErrIdentityExists):
		httpx.Error(w, r, http.StatusConflict, "identity_exists", "phone or employee number is already in use")
	case errors.Is(err, ErrOrganizationScope):
		httpx.Error(w, r, http.StatusForbidden, "organization_scope_forbidden", "organization binding is outside the current data access scope")
	case errors.Is(err, ErrUserStatusForbidden):
		httpx.Error(w, r, http.StatusForbidden, "user_profile_forbidden", "current identity cannot update this user profile")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "user_profile_update_failed", "failed to update user profile")
	}
}
