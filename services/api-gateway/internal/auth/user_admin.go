package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
)

func (h *Handler) ListManagedUsers(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 100, 200)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "limit must be between 1 and 200")
		return
	}
	cursor, err := pagination.Decode(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "cursor is invalid")
		return
	}
	actorScope, _ := AccessScopeFromContext(r.Context())
	filter := ManagedUserFilter{
		Query: strings.TrimSpace(r.URL.Query().Get("q")), Role: strings.TrimSpace(r.URL.Query().Get("role")),
		Limit: limit + 1, CursorCreatedAt: cursor.CreatedAt, CursorID: cursor.ID,
	}
	if !actorScope.IsPlatform && !actorScope.TenantWide {
		filter.RestrictSchools = true
		filter.SchoolIDs = append([]string(nil), actorScope.SchoolIDs...)
	}
	users, err := h.store.ListManagedUsers(r.Context(), actor.TenantID, filter)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "user_list_failed", "failed to list users")
		return
	}
	hasMore := len(users) > limit
	if hasMore {
		users = users[:limit]
	}
	nextCursor := ""
	if hasMore && len(users) > 0 {
		last := users[len(users)-1]
		nextCursor = pagination.Encode(last.CreatedAt, last.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"users": users, "next_cursor": nextCursor, "has_more": hasMore})
}

func (h *Handler) ListAssignableRoles(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	roles, err := h.store.ListAssignableRoles(r.Context(), actor)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "role_list_failed", "failed to list roles")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"roles": roles})
}

func (h *Handler) CreateManagedUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, _ := UserFromContext(r.Context())
	var input CreateManagedUserInput
	if err := decodeAuthJSON(w, r, &input, true); err != nil {
		if authRequestBodyTooLarge(err) {
			httpx.Error(w, r, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
			return
		}
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid user fields")
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	input.Phone = strings.TrimSpace(input.Phone)
	input.EmployeeNo = strings.TrimSpace(input.EmployeeNo)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.RoleCode = strings.TrimSpace(input.RoleCode)
	input.SchoolID = strings.TrimSpace(input.SchoolID)
	input.ClassIDs = uniqueSortedIDs(input.ClassIDs)
	if input.Phone != "" {
		normalizedPhone, err := NormalizePhone(input.Phone)
		if err != nil {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_phone", "phone must be a valid mobile number")
			return
		}
		input.Phone = normalizedPhone
	}
	invited := input.Password == ""
	if input.DisplayName == "" || input.RoleCode == "" || (invited && input.Phone == "" && input.EmployeeNo == "") || (!invited && input.Username == "") {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "display_name, role_code and a login identity are required")
		return
	}
	if input.Username == "" {
		input.Username = generatedManagedUsername(actor.TenantID, input.Phone, input.EmployeeNo)
	}
	if !managedUserFieldsWithinLimits(input) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "user fields exceed supported size limits")
		return
	}
	// 邀请账户没有可登录密码；只保存激活令牌哈希，原令牌仅在本次响应交给调用方。
	passwordHash := "!activation-required"
	activationToken := ""
	if invited {
		var err error
		activationToken, input.ActivationTokenHash, err = NewToken()
		if err != nil {
			httpx.Error(w, r, http.StatusInternalServerError, "token_generation_failed", "failed to create invitation")
			return
		}
		input.ActivationExpiresAt = time.Now().UTC().Add(48 * time.Hour)
	} else {
		if !strongBootstrapPassword(input.Password) {
			httpx.Error(w, r, http.StatusBadRequest, "weak_password", "password must be a long passphrase of at least 15 characters")
			return
		}
		var err error
		passwordHash, err = HashPassword(input.Password)
		if err != nil {
			httpx.Error(w, r, http.StatusInternalServerError, "password_hash_failed", "failed to create user")
			return
		}
	}
	actorScope, ok := AccessScopeFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
		return
	}
	created, err := h.store.CreateManagedUser(r.Context(), actor, actorScope, input, passwordHash)
	if err != nil {
		switch {
		case errors.Is(err, ErrUsernameExists):
			httpx.Error(w, r, http.StatusConflict, "username_exists", "username already exists")
		case errors.Is(err, ErrIdentityExists):
			httpx.Error(w, r, http.StatusConflict, "identity_exists", "phone or employee number is already in use")
		case errors.Is(err, ErrRoleNotFound):
			httpx.Error(w, r, http.StatusBadRequest, "role_not_assignable", "role is not assignable in the current organization")
		case errors.Is(err, ErrRoleAssignment):
			httpx.Error(w, r, http.StatusForbidden, "role_assignment_forbidden", "current identity cannot assign this role")
		case errors.Is(err, ErrOrganizationScope):
			httpx.Error(w, r, http.StatusForbidden, "organization_scope_forbidden", "organization binding is outside the current data access scope")
		case errors.Is(err, ErrInvalidRoleBinding):
			httpx.Error(w, r, http.StatusBadRequest, "invalid_role_binding", "role organization binding is invalid")
		default:
			httpx.Error(w, r, http.StatusInternalServerError, "user_create_failed", "failed to create user")
		}
		return
	}
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID: actor.TenantID, ActorID: actor.ID, Action: "auth.user_created",
		TargetType: "user", TargetID: created.ID,
		AfterValue: map[string]any{"username": created.Username, "display_name": created.DisplayName, "phone": created.PhoneMasked, "employee_no": created.EmployeeNo, "role_code": input.RoleCode, "school_id": created.SchoolID, "class_ids": input.ClassIDs, "status": created.Status},
		Reason:     "create organization user", IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	response := map[string]any{"user": created}
	if invited {
		response["activation"] = map[string]any{
			"token": activationToken, "expires_at": input.ActivationExpiresAt,
			"path": "/activate?token=" + activationToken,
		}
	}
	httpx.JSON(w, http.StatusCreated, response)
}

func generatedManagedUsername(tenantID, phone, employeeNo string) string {
	identity := phone
	if identity == "" {
		identity = employeeNo
	}
	sum := sha256.Sum256([]byte(tenantID + "|" + identity))
	return "member_" + hex.EncodeToString(sum[:8])
}

func (h *Handler) UpdateManagedUserStatus(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	var input UpdateManagedUserStatusInput
	if err := decodeAuthJSON(w, r, &input, true); err != nil {
		if authRequestBodyTooLarge(err) {
			httpx.Error(w, r, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
			return
		}
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid status field")
		return
	}
	input.Status = strings.TrimSpace(input.Status)
	if input.Status != "active" && input.Status != "disabled" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_user_status", "status must be active or disabled")
		return
	}
	actorScope, ok := AccessScopeFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
		return
	}
	updated, previousStatus, err := h.store.UpdateManagedUserStatus(r.Context(), actor, actorScope, strings.TrimSpace(r.PathValue("id")), input.Status)
	if err != nil {
		switch {
		case errors.Is(err, ErrManagedUserNotFound):
			httpx.Error(w, r, http.StatusNotFound, "managed_user_not_found", "managed user not found")
		case errors.Is(err, ErrUserStatusForbidden):
			httpx.Error(w, r, http.StatusForbidden, "user_status_forbidden", "current identity cannot update this user")
		case errors.Is(err, ErrOrganizationScope):
			httpx.Error(w, r, http.StatusForbidden, "organization_scope_forbidden", "organization binding is outside the current data access scope")
		case errors.Is(err, ErrLastSchoolAdmin):
			httpx.Error(w, r, http.StatusConflict, "last_school_admin", "the last active school administrator cannot be disabled")
		default:
			httpx.Error(w, r, http.StatusInternalServerError, "user_status_update_failed", "failed to update user status")
		}
		return
	}
	if previousStatus != updated.Status {
		RecordAudit(r.Context(), h.store, AuditEvent{
			TenantID: actor.TenantID, ActorID: actor.ID, Action: "auth.user_status_updated",
			TargetType: "user", TargetID: updated.ID,
			BeforeValue: map[string]any{"status": previousStatus}, AfterValue: map[string]any{"status": updated.Status},
			Reason: "update organization user status", IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user": updated})
}
