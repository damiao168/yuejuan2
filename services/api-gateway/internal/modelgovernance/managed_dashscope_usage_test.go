package modelgovernance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestDashScopeNativeUsageReadsCacheAliasesWithoutDoubleCountingInput(t *testing.T) {
	for _, test := range []struct {
		name   string
		cache  string
		cached int64
	}{
		{name: "documented details", cache: `"prompt_tokens_details":{"cached_tokens":4}`, cached: 4},
		{name: "older region", cache: `"cached_tokens":4`, cached: 4},
		{name: "compatible input details alias", cache: `"input_tokens_details":{"cached_tokens":4}`, cached: 4},
		{name: "overlapping aliases", cache: `"cached_tokens":4,"prompt_tokens_details":{"cached_tokens":6},"input_tokens_details":{"cached_tokens":5}`, cached: 6},
		{name: "cache is subset of input", cache: `"cached_tokens":99`, cached: 10},
		{name: "negative cache excluded", cache: `"cached_tokens":-1`, cached: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := json.RawMessage(`{"input_tokens":10,"output_tokens":3,` + test.cache + `}`)
			usage := parseAgentUsage(raw, true)
			if usage.InputTokens != 10 || usage.OutputTokens != 3 || usage.TotalTokens != 13 || usage.CachedInputTokens != test.cached {
				t.Fatalf("native cache accounting changed input totals: %+v", usage)
			}
		})
	}
}

func TestDashScopeNativeCompletionParsersRetainCachedUsage(t *testing.T) {
	connection := ManagedAPIConnection{Config: ManagedAPIConfig{AdapterType: "dashscope_native", ModelName: "qwen-model"}}
	body := []byte(`{"output":{"choices":[{"message":{"role":"assistant","content":"回答"},"finish_reason":"stop"}]},"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":4}}}`)
	agent, err := parseAgentCompletion(connection, body)
	if err != nil || agent.Usage.CachedInputTokens != 4 || agent.Usage.InputTokens != 10 || agent.Usage.TotalTokens != 13 {
		t.Fatalf("agent completion lost native cached usage: %+v %v", agent.Usage, err)
	}
	chat, err := parseManagedDashScopeCompletion(connection, body)
	if err != nil || chat.Usage != agent.Usage {
		t.Fatalf("compatibility completion accounting differs: %+v %v", chat.Usage, err)
	}
}

func TestDashScopeNativeStreamsReplaceCumulativeUsageSnapshots(t *testing.T) {
	// 多次使用同一最终用量帧，验证帧重发不会使输入、输出或缓存命中翻倍。
	initial := `{"output":{"choices":[{"message":{"content":"回答"},"finish_reason":"stop"}]},"usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":4}}}`
	final := `{"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":4}}}`
	stream := fmt.Sprintf("data: %s\n\ndata: %s\n\ndata: %s\n\ndata: [DONE]\n\n", initial, final, final)
	connection := ManagedAPIConnection{Config: ManagedAPIConfig{AdapterType: "dashscope_native", ModelName: "qwen-model"}}
	state := agentStreamState{dashscope: true}
	if err := state.read(strings.NewReader(stream), func(AgentEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	agent, err := state.result(connection, nil)
	if err != nil || agent.Message.Content != "回答" || agent.Usage != (ManagedAPIProbeUsage{InputTokens: 10, OutputTokens: 3, TotalTokens: 13, CachedInputTokens: 4}) {
		t.Fatalf("agent stream accumulated duplicate snapshots: %+v %v", agent, err)
	}
	var completion *ManagedChatCompletion
	err = consumeManagedChatStream(strings.NewReader(stream), connection, true, func(event ManagedChatEvent) error {
		if event.Type == "done" {
			completion = event.Completion
		}
		return nil
	})
	if err != nil || completion == nil || completion.Usage != agent.Usage || completion.Message.Content != "回答" {
		t.Fatalf("compatibility stream accumulated duplicate snapshots: %+v %v", completion, err)
	}
}
