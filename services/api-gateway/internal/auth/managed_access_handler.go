package auth

import (
	"errors"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

func (h *Handler) UpdateManagedUserAccess(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	var input UpdateManagedUserAccessInput
	if err := decodeAuthJSON(w, r, &input, true); err != nil {
		if authRequestBodyTooLarge(err) {
			httpx.Error(w, r, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
			return
		}
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid access fields")
		return
	}
	// 职责旧值必填，省略不能绕过并发保护；教师资格由资料页单独维护。
	if input.RoleCodes == nil || input.ExpectedRoles == nil || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 2000 || len(input.ExpectedRoles) > 8 || !managedUserFieldsWithinLimits(CreateManagedUserInput{RoleCodes: input.RoleCodes}) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "roles, expected roles and a reason are required")
		return
	}
	input.RoleCodes = uniqueSortedIDs(input.RoleCodes)
	input.Reason = strings.TrimSpace(input.Reason)
	scope, ok := AccessScopeFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
		return
	}
	user, err := h.store.UpdateManagedUserAccess(r.Context(), actor, scope, strings.TrimSpace(r.PathValue("id")), input)
	if err != nil {
		switch {
		case errors.Is(err, ErrManagedUserNotFound):
			httpx.Error(w, r, http.StatusNotFound, "managed_user_not_found", "managed user not found")
		case errors.Is(err, ErrManagedUserAccessConflict):
			httpx.Error(w, r, http.StatusConflict, "managed_user_access_conflict", "access changed; reload before editing")
		case errors.Is(err, ErrLastSchoolAdmin):
			httpx.Error(w, r, http.StatusConflict, "last_school_admin", "the last active school administrator cannot be removed")
		case errors.Is(err, ErrInvalidTeachingSubjects):
			httpx.Error(w, r, http.StatusBadRequest, "invalid_teaching_subjects", "configure teaching subjects before assigning a review teacher role")
		case errors.Is(err, ErrInvalidRoleBinding), errors.Is(err, ErrRoleNotFound):
			httpx.Error(w, r, http.StatusBadRequest, "invalid_role_binding", "role or school binding is invalid")
		case errors.Is(err, ErrOrganizationScope), errors.Is(err, ErrRoleAssignment), errors.Is(err, ErrUserStatusForbidden):
			httpx.Error(w, r, http.StatusForbidden, "role_assignment_forbidden", "current identity cannot change this access")
		default:
			httpx.Error(w, r, http.StatusInternalServerError, "user_access_update_failed", "failed to update user access")
		}
		return
	}
	RecordAudit(r.Context(), h.store, AuditEvent{TenantID: actor.TenantID, ActorID: actor.ID, Action: "auth.user_access_updated", TargetType: "user", TargetID: user.ID, BeforeValue: map[string]any{"role_codes": input.ExpectedRoles}, AfterValue: map[string]any{"role_codes": user.Roles}, Reason: input.Reason, IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context())})
	httpx.JSON(w, http.StatusOK, map[string]any{"user": user})
}
