package subjective

import (
	"context"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/grading"
)

var ErrAIGradingUnavailable = errors.New("ai grading unavailable")

type RuntimeStatus struct {
	Enabled       bool   `json:"enabled"`
	Available     bool   `json:"available"`
	Mode          string `json:"mode"`
	ErrorCode     string `json:"error_code,omitempty"`
	ModelVersion  string `json:"model_version,omitempty"`
	PromptVersion string `json:"prompt_version,omitempty"`
}

type RuntimeStatusProvider interface {
	RuntimeStatus() RuntimeStatus
}

type MockLLMAdapter struct{}

func NewMockLLMAdapter() *MockLLMAdapter {
	return &MockLLMAdapter{}
}

func (a *MockLLMAdapter) Name() string {
	return "mock_llm_adapter"
}

func (a *MockLLMAdapter) RuntimeStatus() RuntimeStatus {
	return RuntimeStatus{Enabled: true, Available: true, Mode: "mock"}
}

// Mock 适配器只提供占位结果，必须保留低置信度和人工复核标记，不能被误当成真实评分。
func (a *MockLLMAdapter) Grade(_ context.Context, input AdapterInput) (AdapterOutput, error) {
	return AdapterOutput{
		RequestID:      input.RequestID,
		SuggestedScore: 0,
		Confidence:     0.5,
		MatchedPoints:  []grading.PointResult{},
		MissingPoints: []grading.PointResult{
			{Code: "mock_not_scored", Label: "mock adapter does not perform real subjective scoring", Score: input.Question.Score},
		},
		Evidence: []grading.Evidence{
			{Type: "mock", AnswerSegment: "", AnswerText: "", StandardAnswer: "", Rule: "mock output; not real model evidence"},
		},
		RiskFlags:        []string{"mock_llm_output", "low_model_confidence"},
		NeedsHumanReview: true,
		StudentFeedback:  "MOCK: subjective AI feedback is not available from a real model in this environment.",
		TeacherNote:      "MOCK LLM adapter returned a fixed low-confidence placeholder. Human review is required.",
		ModelVersion:     input.ModelPolicy.ModelVersion,
		PromptVersion:    input.ModelPolicy.PromptVersion,
		RubricVersion:    input.Rubric.Version,
		DeliveryMode:     "teacher_review",
		Telemetry: AdapterTelemetry{
			Adapter:         a.Name(),
			Attempts:        1,
			PriorErrorCodes: []string{},
		},
		RawOutput: map[string]any{
			"adapter":        a.Name(),
			"mock":           true,
			"model_version":  input.ModelPolicy.ModelVersion,
			"prompt_version": input.ModelPolicy.PromptVersion,
		},
		Mock: true,
	}, nil
}

type DisabledAdapter struct {
	status RuntimeStatus
}

func NewDisabledAdapter(code string, modelVersion string, promptVersion string) *DisabledAdapter {
	if code == "" {
		code = "ai_grading_disabled"
	}
	return &DisabledAdapter{status: RuntimeStatus{
		Enabled:       false,
		Available:     false,
		Mode:          "disabled",
		ErrorCode:     code,
		ModelVersion:  modelVersion,
		PromptVersion: promptVersion,
	}}
}

func (a *DisabledAdapter) Name() string {
	return "disabled_ai_adapter"
}

func (a *DisabledAdapter) RuntimeStatus() RuntimeStatus {
	return a.status
}

// 禁用状态直接失败并交给人工流程，避免调用方在未配置模型时继续写入 AI 分数。
func (a *DisabledAdapter) Grade(context.Context, AdapterInput) (AdapterOutput, error) {
	return AdapterOutput{}, ErrAIGradingUnavailable
}
