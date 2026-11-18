package modelgovernance

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"errors"
	"net/http"
	"strings"
	"time"
)

func (h *Handler) ListManagedAPIConfigs(w http.ResponseWriter, r *http.Request) {
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	tenantID, ok := h.targetManagedTenant(w, r, r.URL.Query().Get("tenant_id"), "model:read")
	if !ok {
		return
	}
	if !managedSchoolTenant(w, r, tenantID) {
		return
	}
	items, err := store.ListManagedAPIConfigs(r.Context(), tenantID)
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"configs": items})
}

func (h *Handler) CreateManagedAPIConfig(w http.ResponseWriter, r *http.Request) {
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	var input ManagedAPIConfigInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetManagedTenant(w, r, input.TenantID, "model:managed_api:manage")
	if !ok {
		return
	}
	if !managedSchoolTenant(w, r, tenantID) {
		return
	}
	input.TenantID = tenantID
	normalized, err := normalizeManagedAPIInput(input, true)
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	prober := h.managedAPIProber
	if prober == nil {
		prober = NewHTTPManagedAPIProber(0)
	}
	connection := ManagedAPIConnection{Config: ManagedAPIConfig{
		TenantID: tenantID, ProviderKey: normalized.ProviderKey, DisplayName: normalized.DisplayName,
		AdapterType: normalized.AdapterType, BaseURL: normalized.BaseURL, ModelName: normalized.ModelName,
		ModelVersion: normalized.ModelVersion, Region: normalized.Region, Status: normalized.Status,
	}, APIKey: normalized.APIKey}
	result := managedQuickProbe(r.Context(), prober, connection)
	h.observeManagedProbe("create", result)
	if !result.OK {
		h.writeManagedValidationFailure(w, r, result)
		return
	}
	normalized.IsDefault = false
	normalized.InitialProbe = &result
	item, err := store.CreateManagedAPIConfig(r.Context(), tenantID, mustUser(r).ID, normalized)
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	h.auditAction(r, "model.managed_api_created", "managed_model_api_config", item.ID, "assign encrypted third-party model API to school",
		map[string]any{"tenant_id": tenantID, "provider_key": item.ProviderKey, "adapter_type": item.AdapterType, "base_url": item.BaseURL, "model_version": item.ModelVersion, "is_default": item.IsDefault, "latency_ms": result.LatencyMS})
	httpx.JSON(w, http.StatusCreated, map[string]any{"config": item, "validation": result})
}

// 自动配置先做租户范围校验，再探测并保存；只把脱敏结果返回给调用方，密钥始终留在存储层。
func (h *Handler) AutoCreateManagedAPIConfig(w http.ResponseWriter, r *http.Request) {
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	var input AutoManagedAPIConfigInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetManagedTenant(w, r, input.TenantID, "model:managed_api:manage")
	if !ok || !managedSchoolTenant(w, r, tenantID) {
		return
	}
	service := NewAutoManagedAPIConfigService(store, h.managedAPIProber)
	item, resolved, result, err := service.Create(r.Context(), tenantID, mustUser(r).ID, input)
	if err != nil {
		if errors.Is(err, ErrManagedProviderUnknown) {
			h.writeManagedProviderUnknown(w, r, input.ModelName)
			return
		}
		h.writeManagedAPIError(w, r, err)
		return
	}
	h.observeManagedProbe("auto_create", result)
	if !result.OK {
		h.writeManagedValidationFailure(w, r, result)
		return
	}
	h.auditAction(r, "model.managed_api_auto_configured", "managed_model_api_config", item.ID, "automatically validate and assign third-party model API to school",
		map[string]any{"tenant_id": tenantID, "provider_key": item.ProviderKey, "model_name": item.ModelName, "registry_version": resolved.RegistryVersion, "is_default": item.IsDefault, "latency_ms": result.LatencyMS})
	httpx.JSON(w, http.StatusCreated, map[string]any{"config": item, "validation": result})
}

func (h *Handler) UpdateManagedAPIConfig(w http.ResponseWriter, r *http.Request) {
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	var input ManagedAPIConfigUpdateInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetManagedTenant(w, r, r.URL.Query().Get("tenant_id"), "model:managed_api:manage")
	if !ok {
		return
	}
	if !managedSchoolTenant(w, r, tenantID) {
		return
	}
	if input.CredentialOnly {
		if strings.TrimSpace(input.APIKey) == "" || strings.TrimSpace(input.ClientExpectedUpdatedAt) == "" {
			h.writeManagedAPIError(w, r, ErrInvalidManagedConfig)
			return
		}
		items, err := store.ListManagedAPIConfigs(r.Context(), tenantID)
		if err != nil {
			h.writeManagedAPIError(w, r, err)
			return
		}
		current := managedAPIConfigByID(items, r.PathValue("id"))
		if current == nil {
			h.writeManagedAPIError(w, r, ErrNotFound)
			return
		}
		// 只轮换显式输入的密钥；名称、地址、模型、启停和默认用途都沿用此刻的服务端记录。
		input.DisplayName, input.AdapterType, input.BaseURL = current.DisplayName, current.AdapterType, current.BaseURL
		input.ModelName, input.ModelVersion, input.Region = current.ModelName, current.ModelVersion, current.Region
		input.Status, input.IsDefault = current.Status, current.IsDefault
		policy := current.ContextPolicy
		input.ContextPolicy = &policy
	}
	normalized, err := normalizeManagedAPIUpdate(input)
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	input = normalized
	current, connectionChanged, err := resolveManagedAPIUpdateTarget(r.Context(), store, tenantID, r.PathValue("id"), input)
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	// 普通编辑和密钥轮换都必须携带客户端读到的版本，不能给旧表单补上当前版本后覆盖。
	if strings.TrimSpace(input.ClientExpectedUpdatedAt) == "" {
		h.writeManagedAPIError(w, r, ErrInvalidManagedConfig)
		return
	}
	{
		expected, err := time.Parse(time.RFC3339Nano, input.ClientExpectedUpdatedAt)
		if err != nil {
			h.writeManagedAPIError(w, r, ErrInvalidManagedConfig)
			return
		}
		if !current.UpdatedAt.Equal(expected) {
			h.writeManagedAPIError(w, r, ErrManagedProbeStale)
			return
		}
	}
	// 存储层在探测后的写锁内再次比对；预先通过版本核对不代表轮换期间没有其他管理员修改。
	input.ExpectedUpdatedAt = current.UpdatedAt
	if connectionChanged {
		connection, result, connectionErr := h.probeManagedAPIUpdate(r, store, tenantID, current, input)
		if connectionErr != nil {
			h.writeManagedAPIError(w, r, connectionErr)
			return
		}
		h.observeManagedProbe("update", result)
		h.recordManagedProbeUsage(r.Context(), tenantID, connection, "update", result)
		if !result.OK {
			h.writeManagedValidationFailure(w, r, result)
			return
		}
		input.InitialProbe = &result
	}
	item, err := store.UpdateManagedAPIConfig(r.Context(), tenantID, r.PathValue("id"), input)
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	h.auditAction(r, "model.managed_api_updated", "managed_model_api_config", item.ID, "update school third-party model API assignment",
		map[string]any{"tenant_id": tenantID, "provider_key": item.ProviderKey, "adapter_type": item.AdapterType, "base_url": item.BaseURL, "model_version": item.ModelVersion, "status": item.Status, "is_default": item.IsDefault, "credential_rotated": strings.TrimSpace(input.APIKey) != ""})
	httpx.JSON(w, http.StatusOK, map[string]any{"config": item})
}

// 先查当前配置再判断备份能否升为默认；普通回复证据必须属于本次保存的连接身份。
func resolveManagedAPIUpdateTarget(ctx context.Context, store ManagedAPIConfigStore, tenantID, id string, input ManagedAPIConfigUpdateInput) (*ManagedAPIConfig, bool, error) {
	items, err := store.ListManagedAPIConfigs(ctx, tenantID)
	if err != nil {
		return nil, false, err
	}
	current := managedAPIConfigByID(items, id)
	if current == nil {
		return nil, false, ErrNotFound
	}
	// 版本和区域也属于存储会使能力证据失效的连接身份；默认模型不能跳过检测再保存。
	// 展示名称变化不影响连接，沿用原有验证结果。
	connectionChanged := current.AdapterType != input.AdapterType || current.BaseURL != input.BaseURL ||
		current.ModelName != input.ModelName || current.ModelVersion != input.ModelVersion ||
		current.Region != input.Region || strings.TrimSpace(input.APIKey) != ""
	textVerified := managedTextVerified(*current)
	if input.IsDefault && !current.IsDefault && (!textVerified || connectionChanged) {
		return nil, false, ErrManagedTextRequired
	}
	return current, connectionChanged, nil
}

func managedAPIConfigByID(items []ManagedAPIConfig, id string) *ManagedAPIConfig {
	for index := range items {
		if items[index].ID == id {
			return &items[index]
		}
	}
	return nil
}

func (h *Handler) DeleteManagedAPIConfig(w http.ResponseWriter, r *http.Request) {
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	tenantID, ok := h.targetManagedTenant(w, r, r.URL.Query().Get("tenant_id"), "model:managed_api:manage")
	if !ok || !managedSchoolTenant(w, r, tenantID) {
		return
	}
	id := r.PathValue("id")
	user, _ := auth.UserFromContext(r.Context())
	if err := store.DeleteManagedAPIConfig(r.Context(), tenantID, user.ID, id); err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	h.auditAction(r, "model.managed_api_deleted", "managed_model_api_config", id, "remove unused third-party model API from school",
		map[string]any{"tenant_id": tenantID})
	w.WriteHeader(http.StatusNoContent)
}
