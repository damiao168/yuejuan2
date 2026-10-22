package grading

import (
	"math"
	"testing"
)

func TestGradingAIPlanReportsActualDistinctModelsAndDoesNotRequireThree(t *testing.T) {
	models := []GradingAIModel{{ModelConfigID: "a", ModelName: "model-a", ModelVersion: "v1"}, {ModelConfigID: "same-a", ModelName: "MODEL-A", ModelVersion: "V1"}, {ModelConfigID: "b", ModelName: "model-b", ModelVersion: "v1"}, {ModelConfigID: "c", ModelName: "model-c", ModelVersion: "v1"}}
	plan := defaultGradingAIPlan()
	plan.FallbackModelConfigID = "a"
	if got := resolveGradingAIPlan(plan, models); got.EffectiveMode != "single" || got.IndependentModelCount != 1 || got.SingleModel.ModelConfigID != "a" {
		t.Fatalf("one model must remain usable: %+v", got)
	}
	plan.Roles = map[string]GradingAIRoleSelection{"primary_a": {ModelConfigID: "a", StrengthRank: 1}, "primary_b": {ModelConfigID: "same-a", StrengthRank: 1}, "arbiter": {ModelConfigID: "c", StrengthRank: 2}}
	if got := resolveGradingAIPlan(plan, models); got.EffectiveMode != "single" || got.IndependentModelCount != 1 {
		t.Fatalf("duplicate configurations claimed independent reviewers: %+v", got)
	}
	plan.Roles["primary_b"] = GradingAIRoleSelection{ModelConfigID: "b", StrengthRank: 1}
	if got := resolveGradingAIPlan(plan, models); got.EffectiveMode != "dual_conditional_arbiter" || got.IndependentModelCount != 3 {
		t.Fatalf("three distinct models were not resolved: %+v", got)
	}
	plan.Roles["arbiter"] = GradingAIRoleSelection{ModelConfigID: "c", StrengthRank: 1}
	if got := resolveGradingAIPlan(plan, models); got.EffectiveMode != "dual" || got.IndependentModelCount != 2 {
		t.Fatalf("weak arbiter must leave a usable dual plan: %+v", got)
	}
	delete(plan.Roles, "arbiter")
	if got := resolveGradingAIPlan(plan, models); got.EffectiveMode != "dual" {
		t.Fatalf("two models must remain usable: %+v", got)
	}
}

func TestGradingAIPlanValidatesManualPointDifferencesAndVersion(t *testing.T) {
	input := GradingAIPlanInput{ExpectedRevision: "absent", DecisionConfig: defaultGradingAIPlan().DecisionConfig}
	if _, err := normalizeGradingAIPlanInput(input); err != nil {
		t.Fatal(err)
	}
	if input.DecisionConfig.PrimaryAllowedDiffPoints != nil || input.DecisionConfig.TeacherAllowedDiffPoints != nil {
		t.Fatal("new exam supplied numeric difference defaults")
	}
	for _, config := range []GradingAIDecisionConfig{
		{PrimaryAllowedDiffPoints: pointDifference(3), TeacherAllowedDiffPoints: pointDifference(2), AggregationVersion: "dual-conditional-arbiter-v1", Rounding: "ceil_final"},
		{PrimaryAllowedDiffPoints: pointDifference(math.NaN()), TeacherAllowedDiffPoints: pointDifference(2), AggregationVersion: "dual-conditional-arbiter-v1", Rounding: "ceil_final"},
		{PrimaryAllowedDiffPoints: pointDifference(1), TeacherAllowedDiffPoints: pointDifference(1001), AggregationVersion: "dual-conditional-arbiter-v1", Rounding: "ceil_final"},
		{PrimaryAllowedDiffPoints: pointDifference(1), TeacherAllowedDiffPoints: pointDifference(2), AggregationVersion: "unrecognized", Rounding: "ceil_final"},
	} {
		input.DecisionConfig = config
		if _, err := normalizeGradingAIPlanInput(input); err == nil {
			t.Fatalf("accepted invalid policy %+v", config)
		}
	}
	input.DecisionConfig = GradingAIDecisionConfig{PrimaryAllowedDiffPoints: pointDifference(0), TeacherAllowedDiffPoints: pointDifference(0), AggregationVersion: "dual-conditional-arbiter-v1", Rounding: "ceil_final"}
	if _, err := normalizeGradingAIPlanInput(input); err != nil {
		t.Fatalf("explicit zero is a valid manual setting: %v", err)
	}
}

func TestGradingAIPlanReadinessRequiresConfirmationOnlyForLargeImageQuestions(t *testing.T) {
	plan := defaultGradingAIPlan()
	plan.EffectiveMode = "single"
	if gradingAIPlanReadinessCheck(plan, false) != nil {
		t.Fatal("choice or fill-only exam required big-question configuration")
	}
	if check := gradingAIPlanReadinessCheck(plan, true); check == nil || check.Passed || check.Code != "grading_ai_plan_confirmation_required" {
		t.Fatalf("unconfirmed large-question plan passed: %+v", check)
	}
	plan.Revision = "confirmed"
	if check := gradingAIPlanReadinessCheck(plan, true); check == nil || !check.Passed {
		t.Fatal("single model without irrelevant thresholds was blocked")
	}
	plan.EffectiveMode = "dual"
	if check := gradingAIPlanReadinessCheck(plan, true); check == nil || check.Passed || check.Code != "grading_ai_threshold_required" {
		t.Fatalf("dual plan without manual differences passed: %+v", check)
	}
	plan.DecisionConfig.PrimaryAllowedDiffPoints, plan.DecisionConfig.TeacherAllowedDiffPoints = pointDifference(0), pointDifference(0)
	if check := gradingAIPlanReadinessCheck(plan, true); check == nil || !check.Passed {
		t.Fatal("manually configured zero difference was rejected")
	}
	plan.EffectiveMode = "unconfigured"
	if check := gradingAIPlanReadinessCheck(plan, true); check == nil || check.Passed || check.Code != "grading_model_required" {
		t.Fatal("empty confirmed plan became ready for large-question AI")
	}
	for _, questionType := range []string{"single_choice", "multiple_choice", "true_false", "fill_blank", "numeric"} {
		if GradingAIPlanAppliesToQuestion(questionType) {
			t.Fatalf("big-question difference rules leaked to %s", questionType)
		}
	}
}

func pointDifference(value float64) *float64 { return &value }
