package score

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"net/http"
)

func (s *PostgresStore) RecoverCommand(ctx context.Context, tenant, actor, id string) (commandreceipt.Receipt, error) {
	return commandreceipt.Recover(ctx, s.db, tenant, actor, id, "score.")
}
func (s *MemoryStore) RecoverCommand(ctx context.Context, tenant, actor, id string) (commandreceipt.Receipt, error) {
	return s.receipts.Recover(ctx, tenant, actor, id)
}
// RecoverCommand 只查询当前操作者的持久化回执，不重新执行确认或发布；客户端按返回状态决定后续动作。
func (h *Handler) RecoverCommand(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	result, err := h.store.RecoverCommand(r.Context(), user.TenantID, user.ID, r.PathValue("commandId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	w.Header().Set(httpx.CommandIDHeader, result.CommandID)
	w.Header().Set(httpx.OperationOutcomeHeader, result.Status)
	if result.Status == "processing" {
		w.Header().Set("Retry-After", "2")
	}
	httpx.JSON(w, http.StatusOK, result)
}
