package review

import (
	"context"
	"encoding/json"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

func bindTaskSuggestionFixture(value *Context, segmentID string) {
	value.AnswerVersion = "answer-version-1"
	bind := func(item map[string]any) {
		item["answer_segment_id"], item["question_id"] = segmentID, value.Question.ID
		item["answer_version"], item["exam_question_snapshot_id"], item["rubric_version"] = value.AnswerVersion, value.AssessmentSnapshot.ID, value.Rubric.Version
	}
	if len(value.AISuggestion) == 0 {
		return
	}
	bind(value.AISuggestion)
	if history, ok := value.AISuggestion["suggestion_history"].([]any); ok {
		for _, entry := range history {
			if item, ok := entry.(map[string]any); ok {
				bind(item)
			}
		}
	}
}

func TestTaskContextFiltersCurrentSuggestionAndHistoryByVersions(t *testing.T) {
	for _, field := range []string{"answer_version", "exam_question_snapshot_id", "rubric_version", "answer_segment_id", "question_id", "unknown_binding"} {
		t.Run(field, func(t *testing.T) {
			store := NewMemoryStore()
			value := reviewContext()
			value.AssessmentSnapshot = taskContextSnapshot(assessment.RiskR2, assessment.ScoringAIAssist)
			value.AISuggestion = map[string]any{"id": "current-head", "suggested_score": 4.0, "suggestion_history": []any{map[string]any{"id": "valid-history", "suggested_score": 3.0}}}
			bindTaskSuggestionFixture(&value, "segment-1")
			valid := cloneMap(value.AISuggestion["suggestion_history"].([]any)[0].(map[string]any))
			stale := cloneMap(valid)
			stale["id"] = "stale-history"
			if field == "unknown_binding" {
				delete(stale, "exam_question_snapshot_id")
				delete(value.AISuggestion, "exam_question_snapshot_id")
			} else {
				stale[field], value.AISuggestion[field] = "old-version", "old-version"
			}
			value.AISuggestion["suggestion_history"] = []any{stale, valid}
			store.AddContext(tenantID, "segment-1", value)
			task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{AnswerSegmentID: "segment-1", Source: "manual_sample", AssignedTo: "reviewer-1"})
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(store.contexts[key(tenantID, "segment-1")].AISuggestion)
			result, err := store.GetTaskContext(context.Background(), tenantID, task.ID)
			if err != nil || result.AISecondOpinion == nil || result.AISecondOpinion.Metadata["id"] != "valid-history" || len(result.AISecondOpinion.History) != 1 || result.AISecondOpinion.History[0]["id"] != "valid-history" {
				t.Fatalf("stale suggestion survived or valid same-version history lost: %#v %v", result.AISecondOpinion, err)
			}
			after, _ := json.Marshal(store.contexts[key(tenantID, "segment-1")].AISuggestion)
			if string(before) != string(after) {
				t.Fatal("read projection rewrote stored AI history")
			}
			value.AISuggestion["suggestion_history"] = []any{stale}
			store.AddContext(tenantID, "segment-1", value)
			result, err = store.GetTaskContext(context.Background(), tenantID, task.ID)
			if err != nil || result.AISecondOpinion != nil {
				t.Fatalf("old-only suggestion remained adoptable: %#v %v", result.AISecondOpinion, err)
			}
		})
	}
}
