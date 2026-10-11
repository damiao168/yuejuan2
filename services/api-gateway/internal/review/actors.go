package review

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/workflowactors"
	"net/http"
)

func (s *PostgresStore) ListReviewActors(ctx context.Context, tenantID, examID string) ([]workflowactors.Actor, error) {
	return workflowactors.List(ctx, s.db, tenantID, examID, "review:work")
}
func (h *Handler) ListReviewActors(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	source, ok := h.store.(interface {
		ListReviewActors(context.Context, string, string) ([]workflowactors.Actor, error)
	})
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "review_directory_unavailable", "review directory unavailable")
		return
	}
	values, err := source.ListReviewActors(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "review_directory_failed", "review directory failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"actors": values})
}
