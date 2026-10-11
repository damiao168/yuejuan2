package modelgovernance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

var (
	ErrAgentInvalidRequest    = errors.New("invalid managed agent request")
	ErrAgentInvalidResponse   = errors.New("invalid managed agent response")
	ErrAgentToolsUnsupported  = errors.New("managed model does not support native tools")
	ErrAgentResponseTruncated = errors.New("managed agent response was truncated")
)

// AgentMessage 只用于服务端编排，不作为客户端消息的解码结构。
// system、tool 和 ToolCalls 必须由服务端创建，业务授权仍由工具执行层检查。
type AgentMessage struct {
	Role             string          `json:"role"`
	Content          string          `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	Name             string          `json:"name,omitempty"`
	ToolCalls        []AgentToolCall `json:"tool_calls,omitempty"`
}

type AgentTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type AgentToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type AgentRequest struct {
	Messages   []AgentMessage `json:"messages"`
	Tools      []AgentTool    `json:"tools,omitempty"`
	ToolChoice string         `json:"tool_choice,omitempty"`
}

type AgentResponse struct {
	Message      AgentMessage         `json:"message"`
	ModelName    string               `json:"model_name"`
	DisplayName  string               `json:"display_name"`
	ProviderKey  string               `json:"provider_key"`
	FinishReason string               `json:"finish_reason,omitempty"`
	Usage        ManagedAPIProbeUsage `json:"usage"`
}

type AgentEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

// AgentModel 返回模型提出的调用计划，不执行工具，也不推断调用方的身份或数据范围。
type AgentModel interface {
	Complete(context.Context, ManagedAPIConnection, AgentRequest) (AgentResponse, error)
}

// AgentStreamModel 用于最终回答；请求必须禁止工具调用，防止把未完成的工具计划显示为答案。
type AgentStreamModel interface {
	Stream(context.Context, ManagedAPIConnection, AgentRequest, func(AgentEvent) error) (AgentResponse, error)
}

// NormalizeAgentUserMessages 复用现有客户端边界，并把已校验附件转成带内容边界的文本。
// 不接受客户端 system/tool 角色，也不把历史 assistant 文本解析为可执行工具调用。
func NormalizeAgentUserMessages(messages []ManagedChatMessage) ([]AgentMessage, error) {
	normalized, err := normalizeManagedChatMessages(messages)
	if err != nil {
		return nil, err
	}
	providerMessages := managedChatProviderMessages(normalized)
	result := make([]AgentMessage, 0, len(providerMessages))
	for _, message := range providerMessages {
		result = append(result, AgentMessage{Role: message["role"], Content: message["content"]})
	}
	return result, nil
}

func agentResponse(connection ManagedAPIConnection, message AgentMessage, finishReason string, usage ManagedAPIProbeUsage) (AgentResponse, error) {
	response := AgentResponse{
		Message: message, ModelName: connection.Config.ModelName, DisplayName: connection.Config.DisplayName,
		ProviderKey: connection.Config.ProviderKey, FinishReason: strings.TrimSpace(finishReason), Usage: usage,
	}
	if response.Usage.TotalTokens == 0 {
		response.Usage.TotalTokens = response.Usage.InputTokens + response.Usage.OutputTokens
	}
	if response.FinishReason == "length" || response.FinishReason == "max_tokens" {
		// 截断参数不能交给执行层；保留用量，便于调用方记录这次失败的模型开销。
		return response, ErrAgentResponseTruncated
	}
	if response.FinishReason == "content_filter" {
		return response, ErrAgentInvalidResponse
	}
	response.Message.Role = "assistant"
	response.Message.Content = strings.TrimSpace(message.Content)
	if len(message.ToolCalls) > managedAgentMaxToolCalls {
		return response, ErrAgentInvalidResponse
	}
	seen := make(map[string]bool, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		if !validAgentCall(call) || seen[call.ID] {
			return response, ErrAgentInvalidResponse
		}
		seen[call.ID] = true
	}
	if response.Message.Content == "" && len(message.ToolCalls) == 0 {
		return response, ErrManagedChatEmptyResponse
	}
	return response, nil
}
