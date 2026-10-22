package review

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
)

// 同一 HTTP 入口分别触发接管、错误和普通回退；投影失败不能改变已接管的成功响应。
func TestBatch38SubmitGradeKeepsSeedFailureAndFallbackOrder(t *testing.T) {
	input := SubmitGradeInput{
		Score: 4, ExpectedRevision: 7, Comments: "teacher note",
		RubricSelections: []RubricSelection{{PointID: "p1", Score: 2}, {PointID: "p1", Score: 4}},
	}
	cases := []reviewSeedSubmitCase{
		{name: "seed accepted despite refresh failure", handled: true, wantStatus: http.StatusCreated, wantCalls: []string{"context", "seed_submit", "refresh"}},
		{name: "seed error precedes unhandled fallback", seedErr: seedquality.ErrConflict, contextErr: seedquality.ErrContextUnavailable, wantStatus: http.StatusConflict, wantCalls: []string{"context", "seed_submit"}},
		{name: "unhandled seed uses ordinary store", wantStatus: http.StatusCreated, wantCalls: []string{"context", "seed_submit", "ordinary_submit"}},
		{name: "context failure does not mask accepted submit", handled: true, contextErr: seedquality.ErrContextUnavailable, wantStatus: http.StatusCreated, wantCalls: []string{"context", "seed_submit"}},
		{name: "no hook uses ordinary store", noHook: true, wantStatus: http.StatusCreated, wantCalls: []string{"ordinary_submit"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assertReviewSeedSubmitFallbackOrder(t, test, input)
		})
	}
}

type batch38SubmitStore struct {
	*MemoryStore
	t     *testing.T
	input SubmitGradeInput
	calls *[]string
}

func (s *batch38SubmitStore) SubmitGrade(ctx context.Context, tenant, task, reviewer string, input SubmitGradeInput) (SubmitResult, error) {
	*s.calls = append(*s.calls, "ordinary_submit")
	if tenant != tenantID || task != "task-38" || reviewer != "reviewer-38" || !reflect.DeepEqual(input, s.input) {
		s.t.Fatalf("ordinary submit identity/input changed: %s %s %s %#v", tenant, task, reviewer, input)
	}
	batch38CheckCommandInput(s.t, ctx, s.input)
	return SubmitResult{Task: ReviewTask{ID: task, Status: "submitted"}, Grade: HumanGrade{ID: "grade-38", Score: input.Score}}, nil
}

type batch38SeedHook struct {
	seedquality.ReviewHook
	t          *testing.T
	input      SubmitGradeInput
	calls      *[]string
	handled    bool
	seedErr    error
	contextErr error
}

func (h *batch38SeedHook) GetGraderTaskContext(ctx context.Context, tenant, task, reviewer string) (seedquality.GraderTaskContext, bool, error) {
	*h.calls = append(*h.calls, "context")
	if tenant != tenantID || task != "task-38" || reviewer != "reviewer-38" {
		h.t.Fatalf("seed context scope changed: %s %s %s", tenant, task, reviewer)
	}
	batch38CheckCommandInput(h.t, ctx, h.input)
	return seedquality.GraderTaskContext{Task: seedquality.GraderTask{ExamID: "seed-exam-38", QuestionID: "seed-question-38"}}, true, h.contextErr
}

func (h *batch38SeedHook) TrySubmit(ctx context.Context, tenant, task, reviewer string, input seedquality.SubmitInput) (seedquality.SubmitReceipt, bool, error) {
	*h.calls = append(*h.calls, "seed_submit")
	if tenant != tenantID || task != "task-38" || reviewer != "reviewer-38" || input.Score != 4 || input.ExpectedRevision != 7 || !reflect.DeepEqual(input.RubricSelections, map[string]any{"p1": float64(4)}) {
		h.t.Fatalf("seed submit identity/input changed: %s %s %s %#v", tenant, task, reviewer, input)
	}
	batch38CheckCommandInput(h.t, ctx, h.input)
	return seedquality.SubmitReceipt{TaskID: "seed-task-38", Status: "submitted", Revision: 8}, h.handled, h.seedErr
}

type batch38SeedRefresher struct {
	t     *testing.T
	calls *[]string
}

func (s *batch38SeedRefresher) RefreshSeedObservation(ctx context.Context, tenant, exam, question, reviewer string) error {
	*s.calls = append(*s.calls, "refresh")
	if tenant != tenantID || exam != "seed-exam-38" || question != "seed-question-38" || reviewer != "reviewer-38" || commandreceipt.ID(ctx) != "command-38" {
		s.t.Fatalf("seed refresh scope changed: %s %s %s %s", tenant, exam, question, reviewer)
	}
	return errors.New("projection unavailable")
}

// 用公开回执 API 的指纹核对 context 原输入，避免测试依赖私有 context key。
func batch38CheckCommandInput(t *testing.T, ctx context.Context, original SubmitGradeInput) {
	t.Helper()
	if commandreceipt.ID(ctx) != "command-38" {
		t.Fatalf("command ID lost: %q", commandreceipt.ID(ctx))
	}
	changed := original
	changed.Score = 99
	var receipts commandreceipt.Memory
	if err := receipts.Save(ctx, tenantID, "reviewer-38", "review.submit", "task-38", changed, true); err != nil {
		t.Fatal(err)
	}
	var result bool
	loaded, err := receipts.Load(commandreceipt.WithID(context.Background(), "command-38"), tenantID, "reviewer-38", "review.submit", "task-38", original, &result)
	if err != nil || !loaded || !result {
		t.Fatalf("original command input lost: loaded=%v result=%v err=%v", loaded, result, err)
	}
}

func TestBatch38TaskContextKeepsHistoryShapeAndSourceMaterial(t *testing.T) {
	mixed := map[string]any{
		"suggested_score": 4.0, "confidence": 0.83,
		"suggestion_history": []any{
			map[string]any{"id": "first", "score": 1.0, "evidence": []any{map[string]any{"max_score": 2.0, "label": "method"}}},
			"invalid", nil,
			map[string]any{"id": "second", "max_score": 3.0},
		},
	}
	cases := []reviewSecondOpinionCase{
		{name: "R3 mixed history", suggestion: mixed, risk: assessment.RiskR3, mode: assessment.ScoringHumanPrimary, historyIDs: []string{"first", "second"}},
		{name: "R3 invalid history stays empty array", suggestion: map[string]any{"confidence": 0.83, "suggestion_history": "invalid"}, risk: assessment.RiskR3, mode: assessment.ScoringHumanPrimary},
		{name: "R2 retains score material", suggestion: mixed, risk: assessment.RiskR2, mode: assessment.ScoringAIAssist, historyIDs: []string{"first", "second"}},
		{name: "empty suggestion stays absent", risk: assessment.RiskR3, mode: assessment.ScoringHumanPrimary},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assertReviewTaskContextSourceAndOpinion(t, test)
		})
	}
}

// 固定优先级、创建时间和 ID 的平局，排除 map 遍历顺序对候选选择的影响。
func TestBatch38ClaimNextTaskKeepsAssignedPoolAndAtomicClaimFacts(t *testing.T) {
	store := NewMemoryStore()
	created := time.Date(2026, time.October, 8, 0, 0, 0, 0, time.UTC)
	due := created.Add(time.Hour)
	tasks := []ReviewTask{
		{ID: "assigned-b", TenantID: tenantID, AssignedTo: "reviewer-38", Status: "returned", Priority: 80, CreatedAt: created, Revision: 8, DueAt: &due},
		{ID: "assigned-a", TenantID: tenantID, AssignedTo: "reviewer-38", Status: "returned", Priority: 80, CreatedAt: created, Revision: 8, DueAt: &due},
		{ID: "assigned-newer", TenantID: tenantID, AssignedTo: "reviewer-38", Status: "assigned", Priority: 80, CreatedAt: created.Add(time.Minute)},
		{ID: "assigned-lower", TenantID: tenantID, AssignedTo: "reviewer-38", Status: "assigned", Priority: 79, CreatedAt: created.Add(-time.Minute)},
		{ID: "unassigned", TenantID: tenantID, Status: "pending", Priority: 100, CreatedAt: created},
		{ID: "other-reviewer", TenantID: tenantID, AssignedTo: "reviewer-other", Status: "assigned", Priority: 100, CreatedAt: created},
		{ID: "other-tenant", TenantID: "other-tenant", AssignedTo: "reviewer-38", Status: "assigned", Priority: 100, CreatedAt: created},
	}
	for _, task := range tasks {
		store.tasks[key(task.TenantID, task.ID)] = task
	}
	before := time.Now().UTC()
	claimed, err := store.ClaimNextTask(context.Background(), tenantID, "reviewer-38", NextTaskInput{}, ClaimTaskOptions{AllowUnassigned: true})
	if err != nil || claimed.ID != "assigned-a" || claimed.Status != "returned" || claimed.Revision != 9 || claimed.AssignedTo != "reviewer-38" {
		t.Fatalf("claim facts changed: %#v err=%v", claimed, err)
	}
	claim := store.claims[key(tenantID, claimed.ID)]
	if claim.OwnerID != "reviewer-38" || !claim.ClaimedAt.Equal(claimed.UpdatedAt) || claim.ClaimedAt.Before(before) || claim.ClaimedAt.After(time.Now().UTC()) || claim.ExpiresAt.Sub(claim.ClaimedAt) != 30*time.Minute {
		t.Fatalf("lease facts changed: %#v task=%#v", claim, claimed)
	}
	*claimed.DueAt = due.Add(time.Hour)
	persisted, err := store.GetTask(context.Background(), tenantID, claimed.ID)
	if err != nil || persisted.DueAt == nil || !persisted.DueAt.Equal(due) || persisted.Revision != 9 {
		t.Fatalf("returned task aliases persisted facts: %#v err=%v", persisted, err)
	}
	if store.tasks[key(tenantID, "unassigned")].Status != "pending" || store.tasks[key(tenantID, "assigned-b")].Revision != 8 || len(store.claims) != 1 {
		t.Fatal("claim changed another candidate")
	}
}

type reviewSeedSubmitCase struct {
	name       string
	noHook     bool
	handled    bool
	seedErr    error
	contextErr error
	wantStatus int
	wantCalls  []string
}

func assertReviewSeedSubmitFallbackOrder(t *testing.T, test reviewSeedSubmitCase, input SubmitGradeInput) {
	t.Helper()
	calls := []string{}
	store := &batch38SubmitStore{MemoryStore: NewMemoryStore(), t: t, input: input, calls: &calls}
	handler := NewHandler(store, nil, nil, nil)
	if !test.noHook {
		handler.WithSeedHook(&batch38SeedHook{t: t, input: input, calls: &calls, handled: test.handled, seedErr: test.seedErr, contextErr: test.contextErr})
	}
	handler.WithSeedObservationRefresher(&batch38SeedRefresher{t: t, calls: &calls})
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/review-tasks/task-38/grade", strings.NewReader(string(raw)))
	request.SetPathValue("id", "task-38")
	request.Header.Set("Idempotency-Key", "command-38")
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "reviewer-38", TenantID: tenantID, Roles: []string{"grader"}}))
	response := httptest.NewRecorder()
	handler.SubmitGrade(response, request)
	if response.Code != test.wantStatus || !reflect.DeepEqual(calls, test.wantCalls) {
		t.Fatalf("status=%d calls=%v body=%s", response.Code, calls, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if test.seedErr != nil {
		if _, hasGrade := body["human_grade"]; hasGrade {
			t.Fatalf("seed failure returned an ordinary grade: %v", body)
		}
		return
	}
	if test.handled {
		want := map[string]any{"task": map[string]any{"id": "seed-task-38", "status": "submitted", "revision": float64(8)}}
		if !reflect.DeepEqual(body, want) {
			t.Fatalf("seed response=%v want=%v", body, want)
		}
	} else if _, hasGrade := body["human_grade"]; !hasGrade {
		t.Fatalf("ordinary response lost human_grade: %v", body)
	}
}

type reviewSecondOpinionCase struct {
	name       string
	suggestion map[string]any
	risk       assessment.RiskTier
	mode       assessment.ScoringMode
	historyIDs []string
}

func assertReviewTaskContextSourceAndOpinion(t *testing.T, test reviewSecondOpinionCase) {
	t.Helper()
	store := NewMemoryStore()
	value := reviewContext()
	value.AssessmentSnapshot = taskContextSnapshot(test.risk, test.mode)
	value.AISuggestion = test.suggestion
	bindTaskSuggestionFixture(&value, "segment-38")
	score, maximum := 4.0, 5.0
	value.AutomationResult = &AutomationResult{Decision: "review", Score: &score, MaxScore: &maximum}
	store.AddContext(tenantID, "segment-38", value)
	task, err := store.CreateTask(context.Background(), tenantID, "manager-38", CreateTaskInput{AnswerSegmentID: "segment-38", Source: "manual_sample", AssignedTo: "reviewer-38"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(store.contexts[key(tenantID, "segment-38")].AISuggestion)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.GetTaskContext(context.Background(), tenantID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(store.contexts[key(tenantID, "segment-38")].AISuggestion)
	if err != nil || string(before) != string(after) || value.AutomationResult.Score == nil || value.AutomationResult.MaxScore == nil {
		t.Fatalf("context read changed source material: before=%s after=%s err=%v", before, after, err)
	}
	prefillAllowed := test.risk == assessment.RiskR2
	if (result.AutomationResult.Score != nil) != prefillAllowed || (result.AutomationResult.MaxScore != nil) != prefillAllowed {
		t.Fatalf("automation score projection changed: %#v", result.AutomationResult)
	}
	if len(test.suggestion) == 0 {
		if result.AISecondOpinion != nil {
			t.Fatalf("empty suggestion produced an opinion: %#v", result.AISecondOpinion)
		}
		return
	}
	assertReviewSecondOpinionHistory(t, test, result.AISecondOpinion, prefillAllowed)
}

// 风险等级控制分数可见性，历史意见仍保留顺序和空数组形状；源建议已在调用前核对未被改写。
func assertReviewSecondOpinionHistory(t *testing.T, test reviewSecondOpinionCase, opinion *AISecondOpinion, prefillAllowed bool) {
	t.Helper()
	if opinion == nil || !opinion.Available || opinion.Presentation != "explicit_second_opinion" || opinion.ScorePrefillAllowed != prefillAllowed || opinion.Metadata["confidence"] != 0.83 {
		t.Fatalf("opinion facts changed: %#v", opinion)
	}
	if _, exists := opinion.Metadata["suggestion_history"]; exists {
		t.Fatal("history remains inside metadata")
	}
	historyHasScore := assertReviewOpinionHistoryShapeAndOrder(t, opinion.History, test.historyIDs)
	if containsScoreKey(opinion.Metadata) != prefillAllowed || historyHasScore != (prefillAllowed && len(test.historyIDs) > 0) {
		t.Fatalf("score visibility changed: metadata=%#v history=%#v", opinion.Metadata, opinion.History)
	}
	if len(test.historyIDs) == 0 {
		raw, err := json.Marshal(opinion.History)
		if err != nil || string(raw) != "[]" {
			t.Fatalf("empty history JSON=%s err=%v", raw, err)
		}
	}
}

func assertReviewOpinionHistoryShapeAndOrder(t *testing.T, history []map[string]any, historyIDs []string) bool {
	t.Helper()
	if history == nil || len(history) != len(historyIDs) {
		t.Fatalf("history shape changed: %#v", history)
	}
	historyHasScore := false
	for index, id := range historyIDs {
		if history[index]["id"] != id {
			t.Fatalf("history order changed: %#v", history)
		}
		historyHasScore = historyHasScore || containsScoreKey(history[index])
	}
	return historyHasScore
}
