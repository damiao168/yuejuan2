package subjective

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func imageTestObservation(questionID, hash string) StudentObservation {
	return StudentObservation{SchemaVersion: ImageProtocolVersion, InputVersionID: "input-frozen", CropSHA256: hash, PromptVersion: ImageObservationPromptVersion, Members: []ObservedMember{{QuestionID: questionID, Presence: "present", Association: "matched", Confidence: .97, Answer: &ObservedAnswer{Type: "latex", Value: "-1/2 m"}, Evidence: []ObservationEvidence{{EvidenceID: "student-step", Text: "-1/2 m", BBox: []float64{.1, .2, .3, .1}}}}}}
}

func TestImageObservationReusesKnownPromptVersionsWithInputBinding(t *testing.T) {
	members := []ObservationMember{{QuestionID: "q1"}}
	for _, version := range []string{"student-observation-v3-20261021", "student-observation-v3-20261021-r2", ImageObservationPromptVersion, "unrecognized-version"} {
		observation := imageTestObservation("q1", "crop-hash")
		observation.PromptVersion = version
		err := ValidateStudentObservation(observation, "input-frozen", "crop-hash", members)
		if (err == nil) != (version != "unrecognized-version") {
			t.Fatalf("unexpected version compatibility: %s %v", version, err)
		}
		if ValidateStudentObservation(observation, "other-input", "crop-hash", members) == nil {
			t.Fatal("prompt compatibility bypassed input binding")
		}
	}
}

func TestImageGradeWithoutOCRSendsActualPNGAndUsesFrozenStudentEvidence(t *testing.T) {
	crop := validResolvedActiveCrop(t)
	model := &ImageModelConfig{AdapterType: "openai_compatible", ProviderKey: "fixture", BaseURL: "https://fixture.invalid/v1", APIKey: "private-test-credential", ModelName: "fixture-vision", ModelVersion: "vision-v1", ConfigRevision: "config-revision-1"}
	observation := imageTestObservation("q1", crop.SHA256)
	observation.ModelBinding = imageModelBinding(model)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/grading/grade-image-v3" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var request imageAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		data, _ := base64.StdEncoding.DecodeString(request.StudentImage.Data)
		if request.InputMode != "image" || request.AnswerText != "" || string(data) != string(crop.Data) || request.Observation == nil {
			t.Errorf("image-only request lost actual image or observation")
		}
		if request.FrozenReference.StandardAnswer != "teacher-reference" {
			t.Error("comparison did not receive frozen reference")
		}
		_ = json.NewEncoder(w).Encode(imageAgentResponse{SchemaVersion: ImageProtocolVersion, RequestID: request.RequestID, Observation: observation, Judgments: []ImageCriterionJudgment{{CriterionID: "p1", Status: "supported", EvidenceIDs: []string{"student-step"}, Reason: "学生保留了负号和单位"}}, StudentFeedback: "请核对结果", TeacherNote: "仅作建议", ModelVersion: "vision-v1", PromptVersion: ImageComparisonPromptVersion, RubricVersion: "rubric-v1", NeedsHumanReview: true, Telemetry: gradingAgentTelemetry{Adapter: "openai_compatible", Provider: "fixture", Deployment: "config-revision-1", Region: "managed", Attempts: 1}})
	}))
	defer server.Close()
	adapter := NewHTTPAdapter(HTTPAdapterConfig{BaseURL: server.URL, Token: strings.Repeat("t", 32), ModelVersion: "vision-v1", PromptVersion: "governed-prompt", AdapterType: "openai_compatible", ProviderKey: "fixture", DeploymentKey: "config-revision-1", DeploymentRegion: "managed"})
	input := AdapterInput{RequestID: "image-request-1", SegmentID: "segment1", InputVersionID: "input-frozen", CropSHA256: crop.SHA256, Question: paper.Question{ID: "q1", QuestionType: "calculation", Score: 3}, Rubric: paper.Rubric{ID: "rubric1", Version: "rubric-v1", MaxScore: 3, Points: []paper.RubricPoint{{ID: "p1", Score: 3}}}, ActiveCrop: &crop, Observation: &observation, ImageModel: model, ModelPolicy: adapter.Policy(), ReferenceContext: &GradingReferenceContext{StandardAnswer: "teacher-reference"}}
	output, err := adapter.Grade(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	value := Context{SegmentID: input.SegmentID, InputVersionID: input.InputVersionID, CropSHA256: input.CropSHA256, Question: input.Question, Rubric: input.Rubric, Observation: &observation}
	if err := ValidateOutput(output, value); err != nil {
		t.Fatalf("image evidence rejected: %v", err)
	}
	if output.SuggestedScore != 3 || !output.NeedsHumanReview || output.Evidence[0].Type != "observation" {
		t.Fatalf("unexpected suggestion %+v", output)
	}
	// 外部 worker 即使伪造高分和采分点，也只能按冻结观察与分类重新结算。
	output.SuggestedScore = 999
	output.MatchedPoints[0].Score = 999
	pipeline := NewGradeSettlementPipeline(nil, nil)
	if err := pipeline.Settle(context.Background(), GradeSettlementInput{Context: value, Policy: adapter.Policy()}, &output); err != nil {
		t.Fatal(err)
	}
	if output.SuggestedScore != 3 {
		t.Fatal("worker numeric score bypassed rubric")
	}
	output.ImageJudgments[0].EvidenceIDs = []string{"teacher-reference"}
	if err := pipeline.Settle(context.Background(), GradeSettlementInput{Context: value, Policy: adapter.Policy()}, &output); err == nil {
		t.Fatal("teacher evidence accepted as student work")
	}
}

func TestGroupObservationKeepsBlankUnreadableAndUnresolvedDistinct(t *testing.T) {
	observation := imageTestObservation("q12", "crop-hash")
	observation.Members = append(observation.Members, ObservedMember{QuestionID: "q13", Presence: "blank", Association: "matched", Confidence: .99, Evidence: []ObservationEvidence{{EvidenceID: "blank-region", BBox: []float64{.1, .4, .3, .1}}}}, ObservedMember{QuestionID: "q14", Presence: "uncertain", Association: "unresolved", Confidence: .2})
	members := []ObservationMember{{QuestionID: "q12"}, {QuestionID: "q13"}, {QuestionID: "q14"}}
	if err := ValidateStudentObservation(observation, "input-frozen", "crop-hash", members); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStudentObservation(observation, "input-frozen", "changed-crop", members); err == nil {
		t.Fatal("stale crop accepted")
	}
	observation.Members[1].Evidence = nil
	if err := ValidateStudentObservation(observation, "input-frozen", "crop-hash", members); err == nil {
		t.Fatal("empty output was promoted to blank")
	}
	observation.Members[1].Presence = "unreadable"
	if err := ValidateStudentObservation(observation, "input-frozen", "crop-hash", members); err != nil {
		t.Fatal(err)
	}
	observation.Members[1].Evidence = []ObservationEvidence{observation.Members[0].Evidence[0]}
	if err := ValidateStudentObservation(observation, "input-frozen", "crop-hash", members); err == nil {
		t.Fatal("shared evidence identity accepted for two answers")
	}
}

func TestImageModelResolverNeverFallsBackToLocalAndDoesNotPersistCredential(t *testing.T) {
	handler := NewHandler(nil, NewHTTPAdapter(HTTPAdapterConfig{ModelVersion: "local-text"}), nil)
	if _, _, err := handler.imageAdapter(context.Background(), "school"); err == nil {
		t.Fatal("local text fallback enabled")
	}
	model := &ImageModelConfig{AdapterType: "openai_compatible", ProviderKey: "fixture", BaseURL: "https://fixture.invalid/v1", APIKey: "private-secret", ModelName: "visual", ModelVersion: "visual-v1", ConfigRevision: "revision1"}
	handler.WithImageModelResolver(func(context.Context, string) (*ImageModelConfig, error) { return model, nil })
	adapter, selected, err := handler.imageAdapter(context.Background(), "school")
	if err != nil || adapter.Policy().ModelVersion != "visual-v1" {
		t.Fatal("selected school model was not used")
	}
	raw, _ := json.Marshal(AdapterInput{ImageModel: selected})
	if strings.Contains(string(raw), "private-secret") {
		t.Fatal("credential entered persistent input JSON")
	}
	old := imageModelBinding(model)
	model.APIKey = "rotated-secret"
	if imageModelBinding(model) != old {
		t.Fatal("credential bytes must not become artifact identity")
	}
	model.ConfigRevision = "revision2"
	if imageModelBinding(model) == old {
		t.Fatal("config revision change failed to invalidate image model binding")
	}
}
