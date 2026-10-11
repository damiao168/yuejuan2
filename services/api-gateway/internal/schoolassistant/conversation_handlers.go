package schoolassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

func (h *Handler) ConversationStream(w http.ResponseWriter, r *http.Request) {
	var input ConversationRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		httpx.Error(w, r, 400, "invalid_json", "请求必须为包含已知字段的 JSON 对象。")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, 400, "invalid_json", "请求只能包含一个 JSON 对象。")
		return
	}
	user, _, err := principal(r.Context())
	if err != nil {
		writeQueryError(w, r, err)
		return
	}
	release, allowed := h.conversation.reserve(user)
	if !allowed {
		httpx.Error(w, r, 429, "ai_chat_concurrency_limit", "最多同时进行 5 个对话。")
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	// 页面范围、消息角色和模型配置在流开始前核实；模型提出的新资源在工具执行时再次授权。
	p, err := h.conversation.prepare(ctx, input)
	if err != nil {
		writeQueryError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := http.NewResponseController(w).Flush(); err != nil {
		return
	}
	seq := 0
	emit := func(event ConversationEvent) error { seq = event.Seq; return writeConversationSSE(ctx, w, event) }
	if err = h.conversation.run(ctx, p, emit); err != nil && r.Context().Err() == nil {
		code, message := conversationError(err)
		_ = writeConversationSSE(r.Context(), w, ConversationEvent{Type: "error", RequestID: p.requestID, Seq: seq + 1, Code: code, Message: message})
	}
}

func conversationError(err error) (string, string) {
	var rejection *queryError
	if errors.As(err, &rejection) {
		return rejection.code, rejection.message
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "ai_chat_timeout", "对话处理超时，请缩小问题范围后重试。"
	}
	if errors.Is(err, errConversationBudget) {
		return "ai_chat_budget_exceeded", "本次问题需要的查询步骤过多，请分成更具体的问题。"
	}
	if errors.Is(err, modelgovernance.ErrAgentToolsUnsupported) {
		return "ai_chat_tools_unsupported", "当前模型不支持业务工具调用，请联系平台管理员更换兼容模型。"
	}
	var providerErr modelgovernance.ManagedChatProviderError
	if errors.As(err, &providerErr) && providerErr.StatusCode == 429 {
		return "ai_chat_rate_limited", "模型服务繁忙，请稍后重试。"
	}
	return "ai_chat_provider_failed", "模型暂时无法回答，请稍后重试。"
}

func writeConversationSSE(ctx context.Context, w http.ResponseWriter, event ConversationEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, encoded); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}
