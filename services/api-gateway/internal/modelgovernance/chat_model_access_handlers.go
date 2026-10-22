package modelgovernance

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	database "edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"errors"
	"github.com/google/uuid"
	"net/http"
)

func (h *Handler) GetPlatformChatModelAccess(w http.ResponseWriter, r *http.Request) {
	if !chatAccessPlatformActor(w, r) {
		return
	}
	tenantID, ok := h.targetManagedTenant(w, r, r.URL.Query().Get("tenant_id"), "model:read")
	if !ok || !managedSchoolTenant(w, r, tenantID) {
		return
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		h.writeManagedAPIError(w, r, ErrInvalidManagedConfig)
		return
	}
	store, ok := h.store.(ChatModelAccessStore)
	if !ok {
		h.writeManagedAPIError(w, r, ErrManagedConfigUnavailable)
		return
	}
	access, err := store.GetChatModelAccess(database.WithTenant(r.Context(), tenantID), tenantID)
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	// 删除过的配置不再出现在开放表单；下次保存会清掉悬空编号，避免阻塞其他模型的开放。
	if configs, ok := h.store.(ManagedAPIConfigStore); ok {
		items, listErr := configs.ListManagedAPIConfigs(database.WithTenant(r.Context(), tenantID), tenantID)
		if listErr != nil {
			h.writeManagedAPIError(w, r, listErr)
			return
		}
		visible := []string{}
		for _, item := range items {
			if contains(access.ModelIDs, item.ID) {
				visible = append(visible, item.ID)
			}
		}
		access.ModelIDs = visible
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"access": access})
}
func (h *Handler) SavePlatformChatModelAccess(w http.ResponseWriter, r *http.Request) {
	if !chatAccessPlatformActor(w, r) {
		return
	}
	var input struct {
		TenantID string `json:"tenant_id"`
		ChatModelAccessInput
	}
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	// 缺少字段或 null 不能解释成撤销全部；只有调用方明确提交 [] 才允许清空开放范围。
	if input.ModelIDs == nil {
		h.writeManagedAPIError(w, r, ErrInvalidManagedConfig)
		return
	}
	tenantID, ok := h.targetManagedTenant(w, r, input.TenantID, "model:managed_api:manage")
	if !ok || !managedSchoolTenant(w, r, tenantID) {
		return
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		h.writeManagedAPIError(w, r, ErrInvalidManagedConfig)
		return
	}
	store, ok := h.store.(ChatModelAccessStore)
	if !ok {
		h.writeManagedAPIError(w, r, ErrManagedConfigUnavailable)
		return
	}
	access, err := store.SaveChatModelAccess(database.WithTenant(r.Context(), tenantID), tenantID, mustUser(r).ID, input.ChatModelAccessInput)
	if errors.Is(err, ErrChatAccessChanged) {
		httpx.Error(w, r, http.StatusConflict, "chat_model_access_changed", "对话开放设置已变更，请刷新后重试")
		return
	}
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	h.auditAction(r, "model.chat_access_updated", "tenant_chat_model_access", tenantID, "configure school chat access", map[string]any{"tenant_id": tenantID, "model_ids": access.ModelIDs})
	httpx.JSON(w, http.StatusOK, map[string]any{"access": access})
}

func chatAccessPlatformActor(w http.ResponseWriter, r *http.Request) bool {
	user := mustUser(r)
	if user.TenantID != auth.PlatformTenantID || !contains(user.Roles, "platform_admin") {
		httpx.Error(w, r, http.StatusForbidden, "forbidden", "只有平台管理员可以开放学校对话模型")
		return false
	}
	return true
}

// 学校只取得已开放模型的公开名称与资格；不返回端点、密钥或其他学校配置。
func (h *Handler) getChatModels(w http.ResponseWriter, r *http.Request) {
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	tenantID := mustUser(r).TenantID
	configs, err := store.ListManagedAPIConfigs(r.Context(), tenantID)
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	access := initialChatAccess(configs)
	if accessStore, ok := store.(ChatModelAccessStore); ok {
		access, err = accessStore.GetChatModelAccess(r.Context(), tenantID)
		if err != nil {
			h.writeManagedAPIError(w, r, err)
			return
		}
	}
	models := []ManagedChatModelStatus{}
	selected := ManagedChatModelStatus{Message: "学校尚未开放可用的对话模型，请联系平台管理员"}
	for _, config := range configs {
		if config.TenantID != tenantID || !contains(access.ModelIDs, config.ID) {
			continue
		}
		status := ManagedChatModelStatus{ID: config.ID, DisplayName: config.DisplayName, ModelName: config.ModelName, ProviderKey: config.ProviderKey, Available: chatConfigEligible(config), Message: "模型暂不可用，请联系平台管理员"}
		if status.Available {
			status.Message = "当前模型可用"
		}
		models = append(models, status)
		if status.Available && (config.IsDefault || selected.ID == "") {
			selected = status
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"model": selected, "models": models})
}
