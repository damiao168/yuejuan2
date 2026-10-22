package subjective

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/grading"
)

// ImageModelConfig 只在内部认证请求中传递；凭据不得写入运行、日志或浏览器响应。
// ConfigRevision 随学校选择和连接配置变化，由装配层从真实配置生成。
type ImageModelConfig struct {
	ConfigID           string `json:"-"`
	AdapterType        string `json:"adapter_type"`
	ProviderKey        string `json:"provider_key"`
	BaseURL            string `json:"base_url"`
	APIKey             string `json:"api_key"`
	ModelName          string `json:"model_name"`
	ModelVersion       string `json:"model_version"`
	ConfigRevision     string `json:"config_revision"`
	CapabilityRevision string `json:"capability_revision,omitempty"`
}

func bindImageGradeModel(value *Context, policy ModelPolicy, model *ImageModelConfig) error {
	if value.InputVersionID == "" {
		return nil
	}
	// OpenAI compatible 的调用事实必须关联真实托管连接；配置 revision 只是哈希，不能冒充连接 ID。
	// 完成回调重新比对原观察的模型绑定，换选或改配置后不能把旧结果记到新连接。
	if model == nil || model.ConfigID == "" || model.ModelName == "" || model.ModelVersion != policy.ModelVersion ||
		value.Observation == nil || imageModelBinding(model) != value.Observation.ModelBinding {
		return ErrWorkerResultVersionConflict
	}
	value.modelConfigID, value.modelName = model.ConfigID, model.ModelName
	return nil
}

func (h *Handler) bindWorkerImageGradeModel(ctx context.Context, tenantID string, value *Context, policy ModelPolicy) error {
	if value.InputVersionID == "" {
		return nil
	}
	var model *ImageModelConfig
	var err error
	if value.AIPlan == nil {
		if h.imageModelResolver == nil {
			return ErrWorkerResultVersionConflict
		}
		model, err = h.imageModelResolver(ctx, tenantID)
	} else {
		_, model, err = h.imageAdapterForPlan(ctx, tenantID, imagePlanForQuestion(value.AIPlan, value.Question.QuestionType))
	}
	if err != nil {
		return ErrWorkerResultVersionConflict
	}
	return bindImageGradeModel(value, policy, model)
}

// WithImageCropResolver 使图像 v3 独立于旧数学 v2 开关；裁图权限及哈希校验仍由同一个解析器执行。
func (h *Handler) WithImageCropResolver(crops *ActiveCropResolver) *Handler {
	h.activeCrops = crops
	return h
}

// WithImageModelResolver 使用学校明确选择的视觉理解模型，不回退本地文本或其它学校模型。
func (h *Handler) WithImageModelResolver(resolve func(context.Context, string) (*ImageModelConfig, error)) *Handler {
	h.imageModelResolver = resolve
	return h
}

func (h *Handler) WithImageModelByIDResolver(resolve func(context.Context, string, string, string) (*ImageModelConfig, error)) *Handler {
	h.imageModelByIDResolver = resolve
	return h
}

func imageModelBinding(config *ImageModelConfig) string {
	if config == nil {
		return ""
	}
	raw, _ := json.Marshal([]string{config.AdapterType, config.ProviderKey, strings.TrimRight(config.BaseURL, "/"), config.ModelName, config.ModelVersion, config.ConfigRevision})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (h *Handler) imageAdapter(ctx context.Context, tenantID string) (*HTTPAdapter, *ImageModelConfig, error) {
	return h.imageAdapterForPlan(ctx, tenantID, nil)
}

func (h *Handler) imageAdapterForPlan(ctx context.Context, tenantID string, plan *grading.GradingAIPlan) (*HTTPAdapter, *ImageModelConfig, error) {
	base, ok := h.adapter.(*HTTPAdapter)
	if !ok || h.imageModelResolver == nil {
		return nil, nil, &GradingAgentError{Code: "visual_model_unavailable"}
	}
	var model *ImageModelConfig
	var err error
	if plan == nil {
		model, err = h.imageModelResolver(ctx, tenantID)
	} else {
		identity := plan.SingleModel
		if plan.EffectiveMode == "dual" || plan.EffectiveMode == "dual_conditional_arbiter" {
			primary, exists := plan.ResolvedRoles["primary_a"]
			if !exists {
				return nil, nil, ErrWorkerResultVersionConflict
			}
			identity = &primary
		}
		if identity == nil || h.imageModelByIDResolver == nil {
			return nil, nil, &GradingAgentError{Code: "visual_model_not_configured"}
		}
		model, err = h.imageModelByIDResolver(ctx, tenantID, identity.ModelConfigID, identity.BindingRevision)
		if err == nil && (model == nil || model.ConfigRevision != identity.ConfigRevision || model.ModelName != identity.ModelName || model.ModelVersion != identity.ModelVersion) {
			return nil, nil, ErrWorkerResultVersionConflict
		}
	}
	if err != nil || model == nil || model.ConfigRevision == "" || model.ModelVersion == "" || model.APIKey == "" {
		return nil, nil, &GradingAgentError{Code: "visual_model_unavailable", cause: err}
	}
	selected := *base
	selected.policy.ModelVersion = model.ModelVersion
	// 图像比较使用独立提示词版本，不能借用旧文本提示词的评测或校准批准。
	selected.policy.PromptVersion = ImageComparisonPromptVersion
	selected.adapterType, selected.providerKey, selected.deploymentKey, selected.deploymentRegion = model.AdapterType, model.ProviderKey, model.ConfigRevision, "managed"
	return &selected, model, nil
}

// ProbeImageModel 只发送有随机色块的合成图；不读取学生作答。它会调用已选供应商一次，结果按配置版本缓存。
func (h *Handler) ProbeImageModel(ctx context.Context, tenantID, requestID string) (json.RawMessage, error) {
	adapter, model, err := h.imageAdapter(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return h.probeImageModelRequest(ctx, adapter, model, requestID, false)
}

func (h *Handler) ProbeExamImageModel(ctx context.Context, tenantID string, identity grading.GradingAIModel, requestID string, statusOnly bool) (grading.GradingVisionProbe, error) {
	result := grading.GradingVisionProbe{ModelConfigID: identity.ModelConfigID, VisionProbeStatus: "unavailable", VisionProbeReason: "visual_service_unavailable"}
	if h.imageModelByIDResolver == nil {
		return result, nil
	}
	model, err := h.imageModelByIDResolver(ctx, tenantID, identity.ModelConfigID, identity.BindingRevision)
	if err != nil || model == nil || model.ConfigRevision != identity.ConfigRevision {
		result.VisionProbeReason = "visual_model_changed"
		return result, nil
	}
	adapter, ok := h.adapter.(*HTTPAdapter)
	if !ok {
		return result, nil
	}
	raw, err := h.probeImageModelRequest(ctx, adapter, model, requestID, statusOnly)
	if err != nil {
		result.VisionProbeStatus, result.VisionProbeReason = "failed", "visual_model_probe_failed"
		return result, nil
	}
	var response struct {
		VisionProbeStatus string `json:"vision_probe_status"`
		VisionProbeReason string `json:"vision_probe_reason"`
		CacheHit          bool   `json:"cache_hit"`
		Capability        struct {
			VisualSupported bool `json:"visual_supported"`
		} `json:"capability"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return result, nil
	}
	result.VisionProbeStatus, result.VisionProbeReason, result.CacheHit = response.VisionProbeStatus, response.VisionProbeReason, response.CacheHit
	if result.VisionProbeStatus == "" && response.Capability.VisualSupported {
		result.VisionProbeStatus = "passed"
	}
	if result.VisionProbeStatus == "" {
		result.VisionProbeStatus, result.VisionProbeReason = "not_checked", "visual_model_probe_required"
	}
	return result, nil
}

func (h *Handler) probeImageModelRequest(ctx context.Context, adapter *HTTPAdapter, model *ImageModelConfig, requestID string, statusOnly bool) (json.RawMessage, error) {
	raw, err := json.Marshal(map[string]any{"request_id": requestID, "managed_model": model, "status_only": statusOnly})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, adapter.baseURL+"/grading/probe-image-v3", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+adapter.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", requestID)
	response, err := adapter.client.Do(req)
	if err != nil {
		return nil, &GradingAgentError{Code: "visual_model_probe_unavailable", cause: err}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxGradingAgentResponseBytes+1))
	if err != nil || len(body) > int(maxGradingAgentResponseBytes) {
		return nil, ErrInvalidModelOutput
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, parseAgentError(body, response.StatusCode, ImageProtocolVersion, requestID)
	}
	var result struct {
		SchemaVersion string `json:"schema_version"`
		RequestID     string `json:"request_id"`
		Capability    struct {
			ModelBinding    string `json:"model_binding"`
			VisualSupported bool   `json:"visual_supported"`
		} `json:"capability"`
	}
	if json.Unmarshal(body, &result) != nil || result.SchemaVersion != ImageProtocolVersion || result.RequestID != requestID || !statusOnly && !result.Capability.VisualSupported || result.Capability.ModelBinding != imageModelBinding(model) {
		return nil, ErrInvalidModelOutput
	}
	return body, nil
}
