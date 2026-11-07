package schoolassistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

type waitingConversationModel struct {
	modelgovernance.AgentModel
	started chan struct{}
	calls   int
}

func (m *waitingConversationModel) Complete(ctx context.Context, _ modelgovernance.ManagedAPIConnection, _ modelgovernance.AgentRequest) (modelgovernance.AgentResponse, error) {
	m.calls++
	if m.started != nil {
		close(m.started)
	}
	<-ctx.Done()
	return modelgovernance.AgentResponse{Usage: modelgovernance.ManagedAPIProbeUsage{InputTokens: 7, TotalTokens: 7}}, ctx.Err()
}

func TestConversationModelTimeoutIsBoundedAndKeepsUsage(t *testing.T) {
	model := &waitingConversationModel{}
	service := NewConversationService(nil, ConversationDependencies{Model: model})
	response, err := service.completeConversationModel(context.Background(), preparedConversation{}, modelgovernance.AgentRequest{}, "planning", 1, 10*time.Millisecond)
	code, _ := conversationError(err)
	if code != "ai_chat_model_timeout" || response.Usage.TotalTokens != 7 || model.calls != 1 {
		t.Fatalf("timeout must stay explicit without retry or lost usage: code=%s usage=%d calls=%d", code, response.Usage.TotalTokens, model.calls)
	}
}

func TestConversationModelCancellationIsNotReportedAsProviderTimeout(t *testing.T) {
	model := &waitingConversationModel{started: make(chan struct{})}
	service := NewConversationService(nil, ConversationDependencies{Model: model})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-model.started; cancel() }()
	_, err := service.completeConversationModel(ctx, preparedConversation{}, modelgovernance.AgentRequest{}, "planning", 1, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("user cancellation was replaced: %v", err)
	}
}

func TestConversationModelHonorsWholeTurnDeadline(t *testing.T) {
	model := &waitingConversationModel{}
	service := NewConversationService(nil, ConversationDependencies{Model: model})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := service.completeConversationModel(ctx, preparedConversation{}, modelgovernance.AgentRequest{}, "planning", 1, time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("whole turn deadline was replaced: %v", err)
	}
}

type waitingConversationStreamModel struct {
	modelgovernance.AgentStreamModel
	chunks   int
	interval time.Duration
}

func (m waitingConversationStreamModel) Stream(ctx context.Context, _ modelgovernance.ManagedAPIConnection, _ modelgovernance.AgentRequest, emit func(modelgovernance.AgentEvent) error) (modelgovernance.AgentResponse, error) {
	for i := 0; i < m.chunks; i++ {
		select {
		case <-ctx.Done():
			return modelgovernance.AgentResponse{}, ctx.Err()
		case <-time.After(m.interval):
		}
		if err := emit(modelgovernance.AgentEvent{Type: "content", Delta: "正文"}); err != nil {
			return modelgovernance.AgentResponse{}, err
		}
	}
	if m.chunks == 0 {
		<-ctx.Done()
		return modelgovernance.AgentResponse{}, ctx.Err()
	}
	return modelgovernance.AgentResponse{}, nil
}

func TestConversationStreamIdleStopsUnresponsiveModel(t *testing.T) {
	_, err := streamConversationModel(context.Background(), waitingConversationStreamModel{}, modelgovernance.ManagedAPIConnection{}, modelgovernance.AgentRequest{}, func(modelgovernance.AgentEvent) error { return nil }, 10*time.Millisecond)
	code, _ := conversationError(err)
	if code != "ai_chat_model_timeout" {
		t.Fatalf("idle stream returned wrong error: %v", err)
	}
}

func TestConversationStreamActiveAnswerCanOutliveIdleWindow(t *testing.T) {
	// 总输出时间超过空闲阈值，但每个正文片段间隔更短；不能把正常长回答误判为卡住。
	model := waitingConversationStreamModel{chunks: 5, interval: 10 * time.Millisecond}
	chunks := 0
	_, err := streamConversationModel(context.Background(), model, modelgovernance.ManagedAPIConnection{}, modelgovernance.AgentRequest{}, func(modelgovernance.AgentEvent) error { chunks++; return nil }, 40*time.Millisecond)
	if err != nil || chunks != 5 {
		t.Fatalf("active response was interrupted: chunks=%d err=%v", chunks, err)
	}
}
