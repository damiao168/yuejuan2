package subjective

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestImagePrefixVersionIsSelectedOnlyForNewTasks(t *testing.T) {
	model := &ImageModelConfig{AdapterType: "openai_compatible", ProviderKey: "fixture", APIKey: "synthetic-key", ModelName: "vision", ModelVersion: "vision-v1", ConfigRevision: "revision-1"}
	h := NewHandler(nil, NewHTTPAdapter(HTTPAdapterConfig{}), nil).
		WithImageModelResolver(func(context.Context, string) (*ImageModelConfig, error) { return model, nil })
	for _, enabled := range []bool{false, true, false} {
		h.WithImagePrefixCaching(enabled)
		adapter, _, err := h.imageAdapter(t.Context(), "school")
		want := ImageComparisonPromptVersion
		if enabled {
			want = ImageComparisonPrefixPromptVersion
		}
		if err != nil || adapter.Policy().PromptVersion != want {
			t.Fatalf("new task version: enabled=%v adapter=%v err=%v", enabled, adapter, err)
		}
	}
}

func TestImageAdapterInputUsesFrozenVersionAfterRolloutChanges(t *testing.T) {
	for _, version := range []string{ImageComparisonPromptVersion, ImageComparisonPrefixPromptVersion} {
		crop := newActiveCropResolverFixture(t)
		model := &ImageModelConfig{AdapterType: "openai_compatible", ProviderKey: "fixture", APIKey: "synthetic-key", ModelName: "vision", ModelVersion: "vision-v1", ConfigRevision: "revision-1"}
		observation := imageTestObservation("question-1", crop.evidence.CropSHA256)
		observation.ModelBinding = imageModelBinding(model)
		h := NewHandler(nil, NewHTTPAdapter(HTTPAdapterConfig{}), nil).
			WithImageModelResolver(func(context.Context, string) (*ImageModelConfig, error) { return model, nil }).
			WithImageCropResolver(crop.resolver).
			WithImagePrefixCaching(version == ImageComparisonPromptVersion)
		value := Context{SegmentID: "segment-1", InputVersionID: observation.InputVersionID, CropSHA256: observation.CropSHA256, Question: paper.Question{ID: "question-1"}, Observation: &observation}
		policy := ModelPolicy{ModelVersion: model.ModelVersion, PromptVersion: version}
		input, adapter, _, err := h.buildAdapterInput(t.Context(), "tenant-1", "request", value, policy, PromptGuard{}, aieligibility.OutputConstraint{})
		selected, ok := adapter.(*HTTPAdapter)
		if err != nil || !ok || input.ModelPolicy.PromptVersion != version || selected.Policy().PromptVersion != version {
			t.Fatalf("queue task switched prompt version: %s %v", version, err)
		}
	}
}

func TestImageComparisonRequiresExactFrozenVersion(t *testing.T) {
	crop := validResolvedActiveCrop(t)
	model := &ImageModelConfig{AdapterType: "openai_compatible", ProviderKey: "fixture", APIKey: "synthetic-key", ModelName: "vision", ModelVersion: "vision-v1", ConfigRevision: "revision-1"}
	observation := imageTestObservation("q1", crop.SHA256)
	observation.ModelBinding = imageModelBinding(model)
	for _, version := range []string{ImageComparisonPromptVersion, ImageComparisonPrefixPromptVersion} {
		for _, wrongResponse := range []bool{false, true} {
			t.Run(version+map[bool]string{true: "/wrong-response", false: "/exact"}[wrongResponse], func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var request imageAgentRequest
					if json.NewDecoder(r.Body).Decode(&request) != nil || request.ModelPolicy.PromptVersion != version {
						t.Error("request lost frozen prompt version")
					}
					responseVersion := version
					if wrongResponse {
						responseVersion = "unrequested-version"
					}
					_ = json.NewEncoder(w).Encode(imageAgentResponse{SchemaVersion: ImageProtocolVersion, RequestID: request.RequestID, Observation: observation,
						Judgments:       []ImageCriterionJudgment{{CriterionID: "p1", Status: "supported", EvidenceIDs: []string{"student-step"}, Reason: "合成步骤"}},
						StudentFeedback: "核对步骤", TeacherNote: "建议", ModelVersion: model.ModelVersion, PromptVersion: responseVersion, RubricVersion: "rubric-v1", NeedsHumanReview: true,
						Telemetry: gradingAgentTelemetry{Adapter: model.AdapterType, Provider: model.ProviderKey, Deployment: model.ConfigRevision, Region: "managed", Attempts: 1}})
				}))
				defer server.Close()
				adapter := NewHTTPAdapter(HTTPAdapterConfig{BaseURL: server.URL, Token: strings.Repeat("t", 32), ModelVersion: model.ModelVersion, PromptVersion: version, AdapterType: model.AdapterType, ProviderKey: model.ProviderKey, DeploymentKey: model.ConfigRevision, DeploymentRegion: "managed"})
				input := AdapterInput{RequestID: "synthetic-request", SegmentID: "segment", InputVersionID: observation.InputVersionID, CropSHA256: crop.SHA256,
					Question: paper.Question{ID: "q1", Score: 3}, Rubric: paper.Rubric{Version: "rubric-v1", MaxScore: 3, Points: []paper.RubricPoint{{ID: "p1", Score: 3}}},
					ActiveCrop: &crop, ImageModel: model, Observation: &observation, ModelPolicy: adapter.Policy()}
				output, err := adapter.Grade(t.Context(), input)
				if wrongResponse {
					if err == nil {
						t.Fatal("unrequested prompt version accepted")
					}
				} else if err != nil || output.PromptVersion != version || output.RawOutput["comparison_prompt_version"] != version {
					t.Fatalf("lost exact version: %+v %v", output, err)
				}
				input.ModelPolicy.PromptVersion = "unknown-frozen-version"
				if _, err := adapter.Grade(t.Context(), input); err == nil || calls != 1 {
					t.Fatal("invalid frozen version reached provider")
				}
			})
		}
	}
}

func TestImagePanelRetainsFrozenVersionAcrossRollout(t *testing.T) {
	for _, version := range []string{ImageComparisonPromptVersion, ImageComparisonPrefixPromptVersion} {
		value, policy, output := imagePanelFixture(t, "dual", map[string]float64{"primary_a": 6, "primary_b": 6})
		policy.PromptVersion, output.PromptVersion = version, version
		for i := range output.PanelExecution.RoleResults {
			output.PanelExecution.RoleResults[i].Output.PromptVersion = version
		}
		if err := settleImagePanelOutput(value, policy, &output); err != nil || output.RawOutput["comparison_prompt_version"] != version {
			t.Fatalf("frozen panel version lost: %s %v", version, err)
		}
		// 单个角色混入另一版本，不能将其建议并入本次冻结任务。
		output.PanelExecution.RoleResults[0].Output.PromptVersion = "different-version"
		if settleImagePanelOutput(value, policy, &output) == nil {
			t.Fatal("mixed role versions accepted")
		}
	}
}
