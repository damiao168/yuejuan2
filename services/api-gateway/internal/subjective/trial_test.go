package subjective

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

const trialTestTenant = "trial-tenant"

func trialFixture(t *testing.T) (*MemoryStore, CreateTrialInput, ModelPolicy) {
	t.Helper()
	store := NewMemoryStore()
	ids := []string{}
	input := CreateTrialInput{CommandID: "trial-command", QuestionID: "question-short_answer"}
	for index := 0; index < 8; index++ {
		id := fmt.Sprintf("segment-%d", index)
		value := ContextForTest("short_answer", 10, "独立合成答案", nil)
		value.AssessmentSnapshot.ScoringPolicySnapshot.Mode = assessment.ScoringAIAssist
		store.AddContext(trialTestTenant, id, value)
		ids = append(ids, id)
		if index < 5 {
			roles := [][]string{{"representative"}, {"boundary"}, {"incomplete_evidence"}, {}, {}}
			input.Samples = append(input.Samples, TrialSampleInput{SegmentID: id, HumanScore: float64(index), Roles: roles[index]})
		}
	}
	other := ContextForTest("essay", 20, "另一题答案", nil)
	other.AssessmentSnapshot.ScoringPolicySnapshot.Mode = assessment.ScoringAIAssist
	store.AddContext(trialTestTenant, "other-question", other)
	ids = append(ids, "other-question")
	store.BindScoringTrialRun(trialTestTenant, "run-1", ids)
	return store, input, NormalizePolicy(ModelPolicy{})
}

func createFixtureTrial(t *testing.T, store *MemoryStore, input CreateTrialInput, policy ModelPolicy) ScoringTrial {
	t.Helper()
	trial, err := store.CreateScoringTrial(context.Background(), trialTestTenant, "teacher-1", "run-1", input, policy)
	if err != nil {
		t.Fatal(err)
	}
	return trial
}

func finishTrialSamples(t *testing.T, store *MemoryStore, trial ScoringTrial, policy ModelPolicy) ScoringTrial {
	t.Helper()
	ctx := context.Background()
	for _, sample := range trial.Samples {
		value, err := store.LoadContext(ctx, trialTestTenant, sample.SegmentID)
		if err != nil {
			t.Fatal(err)
		}
		run, err := store.GetOrCreateRun(ctx, trialTestTenant, "teacher-1", runInputFor(value, trial.SampleBatchID, policy, "sample-"+sample.SegmentID))
		if err != nil {
			t.Fatal(err)
		}
		grade, err := store.CreateGrade(ctx, trialTestTenant, "teacher-1", Grade{RunID: run.ID, AnswerSegmentID: sample.SegmentID,
			QuestionID: trial.QuestionID, AdapterRequestID: run.RequestID, SuggestedScore: sample.HumanScore + 1, Status: RunSucceeded,
			TotalTokens: 42, AdapterLatencyMS: 12})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateRun(ctx, trialTestTenant, run.ID, UpdateRunInput{Status: RunSucceeded, GradeID: grade.ID}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.ListScoringTrials(ctx, trialTestTenant, trial.RunID, policy)
	if err != nil || len(items) != 1 {
		t.Fatalf("refresh samples: %+v, %v", items, err)
	}
	return items[0]
}

func TestTrialHumanScoreRequiredBeforeAnyAI(t *testing.T) {
	for _, body := range []string{`{"segment_id":"s","roles":[]}`, `{"segment_id":"s","human_score":null,"roles":[]}`} {
		var input TrialSampleInput
		if err := json.Unmarshal([]byte(body), &input); err == nil {
			t.Fatal("missing human baseline was accepted")
		}
	}
	var zero TrialSampleInput
	if err := json.Unmarshal([]byte(`{"segment_id":"s","human_score":0,"roles":[]}`), &zero); err != nil || zero.HumanScore != 0 {
		t.Fatalf("explicit zero must be a valid human baseline: %v", err)
	}
	store, input, policy := trialFixture(t)
	trial := createFixtureTrial(t, store, input, policy)
	if trial.Status != "awaiting_enqueue" || len(store.runs) != 0 || len(store.grades) != 0 {
		t.Fatal("saving baselines generated AI results before explicit enqueue")
	}
	if _, err := store.CreateScoringTrial(context.Background(), trialTestTenant, "teacher-2", "run-1", input, policy); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("another actor reused the command: %v", err)
	}
	replay := createFixtureTrial(t, store, input, policy)
	if replay.ID != trial.ID || len(store.batches) != 1 {
		t.Fatal("same create command generated a second trial or batch")
	}
}

func TestTrialRejectsMissingCoverageAndOutOfScopeBaselines(t *testing.T) {
	for _, scenario := range []string{"too_few", "too_many", "duplicate", "missing_role", "foreign_question", "score_over_max"} {
		t.Run(scenario, func(t *testing.T) {
			store, input, policy := trialFixture(t)
			switch scenario {
			case "too_few":
				input.Samples = input.Samples[:4]
			case "too_many":
				input.Samples = append(input.Samples, input.Samples...)
				input.Samples = append(input.Samples, input.Samples[0])
			case "duplicate":
				input.Samples[4].SegmentID = input.Samples[0].SegmentID
			case "missing_role":
				input.Samples[2].Roles = nil
			case "foreign_question":
				input.Samples[4].SegmentID = "other-question"
			case "score_over_max":
				input.Samples[4].HumanScore = 11
			}
			if _, err := store.CreateScoringTrial(context.Background(), trialTestTenant, "teacher-1", "run-1", input, policy); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("invalid sample was accepted: %v", err)
			}
			if len(store.batches) != 0 {
				t.Fatal("invalid baselines left an enqueueable batch")
			}
		})
	}
}

func TestTrialNewRunGateCoversGenericBatchAndDirectGrade(t *testing.T) {
	store, input, policy := trialFixture(t)
	ctx := context.Background()
	for _, runID := range []string{"run-1", ""} {
		if _, err := store.CreateBatch(ctx, trialTestTenant, "teacher-1", CreateBatchInput{IdempotencyKey: "bypass" + runID, ScoringRunID: runID, SegmentIDs: []string{"segment-0"}}); !errors.Is(err, ErrTrialConflict) {
			t.Fatalf("generic batch bypassed the trial gate: %v", err)
		}
	}
	value, _ := store.LoadContext(ctx, trialTestTenant, "segment-0")
	if _, err := store.GetOrCreateRun(ctx, trialTestTenant, "teacher-1", runInputFor(value, "", policy, "direct")); !errors.Is(err, ErrTrialConflict) {
		t.Fatalf("direct grading bypassed the trial gate: %v", err)
	}
	handler := NewHandler(store, NewMockLLMAdapter(), nil)
	req := httptest.NewRequest(http.MethodPost, "/grade", bytes.NewBufferString(`{}`))
	req.SetPathValue("id", "segment-0")
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "teacher-1", TenantID: trialTestTenant}))
	rec := httptest.NewRecorder()
	handler.Grade(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("direct HTTP gate should return 409: %d %s", rec.Code, rec.Body.String())
	}
	trial := createFixtureTrial(t, store, input, policy)
	if _, err := store.GetOrCreateRun(ctx, trialTestTenant, "teacher-1", runInputFor(value, trial.SampleBatchID, policy, "sample")); err != nil {
		t.Fatalf("authorized sample could not create its run: %v", err)
	}
	other, _ := store.LoadContext(ctx, trialTestTenant, "other-question")
	if _, err := store.GetOrCreateRun(ctx, trialTestTenant, "teacher-1", runInputFor(other, trial.SampleBatchID, policy, "foreign")); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("sample batch admitted a foreign segment: %v", err)
	}
}

func TestTrialResultsStayBoundToOwnBatchAndExpansionStaysOnQuestion(t *testing.T) {
	store, input, policy := trialFixture(t)
	trial := createFixtureTrial(t, store, input, policy)
	if _, err := store.DecideScoringTrial(context.Background(), trialTestTenant, "teacher-1", trial.RunID, trial.ID, TrialDecisionInput{Decision: "expand", Reason: "too early", ExpectedRevision: trial.Revision}, policy); !errors.Is(err, ErrTrialConflict) {
		t.Fatalf("unfinished samples expanded: %v", err)
	}
	// 同片段的无关历史成绩不能填补样本结果；只有本次批次与 request/run 身份完全相同的成绩可展示。
	if _, err := store.CreateGrade(context.Background(), trialTestTenant, "teacher-1", Grade{AnswerSegmentID: "segment-0", QuestionID: trial.QuestionID, AdapterRequestID: "unrelated", SuggestedScore: 10, Status: RunSucceeded}); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListScoringTrials(context.Background(), trialTestTenant, trial.RunID, policy)
	if err != nil || items[0].Samples[0].Result != nil {
		t.Fatalf("historical grade leaked into current samples: %+v %v", items, err)
	}
	trial = finishTrialSamples(t, store, trial, policy)
	if trial.Status != "reviewing" {
		t.Fatalf("completed samples are not reviewable: %+v", trial)
	}
	for _, sample := range trial.Samples {
		result := sample.Result
		if result == nil || result.SuggestedScore == nil || *result.SuggestedScore != sample.HumanScore+1 || result.LatencyMS == nil || *result.TotalTokens != 42 || !containsString(result.RiskFlags, "trial_sample") {
			t.Fatalf("sample result is incomplete or unmarked: %+v", sample)
		}
	}
	decision := TrialDecisionInput{Decision: "expand", Reason: "已逐份核对，继续生成同题建议", ExpectedRevision: trial.Revision}
	expanded, err := store.DecideScoringTrial(context.Background(), trialTestTenant, "teacher-1", trial.RunID, trial.ID, decision, policy)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := store.GetBatch(context.Background(), trialTestTenant, expanded.ExpansionBatchID)
	if err != nil || !reflect.DeepEqual(batch.SegmentIDs, []string{"segment-5", "segment-6", "segment-7"}) {
		t.Fatalf("expansion crossed question or sample scope: %+v %v", batch, err)
	}
	replayed, err := store.DecideScoringTrial(context.Background(), trialTestTenant, "teacher-1", trial.RunID, trial.ID, decision, policy)
	if err != nil || replayed.ExpansionBatchID != expanded.ExpansionBatchID || len(store.batches) != 2 {
		t.Fatalf("approval replay created a second expansion: %+v %v", replayed, err)
	}
}

func TestTrialVersionChangeInvalidatesAndCancelsSavedScope(t *testing.T) {
	for _, version := range []string{"model", "prompt", "rubric", "reference", "assessment", "answer"} {
		t.Run(version, func(t *testing.T) {
			store, input, policy := trialFixture(t)
			trial := createFixtureTrial(t, store, input, policy)
			value, _ := store.LoadContext(context.Background(), trialTestTenant, "segment-0")
			switch version {
			case "model":
				policy.ModelVersion += "-changed"
			case "prompt":
				policy.PromptVersion += "-changed"
			case "rubric":
				for _, id := range store.trialRunSegments[key(trialTestTenant, "run-1")] {
					changed, _ := store.LoadContext(context.Background(), trialTestTenant, id)
					changed.Rubric.Version += "-changed"
					store.AddContext(trialTestTenant, id, changed)
				}
				value.Rubric.Version += "-changed"
			case "reference":
				for _, id := range store.trialRunSegments[key(trialTestTenant, "run-1")] {
					changed, _ := store.LoadContext(context.Background(), trialTestTenant, id)
					changed.ReferenceContext = &GradingReferenceContext{SnapshotHash: "changed"}
					store.AddContext(trialTestTenant, id, changed)
				}
				value.ReferenceContext = &GradingReferenceContext{SnapshotHash: "changed"}
			case "assessment":
				for _, id := range store.trialRunSegments[key(trialTestTenant, "run-1")] {
					changed, _ := store.LoadContext(context.Background(), trialTestTenant, id)
					changed.AssessmentSnapshot.ContentHash = "changed"
					store.AddContext(trialTestTenant, id, changed)
				}
				value.AssessmentSnapshot.ContentHash = "changed"
			case "answer":
				value.AnswerVersion = "changed-answer"
			}
			store.AddContext(trialTestTenant, "segment-0", value)
			items, err := store.ListScoringTrials(context.Background(), trialTestTenant, trial.RunID, policy)
			if err != nil || items[0].Status != "invalidated" {
				t.Fatalf("changed %s did not invalidate trial: %+v %v", version, items, err)
			}
			batch, _ := store.GetBatch(context.Background(), trialTestTenant, trial.SampleBatchID)
			if batch.Status != "cancelled" {
				t.Fatal("invalidated sample batch is still enqueueable")
			}
			if err := store.AuthorizeTrialBatch(context.Background(), trialTestTenant, trial.SampleBatchID, policy); !errors.Is(err, ErrTrialConflict) {
				t.Fatalf("invalidated batch was authorized: %v", err)
			}
		})
	}
}

func TestTrialStopRejectsLateWorkerAndLateRunCreation(t *testing.T) {
	store, input, policy := trialFixture(t)
	trial := createFixtureTrial(t, store, input, policy)
	value, _ := store.LoadContext(context.Background(), trialTestTenant, "segment-0")
	run, err := store.GetOrCreateRun(context.Background(), trialTestTenant, "teacher-1", runInputFor(value, trial.SampleBatchID, policy, "late-worker"))
	if err != nil {
		t.Fatal(err)
	}
	runtime := workerruntime.NewMemoryStore()
	if _, err := runtime.CreateTask(context.Background(), trialTestTenant, "teacher-1", workerruntime.CreateTaskInput{TaskType: "ai_grade", QueueName: "subjective-grading", SourceType: "subjective_grading_run", SourceID: run.ID, Payload: map[string]any{}, PayloadSchemaVersion: "subjective-grade-v1", IdempotencyKey: run.RequestID, MaxAttempts: 2}); err != nil {
		t.Fatal(err)
	}
	tasks, err := runtime.Claim(context.Background(), trialTestTenant, workerruntime.ClaimInput{QueueName: "subjective-grading", WorkerService: "worker", WorkerInstanceID: "one", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("claim sample: %+v %v", tasks, err)
	}
	task := tasks[0]
	_, err = store.DecideScoringTrial(context.Background(), trialTestTenant, "teacher-1", trial.RunID, trial.ID, TrialDecisionInput{Decision: "stop", Reason: "样本建议待调整", ExpectedRevision: trial.Revision}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOrCreateRun(context.Background(), trialTestTenant, "teacher-1", runInputFor(value, trial.SampleBatchID, policy, "late-enqueue")); !errors.Is(err, ErrBatchCancelled) {
		t.Fatalf("late enqueue created an active run: %v", err)
	}
	if _, err := NewWorkerExecutionService(store, runtime, nil, allowWorkerEligibility).Prepare(context.Background(), WorkerPrepareInput{TenantID: trialTestTenant, RunID: run.ID, TaskID: task.ID, LeaseToken: task.LeaseToken}); err == nil {
		t.Fatal("stopped sample still obtained model execution permission")
	}
	output := validServiceOutput()
	output.RequestID, output.ModelVersion, output.PromptVersion, output.RubricVersion = run.RequestID, run.ModelVersion, run.PromptVersion, run.RubricVersion
	prepared := WorkerExecutionContext{Run: run, Task: task, Context: value, Policy: policy}
	if _, err := NewWorkerCompletionService(store, runtime).Complete(context.Background(), trialTestTenant, "teacher-1", prepared, WorkerResultInput{TaskID: task.ID, LeaseToken: task.LeaseToken, ResultSchemaVersion: "subjective-grade-v1", Output: output}, output); err == nil {
		t.Fatal("late worker result revived a stopped sample")
	}
	if _, err := store.GetGradeByAdapterRequestID(context.Background(), trialTestTenant, run.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late result persisted a grade: %v", err)
	}
}

func TestTrialRestartCannotRelabelExposedAnswersAsIndependent(t *testing.T) {
	for _, exposure := range []string{"never_enqueued", "run_created", "grade_returned", "too_few_fresh_samples"} {
		t.Run(exposure, func(t *testing.T) {
			store, input, policy := trialFixture(t)
			trial := createFixtureTrial(t, store, input, policy)
			value, _ := store.LoadContext(context.Background(), trialTestTenant, "segment-0")
			switch exposure {
			case "run_created":
				if _, err := store.GetOrCreateRun(context.Background(), trialTestTenant, "teacher-1", runInputFor(value, trial.SampleBatchID, policy, "started")); err != nil {
					t.Fatal(err)
				}
			case "grade_returned":
				if _, err := store.CreateGrade(context.Background(), trialTestTenant, "teacher-1", Grade{AnswerSegmentID: "segment-0", AnswerVersion: value.AnswerVersion, QuestionID: trial.QuestionID, AdapterRequestID: "prior-ai", Status: RunSucceeded}); err != nil {
					t.Fatal(err)
				}
			case "too_few_fresh_samples":
				for _, sample := range trial.Samples {
					current, _ := store.LoadContext(context.Background(), trialTestTenant, sample.SegmentID)
					if _, err := store.GetOrCreateRun(context.Background(), trialTestTenant, "teacher-1", runInputFor(current, trial.SampleBatchID, policy, "started-"+sample.SegmentID)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := store.DecideScoringTrial(context.Background(), trialTestTenant, "teacher-1", trial.RunID, trial.ID, TrialDecisionInput{Decision: "stop", Reason: "重新组织独立样本", ExpectedRevision: trial.Revision}, policy); err != nil {
				t.Fatal(err)
			}
			input.CommandID = "new-command"
			_, err := store.CreateScoringTrial(context.Background(), trialTestTenant, "teacher-1", trial.RunID, input, policy)
			if exposure == "never_enqueued" && err != nil {
				t.Fatalf("never started cancelled samples should be reusable: %v", err)
			}
			if exposure != "never_enqueued" && !errors.Is(err, ErrTrialIndependentSamplesRequired) {
				t.Fatalf("exposed answer was relabelled as an independent baseline: %v", err)
			}
		})
	}
}

func TestTrialOversizedExpansionLeavesDecisionAndBatchUnchanged(t *testing.T) {
	store, input, policy := trialFixture(t)
	ids := append([]string(nil), store.trialRunSegments[key(trialTestTenant, "run-1")]...)
	for index := 8; index < 1006; index++ {
		id := fmt.Sprintf("segment-%d", index)
		value := ContextForTest("short_answer", 10, "独立合成答案", nil)
		value.AssessmentSnapshot.ScoringPolicySnapshot.Mode = assessment.ScoringAIAssist
		store.AddContext(trialTestTenant, id, value)
		ids = append(ids, id)
	}
	store.BindScoringTrialRun(trialTestTenant, "run-1", ids)
	trial := finishTrialSamples(t, store, createFixtureTrial(t, store, input, policy), policy)
	if len(trial.RemainingSegmentIDs) != 1001 {
		t.Fatalf("expected an oversized scope, got %d", len(trial.RemainingSegmentIDs))
	}
	decision := TrialDecisionInput{Decision: "expand", Reason: "已经核对样本", ExpectedRevision: trial.Revision}
	_, err := store.DecideScoringTrial(context.Background(), trialTestTenant, "teacher-1", trial.RunID, trial.ID, decision, policy)
	if !errors.Is(err, ErrTrialExpansionLimitExceeded) {
		t.Fatalf("oversized expansion did not report its limit: %v", err)
	}
	items, err := store.ListScoringTrials(context.Background(), trialTestTenant, trial.RunID, policy)
	if err != nil || len(store.batches) != 1 || items[0].Status != "reviewing" || items[0].Revision != trial.Revision || items[0].ExpansionBatchID != "" || items[0].DecidedBy != "" {
		t.Fatalf("rejected expansion changed the decision or created a partial batch: %+v %v", items, err)
	}
	body, _ := json.Marshal(decision)
	req := httptest.NewRequest(http.MethodPost, "/decision", bytes.NewReader(body))
	req.SetPathValue("runId", trial.RunID)
	req.SetPathValue("trialId", trial.ID)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "teacher-1", TenantID: trialTestTenant}))
	rec := httptest.NewRecorder()
	NewHandler(store, NewMockLLMAdapter(), nil).DecideScoringTrial(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "scoring_trial_expansion_limit_exceeded") {
		t.Fatalf("expansion limit was not clear at the HTTP boundary: %d %s", rec.Code, rec.Body.String())
	}
}
