package modelgovernance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

func (p *HTTPManagedAPIProber) probeOpenAICompatibleText(ctx context.Context, connection ManagedAPIConnection, result ManagedAPIProbeResult) ManagedAPIProbeResult {
	list, access := p.ListModels(ctx, connection)
	result.CredentialCheck = access.CredentialCheck
	result.ConnectionDiagnostic = access.ConnectionDiagnostic
	result.StatusCode = access.StatusCode
	if !access.OK {
		result.ErrorCode, result.Message = access.ErrorCode, access.Message
		result.ConnectionDiagnostic = access.ConnectionDiagnostic
		return result
	}
	if !slices.Contains(list.Models, connection.Config.ModelName) {
		return failedManagedProbe(result, "model_not_found", fmt.Sprintf("没有找到模型 %s", connection.Config.ModelName), "model")
	}
	result.ModelCheck = ManagedAPICheckResult{OK: true, Message: "模型可用"}
	endpoint, err := managedCompletionEndpoint(connection.Config.BaseURL)
	if err != nil {
		return failedManagedProbe(result, "invalid_endpoint", managedProbeInvalidEndpoint, "text")
	}
	parameters := map[string]any{
		"model": connection.Config.ModelName, "messages": []map[string]string{{"role": "user", "content": "请只回复 OK。"}},
		"max_tokens": managedCapabilityMaxTokens, "temperature": 0, "stream": false,
	}
	applyManagedThinkingPolicy(parameters, connection)
	payload, _ := json.Marshal(parameters)
	result.GeneratedRequest = true
	result.Diagnostic.ResponseFormat = "text"
	response, err := p.doRequest(ctx, p.capabilityHTTPClient, http.MethodPost, endpoint, connection, payload, &result)
	if err != nil {
		return networkManagedProbeFailure(result, err, "text")
	}
	result.StatusCode = response.StatusCode
	body, readErr := readManagedProbeBody(response)
	if readErr != nil {
		return bodyManagedProbeFailure(result, readErr, "text")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return statusManagedProbeFailure(result, response.StatusCode, "text")
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
		return failedManagedProbe(result, "provider_invalid_response", "供应商返回了无法解析的响应", "text")
	}
	result.Usage = completion.Usage.normalized()
	if len(completion.Choices) == 0 {
		return failedManagedProbe(result, "no_choices", "模型响应中没有可用结果", "text")
	}
	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	result.Diagnostic = managedCapabilityDiagnostic(content, completion.Choices[0].FinishReason, result.Diagnostic.ResponseFormat)
	if content == "" {
		return failedManagedProbe(result, "empty_content", "模型返回内容为空", "text")
	}
	result.TextCheck = ManagedAPICheckResult{OK: true, Message: "模型返回了文本"}
	result.CapabilityCheck = ManagedAPICheckResult{Code: "not_run", Message: "本次只验证文本生成，未检查 JSON 输出"}
	result.OK = true
	result.Message = "文本生成检测通过"
	return result
}

func (p *HTTPManagedAPIProber) probeDashScopeText(ctx context.Context, client *http.Client, connection ManagedAPIConnection, result ManagedAPIProbeResult) ManagedAPIProbeResult {
	if connection.Config.BaseURL != "https://dashscope.aliyuncs.com/api/v1" {
		return failedManagedProbe(result, "invalid_endpoint", "DashScope 原生接口地址无效", "credential")
	}
	parameters := map[string]any{"max_tokens": managedCapabilityMaxTokens, "result_format": "message", "temperature": 0}
	if managedModelSupportsThinkingDisable(connection.Config.ProviderKey, connection.Config.ModelName) {
		parameters["enable_thinking"] = false
	}
	payload, _ := json.Marshal(map[string]any{
		"model":      connection.Config.ModelName,
		"input":      map[string]any{"messages": []map[string]string{{"role": "user", "content": "请只回复 OK。"}}},
		"parameters": parameters,
	})
	result.GeneratedRequest = true
	result.Diagnostic.ResponseFormat = "text"
	endpoint := connection.Config.BaseURL + "/services/aigc/text-generation/generation"
	response, err := p.doRequest(ctx, client, http.MethodPost, endpoint, connection, payload, &result)
	if err != nil {
		return networkManagedProbeFailure(result, err, "text")
	}
	result.StatusCode = response.StatusCode
	body, readErr := readManagedProbeBody(response)
	if readErr != nil {
		return bodyManagedProbeFailure(result, readErr, "text")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return statusManagedProbeFailure(result, response.StatusCode, "text")
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
		return failedManagedProbe(result, "provider_invalid_response", "供应商返回了无法解析的响应", "text")
	}
	usage := completion.Usage
	result.Usage = ManagedAPIProbeUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens}
	if result.Usage.TotalTokens == 0 {
		result.Usage.TotalTokens = result.Usage.InputTokens + result.Usage.OutputTokens
	}
	if len(completion.Output.Choices) == 0 {
		return failedManagedProbe(result, "no_choices", "模型响应中没有可用结果", "text")
	}
	content := strings.TrimSpace(completion.Output.Choices[0].Message.Content)
	result.Diagnostic = managedCapabilityDiagnostic(content, completion.Output.Choices[0].FinishReason, result.Diagnostic.ResponseFormat)
	if content == "" {
		return failedManagedProbe(result, "empty_content", "模型返回内容为空", "text")
	}
	result.TextCheck = ManagedAPICheckResult{OK: true, Message: "模型返回了文本"}
	result.CapabilityCheck = ManagedAPICheckResult{Code: "not_run", Message: "本次只验证文本生成，未检查 JSON 输出"}
	result.OK = true
	result.Message = "文本生成检测通过"
	return result
}
