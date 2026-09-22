package modelgovernance

import (
	"errors"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func (h *Handler) WithManagedAPIProber(prober ManagedAPIProber) *Handler {
	h.managedAPIProber = prober
	return h
}

func (h *Handler) ListManagedAPIConfigs(w http.ResponseWriter, r *http.Request) {
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
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
	tenantID, ok := h.targetTenant(w, r, input.TenantID)
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

func (h *Handler) ResolveManagedAPIProvider(w http.ResponseWriter, r *http.Request) {
	var input ResolveManagedProviderInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	service := NewAutoManagedAPIConfigService(nil, h.managedAPIProber)
	resolved, err := service.Resolve(input)
	if err != nil {
		if errors.Is(err, ErrManagedProviderUnknown) {
			h.writeManagedProviderUnknown(w, r, input.ModelName)
			return
		}
		h.writeManagedAPIError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, resolved)
}

func (h *Handler) ValidateManagedAPIConfig(w http.ResponseWriter, r *http.Request) {
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	var input AutoManagedAPIConfigInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetTenant(w, r, input.TenantID)
	if !ok || !managedSchoolTenant(w, r, tenantID) {
		return
	}
	service := NewAutoManagedAPIConfigService(store, h.managedAPIProber)
	resolved, result, err := service.Validate(r.Context(), tenantID, input)
	if err != nil {
		if errors.Is(err, ErrManagedProviderUnknown) {
			h.writeManagedProviderUnknown(w, r, input.ModelName)
			return
		}
		h.writeManagedAPIError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"provider": resolved.Provider, "validation": result})
}

func (h *Handler) ListAvailableManagedAPIModels(w http.ResponseWriter, r *http.Request) {
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	var input AutoManagedAPIConfigInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetTenant(w, r, input.TenantID)
	if !ok || !managedSchoolTenant(w, r, tenantID) {
		return
	}
	service := NewAutoManagedAPIConfigService(store, h.managedAPIProber)
	resolved, list, result, err := service.ListModels(r.Context(), tenantID, input)
	if err != nil {
		if errors.Is(err, ErrManagedProviderUnknown) {
			h.writeManagedProviderUnknown(w, r, input.ModelName)
			return
		}
		h.writeManagedAPIError(w, r, err)
		return
	}
	if !result.OK {
		h.writeManagedValidationFailure(w, r, result)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"provider": resolved.Provider, "models": list.Models, "latency_ms": list.LatencyMS,
	})
}

func (h *Handler) AutoCreateManagedAPIConfig(w http.ResponseWriter, r *http.Request) {
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	var input AutoManagedAPIConfigInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetTenant(w, r, input.TenantID)
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
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	if !managedSchoolTenant(w, r, tenantID) {
		return
	}
	normalized, err := normalizeManagedAPIUpdate(input)
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	input = normalized
	items, err := store.ListManagedAPIConfigs(r.Context(), tenantID)
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	var current *ManagedAPIConfig
	for index := range items {
		if items[index].ID == r.PathValue("id") {
			current = &items[index]
			break
		}
	}
	if current == nil {
		h.writeManagedAPIError(w, r, ErrNotFound)
		return
	}
	connectionChanged := current.AdapterType != input.AdapterType || current.BaseURL != input.BaseURL ||
		current.ModelName != input.ModelName || strings.TrimSpace(input.APIKey) != ""
	capabilityVerified := current.LastCapabilityStatus == "success" && current.LastCapabilityVersion == ManagedCapabilityProbeVersion
	if input.IsDefault && !current.IsDefault && (!capabilityVerified || connectionChanged) {
		h.writeManagedAPIError(w, r, ErrManagedCapabilityRequired)
		return
	}
	if connectionChanged {
		connection, connectionErr := store.GetManagedAPIConnection(r.Context(), tenantID, r.PathValue("id"))
		if connectionErr != nil {
			h.writeManagedAPIError(w, r, connectionErr)
			return
		}
		connection.Config.DisplayName = input.DisplayName
		connection.Config.AdapterType = input.AdapterType
		connection.Config.BaseURL = input.BaseURL
		connection.Config.ModelName = input.ModelName
		connection.Config.ModelVersion = input.ModelVersion
		connection.Config.Region = input.Region
		if input.APIKey != "" {
			connection.APIKey = input.APIKey
		}
		prober := h.managedAPIProber
		if prober == nil {
			prober = NewHTTPManagedAPIProber(0)
		}
		var result ManagedAPIProbeResult
		// Preserve the old working connection when the current model is edited:
		// only a successful full check may replace it.
		if current.IsDefault && input.IsDefault {
			result = prober.Probe(r.Context(), connection)
			if result.ProbeMode == "" {
				result.ProbeMode = "capability"
			}
		} else {
			result = managedQuickProbe(r.Context(), prober, connection)
		}
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

func (h *Handler) DeleteManagedAPIConfig(w http.ResponseWriter, r *http.Request) {
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok || !managedSchoolTenant(w, r, tenantID) {
		return
	}
	id := r.PathValue("id")
	if err := store.DeleteManagedAPIConfig(r.Context(), tenantID, id); err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	h.auditAction(r, "model.managed_api_deleted", "managed_model_api_config", id, "remove unused third-party model API from school",
		map[string]any{"tenant_id": tenantID})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ProbeManagedAPIConfig(w http.ResponseWriter, r *http.Request) {
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	if !managedSchoolTenant(w, r, tenantID) {
		return
	}
	connection, err := store.GetManagedAPIConnection(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	prober := h.managedAPIProber
	if prober == nil {
		prober = NewHTTPManagedAPIProber(0)
	}
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	if mode == "" {
		mode = "quick"
	}
	if mode != "quick" && mode != "capability" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_probe_mode", "检测模式无效")
		return
	}
	force := r.URL.Query().Get("force") == "true"
	var result ManagedAPIProbeResult
	if mode == "quick" {
		if quickProber, quickOK := prober.(ManagedAPIQuickProber); quickOK {
			result = quickProber.ProbeQuick(r.Context(), connection)
		} else {
			result = failedManagedProbe(ManagedAPIProbeResult{
				ProbeMode: "quick", Provider: connection.Config.ProviderKey, Model: connection.Config.ModelName,
			}, "quick_probe_unsupported", "当前适配器不支持零生成 Token 快速检查", "credential")
		}
		result.ProbeMode = "quick"
	} else if !force && connection.Config.LastCapabilityStatus == "success" && connection.Config.LastCapabilityVersion == ManagedCapabilityProbeVersion {
		result = reusedManagedCapabilityProbe(connection)
	} else {
		result = prober.Probe(r.Context(), connection)
		result.ProbeMode = "capability"
	}
	item, recordErr := store.RecordManagedAPIProbe(r.Context(), tenantID, connection.Config.ID, result)
	if recordErr != nil {
		h.writeManagedAPIError(w, r, recordErr)
		return
	}
	h.auditAction(r, "model.managed_api_probed", "managed_model_api_config", item.ID, "test school third-party model API connection without exposing credential",
		map[string]any{"tenant_id": tenantID, "provider_key": item.ProviderKey, "ok": result.OK, "probe_mode": result.ProbeMode,
			"generated_request": result.GeneratedRequest, "reused": result.Reused, "coalesced": result.Coalesced, "status_code": result.StatusCode,
			"latency_ms": result.LatencyMS, "total_tokens": result.Usage.TotalTokens, "error_code": result.ErrorCode,
			"finish_reason": result.Diagnostic.FinishReason, "response_format": result.Diagnostic.ResponseFormat,
			"content_length": result.Diagnostic.ContentLength, "content_sha256": result.Diagnostic.ContentSHA256})
	httpx.JSON(w, http.StatusOK, map[string]any{"result": result, "config": item})
}

func reusedManagedCapabilityProbe(connection ManagedAPIConnection) ManagedAPIProbeResult {
	return ManagedAPIProbeResult{
		OK: true, ProbeMode: "capability", Reused: true,
		Provider: connection.Config.ProviderKey, Model: connection.Config.ModelName,
		Message:         "已复用有效的结构化能力验证，本次未调用模型生成",
		CredentialCheck: ManagedAPICheckResult{OK: true, Code: "reused", Message: "沿用已验证配置"},
		ModelCheck:      ManagedAPICheckResult{OK: true, Code: "reused", Message: "沿用已验证模型"},
		CapabilityCheck: ManagedAPICheckResult{OK: true, Code: "reused", Message: "沿用结构化能力验证"},
	}
}

func managedSchoolTenant(w http.ResponseWriter, r *http.Request, tenantID string) bool {
	if tenantID == "" || tenantID == auth.PlatformTenantID {
		httpx.Error(w, r, http.StatusBadRequest, "school_tenant_required", "请选择要配置的学校")
		return false
	}
	return true
}

func (h *Handler) managedAPIStore(w http.ResponseWriter, r *http.Request) (ManagedAPIConfigStore, bool) {
	store, ok := h.store.(ManagedAPIConfigStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "managed_model_api_unavailable", "第三方模型 API 配置暂不可用")
		return nil, false
	}
	return store, true
}

func (h *Handler) writeManagedAPIError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrManagedProviderUnknown):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "provider_unknown", "暂时无法识别模型供应商")
	case errors.Is(err, ErrManagedDefaultMutation):
		httpx.Error(w, r, http.StatusConflict, "managed_model_current_active_required", "当前使用的模型必须保持启用，请先切换模型或切回本地模型")
	case errors.Is(err, ErrManagedCapabilityRequired):
		httpx.Error(w, r, http.StatusConflict, "managed_model_capability_required", "请先通过完整能力检测，再设为当前使用")
	case errors.Is(err, ErrInvalidManagedConfig):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_managed_model_api", "第三方模型 API 配置不完整或不安全")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "managed_model_api_not_found", "第三方模型 API 配置不存在")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "managed_model_api_conflict", "该学校已配置相同供应商和模型")
	case errors.Is(err, ErrManagedConfigUnavailable):
		httpx.Error(w, r, http.StatusServiceUnavailable, "managed_model_api_unavailable", "第三方模型 API 密钥服务暂不可用")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "managed_model_api_failed", "第三方模型 API 配置操作失败")
	}
}

func (h *Handler) writeManagedProviderUnknown(w http.ResponseWriter, r *http.Request, modelName string) {
	registry := NewProviderRegistry()
	w.Header().Set(httpx.ErrorCodeHeader, "provider_unknown")
	httpx.JSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error":      map[string]string{"code": "provider_unknown", "message": "暂时无法识别模型供应商"},
		"model_name": strings.TrimSpace(modelName),
		"providers":  registry.Providers(),
	})
}

func (h *Handler) writeManagedValidationFailure(w http.ResponseWriter, r *http.Request, result ManagedAPIProbeResult) {
	code := result.ErrorCode
	if code == "" {
		code = "managed_model_validation_failed"
	}
	w.Header().Set(httpx.ErrorCodeHeader, code)
	httpx.JSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error":      map[string]string{"code": code, "message": result.Message},
		"validation": result,
	})
}
