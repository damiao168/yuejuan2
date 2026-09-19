package onboarding

import (
	"context"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

type ReadinessService interface {
	Readiness(ctx context.Context, actor Actor) (OnboardingReadiness, error)
}

type Handler struct{ service ReadinessService }

func NewHandler(service ReadinessService) *Handler { return &Handler{service: service} }

func (h *Handler) Readiness(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	if !auth.HasRole(user, "platform_admin") && !auth.HasRole(user, "tenant_admin") && !auth.HasRole(user, "school_admin") {
		httpx.Error(w, r, http.StatusForbidden, "onboarding_forbidden", "onboarding readiness requires an administrator role")
		return
	}
	scope, _ := auth.AccessScopeFromContext(r.Context())
	result, err := h.service.Readiness(r.Context(), Actor{
		ID: user.ID, TenantID: user.TenantID, Status: user.Status, Roles: append([]string(nil), user.Roles...),
		Scope: ResourceScope{TenantWide: scope.IsPlatform || scope.TenantWide, SchoolIDs: append([]string(nil), scope.SchoolIDs...), GradeIDs: append([]string(nil), scope.GradeIDs...), ClassIDs: append([]string(nil), scope.ClassIDs...), ExamIDs: append([]string(nil), scope.ExamIDs...)},
	})
	if errors.Is(err, ErrUnsupportedActor) {
		httpx.Error(w, r, http.StatusForbidden, "onboarding_forbidden", "onboarding readiness requires an administrator role")
		return
	}
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "onboarding_readiness_failed", "failed to read onboarding readiness")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}
