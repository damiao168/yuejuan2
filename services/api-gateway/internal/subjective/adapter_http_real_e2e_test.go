package subjective

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestHTTPAdapterRealLocalAgent 需显式启用，因为它调用固定版本的本地 llama.cpp 模型，
// 在仅有 CPU 的工作站上可能耗时数分钟。
func TestHTTPAdapterRealLocalAgent(t *testing.T) {
	baseURL := os.Getenv("EDUGRADE_REAL_GRADING_AGENT_URL")
	token := os.Getenv("EDUGRADE_REAL_GRADING_AGENT_TOKEN")
	if baseURL == "" || token == "" {
		t.Skip("set EDUGRADE_REAL_GRADING_AGENT_URL and EDUGRADE_REAL_GRADING_AGENT_TOKEN to run the real-model E2E")
	}

	adapter := NewHTTPAdapter(HTTPAdapterConfig{
		BaseURL:       baseURL,
		Token:         token,
		Timeout:       250 * time.Second,
		MaxRetries:    0,
		ModelVersion:  "Qwen/Qwen3-4B-GGUF:Q4_K_M",
		PromptVersion: "subjective-governed-cn-subject-routing-v6",
		MinConfidence: 0.8,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 260*time.Second)
	defer cancel()
	input := validHTTPAdapterInput()
	// 测试同一个长期运行的本地 agent 进程时，保持此请求 ID 与契约夹具请求不同。
	input.RequestID = "sg-real-e2e-0001"
	output, err := adapter.Grade(ctx, input)
	if err != nil {
		t.Fatalf("real local grading agent request failed: %v", err)
	}
	if output.Mock || !output.NeedsHumanReview || output.Confidence != 0 {
		t.Fatalf("real model output violated shadow governance: %#v", output)
	}
	if output.SuggestedScore < 0 || output.SuggestedScore > 4 || len(output.Evidence) == 0 {
		t.Fatalf("real model output was not a bounded evidence-backed suggestion: %#v", output)
	}
}
