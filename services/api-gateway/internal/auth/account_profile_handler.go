package auth

import (
	"context"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

type accountProfileReader interface {
	ReadAccountProfile(context.Context, string, string) (AccountProfile, error)
}

type AccountProfile struct {
	ID               string   `json:"id"`
	Username         string   `json:"username"`
	DisplayName      string   `json:"display_name"`
	PhoneMasked      string   `json:"phone_masked"`
	EmployeeNo       string   `json:"employee_no"`
	SchoolID         string   `json:"school_id"`
	TeachingSubjects []string `json:"teaching_subjects"`
	Roles            []string `json:"roles"`
}

// AccountProfile 只读取已认证账号本人；资料中的任教学科不能在此变成自行授权的写入口。
func (h *Handler) AccountProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	actor, ok := UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	repository, ok := h.store.(accountProfileReader)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "account_profile_unavailable", "account profile is temporarily unavailable")
		return
	}
	profile, err := repository.ReadAccountProfile(r.Context(), actor.TenantID, actor.ID)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		} else {
			httpx.Error(w, r, http.StatusServiceUnavailable, "account_profile_unavailable", "account profile is temporarily unavailable")
		}
		return
	}
	// 明确投影字段，不能直接序列化含密码哈希、完整手机号和安全版本的仓储对象。
	if profile.TeachingSubjects == nil {
		profile.TeachingSubjects = []string{}
	}
	profile.Roles = actor.Roles
	httpx.JSON(w, http.StatusOK, map[string]any{"profile": profile})
}
