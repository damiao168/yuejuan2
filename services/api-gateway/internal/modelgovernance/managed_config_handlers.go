package modelgovernance

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"errors"
	"net/http"
	"strings"
)

// ManagedAPIProbeObserver 只接收类别和耗时；实现不得将模型名、端点 URL 或凭据值作为指标标签。
type ManagedAPIProbeObserver interface {
	ObserveManagedModelProbe(operation, provider, errorCode, failureStage string, ok bool)
}

func (h *Handler) WithManagedAPIProbeObserver(observer ManagedAPIProbeObserver) *Handler {
	h.managedAPIProbeObserver = observer
	return h
}

func (h *Handler) observeManagedProbe(operation string, result ManagedAPIProbeResult) {
	if h.managedAPIProbeObserver == nil {
		return
	}
	h.managedAPIProbeObserver.ObserveManagedModelProbe(operation, result.Provider, result.ErrorCode,
		result.ConnectionDiagnostic.FailureStage, result.OK)
}

func (h *Handler) recordManagedProbeUsage(ctx context.Context, tenantID string, connection ManagedAPIConnection, operation string, result ManagedAPIProbeResult) {
	if !result.GeneratedRequest || result.Reused {
		return
	}
	status := "failed"
	if result.OK {
		status = "succeeded"
	}
	httpRequestID := logger.RequestID(ctx)
	usageRequestID := result.generationRequestID
	// 合并请求的所有调用者（含发起者）都会标记 Coalesced，不能据此全部跳过；账本按实际生成去重。
	// 自定义 prober 没有生成身份时沿用原请求键，HTTP 来源继续留在元数据与各自审计中。
	if usageRequestID == "" {
		usageRequestID = httpRequestID
	}
	h.recordUsage(ctx, ModelUsageEvent{
		TenantID: tenantID, RequestID: usageRequestID, Feature: "model_probe",
		ProviderKey: connection.Config.ProviderKey, ModelName: connection.Config.ModelName,
		InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens,
		CachedInputTokens: result.Usage.CachedInputTokens, ReasoningTokens: result.Usage.ReasoningTokens,
		TotalTokens: result.Usage.TotalTokens, Status: status,
		Metadata: map[string]any{"operation": operation, "probe_mode": result.ProbeMode, "error_code": result.ErrorCode, "http_request_id": httpRequestID},
	})
}

func (h *Handler) WithManagedAPIProber(prober ManagedAPIProber) *Handler {
	h.managedAPIProber = prober
	return h
}

func managedProbeAuditFields(tenantID, provider string, result ManagedAPIProbeResult) map[string]any {
	diagnostic := result.ConnectionDiagnostic
	return map[string]any{
		"tenant_id": tenantID, "provider_key": managedProbeProviderCategory(provider),
		"ok": result.OK, "probe_mode": result.ProbeMode,
		"error_code": managedProbeErrorCategory(result.ErrorCode, result.OK),
		"latency_ms": result.LatencyMS, "attempts": diagnostic.Attempts,
		"dns_ms": diagnostic.DNSMS, "connect_ms": diagnostic.ConnectMS,
		"tls_ms": diagnostic.TLSMS, "ttfb_ms": diagnostic.TTFBMS,
		"failure_stage": managedProbeFailureStageCategory(diagnostic.FailureStage),
	}
}

func managedProbeProviderCategory(value string) string {
	switch value {
	case "deepseek", "aliyun", "openai", "zhipu", "moonshot", "anthropic", "gemini", "custom":
		return value
	default:
		return "other"
	}
}

func managedProbeErrorCategory(value string, ok bool) string {
	if ok {
		return "none"
	}
	switch value {
	case "provider_timeout", "provider_unavailable", "provider_rate_limited", "provider_error",
		"provider_invalid_response", "credential_invalid", "model_permission_denied", "model_not_found",
		"invalid_endpoint", "invalid_json", "no_choices", "empty_content", "output_truncated", "content_filtered",
		"schema_mismatch", "semantic_mismatch", "response_format_unsupported", "text_probe_unsupported",
		"quick_probe_unsupported":
		return value
	default:
		return "other"
	}
}

func managedProbeFailureStageCategory(value string) string {
	switch value {
	case "dns", "connect", "tls", "ttfb", "response", "body", "request":
		return value
	default:
		return "none"
	}
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

func managedTextProbe(ctx context.Context, prober ManagedAPIProber, connection ManagedAPIConnection) ManagedAPIProbeResult {
	if textProber, ok := prober.(ManagedAPITextProber); ok {
		result := textProber.ProbeText(ctx, connection)
		result.ProbeMode = string(ManagedAPIProbeText)
		return result
	}
	// 不支持独立文本探测时直接说明限制，不能偷偷改发更严格的 JSON 生成请求。
	return failedManagedProbe(ManagedAPIProbeResult{
		ProbeMode: "text", Provider: connection.Config.ProviderKey, Model: connection.Config.ModelName,
	}, "text_probe_unsupported", "当前适配器不支持普通回复检测", "text")
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
	case errors.Is(err, ErrManagedConfigInUse):
		httpx.Error(w, r, http.StatusConflict, "managed_model_in_use", "该模型正在被阅卷角色、试卷解析或治理配置引用，请先调整用途后删除")
	case errors.Is(err, ErrManagedCapabilityRequired):
		httpx.Error(w, r, http.StatusConflict, "managed_model_capability_required", "该功能需要先通过结构化输出检测")
	case errors.Is(err, ErrManagedTextRequired):
		httpx.Error(w, r, http.StatusConflict, "managed_model_text_required", "请先通过普通回复检测，再设为当前使用")
	case errors.Is(err, ErrManagedProbeStale):
		httpx.Error(w, r, http.StatusConflict, "managed_model_probe_stale", "检测期间配置已发生变化，请重新检测")
	case errors.Is(err, ErrManagedCredentialSourceDisabled):
		httpx.Error(w, r, http.StatusConflict, "managed_model_credential_source_disabled", "密钥来源配置已停用，请先启用该配置，再获取模型列表或添加模型")
	case errors.Is(err, ErrManagedCredentialOriginChanged):
		httpx.Error(w, r, http.StatusBadRequest, "managed_model_credential_origin_changed", "更换 API 域名或端口时，请重新输入 API Key（可以使用原密钥）")
	case errors.Is(err, ErrAgentContextPolicy):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_context_policy", "模型上下文预算无效，请核对窗口、输入和输出上限，并预留安全余量")
	case errors.Is(err, ErrInvalidManagedConfig):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_managed_model_api", "第三方模型 API 配置不完整或不安全")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "managed_model_api_not_found", "第三方模型 API 配置不存在")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "managed_model_api_conflict", "模型配置发生冲突，请刷新后重试")
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
