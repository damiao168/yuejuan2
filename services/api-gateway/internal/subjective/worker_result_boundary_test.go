package subjective

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestWorkerResultHTTPRejectsInvalidScoringEvidenceAndClosesTask(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*AdapterOutput)
	}{
		{"total above question maximum", func(output *AdapterOutput) { output.SuggestedScore = 5.01 }},
		{"point above frozen rubric weight", func(output *AdapterOutput) { output.MatchedPoints[0].Score = 5.01 }},
		{"confidence above one", func(output *AdapterOutput) { output.Confidence = 1.01 }},
		{"total diverges from rubric points", func(output *AdapterOutput) { output.SuggestedScore-- }},
		{"evidence not in answer", func(output *AdapterOutput) { output.Evidence[0].AnswerText = "invented quotation" }},
		{"evidence bound to a different point", func(output *AdapterOutput) { output.Evidence[0].RubricPointID = "other-point" }},
		{"orphan evidence", func(output *AdapterOutput) {
			extra := output.Evidence[0]
			extra.EvidenceID = "orphan-evidence"
			output.Evidence = append(output.Evidence, extra)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
			input := workerBoundaryResult(fixture)
			tc.change(&input.Output)
			response := callWorkerBoundaryResult(t, fixture, input)
			if response.Code != http.StatusBadRequest || response.Header().Get(httpx.ErrorCodeHeader) != "invalid_model_output" {
				t.Fatalf("invalid suggestion passed settlement: %d %s", response.Code, response.Body)
			}
			var payload struct {
				TaskFailureRecorded bool `json:"task_failure_recorded"`
				Retryable           bool `json:"retryable"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if !payload.TaskFailureRecorded || payload.Retryable {
				t.Fatalf("worker was not told to stop retrying a recorded rejection: %s", response.Body)
			}
			assertWorkerAndRunFailure(t, fixture, "invalid_model_output", RunFailed)
			if _, err := fixture.store.GetGradeByAdapterRequestID(t.Context(), workerServiceTenant, fixture.run.RequestID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("invalid worker output produced a grade: %v", err)
			}
		})
	}
}

func TestWorkerResultHTTPReplaysOnlyOriginalSubmittedEvidence(t *testing.T) {
	fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
	input := workerBoundaryResult(fixture)
	var savedGradeID, savedResultHash string
	for range 2 {
		response := callWorkerBoundaryResult(t, fixture, input)
		if response.Code != http.StatusOK {
			t.Fatalf("valid result failed: %d %s", response.Code, response.Body)
		}
		var completed WorkerCompletionResult
		if err := json.Unmarshal(response.Body.Bytes(), &completed); err != nil {
			t.Fatal(err)
		}
		if completed.Run.Status != RunSucceeded || completed.Task.Status != workerruntime.StatusSucceeded ||
			completed.Grade.ID == "" || completed.Run.GradeID != completed.Grade.ID ||
			completed.Grade.SuggestedScore != 5 || !completed.Grade.NeedsHumanReview {
			t.Fatalf("worker result not bound to one reviewable grade: %+v", completed)
		}
		task, err := fixture.runtime.Get(t.Context(), workerServiceTenant, fixture.task.ID)
		if err != nil || task.ResultPayloadHash == "" {
			t.Fatalf("completed task missing stored result hash: %v", err)
		}
		if savedGradeID != "" && (savedGradeID != completed.Grade.ID || savedResultHash != task.ResultPayloadHash) {
			t.Fatalf("exact replay replaced a saved result: first=%s next=%s", savedGradeID, completed.Grade.ID)
		}
		savedGradeID, savedResultHash = completed.Grade.ID, task.ResultPayloadHash
	}

	// 同分但引文不同，仍是另一份回执。已经成功的任务不能改写证据，也不能用旧回调重开租约。
	input.Output.Evidence[0].AnswerText = "answer"
	response := callWorkerBoundaryResult(t, fixture, input)
	if response.Code != http.StatusConflict || response.Header().Get(httpx.ErrorCodeHeader) != "subjective_task_completion_rejected" {
		t.Fatalf("updated evidence changed an accepted completion: %d %s", response.Code, response.Body)
	}
	grade, gradeErr := fixture.store.GetGradeByAdapterRequestID(t.Context(), workerServiceTenant, fixture.run.RequestID)
	task, taskErr := fixture.runtime.Get(t.Context(), workerServiceTenant, fixture.task.ID)
	if gradeErr != nil || taskErr != nil || grade.ID != savedGradeID || len(grade.Evidence) != 1 ||
		grade.Evidence[0].AnswerText != "synthetic answer" || task.Status != workerruntime.StatusSucceeded ||
		task.ResultPayloadHash != savedResultHash {
		t.Fatalf("conflicting replay mutated grade or task: grade=%s task=%s errors=%v/%v", grade.ID, task.Status, gradeErr, taskErr)
	}
}

func workerBoundaryResult(fixture workerServiceFixture) WorkerResultInput {
	run := fixture.run
	return WorkerResultInput{
		TaskID: fixture.task.ID, LeaseToken: fixture.task.LeaseToken, ResultSchemaVersion: "subjective-grade-v1",
		Output: AdapterOutput{
			RequestID: run.RequestID, ModelVersion: run.ModelVersion, PromptVersion: run.PromptVersion, RubricVersion: run.RubricVersion,
			SuggestedScore: 5, Confidence: .9, NeedsHumanReview: true, DeliveryMode: "teacher_suggestion", CapabilityProfile: "synthetic",
			StudentFeedback: "pending teacher review", TeacherNote: "synthetic suggestion",
			MatchedPoints: []grading.PointResult{{Code: "p1", Score: 5, EvidenceIDs: []string{"e1"}}},
			Evidence:      []grading.Evidence{{EvidenceID: "e1", RubricPointID: "p1", AnswerText: "synthetic answer", Location: "answer_text", Confidence: .9}},
			Telemetry:     AdapterTelemetry{Adapter: "synthetic", Provider: "local", Deployment: "test", Region: "test", Attempts: 1},
			RawOutput:     map[string]any{"fixture": true},
		},
	}
}

func callWorkerBoundaryResult(t *testing.T, fixture workerServiceFixture, input WorkerResultInput) *httptest.ResponseRecorder {
	t.Helper()
	handler := NewHandler(fixture.store, NewMockLLMAdapter(), nil).WithWorkerRuntimeStore(fixture.runtime)
	request := subjectiveTestRequest(t, http.MethodPost, auth.User{ID: "worker-1", TenantID: workerServiceTenant}, input,
		map[string]string{"runId": fixture.run.ID})
	response := httptest.NewRecorder()
	handler.CompleteWorker(response, request)
	return response
}
