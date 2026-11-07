package modelgovernance

import (
	database "edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"net/http"
	"strings"
)

func (h *Handler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	// 目标租户已经授权；数据库租户上下文也要切换，否则 RLS 会隐藏其允许列表。
	item, err := h.store.GetPolicy(database.WithTenant(r.Context(), tenantID), tenantID)
	if err != nil {
		writeStoreError(w, r, err, "model_policy_read_failed", "failed to read model policy")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"policy": item})
}

func (h *Handler) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	var input PolicyUpdateInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	if input.AllowedDeployments == nil {
		input.AllowedDeployments = []string{}
	}
	if input.AllowedModelConfigIDs == nil {
		input.AllowedModelConfigIDs = []string{}
	}
	policy := PolicyFromUpdate(input)
	if strings.TrimSpace(input.Reason) == "" || input.ExpectedVersion < 1 || ValidateTenantPolicy(policy) != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_policy", "valid policy, expected_version and reason are required")
		return
	}
	if len(input.AllowedModelConfigIDs) > 0 && len(input.AllowedDeployments) > 0 {
		httpx.Error(w, r, http.StatusBadRequest, "mixed_model_policy_sources", "model config IDs and deployment keys cannot be mixed")
		return
	}
	r = r.WithContext(database.WithTenant(r.Context(), tenantID))
	if !h.validatePolicyModelSources(w, r, tenantID, input) {
		return
	}
	item, err := h.store.UpdatePolicy(r.Context(), tenantID, mustUser(r).ID, input)
	if err != nil {
		writeStoreError(w, r, err, "model_policy_update_failed", "failed to update model policy")
		return
	}
	h.auditAction(r, "model.policy_updated", "tenant_model_policy", item.ID, input.Reason,
		map[string]any{"tenant_id": tenantID, "mode": item.Mode, "external_enabled": item.ExternalEnabled, "text_export_enabled": item.TextExportEnabled, "image_export_enabled": item.ImageExportEnabled, "allowed_model_config_ids": item.AllowedModelConfigIDs, "version": item.Version})
	httpx.JSON(w, http.StatusOK, map[string]any{"policy": item})
}

// 先完成请求和租户校验，再检查该租户的白名单来源；写入时仍由 store 检查当前版本及配置状态。
func (h *Handler) validatePolicyModelSources(w http.ResponseWriter, r *http.Request, tenantID string, input PolicyUpdateInput) bool {
	if len(input.AllowedModelConfigIDs) > 0 {
		return h.validatePolicyManagedConfigs(w, r, tenantID, input.AllowedModelConfigIDs)
	}
	if len(input.AllowedDeployments) == 0 {
		return true
	}
	deployments, err := h.store.ListDeployments(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_deployment_list_failed", "failed to validate allowed deployments")
		return false
	}
	if !deploymentKeysExist(input.AllowedDeployments, deployments) {
		httpx.Error(w, r, http.StatusBadRequest, "unknown_model_deployment", "allowed_deployments contains an unknown tenant deployment")
		return false
	}
	return true
}

func (h *Handler) validatePolicyManagedConfigs(w http.ResponseWriter, r *http.Request, tenantID string, ids []string) bool {
	store, ready := h.managedAPIStore(w, r)
	if !ready {
		return false
	}
	configs, err := store.ListManagedAPIConfigs(r.Context(), tenantID)
	if err != nil {
		writeStoreError(w, r, err, "model_config_list_failed", "failed to validate allowed model configurations")
		return false
	}
	eligible := make(map[string]bool, len(configs))
	for _, config := range configs {
		eligible[config.ID] = config.TenantID == tenantID && config.Status == "active" &&
			config.LastCapabilityStatus == "success" && config.LastCapabilityVersion == "structured-json-v4"
	}
	for _, id := range ids {
		if !eligible[id] {
			httpx.Error(w, r, http.StatusBadRequest, "unknown_model_config", "allowed_model_config_ids contains an unavailable school model")
			return false
		}
	}
	return true
}
