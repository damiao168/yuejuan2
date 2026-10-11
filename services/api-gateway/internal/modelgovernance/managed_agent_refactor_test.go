package modelgovernance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type agentRefactorTransport struct {
	reader io.Reader
}

func (transport agentRefactorTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(transport.reader), Request: request}, nil
}

func agentStreamFromReader(t *testing.T, reader io.Reader, dashscope bool, emit func(AgentEvent) error) (AgentResponse, error) {
	t.Helper()
	// 可控响应体用于触发帧边界和读取故障，不连接真实供应商。
	model := NewHTTPAgentModel(0)
	model.httpClient.Transport = agentRefactorTransport{reader: reader}
	t.Cleanup(model.httpClient.CloseIdleConnections)
	adapter := "openai_compatible"
	if dashscope {
		adapter = "dashscope_native"
	}
	connection := ManagedAPIConnection{Config: ManagedAPIConfig{AdapterType: adapter, BaseURL: "https://provider.example/v1", ModelName: "test-model"}}
	return model.Stream(context.Background(), connection, AgentRequest{Messages: []AgentMessage{{Role: "user", Content: "question"}}}, emit)
}

func TestAgentTranscriptKeepsCompleteGroupsAndReusableCallIDs(t *testing.T) {
	first := AgentToolCall{ID: "call-1", Name: "get_exam_progress", Arguments: json.RawMessage(`{}`)}
	second := AgentToolCall{ID: "call-2", Name: "get_exam_progress", Arguments: json.RawMessage(`{}`)}
	group := AgentMessage{Role: "assistant", ToolCalls: []AgentToolCall{first, second}}
	firstResult := AgentMessage{Role: "tool", ToolCallID: first.ID, Content: `{}`}
	secondResult := AgentMessage{Role: "tool", ToolCallID: second.ID, Content: `{}`}
	for _, test := range []struct {
		name     string
		messages []AgentMessage
		valid    bool
	}{
		{name: "results may arrive in reverse order", messages: []AgentMessage{group, secondResult, firstResult, {Role: "user", Content: "continue"}}, valid: true},
		{name: "completed call IDs may be reused", messages: []AgentMessage{group, firstResult, secondResult, {Role: "assistant", ToolCalls: []AgentToolCall{first}}, firstResult}, valid: true},
		{name: "duplicate result cannot finish another call", messages: []AgentMessage{group, firstResult, firstResult, secondResult}},
		{name: "new turn cannot skip a pending result", messages: []AgentMessage{group, firstResult, {Role: "user", Content: "continue"}}},
		{name: "duplicate calls in one group remain invalid", messages: []AgentMessage{{Role: "assistant", ToolCalls: []AgentToolCall{first, first}}, firstResult}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateAgentRequest(AgentRequest{Messages: test.messages})
			if (err == nil) != test.valid || (err != nil && !errors.Is(err, ErrAgentInvalidRequest)) {
				t.Fatalf("valid=%t, got %v", test.valid, err)
			}
		})
	}
}

func TestAgentTranscriptCountsArgumentsAndReasoningWithinSharedBudget(t *testing.T) {
	call := AgentToolCall{ID: "call-1", Name: "get_exam_progress", Arguments: json.RawMessage(`{"exam_id":"a"}`)}
	result := AgentMessage{Role: "tool", ToolCallID: call.ID, Content: `{}`}
	// 工具参数和思考内容都占用正文预算，恰好达到上限时仍可提交完整工具组。
	content := strings.Repeat("x", managedAgentMaxContentBytes-len(call.Arguments)-len(result.Content)-1)
	for _, test := range []struct {
		name      string
		reasoning string
		valid     bool
	}{
		{name: "exact shared byte budget", reasoning: "r", valid: true},
		{name: "one byte over shared budget", reasoning: "rr"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := AgentRequest{Messages: []AgentMessage{{Role: "assistant", Content: content, ReasoningContent: test.reasoning, ToolCalls: []AgentToolCall{call}}, result}}
			if err := validateAgentRequest(request); (err == nil) != test.valid {
				t.Fatalf("valid=%t, got %v", test.valid, err)
			}
		})
	}
}

func TestAgentStreamKeepsFrameAndFinishSemantics(t *testing.T) {
	for _, test := range []struct {
		name       string
		data       string
		dashscope  bool
		finish     string
		totalUsage int64
	}{
		{name: "multiline event ends at EOF", data: "event: message\nid: 1\n: heartbeat\ndata: {\"choices\":[\ndata: {\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}", finish: "stop"},
		{name: "done prevents trailing frames", data: "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"total_tokens\":7}}\n\ndata: [DONE]\n\ndata: invalid\n\n", finish: "stop", totalUsage: 7},
		{name: "native null finish preserves prior finish", dashscope: true, data: "data: {\"output\":{\"choices\":[{\"message\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}}\n\ndata: {\"output\":{\"choices\":[{\"message\":{},\"finish_reason\":\"null\"}]},\"usage\":{\"input_tokens\":3,\"output_tokens\":4}}\n\n", finish: "stop", totalUsage: 7},
		{name: "compatible null finish remains literal", data: "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"null\"}]}\n\n", finish: "null"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var events []AgentEvent
			response, err := agentStreamFromReader(t, strings.NewReader(test.data), test.dashscope, func(event AgentEvent) error {
				events = append(events, event)
				return nil
			})
			if err != nil || response.Message.Content != "answer" || response.FinishReason != test.finish || response.Usage.TotalTokens != test.totalUsage {
				t.Fatalf("frame behavior changed: %#v, %v", response, err)
			}
			if !reflect.DeepEqual(events, []AgentEvent{{Type: "content", Delta: "answer"}}) {
				t.Fatalf("unexpected events: %#v", events)
			}
		})
	}
}

func TestAgentStreamCancellationRetainsReasoningBeforeContent(t *testing.T) {
	data := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"reasoning\",\"content\":\"answer\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":7}}\n\n"
	var events []AgentEvent
	response, err := agentStreamFromReader(t, strings.NewReader(data), false, func(event AgentEvent) error {
		events = append(events, event)
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) || response.Message.ReasoningContent != "reasoning" || response.Message.Content != "" || response.Usage.TotalTokens != 7 {
		t.Fatalf("cancellation order or partial response changed: %#v, %v", response, err)
	}
	if !reflect.DeepEqual(events, []AgentEvent{{Type: "reasoning", Delta: "reasoning"}}) {
		t.Fatalf("content emitted after cancellation: %#v", events)
	}
}

type agentFailingReader struct {
	err error
}

func (r agentFailingReader) Read([]byte) (int, error) {
	return 0, r.err
}

func TestAgentStreamRetainsReadErrorBeforeUnterminatedFrame(t *testing.T) {
	// 帧已到达但尚无分隔空行时底层读取失败，仍先返回读取错误，不补发该帧内容。
	readErr := errors.New("test stream read failure")
	reader := io.MultiReader(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}"), agentFailingReader{err: readErr})
	emitted := 0
	response, err := agentStreamFromReader(t, reader, false, func(AgentEvent) error { emitted++; return nil })
	if !errors.Is(err, readErr) || emitted != 0 || response.Message.Content != "" {
		t.Fatalf("unterminated frame consumed after read failure: %#v, events=%d, err=%v", response, emitted, err)
	}
}
