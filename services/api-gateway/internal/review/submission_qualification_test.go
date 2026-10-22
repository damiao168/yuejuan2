package review

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
)

type mutableSubmissionQualification struct {
	allowed bool
	calls   int
}

func (g *mutableSubmissionQualification) RequireQualification(context.Context, string, string, string, string) error {
	g.calls++
	if !g.allowed {
		return errors.New("qualification revoked")
	}
	return nil
}

func submissionQualificationFixture(t *testing.T, risk assessment.RiskTier) (*MemoryStore, ReviewTask) {
	t.Helper()
	store := NewMemoryStore()
	current := reviewContext()
	current.AssessmentSnapshot = taskContextSnapshot(risk, assessment.ScoringHumanPrimary)
	store.AddContext(tenantID, "segment-1", current)
	task, err := store.CreateTask(context.Background(), tenantID, "manager-1", CreateTaskInput{
		AnswerSegmentID: "segment-1", Source: "manual_sample", AssignedTo: "reviewer-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, task
}

func submitQualificationRequest(handler *Handler, taskID, commandID, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/review-tasks/"+taskID+"/submit", strings.NewReader(body))
	request.SetPathValue("id", taskID)
	request.Header.Set("Idempotency-Key", commandID)
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "reviewer-1", TenantID: tenantID, Roles: []string{"grader"}, Permissions: []string{"review:work"}}))
	response := httptest.NewRecorder()
	handler.SubmitGrade(response, request)
	return response
}

func TestSubmitQualificationChecksDirectR3AndAllowsR2(t *testing.T) {
	for _, test := range []struct {
		name    string
		risk    assessment.RiskTier
		allowed bool
		status  int
		calls   int
	}{
		{"unqualified direct R3", assessment.RiskR3, false, http.StatusForbidden, 1},
		{"qualified R3", assessment.RiskR3, true, http.StatusCreated, 1},
		{"R2 does not require R3 qualification", assessment.RiskR2, false, http.StatusCreated, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, task := submissionQualificationFixture(t, test.risk)
			gate := &mutableSubmissionQualification{allowed: test.allowed}
			handler := NewHandler(store, nil, nil, nil).WithQualificationGate(gate)
			body := fmt.Sprintf(`{"expected_revision":%d,"score":4,"rubric_selections":[{"point_id":"p1","score":4}]}`, task.Revision)
			response := submitQualificationRequest(handler, task.ID, "direct", body)
			if response.Code != test.status || gate.calls != test.calls {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, gate.calls, response.Body.String())
			}
			if test.status == http.StatusForbidden {
				current, _ := store.GetTask(context.Background(), tenantID, task.ID)
				if current.Revision != task.Revision || current.Status != task.Status || len(store.grades[key(tenantID, task.ID)]) != 0 || !strings.Contains(response.Body.String(), "grader_qualification_required") {
					t.Fatalf("rejected R3 changed task or grade: %#v %s", current, response.Body.String())
				}
			}
		})
	}
}

func TestSubmitQualificationRejectsRevocationAfterClaim(t *testing.T) {
	store, task := submissionQualificationFixture(t, assessment.RiskR3)
	gate := &mutableSubmissionQualification{allowed: true}
	handler := NewHandler(store, nil, nil, nil).WithQualificationGate(gate)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/review-tasks/next", strings.NewReader(`{}`))
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "reviewer-1", TenantID: tenantID, Roles: []string{"grader"}, Permissions: []string{"review:work"}}))
	claimed := httptest.NewRecorder()
	handler.ClaimNextTask(claimed, request)
	if claimed.Code != http.StatusOK {
		t.Fatalf("qualified claim: %d %s", claimed.Code, claimed.Body.String())
	}
	current, _ := store.GetTask(context.Background(), tenantID, task.ID)
	gate.allowed = false
	response := submitQualificationRequest(handler, task.ID, "after-claim", fmt.Sprintf(`{"expected_revision":%d,"score":4,"rubric_selections":[{"point_id":"p1","score":4}]}`, current.Revision))
	if response.Code != http.StatusForbidden || len(store.grades[key(tenantID, task.ID)]) != 0 {
		t.Fatalf("revoked qualification accepted score: %d %s", response.Code, response.Body.String())
	}
}

// 先接受成绩，再撤销资格，最后移除工作台资料；同一个冻结命令仍须返回原成绩，改 payload 则必须冲突。
func TestSubmitQualificationPreservesSuccessfulReplayAfterRevocationAndMissingContext(t *testing.T) {
	store, task := submissionQualificationFixture(t, assessment.RiskR3)
	gate := &mutableSubmissionQualification{allowed: true}
	handler := NewHandler(store, nil, nil, nil).WithQualificationGate(gate)
	body := fmt.Sprintf(`{"expected_revision":%d,"score":4,"rubric_selections":[{"point_id":"p1","score":4}]}`, task.Revision)
	first := submitQualificationRequest(handler, task.ID, "replay", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first score: %d %s", first.Code, first.Body.String())
	}
	gate.allowed = false
	for _, missing := range []bool{false, true} {
		if missing {
			delete(store.contexts, key(tenantID, task.AnswerSegmentID))
		}
		replay := submitQualificationRequest(handler, task.ID, "replay", body)
		if replay.Code != http.StatusCreated || replay.Body.String() != first.Body.String() || len(store.grades[key(tenantID, task.ID)]) != 1 {
			t.Fatalf("missing=%t replay=%d %s", missing, replay.Code, replay.Body.String())
		}
		changed := submitQualificationRequest(handler, task.ID, "replay", strings.Replace(body, `"score":4`, `"score":3`, 1))
		if changed.Code != http.StatusConflict || !strings.Contains(changed.Body.String(), "command_request_conflict") {
			t.Fatalf("missing=%t changed command=%d %s", missing, changed.Code, changed.Body.String())
		}
	}
}

func TestSubmitQualificationVerdictCannotMoveToAnotherSnapshotOrIdentity(t *testing.T) {
	store, task := submissionQualificationFixture(t, assessment.RiskR3)
	ctx := prepareSubmissionQualification(context.Background(), store, &mutableSubmissionQualification{allowed: true}, tenantID, task.ID, "reviewer-1")
	current := store.contexts[key(tenantID, task.AnswerSegmentID)]
	for _, mutation := range []func(*ReviewTask, *Context){
		func(task *ReviewTask, _ *Context) { task.ID = "another-task" },
		func(task *ReviewTask, _ *Context) { task.ExamID = "another-exam" },
		func(task *ReviewTask, _ *Context) { task.QuestionID = "another-question" },
		func(_ *ReviewTask, current *Context) { current.AssessmentSnapshot.ID = "another-snapshot" },
	} {
		changedTask, changedContext := task, current
		mutation(&changedTask, &changedContext)
		if err := requireSubmissionQualification(ctx, tenantID, "reviewer-1", changedTask, changedContext); !errors.Is(err, ErrQualificationRequired) {
			t.Fatalf("moved verdict accepted: %#v %#v err=%v", changedTask, changedContext.AssessmentSnapshot, err)
		}
	}
	if err := requireSubmissionQualification(ctx, tenantID, "another-reviewer", task, current); !errors.Is(err, ErrQualificationRequired) {
		t.Fatalf("moved reviewer accepted: %v", err)
	}
	if err := requireSubmissionQualification(ctx, "another-tenant", "reviewer-1", task, current); !errors.Is(err, ErrQualificationRequired) {
		t.Fatalf("moved tenant accepted: %v", err)
	}

	// 预检时风险较低，正式写入前变成 R3 时不能借用没有经过资格门禁的旧结论。
	lowRiskStore, lowRiskTask := submissionQualificationFixture(t, assessment.RiskR2)
	lowRiskCtx := prepareSubmissionQualification(context.Background(), lowRiskStore, &mutableSubmissionQualification{allowed: true}, tenantID, lowRiskTask.ID, "reviewer-1")
	upgraded := lowRiskStore.contexts[key(tenantID, lowRiskTask.AnswerSegmentID)]
	upgraded.AssessmentSnapshot.RiskTier = assessment.RiskR3
	if err := requireSubmissionQualification(lowRiskCtx, tenantID, "reviewer-1", lowRiskTask, upgraded); !errors.Is(err, ErrQualificationRequired) {
		t.Fatalf("upgraded risk accepted unchecked verdict: %v", err)
	}
}

func TestSubmitQualificationPostgresReceiptPrecedesRejectedVerdict(t *testing.T) {
	input := SubmitGradeInput{ExpectedRevision: 1, Score: 4}
	rawInput, _ := json.Marshal(struct {
		Operation, Target string
		Input             SubmitGradeInput
	}{"review.submit", "task", input})
	sum := sha256.Sum256(rawInput)
	saved := SubmitResult{Task: ReviewTask{ID: "task", Status: "submitted"}, Grade: HumanGrade{ID: "saved-grade", Score: 4}}
	rawResult, _ := json.Marshal(saved)
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed=%t", changed), func(t *testing.T) {
			store, script := openReviewBatch34Store(t, []reviewBatch34Step{
				{name: "command lock", kind: "exec", fragment: "pg_advisory_xact_lock", args: []any{"tenant:reviewer:replay:business-command"}},
				{name: "receipt", fragment: "FROM business_command_receipt", columns: 2, args: []any{"tenant", "reviewer", "replay"}, rows: [][]driver.Value{{hex.EncodeToString(sum[:]), rawResult}}},
			})
			ctx := context.WithValue(commandreceipt.WithID(context.Background(), "replay"), submissionQualificationKey{}, submissionQualification{err: ErrQualificationRequired})
			current := input
			if changed {
				current.Score = 3
			}
			result, err := store.SubmitGrade(ctx, "tenant", "task", "reviewer", current)
			if changed && !errors.Is(err, commandreceipt.ErrConflict) || !changed && (err != nil || result.Grade.ID != saved.Grade.ID) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			assertReviewBatch34Script(t, script)
		})
	}
}

type seedQualificationSentinelStore struct {
	*MemoryStore
	t *testing.T
}

func (s *seedQualificationSentinelStore) GetTaskContext(context.Context, string, string) (TaskContext, error) {
	s.t.Fatal("seed command reached ordinary task qualification")
	return TaskContext{}, ErrNotFound
}

func TestSubmitQualificationLeavesAcceptedSeedCommandWithSeedHook(t *testing.T) {
	input := SubmitGradeInput{Score: 4, ExpectedRevision: 7, RubricSelections: []RubricSelection{{PointID: "p1", Score: 4}}}
	calls := []string{}
	hook := &batch38SeedHook{t: t, input: input, calls: &calls, handled: true}
	gate := &mutableSubmissionQualification{}
	handler := NewHandler(&seedQualificationSentinelStore{MemoryStore: NewMemoryStore(), t: t}, nil, nil, nil).WithSeedHook(hook).WithQualificationGate(gate)
	body, _ := json.Marshal(input)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/review-tasks/task-38/submit", strings.NewReader(string(body)))
	request.SetPathValue("id", "task-38")
	request.Header.Set("Idempotency-Key", "command-38")
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "reviewer-38", TenantID: tenantID}))
	response := httptest.NewRecorder()
	handler.SubmitGrade(response, request)
	if response.Code != http.StatusCreated || gate.calls != 0 || len(calls) != 1 || calls[0] != "seed_submit" {
		t.Fatalf("seed boundary changed: response=%d %s calls=%v qualifications=%d", response.Code, response.Body.String(), calls, gate.calls)
	}
}
