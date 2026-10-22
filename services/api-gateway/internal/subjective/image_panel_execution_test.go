package subjective

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func imagePanelFixture(t *testing.T, mode string, scores map[string]float64) (Context, ModelPolicy, AdapterOutput) {
	t.Helper()
	plan := &grading.GradingAIPlan{Version: 1, EffectiveMode: mode, ResolvedRoles: map[string]grading.GradingAIModel{}, DecisionConfig: grading.GradingAIDecisionConfig{PrimaryAllowedDiffPoints: floatPtr(1.2), TeacherAllowedDiffPoints: floatPtr(2.4), AggregationVersion: "dual-conditional-arbiter-v1", Rounding: "ceil_final"}}
	observation := imageTestObservation("q1", "crop-hash")
	observation.ModelBinding = "frozen-source-binding"
	value := Context{SegmentID: "segment", InputVersionID: "input-frozen", CropSHA256: "crop-hash", Question: paper.Question{ID: "q1", QuestionType: "calculation", Score: 12}, Rubric: paper.Rubric{Version: "rubric-v1", MaxScore: 12, Points: []paper.RubricPoint{{ID: "p1", Description: "步骤一", Score: 6}, {ID: "p2", Description: "步骤二", Score: 6}}}, Observation: &observation, AIPlan: plan, ReferenceContext: &GradingReferenceContext{StandardAnswer: "冻结答案", SolutionText: "冻结解法"}}
	execution := &ImagePanelExecution{ConfiguredMode: mode}
	roles := []string{"primary_a", "primary_b", "arbiter"}
	if mode == "single" {
		roles = []string{"single"}
	}
	for _, role := range roles {
		model := grading.GradingAIModel{ModelConfigID: role, ModelName: "model-" + role, ModelVersion: role + "-v1", AdapterType: "openai_compatible", ProviderKey: "fixture", ConfigRevision: "revision-" + role, BindingRevision: "school-revision"}
		plan.ResolvedRoles[role] = model
		if role == "single" {
			plan.SingleModel = &model
		}
		score, included := scores[role]
		if !included {
			continue
		}
		output := AdapterOutput{SchemaVersion: ImageProtocolVersion, RequestID: "parent-request:" + role, ModelVersion: model.ModelVersion, PromptVersion: ImageComparisonPromptVersion, RubricVersion: value.Rubric.Version, DeliveryMode: "teacher_suggestion", CapabilityProfile: "fixture", NeedsHumanReview: true, StudentFeedback: "核对步骤", TeacherNote: "建议", Observation: &observation, RawOutput: map[string]any{}, Telemetry: AdapterTelemetry{Adapter: model.AdapterType, Provider: model.ProviderKey, Deployment: model.ConfigRevision, Region: "managed", Attempts: 1}}
		for index, point := range value.Rubric.Points {
			judgment := ImageCriterionJudgment{CriterionID: point.ID, Status: "contradicted", Reason: "学生未完成此步骤"}
			if score >= float64(index+1)*6 {
				judgment.Status, judgment.Reason, judgment.EvidenceIDs = "supported", "原图中有该步骤", []string{"student-step"}
			}
			output.ImageJudgments = append(output.ImageJudgments, judgment)
		}
		execution.RoleResults = append(execution.RoleResults, ImageRoleResult{Role: role, Status: "succeeded", Model: model, Output: &output})
	}
	policy := ModelPolicy{ModelVersion: plan.ResolvedRoles[roles[0]].ModelVersion, PromptVersion: ImageComparisonPromptVersion, MinConfidence: .8}
	return value, policy, AdapterOutput{SchemaVersion: ImageProtocolVersion, RequestID: "parent-request", ModelVersion: policy.ModelVersion, PromptVersion: policy.PromptVersion, PanelExecution: execution}
}

func TestImagePanelSettlementThresholdsWeightsAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		scores     map[string]float64
		decision   string
		final      *float64
	}{
		{"single stays single", "single", map[string]float64{"single": 6}, "single_suggestion", floatPtr(6)},
		{"primary consensus skips arbiter", "dual_conditional_arbiter", map[string]float64{"primary_a": 6, "primary_b": 6}, "primary_consensus", floatPtr(6)},
		{"large difference without C", "dual", map[string]float64{"primary_a": 0, "primary_b": 12}, "teacher_review", nil},
		{"range guard catches symmetric outliers", "dual_conditional_arbiter", map[string]float64{"primary_a": 0, "primary_b": 12, "arbiter": 6}, "teacher_review", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, policy, output := imagePanelFixture(t, tc.mode, tc.scores)
			if err := settleImagePanelOutput(value, policy, &output); err != nil {
				t.Fatal(err)
			}
			got := output.PanelExecution
			if got.Decision != tc.decision || (got.FinalScore == nil) != (tc.final == nil) || tc.final != nil && *got.FinalScore != *tc.final {
				t.Fatalf("unexpected decision %+v", got)
			}
			if output.RawOutput["score_prefill_allowed"] != (tc.final != nil) {
				t.Fatal("missing aggregate enabled prefill")
			}
		})
	}
	value, policy, output := imagePanelFixture(t, "dual_conditional_arbiter", map[string]float64{"primary_a": 6, "primary_b": 12, "arbiter": 12})
	value.AIPlan.DecisionConfig.TeacherAllowedDiffPoints = floatPtr(7.2)
	if err := settleImagePanelOutput(value, policy, &output); err != nil {
		t.Fatal(err)
	}
	if *output.PanelExecution.RawScore != 10.5 || *output.PanelExecution.FinalScore != 11 {
		t.Fatalf("wrong 25/25/50 or final rounding: %+v", output.PanelExecution)
	}
	value, policy, output = imagePanelFixture(t, "dual", map[string]float64{"primary_a": 6})
	output.PanelExecution.RoleResults = append(output.PanelExecution.RoleResults, ImageRoleResult{Role: "primary_b", Status: "failed", Model: value.AIPlan.ResolvedRoles["primary_b"], ErrorCode: "model_timeout"})
	if err := settleImagePanelOutput(value, policy, &output); err != nil {
		t.Fatal(err)
	}
	if output.PanelExecution.FinalScore != nil || output.SuggestedScore != 6 || output.PanelExecution.Decision != "teacher_review" {
		t.Fatal("failure became a zero or aggregate score")
	}
	if len(output.PanelExecution.ExecutedRoles) != 1 || output.PanelExecution.IndependentModelCount != 1 || len(output.PanelExecution.RoleResults) != 2 {
		t.Fatal("connection preparation failure counted as a real executed model or lost failure details")
	}
}

func TestImagePanelIgnoresForgedScoresAndRetainsReadableFrozenBasis(t *testing.T) {
	value, policy, output := imagePanelFixture(t, "single", map[string]float64{"single": 6})
	output.PanelExecution.RoleResults[0].Output.SuggestedScore = 999
	output.PanelExecution.RawScore, output.PanelExecution.FinalScore = floatPtr(999), floatPtr(999)
	if err := settleImagePanelOutput(value, policy, &output); err != nil {
		t.Fatal(err)
	}
	if output.SuggestedScore != 6 || output.MatchedPoints[0].Label != "步骤一" || output.MatchedPoints[0].Reason == "" || output.MissingPoints[0].Score != 6 || output.MissingPoints[0].Label != "步骤二" {
		t.Fatalf("lost frozen labels, weights or reasons: %+v", output)
	}
	basis := output.RawOutput["scoring_basis"].(map[string]any)
	if basis["standard_answer"] != "冻结答案" || basis["solution_text"] != "冻结解法" {
		t.Fatal("basis did not use frozen reference")
	}
	output.PanelExecution.RoleResults[0].Output.ImageJudgments[0].EvidenceIDs = []string{"teacher-answer"}
	if err := settleImagePanelOutput(value, policy, &output); err == nil {
		t.Fatal("teacher reference was accepted as student evidence")
	}
}

func TestImagePanelDecimalThresholdAndCeilBoundaries(t *testing.T) {
	a, b := .3, .2
	if imageAllowedDifferenceExceeded(math.Abs(a-b), .1) || imageAllowedDifferenceExceeded(.099999, .1) || !imageAllowedDifferenceExceeded(.100001, .1) || imageAllowedDifferenceExceeded(2, 2) || !imageAllowedDifferenceExceeded(3, 2) {
		t.Fatal("decimal threshold boundary drifted")
	}
	for _, tc := range []struct{ raw, max, want float64 }{{1 + 5e-10, 12, 1}, {1 + 1e-6, 12, 2}, {11.9, 11.9, 11.9}, {10.5, 12, 11}} {
		if got := imageCeilFinal(tc.raw, tc.max); got != tc.want {
			t.Fatalf("ceil(%v)=%v want %v", tc.raw, got, tc.want)
		}
	}
	value, policy, output := imagePanelFixture(t, "dual", map[string]float64{"primary_a": 6, "primary_b": 12})
	value.AIPlan.DecisionConfig.PrimaryAllowedDiffPoints = floatPtr(6)
	if err := settleImagePanelOutput(value, policy, &output); err != nil || output.PanelExecution.Decision != "primary_consensus" {
		t.Fatal("equal allowed difference should not require C", err)
	}
	value, policy, output = imagePanelFixture(t, "dual_conditional_arbiter", map[string]float64{"primary_a": 6, "primary_b": 12, "arbiter": 12})
	value.AIPlan.DecisionConfig.TeacherAllowedDiffPoints = floatPtr(3)
	if err := settleImagePanelOutput(value, policy, &output); err != nil || output.PanelExecution.FinalScore == nil {
		t.Fatal("equal teacher allowed difference should remain a weighted suggestion", err)
	}
}

func TestImagePanelObjectiveAndFillScoringNeverUsesLargeQuestionDifferences(t *testing.T) {
	for _, questionType := range []string{"single_choice", "multiple_choice", "true_false", "fill_blank", "numeric"} {
		value, policy, output := imagePanelFixture(t, "dual_conditional_arbiter", map[string]float64{"primary_a": 6, "primary_b": 12})
		value.Question.QuestionType = questionType
		value.AIPlan.DecisionConfig.PrimaryAllowedDiffPoints, value.AIPlan.DecisionConfig.TeacherAllowedDiffPoints = nil, nil
		role := output.PanelExecution.RoleResults[0]
		role.Role = "single"
		role.Output.RequestID = output.RequestID + ":single"
		output.PanelExecution.ConfiguredMode, output.PanelExecution.RoleResults = "single", []ImageRoleResult{role}
		if err := settleImagePanelOutput(value, policy, &output); err != nil {
			t.Fatalf("%s was blocked by irrelevant large-question settings: %v", questionType, err)
		}
		if output.PanelExecution.Decision != "single_suggestion" || len(output.PanelExecution.ExecutedRoles) != 1 || output.PanelExecution.PrimaryThreshold != nil {
			t.Fatalf("%s incorrectly used multiple reviewers", questionType)
		}
	}
}

func TestImagePanelConfirmedBlankCanProduceZeroSuggestion(t *testing.T) {
	value, policy, output := imagePanelFixture(t, "single", map[string]float64{"single": 0})
	value.Observation.Members[0].Presence = "blank"
	value.Observation.Members[0].Answer = nil
	if err := settleImagePanelOutput(value, policy, &output); err != nil {
		t.Fatal(err)
	}
	if output.PanelExecution.FinalScore == nil || *output.PanelExecution.FinalScore != 0 || output.PanelExecution.Decision != "single_suggestion" {
		t.Fatal("confirmed blank was treated as unreadable")
	}
}

func TestImagePanelCallsIndependentModelsAndConditionallySkipsC(t *testing.T) {
	value, policy, _ := imagePanelFixture(t, "dual_conditional_arbiter", map[string]float64{})
	crop := validResolvedActiveCrop(t)
	value.CropSHA256, value.Observation.CropSHA256 = crop.SHA256, crop.SHA256
	var mu sync.Mutex
	roles := []string{}
	needsArbiter := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request imageAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		roles = append(roles, request.AgentRole)
		mu.Unlock()
		if request.ObservationSourceBinding != value.Observation.ModelBinding || request.StudentImage == nil || request.FrozenReference.StandardAnswer != "冻结答案" {
			t.Error("role lost source binding, actual crop or frozen reference")
		}
		judgments := []ImageCriterionJudgment{{CriterionID: "p1", Status: "supported", EvidenceIDs: []string{"student-step"}, Reason: "有学生步骤"}, {CriterionID: "p2", Status: "contradicted", Reason: "无学生步骤"}}
		if needsArbiter && request.AgentRole != "primary_a" {
			judgments[1] = ImageCriterionJudgment{CriterionID: "p2", Status: "supported", EvidenceIDs: []string{"student-step"}, Reason: "另一独立模型认可该步骤"}
		}
		_ = json.NewEncoder(w).Encode(imageAgentResponse{SchemaVersion: ImageProtocolVersion, RequestID: request.RequestID, Observation: *value.Observation, Judgments: judgments, StudentFeedback: "核对", TeacherNote: "建议", ModelVersion: request.ManagedModel.ModelVersion, PromptVersion: ImageComparisonPromptVersion, RubricVersion: value.Rubric.Version, NeedsHumanReview: true, Telemetry: gradingAgentTelemetry{Adapter: request.ManagedModel.AdapterType, Provider: request.ManagedModel.ProviderKey, Deployment: request.ManagedModel.ConfigRevision, Region: "managed", Attempts: 1}})
	}))
	defer server.Close()
	h := NewHandler(nil, NewHTTPAdapter(HTTPAdapterConfig{BaseURL: server.URL, Token: strings.Repeat("t", 32), ModelVersion: policy.ModelVersion}), nil)
	h.WithImageModelByIDResolver(func(_ context.Context, _, configID, _ string) (*ImageModelConfig, error) {
		model := value.AIPlan.ResolvedRoles[configID]
		return &ImageModelConfig{ConfigID: configID, AdapterType: model.AdapterType, ProviderKey: model.ProviderKey, ModelName: model.ModelName, ModelVersion: model.ModelVersion, ConfigRevision: model.ConfigRevision}, nil
	})
	input := AdapterInput{RequestID: "parent-request", SegmentID: value.SegmentID, InputVersionID: value.InputVersionID, CropSHA256: value.CropSHA256, Question: value.Question, Rubric: value.Rubric, Observation: value.Observation, ActiveCrop: &crop, ReferenceContext: value.ReferenceContext}
	got, err := h.executeImagePlan(context.Background(), "tenant", WorkerExecutionContext{Run: GradingRun{RequestID: "parent-request"}, Context: value, Policy: policy}, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 2 || got.PanelExecution.Decision != "primary_consensus" {
		t.Fatalf("unexpected real role calls %v %+v", roles, got.PanelExecution)
	}
	for _, role := range roles {
		if role == "arbiter" {
			t.Fatal("called C below threshold")
		}
	}
	roles, needsArbiter = nil, true
	got, err = h.executeImagePlan(context.Background(), "tenant", WorkerExecutionContext{Run: GradingRun{RequestID: "parent-request"}, Context: value, Policy: policy}, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 3 || roles[2] != "arbiter" || got.PanelExecution.Decision != "teacher_review" {
		t.Fatalf("above-threshold calls did not reach independent C: %v %+v", roles, got.PanelExecution)
	}
	roles = nil
	value.Question.QuestionType, input.Question.QuestionType = "fill_blank", "fill_blank"
	value.AIPlan.DecisionConfig.PrimaryAllowedDiffPoints, value.AIPlan.DecisionConfig.TeacherAllowedDiffPoints = nil, nil
	got, err = h.executeImagePlan(context.Background(), "tenant", WorkerExecutionContext{Run: GradingRun{RequestID: "parent-request"}, Context: value, Policy: policy}, input)
	if err != nil || len(roles) != 1 || roles[0] != "single" || got.PanelExecution.PrimaryThreshold != nil {
		t.Fatalf("fill scoring invoked large-question reviewers or thresholds: roles=%v err=%v", roles, err)
	}
}

func floatPtr(value float64) *float64 { return &value }
