package modelgovernance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// 兼容接口先列模型确认访问权限，再发送 JSON 样例；结构化结果与访问、普通回复证据分别记录。
func (p *HTTPManagedAPIProber) probeOpenAICompatible(ctx context.Context, client *http.Client, connection ManagedAPIConnection, result ManagedAPIProbeResult) ManagedAPIProbeResult {
	modelsEndpoint, err := managedModelsEndpoint(connection.Config.BaseURL)
	if err != nil {
		return failedManagedProbe(result, "invalid_endpoint", managedProbeInvalidEndpoint, "credential")
	}
	response, err := p.doReadOnlyRequest(ctx, client, modelsEndpoint, connection, &result)
	if err != nil {
		return networkManagedProbeFailure(result, err, "credential")
	}
	result.StatusCode = response.StatusCode
	body, readErr := readManagedProbeBody(response)
	if readErr != nil {
		return bodyManagedProbeFailure(result, readErr, "credential")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return statusManagedProbeFailure(result, response.StatusCode, "credential")
	}
	result.CredentialCheck = ManagedAPICheckResult{OK: true, Message: managedProbeAPIKeyValid}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.Unmarshal(body, &models); err != nil {
		return failedManagedProbe(result, "provider_invalid_response", "供应商模型列表格式异常", "model")
	}
	modelFound := false
	for _, model := range models.Data {
		if model.ID == connection.Config.ModelName {
			modelFound = true
			break
		}
	}
	if !modelFound {
		return failedManagedProbe(result, "model_not_found", fmt.Sprintf("没有找到模型 %s", connection.Config.ModelName), "model")
	}
	result.ModelCheck = ManagedAPICheckResult{OK: true, Message: "模型可用"}

	completionEndpoint, err := managedCompletionEndpoint(connection.Config.BaseURL)
	if err != nil {
		return failedManagedProbe(result, "invalid_endpoint", managedProbeInvalidEndpoint, "capability")
	}
	payload, _ := json.Marshal(managedCapabilityPayload(connection))
	result.Diagnostic.ResponseFormat = "json_object"
	result.GeneratedRequest = true
	// 只对生成阶段使用较长超时；请求继承调用方取消信号，失败后不自动重试。
	response, err = p.doRequest(ctx, p.capabilityHTTPClient, http.MethodPost, completionEndpoint, connection, payload, &result)
	if err != nil {
		return networkManagedProbeFailure(result, err, "capability")
	}
	result.StatusCode = response.StatusCode
	body, readErr = readManagedProbeBody(response)
	if readErr != nil {
		return bodyManagedProbeFailure(result, readErr, "capability")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return capabilityStatusManagedProbeFailure(result, response.StatusCode, body)
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage managedOpenAIProbeUsage `json:"usage"`
	}
	if err = json.Unmarshal(body, &completion); err != nil {
		return failedManagedProbe(result, "provider_invalid_response", "供应商返回了无法解析的响应", "capability")
	}
	result.Usage = completion.Usage.normalized()
	if len(completion.Choices) == 0 {
		return failedManagedProbe(result, "no_choices", "模型响应中没有可用结果", "capability")
	}
	result.Diagnostic = managedCapabilityDiagnostic(completion.Choices[0].Message.Content, completion.Choices[0].FinishReason, result.Diagnostic.ResponseFormat)
	if strings.TrimSpace(completion.Choices[0].Message.Content) != "" {
		// 结构化校验失败仍说明模型已经完成文本生成，普通聊天不应因此被禁用。
		result.TextCheck = ManagedAPICheckResult{OK: true, Message: "模型返回了文本"}
	}
	if code, message := validateManagedCapabilityJSON(completion.Choices[0].Message.Content, completion.Choices[0].FinishReason); code != "" {
		return failedManagedProbe(result, code, message, "capability")
	}
	result.CapabilityCheck = ManagedAPICheckResult{OK: true, Message: managedProbeCapabilityOK}
	result.OK = true
	result.ErrorCode = ""
	result.Message = managedProbeCapabilityOK
	return result
}

func (p *HTTPManagedAPIProber) probeDashScope(ctx context.Context, client *http.Client, connection ManagedAPIConnection, result ManagedAPIProbeResult) ManagedAPIProbeResult {
	if connection.Config.BaseURL != "https://dashscope.aliyuncs.com/api/v1" {
		return failedManagedProbe(result, "invalid_endpoint", "DashScope 原生接口地址无效", "credential")
	}
	parameters := map[string]any{"max_tokens": managedCapabilityMaxTokens, "result_format": "message", "temperature": 0}
	if managedModelSupportsThinkingDisable(connection.Config.ProviderKey, connection.Config.ModelName) {
		parameters["enable_thinking"] = false
	}
	payload, _ := json.Marshal(map[string]any{
		"model":      connection.Config.ModelName,
		"input":      map[string]any{"messages": managedCapabilityMessages()},
		"parameters": parameters,
	})
	endpoint := connection.Config.BaseURL + "/services/aigc/text-generation/generation"
	result.GeneratedRequest = true
	result.Diagnostic.ResponseFormat = "prompt_only"
	response, err := p.doRequest(ctx, client, http.MethodPost, endpoint, connection, payload, &result)
	if err != nil {
		return networkManagedProbeFailure(result, err, "credential")
	}
	result.StatusCode = response.StatusCode
	body, readErr := readManagedProbeBody(response)
	if readErr != nil {
		return bodyManagedProbeFailure(result, readErr, "credential")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return statusManagedProbeFailure(result, response.StatusCode, "credential")
	}
	result.CredentialCheck = ManagedAPICheckResult{OK: true, Message: managedProbeAPIKeyValid}
	result.ModelCheck = ManagedAPICheckResult{OK: true, Message: "模型可用"}
	var completion struct {
		Output struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		} `json:"output"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			TotalTokens  int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err = json.Unmarshal(body, &completion); err != nil {
		return failedManagedProbe(result, "provider_invalid_response", "供应商返回了无法解析的响应", "capability")
	}
	result.Usage = ManagedAPIProbeUsage{InputTokens: completion.Usage.InputTokens, OutputTokens: completion.Usage.OutputTokens, TotalTokens: completion.Usage.TotalTokens}
	if result.Usage.TotalTokens == 0 {
		result.Usage.TotalTokens = result.Usage.InputTokens + result.Usage.OutputTokens
	}
	if len(completion.Output.Choices) == 0 {
		return failedManagedProbe(result, "no_choices", "模型响应中没有可用结果", "capability")
	}
	result.Diagnostic = managedCapabilityDiagnostic(completion.Output.Choices[0].Message.Content, completion.Output.Choices[0].FinishReason, result.Diagnostic.ResponseFormat)
	if strings.TrimSpace(completion.Output.Choices[0].Message.Content) != "" {
		result.TextCheck = ManagedAPICheckResult{OK: true, Message: "模型返回了文本"}
	}
	if code, message := validateManagedCapabilityJSON(completion.Output.Choices[0].Message.Content, completion.Output.Choices[0].FinishReason); code != "" {
		return failedManagedProbe(result, code, message, "capability")
	}
	result.CapabilityCheck = ManagedAPICheckResult{OK: true, Message: managedProbeCapabilityOK}
	result.OK = true
	result.Message = managedProbeCapabilityOK
	return result
}
