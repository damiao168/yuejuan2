package schoolassistant

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestConversationWebSearchFailedActionModelRecordsEveryActualCallWithoutCompletion(t *testing.T) {
	for _, plan := range []string{`{"action":"search","query":"公开资料"}`, researchAnswerAction("不客气。", nil, nil)} {
		t.Run(plan, func(t *testing.T) {
			provider := newSearchFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"results":[{"title":"公开资料","url":"https://example.com/public","content":"摘要"}]}`)
			})
			modelErr := errors.New("answer unavailable")
			model := &webSearchScriptModel{plan: plan, streamErr: modelErr}
			usage := &conversationUsage{}
			service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider, Usage: usage})
			input := conversationInput("公开资料")
			input.WebSearch = true
			err := service.Converse(toolTestContext(t, "school_admin"), input, func(event ConversationEvent) error {
				if event.Type == "done" {
					t.Error("failed final model emitted completion")
				}
				return nil
			})
			wantCount, wantTokens := 1, int64(15)
			if strings.Contains(plan, `"action":"search"`) {
				wantCount, wantTokens = 2, 40
			}
			var total int64
			for _, event := range usage.events {
				total += event.TotalTokens
			}
			if !errors.Is(err, modelErr) || len(usage.events) != wantCount || (wantCount > 1 && usage.events[0].Status != "succeeded") || usage.events[wantCount-1].Status != "failed" || usage.events[wantCount-1].Metadata["step"] != wantCount || total != wantTokens {
				t.Fatalf("failed final call lost incurred usage: %v %+v", err, usage.events)
			}
		})
	}
}

func TestConversationWebSearchInvalidPlanNeverFallsBackToLiteralSearch(t *testing.T) {
	for _, plan := range []string{
		"告诉我结果", "```json\n{\"action\":\"search\",\"query\":\"北京天气\"}\n```",
		`{"action":"search"}`, `{"action":"search","query":" "}`,
		`{"action":"search","query":"北京天气","answer":"额外回答"}`,
		`{"action":"search","query":"北京天气","extra":true}`,
		`{"action":"search","query":"北京天气","query":"上海天气"}`,
		`{"action":"search","query":"北京天气"} {}`,
		`{"action":"unknown","query":"北京天气"}`,
		`{"action":"answer","answer":" "}`, `{"action":"clarify","query":"主题"}`,
		`{"action":"search","query":null}`,
		`{"action":"search","query":"` + strings.Repeat("中", webSearchMaxQueryRunes+1) + `"}`,
	} {
		t.Run(plan[:min(len(plan), 60)], func(t *testing.T) {
			provider := newSearchFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid plan reached public search") })
			model := &webSearchScriptModel{plan: plan}
			usage := &conversationUsage{}
			service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider, Usage: usage})
			input := conversationInput("告诉我结果")
			input.WebSearch = true
			err := service.Converse(toolTestContext(t, "school_admin"), input, nil)
			requireToolError(t, err, "invalid_web_search_action")
			if model.streamCalls != 1 || len(model.requests) != 1 || len(usage.events) != 1 {
				t.Fatalf("invalid plan continued or lost incurred usage: %+v %+v", model, usage.events)
			}
		})
	}
}

func TestConversationWebSearchPlanningFailureBudgetAndCancellationStopNetwork(t *testing.T) {
	for _, name := range []string{"model failure", "budget", "tool call", "cancel model event", "cancel planning"} {
		t.Run(name, func(t *testing.T) {
			provider := newSearchFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("failed planning reached public search") })
			ctx, cancel := context.WithCancel(toolTestContext(t, "school_admin"))
			defer cancel()
			model := &webSearchScriptModel{}
			var wantErr error
			wantCalls := 1
			switch name {
			case "model failure":
				wantErr = errors.New("planning unavailable")
				model.planErr = wantErr
			case "budget":
				wantErr = errConversationBudget
				model.planUsage.TotalTokens = conversationMaxTokens
			case "tool call":
				model.planToolCalls = []modelgovernance.AgentToolCall{conversationCall("forged", "web_search", `{}`)}
			case "cancel model event":
				wantErr, wantCalls = context.Canceled, 0
			case "cancel planning":
				wantErr = context.Canceled
				model.onPlan = func(context.Context) { cancel() }
			}
			usage := &conversationUsage{}
			service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider, Usage: usage})
			input := conversationInput("北京天气")
			input.WebSearch = true
			err := service.Converse(ctx, input, func(event ConversationEvent) error {
				if name == "cancel model event" && event.Type == "model" {
					cancel()
				}
				return nil
			})
			if name == "tool call" {
				requireToolError(t, err, "invalid_web_search_action")
			} else if !errors.Is(err, wantErr) {
				t.Fatalf("planning stop reason lost: got=%v want=%v", err, wantErr)
			}
			if model.streamCalls != wantCalls || len(model.requests) != wantCalls || len(usage.events) != wantCalls {
				t.Fatalf("planning stop continued or lost usage: model=%+v usage=%+v", model, usage.events)
			}
		})
	}
}

func TestConversationSearchProviderDoesNotChangeOrdinaryConversation(t *testing.T) {
	provider := newSearchFixture(t, func(_ http.ResponseWriter, _ *http.Request) { t.Error("search ran without opt-in") })
	model := &conversationScriptModel{}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider})
	if err := service.Converse(toolTestContext(t, "school_admin"), conversationInput("写考试通知"), nil); err != nil || model.streamCalls != 1 {
		t.Fatalf("ordinary conversation changed: %v", err)
	}
}

func TestConversationWebSearchKeepsRoleBoundaryBeforeNetwork(t *testing.T) {
	provider := newSearchFixture(t, func(_ http.ResponseWriter, _ *http.Request) { t.Error("unauthorized search reached network") })
	for _, test := range []struct {
		name string
		role string
		code string
	}{
		{"teacher", "review_teacher", "forbidden"},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := &conversationScriptModel{}
			service := NewConversationService(NewService(Dependencies{Exams: &toolTestExams{}}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider})
			input := conversationInput("公开资料")
			input.WebSearch = true
			err := service.Converse(toolTestContext(t, test.role, "exam:manage"), input, nil)
			requireToolError(t, err, test.code)
			if model.streamCalls != 0 || len(model.requests) != 0 {
				t.Fatal("unauthorized search reached model")
			}
		})
	}
}
