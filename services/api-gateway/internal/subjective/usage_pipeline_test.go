package subjective

import "testing"

func TestGradePersistenceInputCarriesUsageFromAdapterResponse(t *testing.T) {
	output := AdapterOutput{
		RequestID: "grade-usage-1", ModelVersion: "model-v1",
		Telemetry: AdapterTelemetry{
			Adapter: "openai_compatible", Provider: "deepseek", Deployment: "deepseek-chat", Region: "global",
			Attempts: 2,
			Usage:    ModelTokenUsage{InputTokens: 301, CachedInputTokens: 120, OutputTokens: 47, ReasoningTokens: 9, TotalTokens: 348},
		},
	}
	ctx := Context{SegmentID: "segment-1"}
	ctx.Question.ID = "question-1"
	ctx.Question.QuestionNo = "1"
	ctx.Question.QuestionType = "short_answer"
	ctx.Question.Score = 10
	ctx.Rubric.Version = "rubric-v1"

	grade := successfulGrade(ctx, ModelPolicy{}, "run-1", output)

	if grade.AdapterRequestID != "grade-usage-1" || grade.AdapterAttempts != 2 || grade.InputTokens != 301 || grade.CachedInputTokens != 120 ||
		grade.OutputTokens != 47 || grade.ReasoningTokens != 9 || grade.TotalTokens != 348 {
		t.Fatalf("grading response usage was not carried into CreateGrade input: %#v", grade)
	}
}
