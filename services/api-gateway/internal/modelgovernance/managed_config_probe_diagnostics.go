package modelgovernance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type managedOpenAIProbeUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
}

func (usage managedOpenAIProbeUsage) normalized() ManagedAPIProbeUsage {
	cached := usage.PromptTokensDetails.CachedTokens
	if usage.PromptCacheHitTokens > cached {
		cached = usage.PromptCacheHitTokens
	}
	total := usage.TotalTokens
	if total == 0 {
		total = usage.PromptTokens + usage.CompletionTokens
	}
	return ManagedAPIProbeUsage{
		InputTokens: usage.PromptTokens, CachedInputTokens: cached,
		OutputTokens: usage.CompletionTokens, ReasoningTokens: usage.CompletionTokensDetails.ReasoningTokens,
		TotalTokens: total,
	}
}

func managedCapabilityPayload(connection ManagedAPIConnection) map[string]any {
	payload := map[string]any{
		"model":           connection.Config.ModelName,
		"messages":        managedCapabilityMessages(),
		"max_tokens":      managedCapabilityMaxTokens,
		"response_format": map[string]string{"type": "json_object"},
		"temperature":     0,
		"stream":          false,
	}
	applyManagedThinkingPolicy(payload, connection)
	return payload
}

// 已知可关闭思考的模型使用相同策略，避免极短探测只有推理 Token 而没有最终回复。
func applyManagedThinkingPolicy(payload map[string]any, connection ManagedAPIConnection) {
	switch NewProviderRegistry().ProbePolicy(connection.Config.ProviderKey) {
	case "deepseek_hybrid":
		if managedModelSupportsThinkingDisable(connection.Config.ProviderKey, connection.Config.ModelName) {
			payload["thinking"] = map[string]string{"type": "disabled"}
		}
	case "qwen_hybrid":
		if managedModelSupportsThinkingDisable(connection.Config.ProviderKey, connection.Config.ModelName) {
			payload["enable_thinking"] = false
		}
	}
}

func managedCapabilityMessages() []map[string]string {
	return []map[string]string{
		{"role": "system", "content": "只输出合法 JSON，不要解释，不要使用 Markdown。"},
		{"role": "user", "content": "只输出一个 JSON 对象，必须包含 question_no（字符串）和 max_score（数字）；字段值可以任意，不要输出其他说明。"},
	}
}

func managedModelSupportsThinkingDisable(providerKey, modelName string) bool {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	switch providerKey {
	case "deepseek":
		return !strings.Contains(modelName, "reasoner")
	case "aliyun":
		return strings.HasPrefix(modelName, "qwen3") && !strings.Contains(modelName, "thinking")
	default:
		return false
	}
}

func managedCapabilityFlightKey(connection ManagedAPIConnection) string {
	mac := hmac.New(sha256.New, []byte(connection.APIKey))
	_, _ = io.WriteString(mac, strings.Join([]string{
		connection.Config.ProviderKey, connection.Config.AdapterType,
		strings.TrimRight(connection.Config.BaseURL, "/"), connection.Config.ModelName,
		connection.Config.TenantID, connection.Config.ID, connection.Config.ModelVersion, connection.Config.Region,
		connection.Config.UpdatedAt.UTC().Format(time.RFC3339Nano), ManagedCapabilityProbeVersion,
	}, "\x00"))
	return hex.EncodeToString(mac.Sum(nil))
}

func managedCapabilityDiagnostic(content, finishReason, responseFormat string) ManagedAPIProbeDiagnostic {
	trimmed := strings.TrimSpace(content)
	hash := sha256.Sum256([]byte(trimmed))
	return ManagedAPIProbeDiagnostic{
		FinishReason: strings.TrimSpace(finishReason), ResponseFormat: responseFormat,
		ContentLength: utf8.RuneCountInString(trimmed), ContentSHA256: hex.EncodeToString(hash[:]),
		ContentPreview: boundedManagedContentPreview(trimmed),
	}
}

func boundedManagedContentPreview(content string) string {
	content = strings.Map(func(value rune) rune {
		if unicode.IsControl(value) && value != '\n' && value != '\t' {
			return -1
		}
		return value
	}, content)
	runes := []rune(content)
	if len(runes) > 256 {
		return string(runes[:256]) + "…"
	}
	return content
}

func normalizeManagedCapabilityJSON(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") && strings.HasSuffix(content, "```") {
		if newline := strings.IndexByte(content, '\n'); newline >= 0 {
			content = strings.TrimSpace(strings.TrimSuffix(content[newline+1:], "```"))
		}
	}
	return content
}

func validateManagedCapabilityJSON(content, finishReason string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(finishReason)) {
	case "length", "max_tokens":
		return "output_truncated", "检测输出被截断，请提高输出上限或更换模型"
	case "content_filter", "content_filtered":
		return "content_filtered", "检测请求被模型内容策略拦截"
	}
	content = normalizeManagedCapabilityJSON(content)
	if content == "" {
		return "empty_content", "模型返回内容为空"
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return "invalid_json", "模型返回内容不是合法 JSON"
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return "invalid_json", "模型返回内容包含 JSON 之外的额外文本"
	}
	questionNo, questionExists := payload["question_no"]
	maxScore, scoreExists := payload["max_score"]
	_, questionTypeOK := questionNo.(string)
	scoreNumber, scoreTypeOK := maxScore.(json.Number)
	if !questionExists || !scoreExists || !questionTypeOK || !scoreTypeOK {
		return "schema_mismatch", "JSON 缺少必要字段或字段类型不正确"
	}
	if _, scoreErr := scoreNumber.Float64(); scoreErr != nil {
		return "schema_mismatch", "max_score 不是有效数字"
	}
	return "", ""
}

func capabilityStatusManagedProbeFailure(result ManagedAPIProbeResult, statusCode int, body []byte) ManagedAPIProbeResult {
	result.StatusCode = statusCode
	if statusCode == http.StatusBadRequest || statusCode == http.StatusUnprocessableEntity {
		lower := strings.ToLower(string(body))
		if strings.Contains(lower, "response_format") || strings.Contains(lower, "json_object") || strings.Contains(lower, "json mode") {
			return failedManagedProbe(result, "response_format_unsupported", "当前模型接口不支持 JSON 输出模式", "capability")
		}
	}
	return statusManagedProbeFailure(result, statusCode, "capability")
}

func failedManagedProbe(result ManagedAPIProbeResult, code, message, stage string) ManagedAPIProbeResult {
	result.OK = false
	result.ErrorCode = code
	result.Message = message
	check := ManagedAPICheckResult{OK: false, Code: code, Message: message}
	switch stage {
	case "credential":
		result.CredentialCheck = check
	case "model":
		result.ModelCheck = check
	case "capability":
		result.CapabilityCheck = check
	case "text":
		result.TextCheck = check
	}
	return result
}

func networkManagedProbeFailure(result ManagedAPIProbeResult, err error, stage string) ManagedAPIProbeResult {
	if errors.Is(err, context.DeadlineExceeded) {
		return failedManagedProbe(result, "provider_timeout", "模型服务连接超时，请稍后重试", stage)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return failedManagedProbe(result, "provider_timeout", "模型服务连接超时，请稍后重试", stage)
	}
	return failedManagedProbe(result, "provider_unavailable", "无法连接模型供应商", stage)
}

func statusManagedProbeFailure(result ManagedAPIProbeResult, statusCode int, stage string) ManagedAPIProbeResult {
	result.StatusCode = statusCode
	switch statusCode {
	case http.StatusUnauthorized:
		return failedManagedProbe(result, "credential_invalid", "API Key 无效或已被禁用", "credential")
	case http.StatusForbidden:
		return failedManagedProbe(result, "model_permission_denied", "API Key 没有访问该模型的权限", stage)
	case http.StatusNotFound:
		return failedManagedProbe(result, "model_not_found", fmt.Sprintf("没有找到模型 %s", result.Model), "model")
	case http.StatusTooManyRequests:
		return failedManagedProbe(result, "provider_rate_limited", "供应商额度不足或请求频率受限", stage)
	case http.StatusRequestTimeout, http.StatusTooEarly:
		return failedManagedProbe(result, "provider_unavailable", "供应商暂时无法处理请求，请稍后重试", stage)
	default:
		return failedManagedProbe(result, "provider_error", fmt.Sprintf("供应商返回 HTTP %d", statusCode), stage)
	}
}
