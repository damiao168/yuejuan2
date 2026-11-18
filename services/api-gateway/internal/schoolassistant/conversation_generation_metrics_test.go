package schoolassistant

import (
	"context"
	"math"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

func TestConversationGenerationMetricsUseProviderTokensAndWholeCallTime(t *testing.T) {
	metrics := conversationGenerationMetrics("model-a", modelgovernance.ManagedAPIProbeUsage{OutputTokens: 60, ReasoningTokens: 20}, 2500*time.Millisecond)
	if metrics == nil || metrics.ModelConfigID != "model-a" || metrics.OutputTokens != 60 || metrics.DurationMS != 2500 || metrics.TokensPerSecond != 24 {
		t.Fatalf("provider output count or wall-time rate changed: %+v", metrics)
	}
	for _, test := range []struct {
		output  int64
		elapsed time.Duration
	}{{0, time.Second}, {-1, time.Second}, {10, 0}, {10, -time.Second}, {10, time.Microsecond}} {
		if got := conversationGenerationMetrics("model-a", modelgovernance.ManagedAPIProbeUsage{OutputTokens: test.output}, test.elapsed); got != nil {
			t.Fatalf("missing measurement invented a rate: %+v", got)
		}
	}
}

type timedConversationMetricsModel struct {
	conversationScriptModel
}

func (m *timedConversationMetricsModel) Stream(ctx context.Context, connection modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest, emit func(modelgovernance.AgentEvent) error) (modelgovernance.AgentResponse, error) {
	// 模拟代理先缓冲一段时间，再一次发完整正文；不能拿最后一次 delta 的发送时间当生成耗时。
	time.Sleep(20 * time.Millisecond)
	return m.conversationScriptModel.Stream(ctx, connection, request, emit)
}

func TestConversationDoneMetricsExcludePlanningTokens(t *testing.T) {
	model := &timedConversationMetricsModel{}
	store := conversationStore()
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: store, Model: model})
	var done ConversationEvent
	err := service.Converse(toolTestContext(t, "school_admin"), conversationInput("帮我起草一份通知"), func(event ConversationEvent) error {
		if event.Type == "done" {
			done = event
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	metrics := done.GenerationMetrics
	if metrics == nil || metrics.ModelConfigID != store.connection.Config.ID || metrics.OutputTokens != 5 || metrics.DurationMS < 20 || done.Completion.Usage.OutputTokens <= metrics.OutputTokens {
		t.Fatalf("answer metric used whole-turn tokens or omitted buffered duration: metrics=%+v completion=%+v", metrics, done.Completion)
	}
	if want := float64(metrics.OutputTokens) * 1000 / float64(metrics.DurationMS); math.Abs(metrics.TokensPerSecond-want) > 1e-9 {
		t.Fatalf("incorrect average output rate: got=%g want=%g", metrics.TokensPerSecond, want)
	}
}

type timedPublicMetricsModel struct {
	publicLoopModel
}

func (m *timedPublicMetricsModel) Stream(ctx context.Context, connection modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest, emit func(modelgovernance.AgentEvent) error) (modelgovernance.AgentResponse, error) {
	time.Sleep(10 * time.Millisecond)
	return m.publicLoopModel.Stream(ctx, connection, request, emit)
}

func TestPublicSearchDoneUsesFinalActionMetrics(t *testing.T) {
	model := &timedPublicMetricsModel{publicLoopModel{replies: []string{publicLoopAnswer("可以把现有通知整理为三句话。")}}}
	store := conversationStore()
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: store, Model: model})
	var done ConversationEvent
	err := service.Converse(toolTestContext(t, "school_admin"), researchConversationInput("帮我整理已有通知"), func(event ConversationEvent) error {
		if event.Type == "done" {
			done = event
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	metrics := done.GenerationMetrics
	if metrics == nil || metrics.OutputTokens != 5 || metrics.DurationMS < 10 || metrics.ModelConfigID != store.connection.Config.ID {
		t.Fatalf("public final answer has no measured rate: %+v", metrics)
	}
}
