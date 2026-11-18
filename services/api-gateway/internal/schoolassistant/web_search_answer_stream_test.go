package schoolassistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

func TestPublicSearchDecisionDeadlineCannotBeResetByRawModelChunks(t *testing.T) {
	ctx, _, closeDecision := publicSearchDecisionContext(context.Background(), 20*time.Millisecond)
	defer closeDecision()
	model := waitingConversationStreamModel{chunks: 20, interval: 5 * time.Millisecond}
	_, err := streamConversationModel(ctx, model, modelgovernance.ManagedAPIConnection{}, validTestAgentRequest(), func(modelgovernance.AgentEvent) error { return nil }, time.Second)
	if err == nil || !errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		t.Fatalf("raw model activity bypassed decision deadline: %v", err)
	}
}

func TestPublicSearchDecisionAllowsAnswerToContinueAfterDecisionWindow(t *testing.T) {
	ctx, answerStarted, closeDecision := publicSearchDecisionContext(context.Background(), 20*time.Millisecond)
	defer closeDecision()
	answerStarted()
	model := waitingConversationStreamModel{chunks: 6, interval: 10 * time.Millisecond}
	_, err := streamConversationModel(ctx, model, modelgovernance.ManagedAPIConnection{}, validTestAgentRequest(), func(modelgovernance.AgentEvent) error { return nil }, time.Second)
	if err != nil {
		t.Fatalf("normal answer was limited as a tool decision: %v", err)
	}
}

func TestPublicSearchAnswerPreviewNeverLeaksToolParameters(t *testing.T) {
	for _, input := range []string{
		`{"action":"search","query":"公开资料","answer":"不该显示"}`,
		`{"action":"read","source_ids":["candidate-1"],"information_needed":"answer:不该显示"}`,
		`{"action":"business"}`,
		`{"evidence":[{"quote":"answer:不该显示"}],"answer":"动作尚未确认"}`,
		`{"action":"ans`,
	} {
		if got := publicSearchAnswerPreview(input); got != "" {
			t.Fatalf("non-answer data reached preview: %q", got)
		}
	}
}

func TestPublicSearchAnswerPreviewHandlesEveryByteBoundary(t *testing.T) {
	answer := "北京天气：晴。\n数学\\公式与引号\"；表情😀。"
	wire, _ := json.Marshal(map[string]any{"action": "answer", "answer": answer, "evidence": []any{}})
	previous := ""
	for index := 0; index <= len(wire); index++ {
		preview := publicSearchAnswerPreview(string(wire[:index]))
		if !strings.HasPrefix(preview, previous) || !strings.HasPrefix(answer, preview) {
			t.Fatalf("preview rewrote text at %d: before=%q after=%q", index, previous, preview)
		}
		previous = preview
	}
	if previous != answer {
		t.Fatalf("incomplete final preview: %q", previous)
	}
}

func TestPublicSearchAnswerPreviewWaitsForWholeSurrogatePair(t *testing.T) {
	prefix := `{"action":"answer","answer":"你好\uD83D\uDE00后续"}`
	for index := strings.Index(prefix, `\uD83D`); index < strings.Index(prefix, "后续"); index++ {
		if got := publicSearchAnswerPreview(prefix[:index]); got != "你好" {
			t.Fatalf("partial surrogate emitted at %d: %q", index, got)
		}
	}
	if got := publicSearchAnswerPreview(prefix); got != "你好😀后续" {
		t.Fatalf("surrogate pair lost: %q", got)
	}
}

func TestPublicSearchAnswerPreviewActionAfterAnswerStaysBuffered(t *testing.T) {
	if got := publicSearchAnswerPreview(`{"answer":"待完整验证后再显示","action":"answer"}`); got != "" {
		t.Fatalf("answer leaked before action: %q", got)
	}
}
