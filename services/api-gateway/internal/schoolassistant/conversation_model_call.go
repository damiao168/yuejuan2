package schoolassistant

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

const conversationPlanningTimeout = 45 * time.Second

var errConversationModelIdle = errors.New("conversation model response idle")

// streamConversationModel 限制上游连续没有内容的时间，已经正常输出的长回答仍可继续到整轮时限。
// 定时器只取消模型请求，不从后台协程写 SSE；内容和结束事件仍由请求协程顺序发送。
func streamConversationModel(ctx context.Context, model modelgovernance.AgentStreamModel, connection modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest, emit func(modelgovernance.AgentEvent) error, limit time.Duration) (modelgovernance.AgentResponse, error) {
	request, estimate, err := prepareConversationModelRequest(ctx, connection, request)
	if err != nil {
		return modelgovernance.AgentResponse{}, err
	}
	callCtx, cancel := context.WithCancelCause(ctx)
	timer := time.AfterFunc(limit, func() { cancel(errConversationModelIdle) })
	defer func() { timer.Stop(); cancel(nil) }()
	response, err := model.Stream(callCtx, connection, request, func(event modelgovernance.AgentEvent) error {
		if event.Delta != "" && (event.Type == "content" || event.Type == "reasoning") {
			timer.Reset(limit)
		}
		return emit(event)
	})
	logConversationContextAccuracy(ctx, connection.Config, estimate, response.Usage)
	if ctx.Err() != nil {
		return response, ctx.Err()
	}
	if errors.Is(context.Cause(callCtx), errConversationModelIdle) {
		return response, reject(504, "ai_chat_model_timeout", "模型响应超时，请重试或切换模型。")
	}
	err = reportConversationActualInput(ctx, request, estimate, response.Usage.InputTokens, err)
	return response, err
}

// completeConversationModel 给非流式规划单独限时，不能让一个无响应的上游耗尽整轮三分钟。
// 子请求仍受整轮时限约束；不换模型、不自动重复已发生的收费请求。
func (s *ConversationService) completeConversationModel(ctx context.Context, p preparedConversation, request modelgovernance.AgentRequest, phase string, step int, limit time.Duration) (modelgovernance.AgentResponse, error) {
	ctx = conversationModelPhase(ctx, phase)
	if err := ctx.Err(); err != nil {
		return modelgovernance.AgentResponse{}, err
	}
	request, estimate, err := prepareConversationModelRequest(ctx, p.connection, request)
	if err != nil {
		return modelgovernance.AgentResponse{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	started := time.Now()
	response, err := s.deps.Model.Complete(callCtx, p.connection, request)
	logConversationContextAccuracy(ctx, p.connection.Config, estimate, response.Usage)
	if ctx.Err() != nil {
		err = ctx.Err()
	} else if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		err = reject(504, "ai_chat_model_timeout", "模型响应超时，请重试或切换模型。")
	}
	if callCtx.Err() == nil {
		err = reportConversationActualInput(ctx, request, estimate, response.Usage.InputTokens, err)
	}
	status := "succeeded"
	code := ""
	if err != nil {
		status = "failed"
		code, _ = conversationError(err)
	}
	// 仅记录阶段和耗时；聊天正文、网页内容、连接地址与凭据均不进入诊断日志。
	slog.InfoContext(ctx, "school assistant model call", "event", "school_assistant_model_call", "turn_request_id", p.requestID, "phase", phase, "step", step, "duration_ms", time.Since(started).Milliseconds(), "status", status, "error_code", code)
	return response, err
}

func prepareConversationModelRequest(ctx context.Context, connection modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest) (modelgovernance.AgentRequest, modelgovernance.AgentContextUsage, error) {
	prepared, usage, err := modelgovernance.ApplyAgentContextBudget(connection.Config, request)
	phase, _ := ctx.Value(conversationModelPhaseKey{}).(string)
	code := ""
	if err != nil {
		code, _ = conversationError(err)
	}
	// 只记录估算、配置上限及裁剪数量；不能把历史、附件、工具参数或凭据放进可观测性日志。
	slog.InfoContext(ctx, "school assistant context budget", "event", "school_assistant_context_budget", "phase", phase, "model_config_id", connection.Config.ID, "estimated_input_tokens", usage.EstimatedInputTokens, "input_budget_tokens", usage.InputBudgetTokens, "max_output_tokens", usage.MaxOutputTokens, "context_window_tokens", usage.ContextWindowTokens, "dropped_messages", usage.DroppedMessages, "dropped_memory_messages", usage.DroppedMemoryMessages, "estimation_method", usage.EstimationMethod, "accepted", err == nil, "error_code", code)
	if emitErr := emitConversationContextBudget(ctx, prepared, usage, err == nil, 0); emitErr != nil {
		return prepared, usage, emitErr
	}
	return prepared, usage, err
}
