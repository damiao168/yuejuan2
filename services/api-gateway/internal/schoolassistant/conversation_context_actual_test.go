package schoolassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

type actualInputFeedbackModel struct {
	usage  modelgovernance.ManagedAPIProbeUsage
	err    error
	onCall func()
}

func (m actualInputFeedbackModel) Complete(context.Context, modelgovernance.ManagedAPIConnection, modelgovernance.AgentRequest) (modelgovernance.AgentResponse, error) {
	if m.onCall != nil {
		m.onCall()
	}
	return modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Role: "assistant", Content: "回答"}, Usage: m.usage}, m.err
}

func (m actualInputFeedbackModel) Stream(ctx context.Context, connection modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest, emit func(modelgovernance.AgentEvent) error) (modelgovernance.AgentResponse, error) {
	response, err := m.Complete(ctx, connection, request)
	if err == nil {
		err = emit(modelgovernance.AgentEvent{Type: "content", Delta: response.Message.Content})
	}
	return response, err
}

func callActualInputFeedbackModel(ctx context.Context, model actualInputFeedbackModel, request modelgovernance.AgentRequest, stream bool) (modelgovernance.AgentResponse, error) {
	if stream {
		return streamConversationModel(conversationModelPhase(ctx, "answer"), model, modelgovernance.ManagedAPIConnection{}, request, func(modelgovernance.AgentEvent) error { return nil }, time.Second)
	}
	service := NewConversationService(nil, ConversationDependencies{Model: model})
	return service.completeConversationModel(ctx, preparedConversation{}, request, "planning", 1, time.Second)
}

func TestConversationActualInputCorrectsEstimateAndRetainsBudget(t *testing.T) {
	request := validTestAgentRequest()
	_, initial, err := modelgovernance.ApplyAgentContextBudget(modelgovernance.ManagedAPIConfig{}, request)
	if err != nil {
		t.Fatal(err)
	}
	// ASCII 正文每增加一字节，默认保守估算也增加一；模拟实际页面上 4750 被供应商 989 修正。
	request.Messages[len(request.Messages)-1].Content += strings.Repeat("x", 4750-initial.EstimatedInputTokens)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			var feedback []ConversationContextBudget
			ctx := withConversationContextObserver(context.Background(), false, func(value ConversationContextBudget) error {
				feedback = append(feedback, value)
				return nil
			})
			model := actualInputFeedbackModel{usage: modelgovernance.ManagedAPIProbeUsage{InputTokens: 989, CachedInputTokens: 500, OutputTokens: 11, TotalTokens: 1000}}
			if _, err := callActualInputFeedbackModel(ctx, model, request, stream); err != nil {
				t.Fatal(err)
			}
			if len(feedback) != 2 || feedback[0].EstimatedInputTokens != 4750 || feedback[0].ActualInputTokens != 0 || feedback[1].ActualInputTokens != 989 {
				t.Fatalf("missing estimate-to-provider correction: %+v", feedback)
			}
			corrected := feedback[1]
			corrected.ActualInputTokens = 0
			if !reflect.DeepEqual(feedback[0], corrected) {
				t.Fatalf("provider feedback changed the applied budget or memory state: before=%+v after=%+v", feedback[0], corrected)
			}
		})
	}
}

func TestConversationActualInputIsPerCallAcrossAnswerPaths(t *testing.T) {
	for _, mode := range []string{"ordinary", "direct", "public"} {
		t.Run(mode, func(t *testing.T) {
			deps := ConversationDependencies{Models: conversationStore(), Model: &conversationScriptModel{}, DirectAnswerEnabled: mode == "direct"}
			input := conversationInput("把现有通知整理为三句话")
			want := []string{"planning:0", "planning:10", "answer:0", "answer:20"}
			if mode == "direct" {
				want = []string{"planning:0", "planning:10"}
			}
			if mode == "public" {
				deps.Model = &publicLoopModel{replies: []string{publicLoopAnswer("可以将现有通知整理为三句话。")}}
				input.WebSearch = true
				want = []string{"public_action:0", "public_action:5"}
			}
			service := NewConversationService(NewService(Dependencies{}), deps)
			var got []string
			var done ConversationEvent
			err := service.Converse(toolTestContext(t, "school_admin"), input, func(event ConversationEvent) error {
				if event.Type == "context" {
					got = append(got, fmt.Sprintf("%s:%d", event.ContextBudget.Phase, event.ContextBudget.ActualInputTokens))
				}
				if event.Type == "done" {
					done = event
				}
				return nil
			})
			if err != nil || done.Completion == nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("provider input accumulated or leaked into another phase: got=%v want=%v err=%v", got, want, err)
			}
		})
	}
}

func TestConversationActualInputMissingUsageAndCancellationRemainEstimates(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, cancelled := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/cancelled=%v", stream, cancelled), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var feedback []ConversationContextBudget
				ctx = withConversationContextObserver(ctx, false, func(value ConversationContextBudget) error {
					feedback = append(feedback, value)
					return nil
				})
				model := actualInputFeedbackModel{usage: modelgovernance.ManagedAPIProbeUsage{OutputTokens: 11, TotalTokens: 1000}}
				if cancelled {
					model.usage.InputTokens = 989
					model.onCall = cancel
				}
				_, err := callActualInputFeedbackModel(ctx, model, validTestAgentRequest(), stream)
				if cancelled && !errors.Is(err, context.Canceled) || !cancelled && err != nil {
					t.Fatalf("request error changed: %v", err)
				}
				if len(feedback) != 1 || feedback[0].ActualInputTokens != 0 {
					t.Fatalf("missing usage or cancelled request invented actual input: %+v", feedback)
				}
			})
		}
	}
}

func TestConversationActualInputPreservesModelAndFeedbackErrors(t *testing.T) {
	modelFailure := errors.New("synthetic model failure")
	clientClosed := errors.New("synthetic feedback failure")
	for _, stream := range []bool{false, true} {
		for _, failure := range []struct{ model, feedback error }{{modelFailure, nil}, {nil, clientClosed}, {modelFailure, clientClosed}} {
			ctx := withConversationContextObserver(context.Background(), false, func(value ConversationContextBudget) error {
				if value.ActualInputTokens > 0 {
					return failure.feedback
				}
				return nil
			})
			model := actualInputFeedbackModel{usage: modelgovernance.ManagedAPIProbeUsage{InputTokens: 989}, err: failure.model}
			response, err := callActualInputFeedbackModel(ctx, model, validTestAgentRequest(), stream)
			if response.Usage.InputTokens != 989 || failure.model != nil && !errors.Is(err, failure.model) || failure.feedback != nil && !errors.Is(err, failure.feedback) {
				t.Fatalf("feedback swallowed original model error or usage: stream=%v err=%v usage=%+v", stream, err, response.Usage)
			}
		}
	}
}

func TestConversationActualInputFieldIsOptionalOnWire(t *testing.T) {
	for _, actual := range []int64{0, 989} {
		encoded, err := json.Marshal(ConversationEvent{Type: "context", ContextBudget: &ConversationContextBudget{EstimatedInputTokens: 4750, ActualInputTokens: actual}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "actual_input_tokens") != (actual > 0) {
			t.Fatalf("optional field changed the old estimate-only contract: %s", encoded)
		}
		var restored ConversationEvent
		if err := json.Unmarshal(encoded, &restored); err != nil || restored.ContextBudget.ActualInputTokens != actual {
			t.Fatalf("context event did not round-trip: %+v %v", restored, err)
		}
	}
}
