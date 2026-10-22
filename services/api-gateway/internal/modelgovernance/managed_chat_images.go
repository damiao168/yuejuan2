package modelgovernance

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
)

// ChatRequestBodyLimit 为三张 3 MB 图片的 base64、文本历史和 JSON 信封预留空间。
const ChatRequestBodyLimit = 14 << 20

const managedChatMaxImageBytes = 3 << 20
const managedChatMaxImageTotalBytes = 9 << 20

var ErrManagedChatInvalidImage = errors.New("invalid managed chat image")
var ErrManagedChatImageBudget = errors.New("managed chat image budget exceeded")

func validateChatImage(attachment ManagedChatAttachment) error {
	if !validManagedChatAttachment(attachment, 1) || (attachment.MediaType != "image/png" && attachment.MediaType != "image/jpeg") || attachment.Size <= 0 || attachment.Size > managedChatMaxImageBytes {
		return ErrManagedChatInvalidImage
	}
	prefix := "data:" + attachment.MediaType + ";base64,"
	if !strings.HasPrefix(attachment.Content, prefix) || len(attachment.Content)-len(prefix) > base64.StdEncoding.EncodedLen(managedChatMaxImageBytes) {
		return ErrManagedChatInvalidImage
	}
	data, err := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(attachment.Content, prefix))
	if err != nil || int64(len(data)) != attachment.Size {
		return ErrManagedChatInvalidImage
	}
	// 只允许内联 PNG/JPEG；不代用户下载 URL，也不依据文件名相信格式或尺寸。
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || "image/"+format != attachment.MediaType || config.Width <= 0 || config.Height <= 0 || config.Width > 10000 || config.Height > 10000 || int64(config.Width)*int64(config.Height) > 20_000_000 {
		return ErrManagedChatInvalidImage
	}
	return nil
}

func chatAgentMessage(message ManagedChatMessage) AgentMessage {
	out := AgentMessage{Role: message.Role}
	sections := []string{}
	if content := strings.TrimSpace(message.Content); content != "" {
		sections = append(sections, content)
	}
	for _, attachment := range message.Attachments {
		if strings.HasPrefix(attachment.MediaType, "image/") {
			out.Images = append(out.Images, attachment)
			sections = append(sections, fmt.Sprintf("图片附件：%s", attachment.Name))
		} else {
			sections = append(sections, fmt.Sprintf("附件：%s（%s）\n--- 文件内容开始 ---\n%s\n--- 文件内容结束 ---", attachment.Name, attachment.MediaType, attachment.Content))
		}
	}
	out.Content = strings.Join(sections, "\n\n")
	return out
}

func agentHasImages(messages []AgentMessage) bool {
	for _, message := range messages {
		if len(message.Images) > 0 {
			return true
		}
	}
	return false
}

func managedChatGenerationPath(messages []ManagedChatMessage) string {
	for _, message := range messages {
		for _, attachment := range message.Attachments {
			if strings.HasPrefix(attachment.MediaType, "image/") {
				return "services/aigc/multimodal-generation/generation"
			}
		}
	}
	return "services/aigc/text-generation/generation"
}

// MarshalJSON 编码图文片段和适配器要求的思考字段；工具消息保留角色和调用标识。
func (message agentProviderMessage) MarshalJSON() ([]byte, error) {
	type plain agentProviderMessage
	if len(message.Images) == 0 && (!message.NativeImages || message.Role == "tool" || len(message.ToolCalls) > 0) {
		// 仅适配器确认需要时保留空思考字段；不为普通历史或其他供应商补造思考正文。
		if message.IncludeEmptyReasoning {
			return json.Marshal(struct {
				ReasoningContent string `json:"reasoning_content"`
				plain
			}{ReasoningContent: message.ReasoningContent, plain: plain(message)})
		}
		return json.Marshal(plain(message))
	}
	parts := []map[string]any{}
	if message.Content != "" {
		if message.NativeImages {
			parts = append(parts, map[string]any{"text": message.Content})
		} else {
			parts = append(parts, map[string]any{"type": "text", "text": message.Content})
		}
	}
	for _, attachment := range message.Images {
		if message.NativeImages {
			parts = append(parts, map[string]any{"image": attachment.Content})
		} else {
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": attachment.Content}})
		}
	}
	return json.Marshal(struct {
		Content any `json:"content"`
		plain
	}{Content: parts, plain: plain(message)})
}

// 视觉模型的回答可使用 text 片段数组；只提取文本，不接受返回的新图像或工具指令。
func (message *agentProviderMessage) UnmarshalJSON(data []byte) error {
	type plain agentProviderMessage
	wire := struct {
		Content json.RawMessage `json:"content"`
		*plain
	}{plain: (*plain)(message)}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if len(wire.Content) == 0 || string(wire.Content) == "null" {
		message.Content = ""
		return nil
	}
	if json.Unmarshal(wire.Content, &message.Content) == nil {
		return nil
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(wire.Content, &parts); err != nil {
		return err
	}
	var content strings.Builder
	for _, part := range parts {
		content.WriteString(part.Text)
	}
	message.Content = content.String()
	return nil
}
