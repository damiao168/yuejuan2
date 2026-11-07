package modelgovernance

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"errors"
	"net/http"
	"strings"
)

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
	tenantID, ok := h.targetManagedTenant(w, r, input.TenantID, "model:managed_api:manage")
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
	h.observeManagedProbe("validate", result)
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
	tenantID, ok := h.targetManagedTenant(w, r, input.TenantID, "model:managed_api:manage")
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
	h.observeManagedProbe("models", result)
	if !result.OK {
		h.auditAction(r, "model.managed_api_model_discovery_failed", "managed_model_api_config", "", "model discovery failed without exposing credential or response",
			managedProbeAuditFields(tenantID, resolved.Provider.Key, result))
		h.writeManagedValidationFailure(w, r, result)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"provider": resolved.Provider, "models": list.Models, "latency_ms": list.LatencyMS,
	})
}

func (h *Handler) probeManagedAPIUpdate(r *http.Request, store ManagedAPIConfigStore, tenantID string, current *ManagedAPIConfig, input ManagedAPIConfigUpdateInput) (ManagedAPIConnection, ManagedAPIProbeResult, error) {
	connection, err := store.GetManagedAPIConnection(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		return ManagedAPIConnection{}, ManagedAPIProbeResult{}, err
	}
	// 元数据查询和密钥读取之间也可能被其他请求更新，不能检测混合版本的连接。
	if !connection.Config.UpdatedAt.Equal(current.UpdatedAt) {
		return ManagedAPIConnection{}, ManagedAPIProbeResult{}, ErrManagedProbeStale
	}
	// 同域改路径仍使用保存的密钥；跨域或换端口必须显式输入，不能把解密后的旧密钥自动发送到新服务。
	if input.APIKey == "" && !sameManagedAPIOrigin(connection.Config.BaseURL, input.BaseURL) {
		return ManagedAPIConnection{}, ManagedAPIProbeResult{}, ErrManagedCredentialOriginChanged
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
	// 当前默认模型更换连接时，只需证明文本生成可用；评分角色的结构化门槛由角色绑定单独校验。
	if current.IsDefault && input.IsDefault {
		result = managedTextProbe(r.Context(), prober, connection)
	} else {
		result = managedQuickProbe(r.Context(), prober, connection)
	}
	return connection, result, nil
}

func (h *Handler) ProbeManagedAPIConfig(w http.ResponseWriter, r *http.Request) {
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	tenantID, ok := h.targetManagedTenant(w, r, r.URL.Query().Get("tenant_id"), "model:managed_api:manage")
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
	if mode != "quick" && mode != "text" && mode != "structured" && mode != "capability" {
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
		result.ProbeMode = string(ManagedAPIProbeQuick)
	} else if mode == "text" {
		result = managedTextProbe(r.Context(), prober, connection)
	} else if !force && connection.Config.LastCapabilityStatus == "success" && connection.Config.LastCapabilityVersion == ManagedCapabilityProbeVersion {
		result = reusedManagedCapabilityProbe(connection)
	} else {
		result = prober.Probe(r.Context(), connection)
	}
	result.ProbeMode = mode
	if !result.Reused {
		h.observeManagedProbe(mode, result)
	}
	h.recordManagedProbeUsage(r.Context(), tenantID, connection, "manual", result)
	item, recordErr := store.RecordManagedAPIProbe(r.Context(), tenantID, connection.Config.ID, connection.Config.UpdatedAt, result)
	if recordErr != nil {
		h.writeManagedAPIError(w, r, recordErr)
		return
	}
	h.auditAction(r, "model.managed_api_probed", "managed_model_api_config", item.ID, "test school third-party model API connection without exposing credential",
		map[string]any{"tenant_id": tenantID, "provider_key": managedProbeProviderCategory(item.ProviderKey), "ok": result.OK, "probe_mode": result.ProbeMode,
			"generated_request": result.GeneratedRequest, "reused": result.Reused, "coalesced": result.Coalesced, "status_code": result.StatusCode,
			"latency_ms": result.LatencyMS, "total_tokens": result.Usage.TotalTokens, "error_code": managedProbeErrorCategory(result.ErrorCode, result.OK),
			"attempts": result.ConnectionDiagnostic.Attempts, "dns_ms": result.ConnectionDiagnostic.DNSMS,
			"connect_ms": result.ConnectionDiagnostic.ConnectMS, "tls_ms": result.ConnectionDiagnostic.TLSMS,
			"ttfb_ms": result.ConnectionDiagnostic.TTFBMS, "failure_stage": managedProbeFailureStageCategory(result.ConnectionDiagnostic.FailureStage),
			"finish_reason": result.Diagnostic.FinishReason, "response_format": result.Diagnostic.ResponseFormat,
			"content_length": result.Diagnostic.ContentLength, "content_sha256": result.Diagnostic.ContentSHA256})
	httpx.JSON(w, http.StatusOK, map[string]any{"result": result, "config": item})
}
