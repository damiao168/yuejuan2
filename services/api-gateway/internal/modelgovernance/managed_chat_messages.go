package modelgovernance

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type ManagedChatMessage struct {
	Role             string                  `json:"role"`
	Content          string                  `json:"content"`
	ReasoningContent string                  `json:"reasoning_content,omitempty"`
	Attachments      []ManagedChatAttachment `json:"attachments,omitempty"`
}

type ManagedChatAttachment struct {
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Content   string `json:"content"`
	Size      int64  `json:"size"`
}

type ManagedChatRequest struct {
	Messages      []ManagedChatMessage `json:"messages"`
	ModelConfigID string               `json:"model_config_id,omitempty"`
}

type ManagedChatCompletion struct {
	Message      ManagedChatMessage   `json:"message"`
	ModelName    string               `json:"model_name"`
	DisplayName  string               `json:"display_name"`
	ProviderKey  string               `json:"provider_key"`
	FinishReason string               `json:"finish_reason,omitempty"`
	Usage        ManagedAPIProbeUsage `json:"usage"`
}

type ManagedChatModelStatus struct {
	ID          string `json:"id,omitempty"`
	Available   bool   `json:"available"`
	DisplayName string `json:"display_name,omitempty"`
	ModelName   string `json:"model_name,omitempty"`
	ProviderKey string `json:"provider_key,omitempty"`
	Message     string `json:"message"`
	// 展示实际请求采用的应用窗口（含保守默认值），不是按模型名称推测的供应商能力。
	ContextWindowTokens int `json:"context_window_tokens,omitempty"`
}

type ManagedChatProviderError struct {
	StatusCode int
}

func (e ManagedChatProviderError) Error() string {
	return fmt.Sprintf("managed chat provider returned status %d", e.StatusCode)
}

type ManagedAPIChatter interface {
	Chat(context.Context, ManagedAPIConnection, []ManagedChatMessage) (ManagedChatCompletion, error)
}

type ManagedChatEvent struct {
	Type       string                 `json:"type"`
	Delta      string                 `json:"delta,omitempty"`
	Completion *ManagedChatCompletion `json:"completion,omitempty"`
}

type ManagedAPIStreamChatter interface {
	StreamChat(context.Context, ManagedAPIConnection, []ManagedChatMessage, func(ManagedChatEvent) error) error
}

type HTTPManagedAPIChatter struct {
	timeout time.Duration
}

// 统一限制消息、附件和总字符数，并要求最后一条来自用户，避免把后台对话接口当成任意代理。
func normalizeManagedChatMessages(messages []ManagedChatMessage) ([]ManagedChatMessage, error) {
	if len(messages) == 0 || len(messages) > managedChatMaxMessages {
		return nil, ErrManagedChatInvalidRequest
	}
	normalized := make([]ManagedChatMessage, 0, len(messages))
	totalRunes := 0
	imageBytes := int64(0)
	for _, message := range messages {
		message, count, err := normalizeManagedChatMessage(message)
		if err != nil {
			return nil, err
		}
		for _, attachment := range message.Attachments {
			if strings.HasPrefix(attachment.MediaType, "image/") {
				imageBytes += attachment.Size
			}
		}
		if imageBytes > managedChatMaxImageTotalBytes {
			return nil, ErrManagedChatImageBudget
		}
		totalRunes += count
		if totalRunes > managedChatMaxTotalRunes {
			return nil, ErrManagedChatInvalidRequest
		}
		normalized = append(normalized, message)
	}
	if normalized[len(normalized)-1].Role != "user" {
		return nil, ErrManagedChatInvalidRequest
	}
	return normalized, nil
}

func normalizeManagedChatMessage(message ManagedChatMessage) (ManagedChatMessage, int, error) {
	message.Role = strings.ToLower(strings.TrimSpace(message.Role))
	message.Content = strings.TrimSpace(message.Content)
	count := utf8.RuneCountInString(message.Content)
	if (message.Role != "user" && message.Role != "assistant") || count > managedChatMaxMessageRunes || len(message.Attachments) > managedChatMaxAttachments {
		return ManagedChatMessage{}, 0, ErrManagedChatInvalidRequest
	}
	if message.Role == "assistant" && len(message.Attachments) > 0 {
		return ManagedChatMessage{}, 0, ErrManagedChatInvalidRequest
	}
	fileRunes, err := normalizeManagedChatAttachments(message.Attachments)
	if err != nil {
		return ManagedChatMessage{}, 0, err
	}
	count += fileRunes
	if count == 0 {
		return ManagedChatMessage{}, 0, ErrManagedChatInvalidRequest
	}
	return message, count, nil
}

// 附件沿用调用方的切片，失败前已处理的附件也保留规范化结果。
func normalizeManagedChatAttachments(attachments []ManagedChatAttachment) (int, error) {
	totalRunes := 0
	for index := range attachments {
		attachment := &attachments[index]
		attachment.Name = strings.TrimSpace(attachment.Name)
		attachment.MediaType = strings.ToLower(strings.TrimSpace(attachment.MediaType))
		attachment.Content = strings.TrimSpace(attachment.Content)
		if strings.HasPrefix(attachment.MediaType, "image/") {
			if err := validateChatImage(*attachment); err != nil {
				return 0, err
			}
			// 图片按字节预算单独限制，不能把 base64 当成六万字的文本附件。
			totalRunes++
			continue
		}
		fileRunes := utf8.RuneCountInString(attachment.Content)
		if !validManagedChatAttachment(*attachment, fileRunes) {
			return 0, ErrManagedChatInvalidRequest
		}
		totalRunes += fileRunes
	}
	return totalRunes, nil
}

func validManagedChatAttachment(attachment ManagedChatAttachment, contentRunes int) bool {
	return attachment.Name != "" && utf8.RuneCountInString(attachment.Name) <= 255 &&
		!strings.ContainsAny(attachment.Name, "\r\n\x00") &&
		attachment.MediaType != "" && len(attachment.MediaType) <= 128 &&
		!strings.ContainsAny(attachment.MediaType, "\r\n\x00") &&
		attachment.Size >= 0 && contentRunes > 0 && contentRunes <= managedChatMaxFileRunes
}

func managedChatProviderMessages(messages []ManagedChatMessage, native ...bool) []agentProviderMessage {
	agentMessages := make([]AgentMessage, 0, len(messages))
	for _, message := range messages {
		agentMessages = append(agentMessages, chatAgentMessage(message))
	}
	return agentProviderMessages(agentMessages, native...)
}
