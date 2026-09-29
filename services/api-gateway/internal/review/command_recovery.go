package review

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"net/http"
)

func (s *PostgresStore) RecoverCommand(ctx context.Context, tenant, actor, id string) (commandreceipt.Receipt, error) {
	return commandreceipt.Recover(ctx, s.db, tenant, actor, id, "review.")
}
func (s *MemoryStore) RecoverCommand(ctx context.Context, tenant, actor, id string) (commandreceipt.Receipt, error) {
	return s.receipts.Recover(ctx, tenant, actor, id)
}
func (h *Handler) RecoverCommand(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	result, err := h.store.RecoverCommand(r.Context(), user.TenantID, user.ID, r.PathValue("commandId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	// 盲测样本沿用普通复核入口，但回执由 seedHook 保存；普通回执不存在时再查它。
	if result.Status == "not_accepted" && h.seedHook != nil {
		result, err = h.seedHook.RecoverCommand(r.Context(), user.TenantID, user.ID, r.PathValue("commandId"))
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
	}
	w.Header().Set(httpx.CommandIDHeader, result.CommandID)
	w.Header().Set(httpx.OperationOutcomeHeader, result.Status)
	if result.Status == "processing" {
		w.Header().Set("Retry-After", "2")
	}
	httpx.JSON(w, http.StatusOK, result)
}
