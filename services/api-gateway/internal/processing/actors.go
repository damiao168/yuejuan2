package processing

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/workflowactors"
	"net/http"
)

func (s *PostgresStore) ListProcessingActors(ctx context.Context, tenantID, examID string) ([]workflowactors.Actor, error) {
	return workflowactors.List(ctx, s.db, tenantID, examID, "capture:manage")
}
func (h *Handler) ListProcessingActors(w http.ResponseWriter, r *http.Request) {
	user, ok := processingUser(w, r)
	if !ok {
		return
	}
	source, ok := h.service.store.(interface {
		ListProcessingActors(context.Context, string, string) ([]workflowactors.Actor, error)
	})
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "processing_directory_unavailable", "processing directory unavailable")
		return
	}
	values, err := source.ListProcessingActors(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"actors": values})
}
