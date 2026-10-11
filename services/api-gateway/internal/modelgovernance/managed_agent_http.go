package modelgovernance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	managedAgentMaxResponseBytes = 4 << 20
	managedAgentMaxToolCalls     = 8
	managedAgentMaxArguments     = 64 << 10
	managedAgentMaxMessages      = 150
	managedAgentMaxContentBytes  = 2 << 20
)

type HTTPAgentModel struct {
	httpClient *http.Client
}

// NewHTTPAgentModel 沿用受管模型的公网拨号和禁止重定向策略。
// 一个实例可复用于多轮请求；连接密钥只从每次传入的租户配置读取。
func NewHTTPAgentModel(timeout time.Duration) *HTTPAgentModel {
	if timeout <= 0 || timeout > 12*time.Minute {
		timeout = 10 * time.Minute
	}
	transport := &http.Transport{
		DialContext: safePublicDialContext, TLSHandshakeTimeout: 8 * time.Second,
		ResponseHeaderTimeout: timeout, IdleConnTimeout: 30 * time.Second,
	}
	return &HTTPAgentModel{httpClient: &http.Client{
		Transport: transport, Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (m *HTTPAgentModel) Complete(ctx context.Context, connection ManagedAPIConnection, request AgentRequest) (AgentResponse, error) {
	response, err := m.send(ctx, connection, request, false)
	if err != nil {
		return AgentResponse{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, managedAgentMaxResponseBytes+1))
	if err != nil {
		return AgentResponse{}, err
	}
	if len(body) > managedAgentMaxResponseBytes {
		return AgentResponse{}, ErrAgentInvalidResponse
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return AgentResponse{}, agentProviderError(response.StatusCode, body, len(request.Tools) > 0)
	}
	return parseAgentCompletion(connection, body)
}

func (m *HTTPAgentModel) send(ctx context.Context, connection ManagedAPIConnection, request AgentRequest, stream bool) (*http.Response, error) {
	if err := validateAgentRequest(request); err != nil {
		return nil, err
	}
	dashscope := connection.Config.AdapterType == "dashscope_native"
	if !dashscope && connection.Config.AdapterType != "openai_compatible" {
		return nil, ErrInvalidManagedConfig
	}
	endpoint, err := managedCompletionEndpoint(connection.Config.BaseURL)
	if dashscope {
		endpoint, err = managedEndpoint(connection.Config.BaseURL, "services/aigc/text-generation/generation")
	}
	if err != nil {
		return nil, err
	}
	payload := agentProviderPayload(connection.Config, request, stream, dashscope)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, ErrAgentInvalidRequest
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+connection.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "EduGrade-School-Assistant/1.0")
	req.Header.Set("Accept", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
		if dashscope {
			req.Header.Set("X-DashScope-SSE", "enable")
		}
	}
	return m.httpClient.Do(req)
}

func applyAgentThinkingPolicy(parameters map[string]any, config ManagedAPIConfig, dashscope bool) {
	// 沿用已检测的混合思考模型兼容规则，避免工具规划走仅支持流式的思考模式。
	// 不向其他供应商发送专有字段，也不改变现有普通聊天的模式。
	if !managedModelSupportsThinkingDisable(config.ProviderKey, config.ModelName) {
		return
	}
	if dashscope {
		parameters["enable_thinking"] = false
		return
	}
	switch NewProviderRegistry().ProbePolicy(config.ProviderKey) {
	case "deepseek_hybrid":
		parameters["thinking"] = map[string]string{"type": "disabled"}
	case "qwen_hybrid":
		parameters["enable_thinking"] = false
	}
}

func agentProviderPayload(config ManagedAPIConfig, request AgentRequest, stream, dashscope bool) map[string]any {
	parameters := map[string]any{"temperature": 0.2, "max_tokens": 4096}
	applyAgentThinkingPolicy(parameters, config, dashscope)
	if len(request.Tools) > 0 {
		parameters["tools"] = agentProviderTools(request.Tools)
		if request.ToolChoice != "" {
			parameters["tool_choice"] = request.ToolChoice
		}
	}
	if dashscope {
		// DashScope 的工具与输出格式位于 parameters；工具结果消息仍保持原生 tool 角色。
		parameters["result_format"] = "message"
		if stream {
			parameters["incremental_output"] = true
		}
		return map[string]any{
			"model":      config.ModelName,
			"input":      map[string]any{"messages": agentProviderMessages(request.Messages)},
			"parameters": parameters,
		}
	}
	parameters["model"] = config.ModelName
	parameters["messages"] = agentProviderMessages(request.Messages)
	parameters["stream"] = stream
	if stream {
		parameters["stream_options"] = map[string]any{"include_usage": true}
	}
	return parameters
}

type agentProviderToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type agentProviderMessage struct {
	Role             string                  `json:"role"`
	Content          string                  `json:"content"`
	ReasoningContent string                  `json:"reasoning_content,omitempty"`
	ToolCallID       string                  `json:"tool_call_id,omitempty"`
	Name             string                  `json:"name,omitempty"`
	ToolCalls        []agentProviderToolCall `json:"tool_calls,omitempty"`
}

func agentProviderMessages(messages []AgentMessage) []agentProviderMessage {
	result := make([]agentProviderMessage, 0, len(messages))
	for _, message := range messages {
		item := agentProviderMessage{
			Role: message.Role, Content: message.Content, ReasoningContent: message.ReasoningContent,
			ToolCallID: message.ToolCallID, Name: message.Name,
		}
		for _, call := range message.ToolCalls {
			wire := agentProviderToolCall{ID: call.ID, Type: "function"}
			wire.Function.Name, wire.Function.Arguments = call.Name, string(call.Arguments)
			item.ToolCalls = append(item.ToolCalls, wire)
		}
		result = append(result, item)
	}
	return result
}

func agentProviderTools(tools []AgentTool) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		result = append(result, map[string]any{
			"type":     "function",
			"function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters},
		})
	}
	return result
}

type agentProviderChoice struct {
	Message      agentProviderMessage `json:"message"`
	FinishReason string               `json:"finish_reason"`
}

func parseAgentCompletion(connection ManagedAPIConnection, body []byte) (AgentResponse, error) {
	var envelope struct {
		Choices []agentProviderChoice `json:"choices"`
		Output  struct {
			Choices []agentProviderChoice `json:"choices"`
		} `json:"output"`
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return AgentResponse{}, ErrAgentInvalidResponse
	}
	dashscope := connection.Config.AdapterType == "dashscope_native"
	choices := envelope.Choices
	if dashscope {
		choices = envelope.Output.Choices
	}
	usage := parseAgentUsage(envelope.Usage, dashscope)
	if len(choices) == 0 {
		return agentResponse(connection, AgentMessage{}, "", usage)
	}
	choice := choices[0]
	message := AgentMessage{Role: "assistant", Content: choice.Message.Content, ReasoningContent: choice.Message.ReasoningContent}
	invalidType := false
	for _, call := range choice.Message.ToolCalls {
		if call.Type != "function" {
			invalidType = true
		}
		message.ToolCalls = append(message.ToolCalls, AgentToolCall{ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)})
	}
	result, err := agentResponse(connection, message, choice.FinishReason, usage)
	if invalidType {
		return result, ErrAgentInvalidResponse
	}
	return result, err
}

func parseAgentUsage(raw json.RawMessage, dashscope bool) ManagedAPIProbeUsage {
	if dashscope {
		var usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			TotalTokens  int64 `json:"total_tokens"`
		}
		if json.Unmarshal(raw, &usage) == nil {
			return ManagedAPIProbeUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens}
		}
		return ManagedAPIProbeUsage{}
	}
	var usage managedOpenAIProbeUsage
	if json.Unmarshal(raw, &usage) == nil {
		return usage.normalized()
	}
	return ManagedAPIProbeUsage{}
}

func validAgentName(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func validAgentCall(call AgentToolCall) bool {
	return call.ID != "" && len(call.ID) <= 128 && !strings.ContainsAny(call.ID, "\r\n\x00") &&
		validAgentName(call.Name) && len(call.Arguments) <= managedAgentMaxArguments && agentJSONObject(call.Arguments)
}

func agentJSONObject(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	return len(value) > 1 && value[0] == '{' && json.Valid(value)
}

func validateAgentRequest(request AgentRequest) error {
	if len(request.Messages) == 0 || len(request.Messages) > managedAgentMaxMessages || len(request.Tools) > 32 {
		return ErrAgentInvalidRequest
	}
	switch request.ToolChoice {
	case "", "auto", "none":
	case "required":
		if len(request.Tools) == 0 {
			return ErrAgentInvalidRequest
		}
	default:
		return ErrAgentInvalidRequest
	}
	tools := make(map[string]bool, len(request.Tools))
	for _, tool := range request.Tools {
		if !validAgentName(tool.Name) || tools[tool.Name] || len(tool.Description) > 8192 || len(tool.Parameters) > managedAgentMaxArguments || !agentJSONObject(tool.Parameters) {
			return ErrAgentInvalidRequest
		}
		tools[tool.Name] = true
	}
	return validateAgentTranscript(request.Messages)
}

func validateAgentTranscript(messages []AgentMessage) error {
	contentBytes := 0
	pending := make(map[string]bool)
	for _, message := range messages {
		contentBytes += len(message.Content) + len(message.ReasoningContent)
		if contentBytes > managedAgentMaxContentBytes || len(message.ToolCalls) > managedAgentMaxToolCalls {
			return ErrAgentInvalidRequest
		}
		if message.Role != "tool" && len(pending) > 0 {
			// 一组 assistant 调用必须全部获得对应结果后，才允许进入下一轮对话。
			return ErrAgentInvalidRequest
		}
		argumentBytes, err := validateAgentTranscriptMessage(message, pending)
		if err != nil {
			return err
		}
		// 工具参数与正文共用整段对话预算；下一条消息和最终检查都包含这些字节。
		contentBytes += argumentBytes
	}
	if len(pending) != 0 || contentBytes > managedAgentMaxContentBytes {
		return ErrAgentInvalidRequest
	}
	return nil
}

func validateAgentTranscriptMessage(message AgentMessage, pending map[string]bool) (int, error) {
	switch message.Role {
	case "system", "user":
		if message.Content == "" || len(message.ToolCalls) > 0 || message.ToolCallID != "" {
			return 0, ErrAgentInvalidRequest
		}
	case "assistant":
		return validateAgentAssistantCalls(message, pending)
	case "tool":
		if !pending[message.ToolCallID] || len(message.ToolCalls) > 0 || message.Content == "" {
			return 0, ErrAgentInvalidRequest
		}
		delete(pending, message.ToolCallID)
	default:
		return 0, ErrAgentInvalidRequest
	}
	return 0, nil
}

func validateAgentAssistantCalls(message AgentMessage, pending map[string]bool) (int, error) {
	if message.ToolCallID != "" || (message.Content == "" && len(message.ToolCalls) == 0) {
		return 0, ErrAgentInvalidRequest
	}
	argumentBytes := 0
	for _, call := range message.ToolCalls {
		if !validAgentCall(call) || pending[call.ID] {
			return 0, ErrAgentInvalidRequest
		}
		argumentBytes += len(call.Arguments)
		pending[call.ID] = true
	}
	return argumentBytes, nil
}

func agentProviderError(status int, body []byte, sentTools bool) error {
	// 只返回稳定错误分类，不回传供应商正文；正文可能含凭据或对话内容。
	if sentTools && (status == http.StatusBadRequest || status == http.StatusUnprocessableEntity) {
		var envelope struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Error   struct {
				Code    string `json:"code"`
				Param   string `json:"param"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &envelope) == nil {
			text := strings.ToLower(fmt.Sprintf("%s %s %s %s %s", envelope.Code, envelope.Message, envelope.Error.Code, envelope.Error.Param, envelope.Error.Message))
			toolRelated := strings.Contains(text, "tools") || strings.Contains(text, "tool_choice") || strings.Contains(text, "function calling") || strings.Contains(text, "tool calling")
			unsupported := strings.Contains(text, "unsupported") || strings.Contains(text, "not support") || strings.Contains(text, "unknown parameter") || strings.Contains(text, "unrecognized")
			if toolRelated && unsupported {
				return ErrAgentToolsUnsupported
			}
		}
	}
	return ManagedChatProviderError{StatusCode: status}
}
