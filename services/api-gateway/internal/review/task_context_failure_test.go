package review

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
)

type taskFailureReaderStub struct {
	kind  string
	err   error
	calls [][]string
}

type seedTaskContextFailureStub struct{ seedquality.ReviewHook }

func (seedTaskContextFailureStub) GetGraderTaskContext(context.Context, string, string, string) (seedquality.GraderTaskContext, bool, error) {
	return seedquality.GraderTaskContext{Task: seedquality.GraderTask{ID: "synthetic-task", AnswerSegmentID: "synthetic-segment"}, AICandidates: []any{}}, true, nil
}

func TestSeedTaskContextDoesNotQuerySourceAIFailure(t *testing.T) {
	reader := &taskFailureReaderStub{kind: "output_truncated"}
	handler := NewHandler(NewMemoryStore(), nil, nil, nil).WithSeedHook(seedTaskContextFailureStub{}).WithAIFailureReader(reader)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks/synthetic-task/context", nil)
	request.SetPathValue("taskId", "synthetic-task")
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "reviewer-1", TenantID: tenantID, Roles: []string{"grader"}}))
	response := httptest.NewRecorder()
	handler.GetTaskContext(response, request)
	if response.Code != http.StatusOK || len(reader.calls) != 0 || strings.Contains(response.Body.String(), "ai_failure") || !strings.Contains(response.Body.String(), "synthetic-segment") {
		t.Fatalf("seed context queried or leaked ordinary source failure: status=%d calls=%d body=%s", response.Code, len(reader.calls), response.Body.String())
	}
}

func (s *taskFailureReaderStub) CurrentAIFailure(_ context.Context, tenant, task, segment, question, snapshot, rubric string) (string, error) {
	s.calls = append(s.calls, []string{tenant, task, segment, question, snapshot, rubric})
	return s.kind, s.err
}

func TestTaskContextAIFailureKeepsManualWorkspaceAndDraft(t *testing.T) {
	for _, test := range []struct {
		name, kind, want string
		err              error
	}{
		{name: "truncated", kind: "output_truncated", want: "output_truncated"},
		{name: "diagnostic_store_unavailable", err: errors.New("private provider response https://internal.invalid/token"), want: "output_incomplete"},
		{name: "unknown_kind", kind: "private provider response", want: "output_incomplete"},
		{name: "no_failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore()
			value := reviewContext()
			value.AssessmentSnapshot = taskContextSnapshot(assessment.RiskR2, assessment.ScoringAIAssist)
			value.AISuggestion = map[string]any{"id": "old-ai", "suggested_score": 1.0, "teacher_note": "old suggestion"}
			oldScore, maxScore := 1.0, 5.0
			value.AutomationResult = &AutomationResult{GradeSource: "ai_suggestion", Score: &oldScore, MaxScore: &maxScore}
			bindTaskSuggestionFixture(&value, "segment-1")
			store.AddContext(tenantID, "segment-1", value)
			task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{AnswerSegmentID: "segment-1", Source: "manual_sample", AssignedTo: "reviewer-1"})
			if err != nil {
				t.Fatal(err)
			}
			score := 4.0
			if _, err := store.SaveDraft(context.Background(), tenantID, task.ID, "reviewer-1", SaveDraftInput{Score: &score, Comments: "人工草稿"}); err != nil {
				t.Fatal(err)
			}
			reader := &taskFailureReaderStub{kind: test.kind, err: test.err}
			handler := NewHandler(store, nil, nil, nil).WithAIFailureReader(reader)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks/"+task.ID+"/context", nil)
			request.SetPathValue("taskId", task.ID)
			request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "reviewer-1", TenantID: tenantID, Roles: []string{"grader"}}))
			response := httptest.NewRecorder()
			handler.GetTaskContext(response, request)
			var body struct {
				Context TaskContext `json:"context"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
				t.Fatalf("manual workspace unavailable: status=%d body=%s", response.Code, response.Body.String())
			}
			if len(reader.calls) != 1 || strings.Join(reader.calls[0], "/") != strings.Join([]string{tenantID, task.ID, task.AnswerSegmentID, task.QuestionID, value.AssessmentSnapshot.ID, value.Rubric.Version}, "/") {
				t.Fatalf("diagnostics lost authorized task binding: %#v", reader.calls)
			}
			if body.Context.Draft == nil || body.Context.Draft.Score == nil || *body.Context.Draft.Score != score || body.Context.Draft.Comments != "人工草稿" || body.Context.AnswerArtifact.SegmentImageURL == "" || body.Context.FrozenRubric.ID != value.Rubric.ID {
				t.Fatalf("AI failure damaged manual workspace: %#v", body.Context)
			}
			if test.want == "" {
				if body.Context.AIFailure != nil || body.Context.AISecondOpinion == nil {
					t.Fatal("current success/no-failure was disabled")
				}
			} else if body.Context.AIFailure == nil || body.Context.AIFailure.FailureKind != test.want || body.Context.AISecondOpinion != nil || len(body.Context.AICandidates) != 0 || strings.Contains(response.Body.String(), "old suggestion") || strings.Contains(response.Body.String(), "private provider") {
				t.Fatalf("failed suggestion or private diagnostics leaked: %s", response.Body.String())
			}
			if test.want != "" && body.Context.AutomationResult != nil && body.Context.AutomationResult.Score != nil {
				t.Fatal("stale AI score leaked through automation summary")
			}
			persisted, err := store.GetTaskContext(context.Background(), tenantID, task.ID)
			if err != nil || persisted.AISecondOpinion == nil {
				t.Fatalf("read projection changed persisted suggestion: %v", err)
			}

			// 同一 reader 不能被未分配的教师用于探测任务诊断，授权必须在查询之前完成。
			denied := request.Clone(auth.WithUser(context.Background(), auth.User{ID: "other-reviewer", TenantID: tenantID, Roles: []string{"grader"}}))
			deniedResponse := httptest.NewRecorder()
			handler.GetTaskContext(deniedResponse, denied)
			if deniedResponse.Code != http.StatusForbidden || len(reader.calls) != 1 {
				t.Fatalf("unauthorized diagnostic read: status=%d calls=%d", deniedResponse.Code, len(reader.calls))
			}
		})
	}
}
