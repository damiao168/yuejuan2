package schoolassistant

import (
	"context"
	"errors"
	"log/slog"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

type ConversationContextBudget struct {
	Phase                string `json:"phase"`
	EstimatedInputTokens int    `json:"estimated_input_tokens"`
	// 最近这一次模型调用的供应商输入计数（含缓存部分）；不代表已生成输出或下一轮输入预测。
	ActualInputTokens   int64  `json:"actual_input_tokens,omitempty"`
	InputBudgetTokens   int    `json:"input_budget_tokens"`
	MaxOutputTokens     int    `json:"max_output_tokens"`
	ContextWindowTokens int    `json:"context_window_tokens"`
	DroppedMessages     int    `json:"dropped_messages"`
	EstimationMethod    string `json:"estimation_method"`
	MemoryUsed          bool   `json:"memory_used"`
	MemoryOmitted       bool   `json:"memory_omitted"`
	Accepted            bool   `json:"accepted"`
}

type conversationContextObserver struct {
	memoryRequested bool
	emit            func(ConversationContextBudget) error
}

type conversationContextObserverKey struct{}
type conversationModelPhaseKey struct{}

func withConversationContextObserver(ctx context.Context, memoryRequested bool, emit func(ConversationContextBudget) error) context.Context {
	return context.WithValue(ctx, conversationContextObserverKey{}, conversationContextObserver{memoryRequested: memoryRequested, emit: emit})
}

func conversationModelPhase(ctx context.Context, phase string) context.Context {
	return context.WithValue(ctx, conversationModelPhaseKey{}, phase)
}

func emitConversationContextBudget(ctx context.Context, request modelgovernance.AgentRequest, usage modelgovernance.AgentContextUsage, accepted bool, actualInputTokens int64) error {
	observer, ok := ctx.Value(conversationContextObserverKey{}).(conversationContextObserver)
	if !ok || observer.emit == nil {
		return nil
	}
	phase, _ := ctx.Value(conversationModelPhaseKey{}).(string)
	memoryUsed := false
	for _, message := range request.Messages {
		memoryUsed = memoryUsed || message.PreserveContext
	}
	return observer.emit(ConversationContextBudget{Phase: phase, EstimatedInputTokens: usage.EstimatedInputTokens, ActualInputTokens: actualInputTokens,
		InputBudgetTokens: usage.InputBudgetTokens, MaxOutputTokens: usage.MaxOutputTokens,
		ContextWindowTokens: usage.ContextWindowTokens, DroppedMessages: usage.DroppedMessages, EstimationMethod: usage.EstimationMethod,
		MemoryUsed: memoryUsed, MemoryOmitted: observer.memoryRequested && phase != "tool_capability" && (!memoryUsed || usage.DroppedMemoryMessages > 0), Accepted: accepted})
}

func reportConversationActualInput(ctx context.Context, request modelgovernance.AgentRequest, estimate modelgovernance.AgentContextUsage, inputTokens int64, callErr error) error {
	if ctx.Err() != nil || inputTokens <= 0 {
		return callErr
	}
	// 只修正同一次调用的展示，不把供应商计数写回预算策略，也不跨调用累计。
	if emitErr := emitConversationContextBudget(ctx, request, estimate, true, inputTokens); emitErr != nil {
		if callErr != nil {
			// 回传计数失败不能抹掉原模型错误；调用方仍可用 errors.Is/As 判断两者。
			return errors.Join(callErr, emitErr)
		}
		return emitErr
	}
	return callErr
}

func logConversationContextAccuracy(ctx context.Context, config modelgovernance.ManagedAPIConfig, estimate modelgovernance.AgentContextUsage, actual modelgovernance.ManagedAPIProbeUsage) {
	if actual.InputTokens <= 0 {
		return
	}
	phase, _ := ctx.Value(conversationModelPhaseKey{}).(string)
	// 偏差只用于观察，不把一次供应商响应学习成跨用户全局计数系数；供应商和协议版本可能不同。
	slog.InfoContext(ctx, "school assistant context accuracy", "event", "school_assistant_context_accuracy", "phase", phase,
		"model_config_id", config.ID, "model_name", config.ModelName, "model_version", config.ModelVersion,
		"estimated_input_tokens", estimate.EstimatedInputTokens, "actual_input_tokens", actual.InputTokens,
		"estimate_error_tokens", int64(estimate.EstimatedInputTokens)-actual.InputTokens, "estimation_method", estimate.EstimationMethod,
		"cached_input_tokens", actual.CachedInputTokens)
}
