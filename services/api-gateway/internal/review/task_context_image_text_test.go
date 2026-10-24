package review

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func imageTextContextFixture() Context {
	value := reviewContext()
	value.RawAnswer, value.OCRText = "", ""
	value.AssessmentSnapshot = taskContextSnapshot(assessment.RiskR3, assessment.ScoringHumanPrimary)
	value.AISuggestion = map[string]any{"id": "image-grade", "status": "succeeded", "suggested_score": 4.0}
	bindTaskSuggestionFixture(&value, "segment-1")
	value.AnswerSegmentID = "segment-1"
	value.AISuggestion["input_binding"] = map[string]any{
		"input_mode": "image", "input_protocol": "grading-image-v3", "input_version_id": value.AnswerVersion,
		"crop_sha256": "current-crop", "assessment_snapshot_id": value.AssessmentSnapshot.ID,
		"answer_segment_id": value.AnswerSegmentID, "question_id": value.Question.ID,
	}
	value.AISuggestion["raw_output"] = map[string]any{
		"schema_version": "grading-image-v3", "input_version_id": value.AnswerVersion,
		"assessment_snapshot_id": value.AssessmentSnapshot.ID,
		"observation":            imageTextObservationFixture(value.Question.ID, "\\frac{1}{6}"),
		"judgments":              []any{map[string]any{"reason": "不得混入学生转写的评分理由", "score": 4.0}},
	}
	return value
}

func imageTextObservationFixture(questionID, text string) map[string]any {
	return map[string]any{
		"schema_version": "grading-image-v3", "input_version_id": "answer-version-1", "crop_sha256": "current-crop",
		"members": []any{
			map[string]any{"question_id": "another-question", "presence": "present", "association": "matched", "answer": map[string]any{"value": "另一题的作答"}},
			map[string]any{"question_id": questionID, "presence": "present", "association": "matched", "answer": map[string]any{"value": text}},
		},
	}
}

func imageTextTaskFixture(t *testing.T, value Context) (*MemoryStore, ReviewTask) {
	t.Helper()
	store := NewMemoryStore()
	store.AddContext(tenantID, "segment-1", value)
	task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{AnswerSegmentID: "segment-1", Source: "manual_sample", AssignedTo: "reviewer-1"})
	if err != nil {
		t.Fatal(err)
	}
	return store, task
}

func TestTaskContextProjectsOnlyCurrentImageStudentTextBeforeSecondOpinion(t *testing.T) {
	value := imageTextContextFixture()
	store, task := imageTextTaskFixture(t, value)
	before, _ := json.Marshal(value.AISuggestion)
	result, err := store.GetTaskContext(context.Background(), tenantID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.AnswerArtifact.OCRText != "\\frac{1}{6}" || result.AnswerArtifact.Source != "image_observation" || result.AnswerArtifact.RawAnswer != "" {
		t.Fatalf("image transcription did not reach the answer artifact: %#v", result.AnswerArtifact)
	}
	if result.AISecondOpinion == nil || result.AISecondOpinion.Presentation != "explicit_second_opinion" || result.AISecondOpinion.ScorePrefillAllowed {
		t.Fatalf("student text changed the independent scoring opinion policy: %#v", result.AISecondOpinion)
	}
	raw, _ := json.Marshal(result.AnswerArtifact)
	if strings.Contains(string(raw), "另一题") || strings.Contains(string(raw), "评分理由") || strings.Contains(string(raw), "suggested_score") {
		t.Fatalf("non-student material leaked into transcription: %s", raw)
	}
	after, _ := json.Marshal(store.contexts[key(tenantID, "segment-1")].AISuggestion)
	if string(before) != string(after) {
		t.Fatal("read projection rewrote stored image grading output")
	}
}

func TestTaskContextImageTextKeepsPartsAndIndependentModelLabels(t *testing.T) {
	value := imageTextContextFixture()
	raw := value.AISuggestion["raw_output"].(map[string]any)
	observation := raw["observation"].(map[string]any)
	member := observation["members"].([]any)[1].(map[string]any)
	member["answer"] = map[string]any{"parts": []string{"第一步", "", "第二步"}}
	store, task := imageTextTaskFixture(t, value)
	result, err := store.GetTaskContext(context.Background(), tenantID, task.ID)
	if err != nil || result.AnswerArtifact.OCRText != "第一步\n第二步" {
		t.Fatalf("parts-only student text was lost: %#v %v", result.AnswerArtifact, err)
	}
	raw["panel_execution"] = map[string]any{"role_results": []any{
		map[string]any{"role": "primary_a", "status": "succeeded", "raw_output": map[string]any{"observation": imageTextObservationFixture(value.Question.ID, "模型 A 转写")}},
		map[string]any{"role": "primary_b", "status": "succeeded", "raw_output": map[string]any{"observation": imageTextObservationFixture(value.Question.ID, "模型 B 转写")}},
		map[string]any{"role": "arbiter", "status": "failed", "raw_output": map[string]any{"observation": imageTextObservationFixture(value.Question.ID, "失败角色的旧转写")}},
	}}
	store.AddContext(tenantID, "segment-1", value)
	result, err = store.GetTaskContext(context.Background(), tenantID, task.ID)
	if err != nil || result.AnswerArtifact.OCRText != "评分 A：\n模型 A 转写\n\n评分 B：\n模型 B 转写" {
		t.Fatalf("independent model transcriptions were mixed: %#v %v", result.AnswerArtifact, err)
	}
}

func TestTaskContextRejectsImageTextOutsideCurrentBinding(t *testing.T) {
	for _, boundary := range []string{"answer_version", "exam_question_snapshot_id", "rubric_version", "answer_segment_id", "question_id", "failed", "cancelled", "binding_input", "binding_crop", "binding_question", "binding_segment", "binding_snapshot", "raw_input", "raw_snapshot", "observation_input", "observation_crop", "observation_question", "unresolved", "unreadable", "duplicate_member", "failed_panel"} {
		t.Run(boundary, func(t *testing.T) {
			value := imageTextContextFixture()
			suggestion := value.AISuggestion
			binding := suggestion["input_binding"].(map[string]any)
			raw := suggestion["raw_output"].(map[string]any)
			observation := raw["observation"].(map[string]any)
			member := observation["members"].([]any)[1].(map[string]any)
			switch boundary {
			case "failed", "cancelled":
				suggestion["status"] = boundary
			case "binding_input":
				binding["input_version_id"] = "old-input"
			case "binding_crop":
				binding["crop_sha256"] = "old-crop"
			case "binding_question":
				binding["question_id"] = "another-question"
			case "binding_segment":
				binding["answer_segment_id"] = "another-segment"
			case "binding_snapshot":
				binding["assessment_snapshot_id"] = "old-snapshot"
			case "raw_input":
				raw["input_version_id"] = "old-input"
			case "raw_snapshot":
				raw["assessment_snapshot_id"] = "old-snapshot"
			case "observation_input":
				observation["input_version_id"] = "old-input"
			case "observation_crop":
				observation["crop_sha256"] = "old-crop"
			case "observation_question":
				member["question_id"] = "different-question"
			case "unresolved":
				member["association"] = "unresolved"
			case "unreadable":
				member["presence"] = "unreadable"
			case "duplicate_member":
				observation["members"] = append(observation["members"].([]any), member)
			case "failed_panel":
				raw["panel_execution"] = map[string]any{"role_results": []any{map[string]any{"role": "single", "status": "failed", "raw_output": raw["observation"]}}}
			default:
				suggestion[boundary] = "old-binding"
			}
			store, task := imageTextTaskFixture(t, value)
			result, err := store.GetTaskContext(context.Background(), tenantID, task.ID)
			if err != nil || result.AnswerArtifact.OCRText != "" {
				t.Fatalf("non-current image text was exposed: %#v %v", result.AnswerArtifact, err)
			}
		})
	}
}

func TestTaskContextCurrentFailureRemovesProjectedImageText(t *testing.T) {
	value := imageTextContextFixture()
	store, task := imageTextTaskFixture(t, value)
	handler := NewHandler(store, nil, nil, nil).WithAIFailureReader(&taskFailureReaderStub{kind: "output_truncated"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks/"+task.ID+"/context", nil)
	request.SetPathValue("taskId", task.ID)
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "reviewer-1", TenantID: tenantID, Roles: []string{"grader"}}))
	response := httptest.NewRecorder()
	handler.GetTaskContext(response, request)
	var body struct {
		Context TaskContext `json:"context"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Context.AIFailure == nil || body.Context.AnswerArtifact.OCRText != "" || body.Context.AnswerArtifact.Source == "image_observation" {
		t.Fatalf("current failure retained the previous image transcription: %s", response.Body.String())
	}
}
