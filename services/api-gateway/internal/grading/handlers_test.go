package grading_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/server"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

const tenantID = "00000000-0000-0000-0000-000000000002"
const userID = "00000000-0000-0000-0000-000000000801"

func TestRecordAnswerRuleGradeListAndAudit(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"grading:manage"})
	gradingStore := grading.NewMemoryStore()
	gradingStore.AddContext(tenantID, "segment-1", questionWithAnswerKey("single_choice", "B", nil, nil))
	router := testRouter(authStore, gradingStore)
	token := login(t, router)

	req := authedRequest(http.MethodPut, "/api/v1/answer-segments/segment-1/answer", bytes.NewBufferString(`{"answer_text":"B","source":"manual_entry","confidence":0.99}`), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"answer_text":"B"`) {
		t.Fatalf("record answer expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	req = authedRequest(http.MethodPost, "/api/v1/answer-segments/segment-1/rule-grade", nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"suggested_score":5`) || !strings.Contains(rec.Body.String(), `"grader_type":"rule_based_objective"`) || !strings.Contains(rec.Body.String(), `"mock":false`) {
		t.Fatalf("rule grade expected created full score, got %d %s", rec.Code, rec.Body.String())
	}

	req = authedRequest(http.MethodGet, "/api/v1/answer-segments/segment-1/ai-grades", nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Count(rec.Body.String(), `"suggested_score"`) != 1 {
		t.Fatalf("list grades expected one grade, got %d %s", rec.Code, rec.Body.String())
	}

	assertAuditAction(t, authStore, "grading.answer_recorded")
	assertAuditAction(t, authStore, "grading.rule_grade_created")
}

func TestRuleGradeMissingAnswerAndAnswerKey(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"grading:manage"})
	gradingStore := grading.NewMemoryStore()
	gradingStore.AddContext(tenantID, "segment-no-answer", questionWithAnswerKey("single_choice", "A", nil, nil))
	gradingStore.AddContext(tenantID, "segment-no-key", paper.Question{ID: "question-no-key", TenantID: tenantID, ExamID: "exam-1", QuestionNo: "Q2", QuestionType: "single_choice", Score: 5})
	router := testRouter(authStore, gradingStore)
	token := login(t, router)

	req := authedRequest(http.MethodPost, "/api/v1/answer-segments/segment-no-answer/rule-grade", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "answer_segment_answer_missing") {
		t.Fatalf("missing answer expected conflict, got %d %s", rec.Code, rec.Body.String())
	}

	req = authedRequest(http.MethodPut, "/api/v1/answer-segments/segment-no-key/answer", bytes.NewBufferString(`{"answer_text":"A","source":"manual_entry"}`), token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("record answer expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	req = authedRequest(http.MethodPost, "/api/v1/answer-segments/segment-no-key/rule-grade", nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "question_answer_key_missing") {
		t.Fatalf("missing answer key expected conflict, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestGradingPermissionDeniedAndUnauthenticated(t *testing.T) {
	gradingStore := grading.NewMemoryStore()
	gradingStore.AddContext(tenantID, "segment-1", questionWithAnswerKey("single_choice", "A", nil, nil))
	router := testRouter(authStoreWithPermissions(t, []string{"system:read"}), gradingStore)
	token := login(t, router)

	req := authedRequest(http.MethodPut, "/api/v1/answer-segments/segment-1/answer", bytes.NewBufferString(`{"answer_text":"A"}`), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/answer-segments/segment-1/rule-grade", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestScoringRecoveryEndpointsCoordinateWorkerRuntime(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"grading:manage"})
	runtimeStore := workerruntime.NewMemoryStore()
	store := newScoringRecoveryTestStore()

	cancelTask, err := runtimeStore.CreateTask(context.Background(), tenantID, userID, recoveryRuntimeTask("cancel-task"))
	if err != nil {
		t.Fatalf("create cancel task: %v", err)
	}
	store.taskIDs = []string{cancelTask.ID}
	router := testRouter(authStore, store, runtimeStore)
	token := login(t, router)

	req := authedRequest(http.MethodGet, "/api/v1/scoring-runs/run-1", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"answer_segment_id":"segment-1"`) ||
		!strings.Contains(rec.Body.String(), `"anonymous_code":"SIM-001"`) ||
		!strings.Contains(rec.Body.String(), `"recognized_answer":"A"`) ||
		!strings.Contains(rec.Body.String(), `"standard_answer":"A"`) ||
		!strings.Contains(rec.Body.String(), `"grade_source":"rule_confirmed"`) ||
		strings.Contains(rec.Body.String(), "student_id") {
		t.Fatalf("scoring run detail should be anonymous and available, got %d %s", rec.Code, rec.Body.String())
	}
	req = authedRequest(http.MethodGet, "/api/v1/exams/exam-1/automation-results", nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"recognized_answer":"A"`) {
		t.Fatalf("exam automation results expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	req = authedRequest(http.MethodPost, "/api/v1/scoring-runs/run-1/cancel", nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancel scoring run expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	cancelled, err := runtimeStore.Get(context.Background(), tenantID, cancelTask.ID)
	if err != nil || cancelled.Status != workerruntime.StatusCancelled {
		t.Fatalf("cancellation must reach worker runtime: task=%#v err=%v", cancelled, err)
	}

	retryTask, err := runtimeStore.CreateTask(context.Background(), tenantID, userID, recoveryRuntimeTask("retry-task"))
	if err != nil {
		t.Fatalf("create retry task: %v", err)
	}
	claimed, err := runtimeStore.Claim(context.Background(), tenantID, workerruntime.ClaimInput{QueueName: "image-quality", WorkerService: "test-worker", WorkerInstanceID: "worker-1", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 || claimed[0].ID != retryTask.ID {
		t.Fatalf("claim retry task: %#v err=%v", claimed, err)
	}
	if _, err = runtimeStore.Fail(context.Background(), tenantID, retryTask.ID, workerruntime.FailInput{LeaseToken: claimed[0].LeaseToken, ErrorCode: "omr_failed", ErrorDetail: map[string]any{}, DurationMS: 1}); err != nil {
		t.Fatalf("fail retry task: %v", err)
	}
	store.run = grading.ScoringRun{ID: "run-2", TenantID: tenantID, ExamID: "exam-1", Status: "failed", TotalCount: 1, FailedCount: 1}
	store.failed = []grading.FailedOMRTask{{OMRRunID: "omr-1", RuntimeTaskID: retryTask.ID}}
	req = authedRequest(http.MethodPost, "/api/v1/scoring-runs/run-2/retry-failed", nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"requeued":1`) {
		t.Fatalf("retry failed should requeue worker task, got %d %s", rec.Code, rec.Body.String())
	}
	requeued, err := runtimeStore.Get(context.Background(), tenantID, retryTask.ID)
	if err != nil || requeued.Status != workerruntime.StatusQueued {
		t.Fatalf("retry must return worker task to queue: task=%#v err=%v", requeued, err)
	}

	store.run = grading.ScoringRun{ID: "run-3", TenantID: tenantID, ExamID: "exam-1", Status: "processing", TotalCount: 1}
	req = authedRequest(http.MethodPost, "/api/v1/answer-segments/segment-1/reprocess-score", bytes.NewBufferString(`{"idempotency_key":"operator-fix-1"}`), token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || store.reprocessedSegmentID != "segment-1" || store.reprocessInput.IdempotencyKey != "operator-fix-1" {
		t.Fatalf("segment-only reprocess should preserve its target and idempotency key: code=%d segment=%q input=%#v body=%s", rec.Code, store.reprocessedSegmentID, store.reprocessInput, rec.Body.String())
	}

	assertAuditAction(t, authStore, "grading.scoring_run_cancelled")
	assertAuditAction(t, authStore, "grading.scoring_run_retry_failed")
	assertAuditAction(t, authStore, "grading.segment_score_reprocessed")
}

func TestScoringReadinessEndpointAndBlockedStartReturnActionableChecks(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"grading:manage"})
	store := newScoringRecoveryTestStore()
	store.readiness = grading.ScoringReadiness{
		Ready:          false,
		ExamStatus:     "collecting",
		TotalQuestions: 19,
		TotalSegments:  76,
		ReadySegments:  75,
		Checks: []grading.ScoringReadinessCheck{{
			Code: "answer_segments_processed", Label: "题块处理", Passed: false, Severity: "blocker",
			Message: "仍有 1 个题块未处理完成或裁剪影像不可用。", Count: 1,
		}},
	}
	store.startErr = &grading.ScoringReadinessError{Readiness: store.readiness}
	router := testRouter(authStore, store)
	token := login(t, router)

	req := authedRequest(http.MethodGet, "/api/v1/exams/exam-1/scoring-readiness", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"ready":false`) ||
		!strings.Contains(rec.Body.String(), `"code":"answer_segments_processed"`) ||
		!strings.Contains(rec.Body.String(), `"count":1`) {
		t.Fatalf("readiness should expose actionable checks, got %d %s", rec.Code, rec.Body.String())
	}

	req = authedRequest(http.MethodPost, "/api/v1/exams/exam-1/scoring-runs", bytes.NewBufferString(`{"idempotency_key":"blocked-start"}`), token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), `"code":"scoring_not_ready"`) ||
		!strings.Contains(rec.Body.String(), `"scoring_readiness"`) ||
		strings.Contains(rec.Body.String(), `"code":"grading_operation_failed"`) {
		t.Fatalf("blocked start should return readiness instead of generic 500, got %d %s", rec.Code, rec.Body.String())
	}
}

func testRouter(authStore *auth.MemoryStore, gradingStore grading.Store, runtimeStores ...workerruntime.Store) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
	}
	return server.NewMemoryRouter(cfg, logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), func(stores *server.ApplicationStores) {
		stores.Identity.Auth = authStore
		stores.Grading.Grading = gradingStore
		if len(runtimeStores) > 0 {
			stores.Capture.WorkerRuntime = runtimeStores[0]
		}
	})
}

// 此替身验证 HTTP 处理器与 runtime 的协作；状态直接在内存改变，不覆盖生产数据库事务和并发。
type scoringRecoveryTestStore struct {
	*grading.MemoryStore
	run                  grading.ScoringRun
	readiness            grading.ScoringReadiness
	startErr             error
	detail               grading.ScoringRunDetail
	taskIDs              []string
	failed               []grading.FailedOMRTask
	reprocessedSegmentID string
	reprocessInput       grading.StartScoringRunInput
}

func newScoringRecoveryTestStore() *scoringRecoveryTestStore {
	run := grading.ScoringRun{ID: "run-1", TenantID: tenantID, ExamID: "exam-1", Status: "processing", TotalCount: 1, QueuedCount: 1}
	confidence, score, maxScore := 0.99, 5.0, 5.0
	return &scoringRecoveryTestStore{
		MemoryStore: grading.NewMemoryStore(),
		run:         run,
		readiness: grading.ScoringReadiness{
			Ready: true, ExamStatus: "collecting", TotalQuestions: 1, TotalSegments: 1, ReadySegments: 1,
			AutomaticCandidates: 1, Checks: []grading.ScoringReadinessCheck{},
		},
		detail: grading.ScoringRunDetail{Run: run, Items: []grading.ScoringRunItem{{
			AnswerSegmentID: "segment-1", QuestionID: "question-1", QuestionNo: "Q1", QuestionType: "single_choice", AnonymousCode: "SIM-001", State: "confirmed",
			RecognitionSource: "omr", RecognizedAnswer: "A", RecognitionDecision: "confirmed", RecognitionConfidence: &confidence,
			StandardAnswer: "A", RuleType: "single_choice", Score: &score, MaxScore: &maxScore, GradeSource: "rule_confirmed",
		}}},
	}
}

func (s *scoringRecoveryTestStore) GetScoringReadiness(_ context.Context, _ string, _ string) (grading.ScoringReadiness, error) {
	return s.readiness, nil
}

func (s *scoringRecoveryTestStore) RecoverScoringCommand(_ context.Context, _, _, _, commandID string) (grading.ScoringCommandRecovery, error) {
	return grading.ScoringCommandRecovery{CommandID: commandID, Status: "succeeded", Run: &s.run}, nil
}

func (s *scoringRecoveryTestStore) StartScoringRun(_ context.Context, _ string, _ string, _ string, _ grading.StartScoringRunInput) (grading.ScoringRun, error) {
	if s.startErr != nil {
		return grading.ScoringRun{}, s.startErr
	}
	return s.run, nil
}

func (s *scoringRecoveryTestStore) GetScoringSummary(_ context.Context, _ string, _ string) (grading.ScoringSummary, error) {
	return grading.ScoringSummary{Run: &s.run, Questions: []grading.ScoringQuestionSummary{}}, nil
}

func (s *scoringRecoveryTestStore) GetOMRRun(_ context.Context, _ string, _ string) (grading.OMRRun, error) {
	return grading.OMRRun{}, grading.ErrNotFound
}

func (s *scoringRecoveryTestStore) ApplyOMRResult(_ context.Context, _ string, _ string, _ string, _ grading.OMRResultInput, _ *grading.Engine) (grading.OMRRun, *grading.QuestionGrade, error) {
	return grading.OMRRun{}, nil, grading.ErrNotFound
}

func (s *scoringRecoveryTestStore) ApplyOMRFailure(_ context.Context, _ string, _ string, _ string, _ map[string]any, _ bool) (grading.OMRRun, error) {
	return grading.OMRRun{}, grading.ErrNotFound
}

func (s *scoringRecoveryTestStore) ConfirmRuleGrade(_ context.Context, _ string, _ string, _ string, _ grading.Grade) (grading.QuestionGrade, error) {
	return grading.QuestionGrade{}, grading.ErrNotFound
}

func (s *scoringRecoveryTestStore) ProcessRuleCandidates(_ context.Context, _ string, _ string, _ string, _ *grading.Engine) error {
	return nil
}

func (s *scoringRecoveryTestStore) GetScoringRunDetail(_ context.Context, _ string, _ string) (grading.ScoringRunDetail, error) {
	s.detail.Run = s.run
	return s.detail, nil
}

func (s *scoringRecoveryTestStore) GetExamAutomationResults(_ context.Context, _ string, _ string) (grading.ExamAutomationResults, error) {
	return grading.ExamAutomationResults{Items: append([]grading.ScoringRunItem{}, s.detail.Items...)}, nil
}

func (s *scoringRecoveryTestStore) BeginScoringRunCancellation(_ context.Context, _ string, _ string) (grading.ScoringRun, []string, error) {
	s.run.Status = "cancelling"
	return s.run, append([]string{}, s.taskIDs...), nil
}

func (s *scoringRecoveryTestStore) FinalizeScoringRunCancellation(_ context.Context, _ string, _ string) (grading.ScoringRun, error) {
	s.run.Status = "cancelled"
	s.run.QueuedCount = 0
	return s.run, nil
}

func (s *scoringRecoveryTestStore) ListFailedOMRTasks(_ context.Context, _ string, _ string) ([]grading.FailedOMRTask, error) {
	return append([]grading.FailedOMRTask{}, s.failed...), nil
}

func (s *scoringRecoveryTestStore) PrepareOMRRetry(_ context.Context, _ string, omrRunID string) (grading.FailedOMRTask, error) {
	for _, item := range s.failed {
		if item.OMRRunID == omrRunID {
			return item, nil
		}
	}
	return grading.FailedOMRTask{}, grading.ErrNotFound
}

func (s *scoringRecoveryTestStore) RestoreOMRRetry(_ context.Context, _ string, _ string) error {
	return nil
}

func (s *scoringRecoveryTestStore) RefreshScoringRun(_ context.Context, _ string, _ string) (grading.ScoringRun, error) {
	if s.run.Status == "failed" {
		s.run.Status = "processing"
		s.run.FailedCount = 0
		s.run.QueuedCount = len(s.failed)
	}
	return s.run, nil
}

func (s *scoringRecoveryTestStore) ReprocessSegmentScore(_ context.Context, _ string, segmentID string, _ string, input grading.StartScoringRunInput) (grading.ScoringRun, error) {
	s.reprocessedSegmentID = segmentID
	s.reprocessInput = input
	return s.run, nil
}

func recoveryRuntimeTask(id string) workerruntime.CreateTaskInput {
	return workerruntime.CreateTaskInput{
		TaskType: "image_quality", QueueName: "image-quality", SourceType: "recovery_test", SourceID: id,
		Payload: map[string]any{}, PayloadSchemaVersion: "recovery-test-v1", IdempotencyKey: "recovery:" + id,
	}
}

func authStoreWithPermissions(t *testing.T, permissions []string) *auth.MemoryStore {
	t.Helper()
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store := auth.NewMemoryStore()
	store.AddUser(auth.UserWithPassword{
		User: auth.User{
			ID:          userID,
			TenantID:    tenantID,
			TenantCode:  "demo",
			Username:    "grading_admin",
			DisplayName: "Grading Admin",
			Status:      "active",
			Roles:       []string{"teacher"},
			Permissions: permissions,
			DataScope:   map[string]any{"scope": "school", "synthetic": true},
		},
		PasswordHash: hash,
	})
	return store
}

func login(t *testing.T, router http.Handler) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"tenant_code": "demo", "username": "grading_admin", "password": "ChangeMe123!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.AccessToken
}

func authedRequest(method string, path string, body *bytes.Buffer, token string) *http.Request {
	var reader io.Reader
	if body != nil {
		reader = body
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func questionWithAnswerKey(kind string, standard any, equiv []any, tolerance any) paper.Question {
	return paper.Question{
		ID:           "question-" + kind,
		TenantID:     tenantID,
		ExamID:       "exam-1",
		QuestionNo:   "Q1",
		QuestionType: kind,
		Score:        5,
		AnswerKey: &paper.AnswerKey{
			ID:                "answer-key-" + kind,
			QuestionID:        "question-" + kind,
			AnswerVersion:     "v1",
			StandardAnswer:    standard,
			EquivalentAnswers: equiv,
			Tolerance:         tolerance,
		},
	}
}

func assertAuditAction(t *testing.T, store *auth.MemoryStore, action string) {
	t.Helper()
	for _, audit := range store.Audits() {
		if audit.Action == action {
			return
		}
	}
	t.Fatalf("missing audit action %s in %#v", action, store.Audits())
}
