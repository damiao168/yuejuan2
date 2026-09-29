package modelgovernance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

const (
	managedChatMaxMessages     = 50
	managedChatMaxMessageRunes = 20_000
	managedChatMaxAttachments  = 3
	managedChatMaxFileRunes    = 60_000
	managedChatMaxTotalRunes   = 200_000
	managedChatBodyLimit       = 1 << 20
)

var (
	ErrManagedChatInvalidRequest = errors.New("invalid managed chat request")
	ErrManagedChatEmptyResponse  = errors.New("managed chat returned an empty response")
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
	Messages []ManagedChatMessage `json:"messages"`
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
	Available   bool   `json:"available"`
	DisplayName string `json:"display_name,omitempty"`
	ModelName   string `json:"model_name,omitempty"`
	ProviderKey string `json:"provider_key,omitempty"`
	Message     string `json:"message"`
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

func NewHTTPManagedAPIChatter(timeout time.Duration) *HTTPManagedAPIChatter {
	if timeout <= 0 || timeout > 12*time.Minute {
		timeout = 10 * time.Minute
	}
	return &HTTPManagedAPIChatter{timeout: timeout}
}

func (c *HTTPManagedAPIChatter) Chat(ctx context.Context, connection ManagedAPIConnection, messages []ManagedChatMessage) (ManagedChatCompletion, error) {
	if connection.Config.AdapterType == "dashscope_native" {
		return c.chatDashScope(ctx, connection, messages)
	}
	return c.chatOpenAICompatible(ctx, connection, messages)
}

func (c *HTTPManagedAPIChatter) StreamChat(ctx context.Context, connection ManagedAPIConnection, messages []ManagedChatMessage, emit func(ManagedChatEvent) error) error {
	var endpoint string
	var payload []byte
	var err error
	dashscope := connection.Config.AdapterType == "dashscope_native"
	if dashscope {
		endpoint = strings.TrimRight(connection.Config.BaseURL, "/") + "/services/aigc/text-generation/generation"
		payload, _ = json.Marshal(map[string]any{
			"model":      connection.Config.ModelName,
			"input":      map[string]any{"messages": managedChatProviderMessages(messages)},
			"parameters": map[string]any{"result_format": "message", "incremental_output": true, "temperature": 0.7},
		})
	} else {
		endpoint, err = managedCompletionEndpoint(connection.Config.BaseURL)
		if err != nil {
			return err
		}
		payload, _ = json.Marshal(map[string]any{
			"model": connection.Config.ModelName, "messages": managedChatProviderMessages(messages),
			"stream": true, "stream_options": map[string]any{"include_usage": true}, "temperature": 0.7,
		})
	}
	transport := &http.Transport{DialContext: safePublicDialContext, TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: c.timeout, IdleConnTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: c.timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+connection.APIKey)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "EduGrade-Admin-Chat/1.0")
	if dashscope {
		req.Header.Set("X-DashScope-SSE", "enable")
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(response.Body, managedProbeResponseLimit))
		return ManagedChatProviderError{StatusCode: response.StatusCode}
	}
	var content, reasoning, finishReason string
	var usage ManagedAPIProbeUsage
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
				Message struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Output struct {
				Choices []struct {
					Message struct {
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
					} `json:"message"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
			} `json:"output"`
			Usage json.RawMessage `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		var answerDelta, reasoningDelta string
		if dashscope && len(chunk.Output.Choices) > 0 {
			choice := chunk.Output.Choices[0]
			answerDelta, reasoningDelta, finishReason = choice.Message.Content, choice.Message.ReasoningContent, choice.FinishReason
		} else if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			answerDelta, reasoningDelta = choice.Delta.Content, choice.Delta.ReasoningContent
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
		}
		if reasoningDelta != "" {
			reasoning += reasoningDelta
			if err := emit(ManagedChatEvent{Type: "reasoning", Delta: reasoningDelta}); err != nil {
				return err
			}
		}
		if answerDelta != "" {
			content += answerDelta
			if err := emit(ManagedChatEvent{Type: "content", Delta: answerDelta}); err != nil {
				return err
			}
		}
		if len(chunk.Usage) > 0 && string(chunk.Usage) != "null" {
			if dashscope {
				var value struct {
					InputTokens  int64 `json:"input_tokens"`
					OutputTokens int64 `json:"output_tokens"`
					TotalTokens  int64 `json:"total_tokens"`
				}
				if json.Unmarshal(chunk.Usage, &value) == nil {
					usage = ManagedAPIProbeUsage{InputTokens: value.InputTokens, OutputTokens: value.OutputTokens, TotalTokens: value.TotalTokens}
				}
			} else {
				var value managedOpenAIProbeUsage
				if json.Unmarshal(chunk.Usage, &value) == nil {
					usage = value.normalized()
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	completion, err := managedChatCompletion(connection, content, finishReason, usage)
	if err != nil {
		return err
	}
	completion.Message.ReasoningContent = reasoning
	return emit(ManagedChatEvent{Type: "done", Completion: &completion})
}

func (c *HTTPManagedAPIChatter) chatOpenAICompatible(ctx context.Context, connection ManagedAPIConnection, messages []ManagedChatMessage) (ManagedChatCompletion, error) {
	endpoint, err := managedCompletionEndpoint(connection.Config.BaseURL)
	if err != nil {
		return ManagedChatCompletion{}, err
	}
	payload, _ := json.Marshal(map[string]any{
		"model": connection.Config.ModelName, "messages": managedChatProviderMessages(messages),
		"temperature": 0.7, "stream": false,
	})
	body, status, err := c.request(ctx, endpoint, connection, payload)
	if err != nil {
		return ManagedChatCompletion{}, err
	}
	if status < 200 || status >= 300 {
		return ManagedChatCompletion{}, ManagedChatProviderError{StatusCode: status}
	}
	var response struct {
		Choices []struct {
			Message      ManagedChatMessage `json:"message"`
			FinishReason string             `json:"finish_reason"`
		} `json:"choices"`
		Usage managedOpenAIProbeUsage `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil || len(response.Choices) == 0 {
		return ManagedChatCompletion{}, ErrManagedChatEmptyResponse
	}
	completion, err := managedChatCompletion(connection, response.Choices[0].Message.Content, response.Choices[0].FinishReason, response.Usage.normalized())
	completion.Message.ReasoningContent = response.Choices[0].Message.ReasoningContent
	return completion, err
}

func (c *HTTPManagedAPIChatter) chatDashScope(ctx context.Context, connection ManagedAPIConnection, messages []ManagedChatMessage) (ManagedChatCompletion, error) {
	endpoint := strings.TrimRight(connection.Config.BaseURL, "/") + "/services/aigc/text-generation/generation"
	payload, _ := json.Marshal(map[string]any{
		"model":      connection.Config.ModelName,
		"input":      map[string]any{"messages": managedChatProviderMessages(messages)},
		"parameters": map[string]any{"result_format": "message", "temperature": 0.7},
	})
	body, status, err := c.request(ctx, endpoint, connection, payload)
	if err != nil {
		return ManagedChatCompletion{}, err
	}
	if status < 200 || status >= 300 {
		return ManagedChatCompletion{}, ManagedChatProviderError{StatusCode: status}
	}
	var response struct {
		Output struct {
			Choices []struct {
				Message      ManagedChatMessage `json:"message"`
				FinishReason string             `json:"finish_reason"`
			} `json:"choices"`
		} `json:"output"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			TotalTokens  int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil || len(response.Output.Choices) == 0 {
		return ManagedChatCompletion{}, ErrManagedChatEmptyResponse
	}
	usage := ManagedAPIProbeUsage{InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens, TotalTokens: response.Usage.TotalTokens}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	choice := response.Output.Choices[0]
	completion, err := managedChatCompletion(connection, choice.Message.Content, choice.FinishReason, usage)
	completion.Message.ReasoningContent = choice.Message.ReasoningContent
	return completion, err
}

func (c *HTTPManagedAPIChatter) request(ctx context.Context, endpoint string, connection ManagedAPIConnection, payload []byte) ([]byte, int, error) {
	transport := &http.Transport{
		DialContext: safePublicDialContext, TLSHandshakeTimeout: 8 * time.Second,
		ResponseHeaderTimeout: c.timeout, IdleConnTimeout: 10 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: c.timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+connection.APIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "EduGrade-Admin-Chat/1.0")
	response, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return body, response.StatusCode, err
}

func managedChatCompletion(connection ManagedAPIConnection, content, finishReason string, usage ManagedAPIProbeUsage) (ManagedChatCompletion, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return ManagedChatCompletion{}, ErrManagedChatEmptyResponse
	}
	return ManagedChatCompletion{
		Message:   ManagedChatMessage{Role: "assistant", Content: content},
		ModelName: connection.Config.ModelName, DisplayName: connection.Config.DisplayName,
		ProviderKey: connection.Config.ProviderKey, FinishReason: strings.TrimSpace(finishReason), Usage: usage,
	}, nil
}

// 统一限制消息、附件和总字符数，并要求最后一条来自用户，避免把后台对话接口当成任意代理。
func normalizeManagedChatMessages(messages []ManagedChatMessage) ([]ManagedChatMessage, error) {
	if len(messages) == 0 || len(messages) > managedChatMaxMessages {
		return nil, ErrManagedChatInvalidRequest
	}
	normalized := make([]ManagedChatMessage, 0, len(messages))
	totalRunes := 0
	for _, message := range messages {
		message.Role = strings.ToLower(strings.TrimSpace(message.Role))
		message.Content = strings.TrimSpace(message.Content)
		count := utf8.RuneCountInString(message.Content)
		if (message.Role != "user" && message.Role != "assistant") || count > managedChatMaxMessageRunes || len(message.Attachments) > managedChatMaxAttachments {
			return nil, ErrManagedChatInvalidRequest
		}
		if message.Role == "assistant" && len(message.Attachments) > 0 {
			return nil, ErrManagedChatInvalidRequest
		}
		for index := range message.Attachments {
			attachment := &message.Attachments[index]
			attachment.Name = strings.TrimSpace(attachment.Name)
			attachment.MediaType = strings.ToLower(strings.TrimSpace(attachment.MediaType))
			attachment.Content = strings.TrimSpace(attachment.Content)
			fileRunes := utf8.RuneCountInString(attachment.Content)
			if !validManagedChatAttachment(*attachment, fileRunes) {
				return nil, ErrManagedChatInvalidRequest
			}
			count += fileRunes
		}
		if count == 0 {
			return nil, ErrManagedChatInvalidRequest
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

func validManagedChatAttachment(attachment ManagedChatAttachment, contentRunes int) bool {
	return attachment.Name != "" && utf8.RuneCountInString(attachment.Name) <= 255 &&
		!strings.ContainsAny(attachment.Name, "\r\n\x00") &&
		attachment.MediaType != "" && len(attachment.MediaType) <= 128 &&
		!strings.ContainsAny(attachment.MediaType, "\r\n\x00") &&
		attachment.Size >= 0 && contentRunes > 0 && contentRunes <= managedChatMaxFileRunes
}

func managedChatProviderMessages(messages []ManagedChatMessage) []map[string]string {
	providerMessages := make([]map[string]string, 0, len(messages))
	for _, message := range messages {
		sections := make([]string, 0, len(message.Attachments)+1)
		if content := strings.TrimSpace(message.Content); content != "" {
			sections = append(sections, content)
		}
		for _, attachment := range message.Attachments {
			sections = append(sections, fmt.Sprintf("附件：%s（%s）\n--- 文件内容开始 ---\n%s\n--- 文件内容结束 ---", attachment.Name, attachment.MediaType, attachment.Content))
		}
		providerMessages = append(providerMessages, map[string]string{"role": message.Role, "content": strings.Join(sections, "\n\n")})
	}
	return providerMessages
}

func (h *Handler) GetManagedChatModel(w http.ResponseWriter, r *http.Request) {
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	items, err := store.ListManagedAPIConfigs(r.Context(), mustUser(r).TenantID)
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	status := ManagedChatModelStatus{Message: "学校尚未配置当前使用的对话模型"}
	for _, item := range items {
		if !item.IsDefault {
			continue
		}
		status.DisplayName, status.ModelName, status.ProviderKey = item.DisplayName, item.ModelName, item.ProviderKey
		status.Available = item.Status == "active" && item.LastCapabilityStatus == "success" && item.LastCapabilityVersion == ManagedCapabilityProbeVersion
		if status.Available {
			status.Message = "当前模型可用"
		} else {
			status.Message = "当前模型尚未通过能力检测，请联系平台管理员"
		}
		break
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"model": status})
}

func (h *Handler) CreateManagedChatCompletion(w http.ResponseWriter, r *http.Request) {
	release, allowed := h.reserveManagedChat(r)
	if !allowed {
		httpx.Error(w, r, http.StatusTooManyRequests, "ai_chat_concurrency_limit", "最多同时进行 5 个对话")
		return
	}
	defer release()
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, managedChatBodyLimit)
	var input ManagedChatRequest
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	messages, err := normalizeManagedChatMessages(input.Messages)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_ai_chat_request", "对话内容为空、过长或格式不正确")
		return
	}
	connection, err := ResolveDefaultManagedAPI(r.Context(), store, mustUser(r).TenantID)
	if err != nil || connection == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "ai_chat_model_unavailable", "当前没有可用的学校对话模型，请联系平台管理员")
		return
	}
	chatter := h.managedAPIChatter
	if chatter == nil {
		chatter = NewHTTPManagedAPIChatter(0)
	}
	completion, err := chatter.Chat(r.Context(), *connection, messages)
	if err != nil {
		h.recordUsage(r.Context(), ModelUsageEvent{TenantID: mustUser(r).TenantID, RequestID: logger.RequestID(r.Context()), Feature: "school_ai_chat", ProviderKey: connection.Config.ProviderKey, ModelName: connection.Config.ModelName, Status: "failed", Metadata: map[string]any{"message_count": len(messages)}})
		var providerErr ManagedChatProviderError
		if errors.As(err, &providerErr) && providerErr.StatusCode == http.StatusTooManyRequests {
			httpx.Error(w, r, http.StatusTooManyRequests, "ai_chat_rate_limited", "模型服务繁忙，请稍后重试")
			return
		}
		httpx.Error(w, r, http.StatusBadGateway, "ai_chat_provider_failed", "模型暂时无法回答，请稍后重试")
		return
	}
	h.recordUsage(r.Context(), ModelUsageEvent{TenantID: mustUser(r).TenantID, RequestID: logger.RequestID(r.Context()), Feature: "school_ai_chat", ProviderKey: connection.Config.ProviderKey, ModelName: connection.Config.ModelName, InputTokens: completion.Usage.InputTokens, OutputTokens: completion.Usage.OutputTokens, CachedInputTokens: completion.Usage.CachedInputTokens, ReasoningTokens: completion.Usage.ReasoningTokens, TotalTokens: completion.Usage.TotalTokens, Status: "succeeded", Metadata: map[string]any{"message_count": len(messages), "finish_reason": completion.FinishReason}})
	h.auditAction(r, "model.managed_chat_completed", "managed_model_api_config", connection.Config.ID, "school administrator used the managed model chat",
		map[string]any{"provider_key": connection.Config.ProviderKey, "model_name": connection.Config.ModelName, "message_count": len(messages), "total_tokens": completion.Usage.TotalTokens, "finish_reason": completion.FinishReason})
	httpx.JSON(w, http.StatusOK, map[string]any{"completion": completion})
}

// 并发额度按租户和用户分别计数；调用方必须执行返回的释放函数，异常路径也不能泄漏额度。
func (h *Handler) reserveManagedChat(r *http.Request) (func(), bool) {
	user := mustUser(r)
	key := user.TenantID + ":" + user.ID
	h.chatMu.Lock()
	defer h.chatMu.Unlock()
	if h.activeChats[key] >= 5 {
		return nil, false
	}
	if h.activeChats == nil {
		h.activeChats = make(map[string]int)
	}
	h.activeChats[key]++
	return func() {
		h.chatMu.Lock()
		defer h.chatMu.Unlock()
		h.activeChats[key]--
		if h.activeChats[key] == 0 {
			delete(h.activeChats, key)
		}
	}, true
}

func (h *Handler) StreamManagedChatCompletion(w http.ResponseWriter, r *http.Request) {
	release, allowed := h.reserveManagedChat(r)
	if !allowed {
		httpx.Error(w, r, http.StatusTooManyRequests, "ai_chat_concurrency_limit", "最多同时进行 5 个对话")
		return
	}
	defer release()
	store, ok := h.managedAPIStore(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, managedChatBodyLimit)
	var input ManagedChatRequest
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	messages, err := normalizeManagedChatMessages(input.Messages)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_ai_chat_request", "对话内容为空、过长或格式不正确")
		return
	}
	connection, err := ResolveDefaultManagedAPI(r.Context(), store, mustUser(r).TenantID)
	if err != nil || connection == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "ai_chat_model_unavailable", "当前没有可用的学校对话模型，请联系平台管理员")
		return
	}
	chatter := h.managedAPIChatter
	if chatter == nil {
		chatter = NewHTTPManagedAPIChatter(0)
	}
	flusher := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := flusher.Flush(); err != nil {
		return
	}
	emit := func(event ManagedChatEvent) error {
		if event.Type == "done" && event.Completion != nil {
			h.recordUsage(r.Context(), ModelUsageEvent{TenantID: mustUser(r).TenantID, RequestID: logger.RequestID(r.Context()), Feature: "school_ai_chat", ProviderKey: connection.Config.ProviderKey, ModelName: connection.Config.ModelName, InputTokens: event.Completion.Usage.InputTokens, OutputTokens: event.Completion.Usage.OutputTokens, CachedInputTokens: event.Completion.Usage.CachedInputTokens, ReasoningTokens: event.Completion.Usage.ReasoningTokens, TotalTokens: event.Completion.Usage.TotalTokens, Status: "succeeded", Metadata: map[string]any{"message_count": len(messages), "finish_reason": event.Completion.FinishReason, "stream": true}})
			h.auditAction(r, "model.managed_chat_completed", "managed_model_api_config", connection.Config.ID, "school administrator used the managed model chat",
				map[string]any{"provider_key": connection.Config.ProviderKey, "model_name": connection.Config.ModelName, "message_count": len(messages), "total_tokens": event.Completion.Usage.TotalTokens, "finish_reason": event.Completion.FinishReason})
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(w, "data: %s\n\n", encoded); err != nil {
			return err
		}
		if err := flusher.Flush(); err != nil {
			return err
		}
		return r.Context().Err()
	}
	if streamer, ok := chatter.(ManagedAPIStreamChatter); ok {
		err = streamer.StreamChat(r.Context(), *connection, messages, emit)
	} else {
		var completion ManagedChatCompletion
		completion, err = chatter.Chat(r.Context(), *connection, messages)
		if err == nil {
			err = emit(ManagedChatEvent{Type: "done", Completion: &completion})
		}
	}
	if err != nil && r.Context().Err() == nil {
		h.recordUsage(r.Context(), ModelUsageEvent{TenantID: mustUser(r).TenantID, RequestID: logger.RequestID(r.Context()), Feature: "school_ai_chat", ProviderKey: connection.Config.ProviderKey, ModelName: connection.Config.ModelName, Status: "failed", Metadata: map[string]any{"message_count": len(messages), "stream": true}})
		_ = emit(ManagedChatEvent{Type: "error"})
	}
}
