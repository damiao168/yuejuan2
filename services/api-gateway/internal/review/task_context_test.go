package review

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestTaskContextAggregatesFrozenFactsAndSubjectHints(t *testing.T) {
	store := NewMemoryStore()
	contextValue := reviewContext()
	contextValue.AssessmentSnapshot = taskContextSnapshot(assessment.RiskR2, assessment.ScoringAIAssist)
	bindTaskSuggestionFixture(&contextValue, "segment-1")
	store.AddContext(tenantID, "segment-1", contextValue)
	task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{
		AnswerSegmentID: "segment-1", Source: "ai_low_confidence", AssignedTo: "reviewer-1",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	result, err := store.GetTaskContext(context.Background(), tenantID, task.ID)
	if err != nil {
		t.Fatalf("get task context: %v", err)
	}
	if result.Task.ID != task.ID || result.ExpectedRevision != task.Revision {
		t.Fatalf("task concurrency guard mismatch: %#v", result)
	}
	if result.QuestionSnapshot.ID != "snapshot-1" || result.FrozenRubric.ID != "rubric-1" {
		t.Fatalf("frozen facts missing: %#v", result)
	}
	if result.AnswerArtifact.AnswerSegmentID != "segment-1" || result.AnswerArtifact.SegmentImageURL == "" {
		t.Fatalf("answer artifact metadata missing: %#v", result.AnswerArtifact)
	}
	if result.SubjectToolHints.SubjectCode != assessment.SubjectMathematics ||
		result.SubjectToolHints.ParserPolicy["math_expression"] != true ||
		len(result.SubjectToolHints.AllowedEvidenceTypes) != 1 {
		t.Fatalf("subject tool hints must come from the frozen snapshot: %#v", result.SubjectToolHints)
	}
	if result.AISecondOpinion == nil || !result.AISecondOpinion.ScorePrefillAllowed {
		t.Fatalf("R2 AI assist should retain opt-in score material: %#v", result.AISecondOpinion)
	}
}

func TestR3HumanPrimaryContextRemovesAllAIScoreFields(t *testing.T) {
	store := NewMemoryStore()
	contextValue := reviewContext()
	contextValue.AssessmentSnapshot = taskContextSnapshot(assessment.RiskR3, assessment.ScoringHumanPrimary)
	contextValue.AISuggestion = map[string]any{
		"id": "ai-grade-1", "suggested_score": 4.0, "max_score": 5.0,
		"confidence":         0.83,
		"evidence":           []any{map[string]any{"label": "method", "score": 2.0}},
		"suggestion_history": []any{map[string]any{"id": "old-ai", "math_artifact_version": 2.0, "suggested_score": 3.0, "evidence": []any{map[string]any{"score": 2.0}}}},
	}
	automationScore, automationMax := 4.0, 5.0
	contextValue.AutomationResult = &AutomationResult{Decision: "review", Score: &automationScore, MaxScore: &automationMax}
	bindTaskSuggestionFixture(&contextValue, "segment-1")
	store.AddContext(tenantID, "segment-1", contextValue)
	task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{
		AnswerSegmentID: "segment-1", Source: "manual_sample", AssignedTo: "reviewer-1",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	result, err := store.GetTaskContext(context.Background(), tenantID, task.ID)
	if err != nil {
		t.Fatalf("get task context: %v", err)
	}
	if result.AISecondOpinion == nil || result.AISecondOpinion.ScorePrefillAllowed ||
		result.AISecondOpinion.Presentation != "explicit_second_opinion" {
		t.Fatalf("R3 human primary must expose only an explicit second opinion: %#v", result.AISecondOpinion)
	}
	raw, _ := json.Marshal(result.AISecondOpinion.Metadata)
	var decoded any
	_ = json.Unmarshal(raw, &decoded)
	if containsScoreKey(decoded) {
		t.Fatalf("R3 human-primary AI metadata contains a score field: %s", raw)
	}
	if result.AISecondOpinion.Metadata["confidence"] != 0.83 {
		t.Fatalf("non-score second-opinion metadata should remain available: %#v", result.AISecondOpinion.Metadata)
	}
	historyRaw, _ := json.Marshal(result.AISecondOpinion.History)
	var historyDecoded any
	_ = json.Unmarshal(historyRaw, &historyDecoded)
	if len(result.AISecondOpinion.History) != 1 || containsScoreKey(historyDecoded) {
		t.Fatalf("history bypassed R3 score guard: %s", historyRaw)
	}
	if _, exists := result.AISecondOpinion.Metadata["suggestion_history"]; exists {
		t.Fatal("history remained in metadata")
	}
	if result.AutomationResult == nil || result.AutomationResult.Score != nil || result.AutomationResult.MaxScore != nil {
		t.Fatalf("R3 human-primary automation metadata must not expose a prefillable score: %#v", result.AutomationResult)
	}
}

func TestGetTaskContextIncludesOnlyAssignedReviewersDraft(t *testing.T) {
	store := NewMemoryStore()
	contextValue := reviewContext()
	contextValue.AssessmentSnapshot = taskContextSnapshot(assessment.RiskR2, assessment.ScoringAIAssist)
	bindTaskSuggestionFixture(&contextValue, "segment-1")
	store.AddContext(tenantID, "segment-1", contextValue)
	task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{
		AnswerSegmentID: "segment-1", Source: "manual_sample", AssignedTo: "reviewer-1",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	score := 3.0
	if _, err := store.SaveDraft(context.Background(), tenantID, task.ID, "reviewer-1", SaveDraftInput{Score: &score}); err != nil {
		t.Fatalf("save draft: %v", err)
	}

	handler := NewHandler(store, nil, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks/"+task.ID+"/context", nil)
	request.SetPathValue("taskId", task.ID)
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "reviewer-1", TenantID: tenantID, Roles: []string{"grader"}}))
	response := httptest.NewRecorder()
	handler.GetTaskContext(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("get context status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Context TaskContext `json:"context"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Context.Draft == nil || body.Context.Draft.ReviewerID != "reviewer-1" || body.Context.Draft.Score == nil || *body.Context.Draft.Score != score {
		t.Fatalf("assigned reviewer draft missing: %#v", body.Context.Draft)
	}
	if !body.Context.Claim.CanRenew {
		t.Fatalf("assigned reviewer should receive a renewable claim: %#v", body.Context.Claim)
	}

	managerRequest := request.Clone(auth.WithUser(context.Background(), auth.User{
		ID: "manager-1", TenantID: tenantID, Roles: []string{"school_admin"}, Permissions: []string{"review:manage"},
	}))
	managerResponse := httptest.NewRecorder()
	handler.GetTaskContext(managerResponse, managerRequest)
	if managerResponse.Code != http.StatusOK {
		t.Fatalf("manager context status=%d body=%s", managerResponse.Code, managerResponse.Body.String())
	}
	if err := json.Unmarshal(managerResponse.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode manager response: %v", err)
	}
	if body.Context.Draft != nil || body.Context.Claim.CanRenew {
		t.Fatalf("manager must not receive the reviewer's private draft or renewable claim: %#v", body.Context)
	}
}

func taskContextSnapshot(risk assessment.RiskTier, mode assessment.ScoringMode) assessment.ExamQuestionSnapshot {
	return assessment.ExamQuestionSnapshot{
		ID: "snapshot-1", TenantID: tenantID, ExamID: "exam-1", QuestionID: "question-1",
		SnapshotVersion: 1, SubjectProfileID: "profile-1", SubjectProfileCode: "senior-mathematics",
		SubjectProfileVersion: 2, EducationStage: assessment.StageSenior,
		SubjectCode: assessment.SubjectMathematics, ArchetypeCode: "structured_steps",
		AllowedEvidenceTypes: []assessment.EvidenceType{assessment.EvidenceMathStep}, RiskTier: risk,
		ProfileSnapshot: map[string]any{
			"parser_policy":   map[string]any{"math_expression": true},
			"evidence_policy": map[string]any{"minimum": 1.0},
		},
		ArchetypeSnapshot:     map[string]any{"response_schema": map[string]any{"type": "steps"}},
		ScoringPolicySnapshot: assessment.ScoringPolicy{Mode: mode, RequireEvidence: true},
	}
}

func containsScoreKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if key == "score" || key == "suggested_score" || key == "max_score" || containsScoreKey(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsScoreKey(nested) {
				return true
			}
		}
	}
	return false
}
