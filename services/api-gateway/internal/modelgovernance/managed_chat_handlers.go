package modelgovernance

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"log/slog"
	"net/http"
)

func (h *Handler) GetManagedChatModel(w http.ResponseWriter, r *http.Request) {
	h.getChatModels(w, r)
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
	connection, err := ResolveChatManagedAPI(r.Context(), store, mustUser(r).TenantID, input.ModelConfigID)
	if err != nil || connection == nil {
		if writeManagedChatContextError(w, r, err) {
			return
		}
		httpx.Error(w, r, http.StatusServiceUnavailable, "ai_chat_model_unavailable", "当前没有可用的学校对话模型，请联系平台管理员")
		return
	}
	messages, ok = prepareManagedChatForHandler(w, r, connection.Config, messages)
	if !ok {
		return
	}
	chatter := h.managedAPIChatter
	if chatter == nil {
		chatter = NewHTTPManagedAPIChatter(0)
	}
	// 每次实际生成都有服务端唯一计量ID；客户端追踪ID只作关联，不承担幂等身份。
	generationID := uuid.NewString()
	completion, err := chatter.Chat(r.Context(), *connection, messages)
	if err != nil {
		h.recordUsage(r.Context(), ModelUsageEvent{TenantID: mustUser(r).TenantID, RequestID: generationID, Feature: "school_ai_chat", ProviderKey: connection.Config.ProviderKey, ModelName: connection.Config.ModelName, Status: "failed", Metadata: map[string]any{"trace_request_id": logger.RequestID(r.Context()), "message_count": len(messages)}})
		if writeManagedChatContextError(w, r, err) {
			return
		}
		var providerErr ManagedChatProviderError
		if errors.As(err, &providerErr) && providerErr.StatusCode == http.StatusTooManyRequests {
			httpx.Error(w, r, http.StatusTooManyRequests, "ai_chat_rate_limited", "模型服务繁忙，请稍后重试")
			return
		}
		httpx.Error(w, r, http.StatusBadGateway, "ai_chat_provider_failed", "模型暂时无法回答，请稍后重试")
		return
	}
	h.recordUsage(r.Context(), ModelUsageEvent{TenantID: mustUser(r).TenantID, RequestID: generationID, Feature: "school_ai_chat", ProviderKey: connection.Config.ProviderKey, ModelName: connection.Config.ModelName, InputTokens: completion.Usage.InputTokens, OutputTokens: completion.Usage.OutputTokens, CachedInputTokens: completion.Usage.CachedInputTokens, ReasoningTokens: completion.Usage.ReasoningTokens, TotalTokens: completion.Usage.TotalTokens, Status: "succeeded", Metadata: map[string]any{"trace_request_id": logger.RequestID(r.Context()), "message_count": len(messages), "finish_reason": completion.FinishReason}})
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
	connection, err := ResolveChatManagedAPI(r.Context(), store, mustUser(r).TenantID, input.ModelConfigID)
	if err != nil || connection == nil {
		if writeManagedChatContextError(w, r, err) {
			return
		}
		httpx.Error(w, r, http.StatusServiceUnavailable, "ai_chat_model_unavailable", "当前没有可用的学校对话模型，请联系平台管理员")
		return
	}
	// 在提交 SSE 响应头前完成预算检查，超限以可读 HTTP 错误返回，不能先启动模型生成。
	messages, ok = prepareManagedChatForHandler(w, r, connection.Config, messages)
	if !ok {
		return
	}
	chatter := h.managedAPIChatter
	if chatter == nil {
		chatter = NewHTTPManagedAPIChatter(0)
	}
	generationID := uuid.NewString()
	flusher := http.NewResponseController(w)
	w.Header().Set(managedChatContentTypeHeader, "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := flusher.Flush(); err != nil {
		return
	}
	emit := func(event ManagedChatEvent) error {
		if event.Type == "done" && event.Completion != nil {
			h.recordUsage(r.Context(), ModelUsageEvent{TenantID: mustUser(r).TenantID, RequestID: generationID, Feature: "school_ai_chat", ProviderKey: connection.Config.ProviderKey, ModelName: connection.Config.ModelName, InputTokens: event.Completion.Usage.InputTokens, OutputTokens: event.Completion.Usage.OutputTokens, CachedInputTokens: event.Completion.Usage.CachedInputTokens, ReasoningTokens: event.Completion.Usage.ReasoningTokens, TotalTokens: event.Completion.Usage.TotalTokens, Status: "succeeded", Metadata: map[string]any{"trace_request_id": logger.RequestID(r.Context()), "message_count": len(messages), "finish_reason": event.Completion.FinishReason, "stream": true}})
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
		h.recordUsage(r.Context(), ModelUsageEvent{TenantID: mustUser(r).TenantID, RequestID: generationID, Feature: "school_ai_chat", ProviderKey: connection.Config.ProviderKey, ModelName: connection.Config.ModelName, Status: "failed", Metadata: map[string]any{"trace_request_id": logger.RequestID(r.Context()), "message_count": len(messages), "stream": true}})
		_ = emit(ManagedChatEvent{Type: "error"})
	}
}

func prepareManagedChatForHandler(w http.ResponseWriter, r *http.Request, config ManagedAPIConfig, messages []ManagedChatMessage) ([]ManagedChatMessage, bool) {
	prepared, usage, err := prepareManagedChatContext(config, messages)
	if err != nil {
		if !writeManagedChatContextError(w, r, err) {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_ai_chat_request", "对话内容为空、过长或格式不正确")
		}
		return nil, false
	}
	slog.InfoContext(r.Context(), "managed chat context budget", "event", "school_assistant_context_budget", "feature", "school_ai_chat",
		"estimated_input_tokens", usage.EstimatedInputTokens, "input_budget_tokens", usage.InputBudgetTokens,
		"max_output_tokens", usage.MaxOutputTokens, "context_window_tokens", usage.ContextWindowTokens,
		"dropped_messages", usage.DroppedMessages, "estimation_method", usage.EstimationMethod)
	return prepared, true
}

func writeManagedChatContextError(w http.ResponseWriter, r *http.Request, err error) bool {
	if errors.Is(err, ErrAgentContextExceeded) {
		httpx.Error(w, r, http.StatusBadRequest, "ai_chat_context_exceeded", "本次问题或附件超过当前模型的上下文预算，请缩小内容或开启新对话。")
		return true
	}
	if errors.Is(err, ErrAgentContextPolicy) {
		httpx.Error(w, r, http.StatusServiceUnavailable, "ai_chat_context_unavailable", "当前模型的上下文容量配置无效，请联系管理员核对。")
		return true
	}
	return false
}
