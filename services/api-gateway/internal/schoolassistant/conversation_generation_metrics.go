package schoolassistant

import (
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

// ConversationGenerationMetrics 只描述生成本次最终回答的模型调用，不累计此前规划、搜索和工具耗时。
// 输出数沿用供应商 usage 口径（可能包含推理或结构化包装）；不按字符数或 SSE 块数推算。
type ConversationGenerationMetrics struct {
	ModelConfigID   string  `json:"model_config_id,omitempty"`
	OutputTokens    int64   `json:"output_tokens"`
	DurationMS      int64   `json:"duration_ms"`
	TokensPerSecond float64 `json:"tokens_per_second"`
}

func conversationGenerationMetrics(modelConfigID string, usage modelgovernance.ManagedAPIProbeUsage, elapsed time.Duration) *ConversationGenerationMetrics {
	durationMS := elapsed.Milliseconds()
	if usage.OutputTokens <= 0 || durationMS <= 0 {
		return nil
	}
	// 整段调用时间包含首 Token 等待，避免代理缓冲后密集发出块时显示虚假的解码速度。
	return &ConversationGenerationMetrics{ModelConfigID: modelConfigID, OutputTokens: usage.OutputTokens,
		DurationMS: durationMS, TokensPerSecond: float64(usage.OutputTokens) * 1000 / float64(durationMS)}
}
