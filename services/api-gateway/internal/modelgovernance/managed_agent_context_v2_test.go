package modelgovernance

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestAgentBudgetDistinguishesWindowAndTurnLimit(t *testing.T) {
	for _, test := range []struct {
		name      string
		content   string
		remaining int64
		want      error
	}{
		{name: "short prompt but no output allowance", content: "短问题", remaining: 500, want: ErrAgentTurnBudgetExceeded},
		{name: "large prompt even with low turn allowance", content: strings.Repeat("大", 2000), remaining: 500, want: ErrAgentContextExceeded},
		{name: "large prompt with enough turn allowance", content: strings.Repeat("大", 2000), remaining: 10000, want: ErrAgentContextExceeded},
		{name: "negative turn allowance", content: "短问题", remaining: -1, want: ErrAgentTurnBudgetExceeded},
		{name: "bounded prompt", content: "短问题", remaining: 2000},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := AgentRequest{Messages: []AgentMessage{{Role: "user", Content: test.content}}, TokenBudget: test.remaining}
			_, _, err := ApplyAgentContextBudget(smallAgentContextConfig(), request)
			if !errors.Is(err, test.want) {
				t.Fatalf("budget classification=%v want=%v", err, test.want)
			}
		})
	}
}

func TestAgentBudgetKeepsUntrustedMemoryBeforeOlderTurnsButDropsItForCurrentQuestion(t *testing.T) {
	request := AgentRequest{Messages: []AgentMessage{
		{Role: "system", Content: "规则"},
		{Role: "user", Content: "先前要求：只比较已发布成绩。", PreserveContext: true},
		{Role: "user", Content: strings.Repeat("旧问题", 1000)},
		{Role: "assistant", Content: "旧回答"},
		{Role: "user", Content: "当前问题"},
	}}
	prepared, usage, err := ApplyAgentContextBudget(smallAgentContextConfig(), request)
	if err != nil || usage.DroppedMessages != 2 || usage.DroppedMemoryMessages != 0 || len(prepared.Messages) != 3 || !prepared.Messages[1].PreserveContext || prepared.Messages[1].Role != "user" {
		t.Fatalf("memory was lost or promoted: prepared=%+v usage=%+v err=%v", prepared, usage, err)
	}
	request.Messages[1].Content = strings.Repeat("旧约定", 1000)
	prepared, usage, err = ApplyAgentContextBudget(smallAgentContextConfig(), request)
	if err != nil || usage.DroppedMemoryMessages != 1 || len(prepared.Messages) != 2 || prepared.Messages[1].Content != "当前问题" {
		t.Fatalf("memory displaced the current question: prepared=%+v usage=%+v err=%v", prepared, usage, err)
	}
}

func TestAgentBudgetRejectsMemoryFlagOnToolsAndWithoutCurrentQuestion(t *testing.T) {
	for _, message := range []AgentMessage{
		{Role: "system", Content: "规则", PreserveContext: true},
		{Role: "assistant", Content: "回答", PreserveContext: true},
		{Role: "user", Content: "只有记忆", PreserveContext: true},
	} {
		request := AgentRequest{Messages: []AgentMessage{message}}
		if _, _, err := ApplyAgentContextBudget(smallAgentContextConfig(), request); !errors.Is(err, ErrAgentInvalidRequest) {
			t.Fatalf("invalid memory flag accepted: role=%s err=%v", message.Role, err)
		}
	}
}

func TestContextTextCounterUsesExplicitEncodingAndEmbeddedVocabulary(t *testing.T) {
	for _, encoding := range []string{"cl100k_base", "o200k_base"} {
		count, method, err := resolveContextCounter(encoding)
		if err != nil || !strings.HasPrefix(method, encoding) {
			t.Fatal("explicit encoding is unavailable", encoding, err)
		}
		if tokens, err := count("hello world"); err != nil || tokens != 2 {
			t.Fatalf("reference ordinary-text count differs for %s: %d %v", encoding, tokens, err)
		}
		// 多并发计数复用同一只读词表，不在请求中改写 tokenizer 配置。
		var workers sync.WaitGroup
		for range 8 {
			workers.Go(func() {
				if tokens, err := count("hello world"); err != nil || tokens != 2 {
					t.Error("concurrent count changed", tokens, err)
				}
			})
		}
		workers.Wait()
	}
	if _, _, err := resolveContextCounter("qwen-guessed-from-name"); !errors.Is(err, ErrAgentContextPolicy) {
		t.Fatal("unknown encoding silently fell back")
	}
}

func TestContextTokenizerMatchesPythonReferenceSamples(t *testing.T) {
	// 样本由 Python tiktoken 0.12.0 的 encode(disallowed_special=()) 生成；只有本文本编码计数，未含供应商消息信封。
	samples := []struct {
		text   string
		cl100k int
		o200k  int
	}{
		{text: "hello world", cl100k: 2, o200k: 2},
		{text: "请只比较已发布的成绩，忽略未发布草稿。", cl100k: 22, o200k: 15},
		{text: "def add(a, b):\n    return a + b\n", cl100k: 12, o200k: 12},
		{text: `{"exam_id":"synthetic-exam","include_unpublished":false}`, cl100k: 15, o200k: 15},
		{text: "目标在前面。\n更正：不要使用学生姓名，只统计班级总数。", cl100k: 25, o200k: 19},
		{text: "<|endoftext|>", cl100k: 7, o200k: 7},
	}
	for _, encoding := range []string{"cl100k_base", "o200k_base"} {
		count, _, err := resolveContextCounter(encoding)
		if err != nil {
			t.Fatal(err)
		}
		for _, sample := range samples {
			want := sample.cl100k
			if encoding == "o200k_base" {
				want = sample.o200k
			}
			if actual, err := count(sample.text); err != nil || actual != want {
				t.Fatalf("encoding=%s actual=%d reference=%d err=%v", encoding, actual, want, err)
			}
		}
	}
}

func TestManagedChatUsesExplicitCounterWithoutDroppingCurrentText(t *testing.T) {
	policy := AgentContextPolicy{ContextWindowTokens: 2048, MaxInputTokens: 1000, MaxOutputTokens: 512}
	messages := []ManagedChatMessage{{Role: "user", Content: strings.Repeat("请只比较已发布的成绩，忽略未发布草稿。", 20)}}
	if _, _, err := prepareManagedChatContext(ManagedAPIConfig{ContextPolicy: policy}, messages); !errors.Is(err, ErrAgentContextExceeded) {
		t.Fatalf("conservative fallback unexpectedly accepted the large byte payload: %v", err)
	}
	policy.Tokenizer = "cl100k_base"
	prepared, usage, err := prepareManagedChatContext(ManagedAPIConfig{ContextPolicy: policy}, messages)
	if err != nil || len(prepared) != 1 || prepared[0].Content != messages[0].Content || usage.EstimatedInputTokens >= 1000 || !strings.HasPrefix(usage.EstimationMethod, "cl100k_base") {
		t.Fatalf("declared encoding lost current text or was not used: usage=%+v err=%v", usage, err)
	}
}

func TestAgentContextCostMeasuresEachMessageOnceBeforeCompaction(t *testing.T) {
	request := AgentRequest{Messages: []AgentMessage{{Role: "user", Content: "最早问题"}, {Role: "assistant", Content: "回答"}, {Role: "user", Content: "当前问题"}}}
	calls := 0
	costs, err := measureAgentContext(request, func(value string) (int, error) { calls++; return len(value), nil })
	if err != nil || calls != len(request.Messages)+1 {
		t.Fatalf("message/schema costs were not precomputed: calls=%d err=%v", calls, err)
	}
	indices := trimAgentContextIndices(request.Messages, []int{0, 1, 2})
	if costs.total(indices) >= costs.total([]int{0, 1, 2}) || calls != len(request.Messages)+1 {
		t.Fatal("compaction recounted input or failed to subtract old messages")
	}
}

func TestContextPolicyTokenizerPersistsAndDoesNotInferModelName(t *testing.T) {
	for _, encoding := range []string{"", "utf8_bytes", "cl100k_base", "o200k_base"} {
		policy, err := NormalizeAgentContextPolicy(AgentContextPolicy{Tokenizer: encoding})
		if err != nil || policy.Tokenizer == "" {
			t.Fatal("policy did not normalize the explicit counter", policy, err)
		}
		stored := ManagedAPIConfig{ModelName: "deepseek-model", DataPolicy: managedContextDataPolicy(nil, policy)}
		resolved, err := ResolveAgentContextPolicy(stored)
		if err != nil || resolved != policy {
			t.Fatal("stored counter was lost or model name changed it", resolved, err)
		}
	}
	if _, err := NormalizeAgentContextPolicy(AgentContextPolicy{Tokenizer: "auto"}); !errors.Is(err, ErrAgentContextPolicy) {
		t.Fatal("unknown counter passed validation")
	}
}

func TestAgentBudgetOmitsUnpinnedMemoryBeforePinnedEntry(t *testing.T) {
	config := ManagedAPIConfig{ContextPolicy: AgentContextPolicy{ContextWindowTokens: 2048, MaxInputTokens: 512, MaxOutputTokens: 512}}
	request := AgentRequest{Messages: []AgentMessage{
		{Role: "system", Content: "规则"},
		{Role: "user", Content: "必须保留的用户要求", PreserveContext: true, ContextPriority: 1000},
		{Role: "user", Content: strings.Repeat("old", 200), PreserveContext: true, ContextPriority: 2},
		{Role: "user", Content: "当前问题"},
	}}
	prepared, usage, err := ApplyAgentContextBudget(config, request)
	if err != nil || usage.DroppedMemoryMessages != 1 || len(prepared.Messages) != 3 || prepared.Messages[1].ContextPriority != 1000 {
		t.Fatalf("unpinned entry displaced pinned user requirements: usage=%+v messages=%+v err=%v", usage, prepared.Messages, err)
	}
}

func TestAgentBudgetReusesOnlyInputBoundPrivateCostCache(t *testing.T) {
	config := ManagedAPIConfig{ContextPolicy: AgentContextPolicy{Tokenizer: "cl100k_base"}}
	request := AgentRequest{Messages: []AgentMessage{{Role: "user", Content: "当前问题", Images: []ManagedChatAttachment{chatTestImage(t)}}}, Tools: []AgentTool{{Name: "read_exam", Parameters: []byte(`{}`)}}}
	prepared, _, err := ApplyAgentContextBudget(config, request)
	if err != nil || prepared.contextCache == nil {
		t.Fatal("prepared request has no private measurement cache", err)
	}
	second, _, err := ApplyAgentContextBudget(config, prepared)
	if err != nil || second.contextCache != prepared.contextCache {
		t.Fatal("identical second preparation did not reuse validated counts", err)
	}
	for _, field := range []string{"content", "image name", "image content", "tool", "encoding", "memory priority"} {
		t.Run(field, func(t *testing.T) {
			changed := prepared
			changed.Messages = append([]AgentMessage(nil), prepared.Messages...)
			changed.Messages[0].Images = append([]ManagedChatAttachment(nil), prepared.Messages[0].Images...)
			changed.Tools = append([]AgentTool(nil), prepared.Tools...)
			nextConfig := config
			invalidImage := false
			switch field {
			case "content":
				changed.Messages[0].Content += "变更"
			case "image name":
				changed.Messages[0].Images[0].Name = "different.png"
			case "image content":
				changed.Messages[0].Images[0].Content = "invalid"
				invalidImage = true
			case "tool":
				changed.Tools[0].Description = "changed tool schema"
			case "encoding":
				nextConfig.ContextPolicy.Tokenizer = "o200k_base"
			case "memory priority":
				changed.Messages[0].ContextPriority = 1000
			}
			next, _, err := ApplyAgentContextBudget(nextConfig, changed)
			if invalidImage {
				if err == nil {
					t.Fatal("modified image bypassed validation through cached counts")
				}
				return
			}
			if err != nil || next.contextCache == prepared.contextCache {
				t.Fatalf("changed %s reused stale count/validation: %v", field, err)
			}
		})
	}
	// 计数可复用，但剩余累计额度是本次调用的新约束，不能被旧缓存覆盖。
	prepared.TokenBudget = 1
	if _, _, err := ApplyAgentContextBudget(config, prepared); !errors.Is(err, ErrAgentTurnBudgetExceeded) {
		t.Fatal("cached validation bypassed the updated turn budget", err)
	}
}
