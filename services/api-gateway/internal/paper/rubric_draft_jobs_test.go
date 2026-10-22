package paper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"github.com/google/uuid"
)

func rubricDraftJobFixture() rubricDraftJobRecord {
	now := time.Now().UTC()
	question := questionRubricFixture()
	question.ID, question.TenantID, question.ExamID = uuid.NewString(), uuid.NewString(), uuid.NewString()
	actor := uuid.NewString()
	return rubricDraftJobRecord{
		Job: RubricDraftJob{ID: uuid.NewString(), ExamID: question.ExamID, QuestionID: question.ID,
			QuestionNo: question.QuestionNo, Status: "queued", RequestID: uuid.NewString(),
			Input: QuestionMathRubricDraftInput{ExpectedQuestion: &question}, CreatedAt: now, UpdatedAt: now},
		TenantID: question.TenantID, ActorID: actor, Scope: auth.AccessScope{TenantID: question.TenantID, ActorID: actor, TenantWide: true},
		Fingerprint: "same-question-snapshot", ModelHash: "same-model", AvailableAt: now,
	}
}

func seedRubricDraftJob(t *testing.T, store rubricDraftJobStore, record rubricDraftJobRecord) RubricDraftJob {
	t.Helper()
	job, err := store.createRubricDraftJob(t.Context(), record, false)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestRubricDraftJobsMemoryConcurrencyAndCancellationKeepFiveSlots(t *testing.T) {
	assertRubricDraftJobConcurrency(t, NewMemoryStore(), nil)
}

// 多个执行者同时领取，模拟共享仓储的多副本。取消正在执行的任务后继续领取，
// 保护占位不能提早释放；只有上游返回并落终态之后，下一题才允许开始。
func assertRubricDraftJobConcurrency(t *testing.T, store rubricDraftJobStore, prepare func(rubricDraftJobRecord)) {
	t.Helper()
	for i := 0; i < 30; i++ {
		record := rubricDraftJobFixture()
		if prepare != nil {
			prepare(record)
		}
		seedRubricDraftJob(t, store, record)
	}
	claimed := make(chan rubricDraftJobRecord, 30)
	errors := make(chan error, 30)
	var workers sync.WaitGroup
	for i := 0; i < 30; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			record, err := store.claimRubricDraftJob(t.Context(), time.Minute, "")
			if err != nil {
				errors <- err
			} else if record != nil {
				claimed <- *record
			}
		}()
	}
	workers.Wait()
	close(errors)
	close(claimed)
	for err := range errors {
		t.Errorf("concurrent claim failed: %v", err)
	}
	if len(claimed) != 5 {
		t.Fatalf("30 simultaneous claimants acquired %d slots, want 5", len(claimed))
	}
	seen := map[string]bool{}
	var running rubricDraftJobRecord
	for record := range claimed {
		if seen[record.Job.ID] || record.Job.Attempt != 1 || record.LeaseToken == "" {
			t.Fatalf("claim was duplicated or unfenced: %#v", record)
		}
		seen[record.Job.ID], running = true, record
	}
	cancelled, err := store.cancelRubricDraftJob(t.Context(), running.TenantID, running.ActorID, running.Job.ExamID, running.Job.ID)
	if err != nil || cancelled.Status != "cancelling" {
		t.Fatalf("running cancellation lost its slot: %#v err=%v", cancelled, err)
	}
	if next, err := store.claimRubricDraftJob(t.Context(), time.Minute, ""); err != nil || next != nil {
		t.Fatalf("cancelling task released a slot before upstream ended: %#v err=%v", next, err)
	}
	result := &MathRubricDraftResponse{SuggestedRubricCandidates: []MathRubricDraftSuggestion{{CandidateID: "must-be-discarded"}}}
	if err := store.finishRubricDraftJob(t.Context(), running, "succeeded", result, "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	finished, err := store.getRubricDraftJob(t.Context(), running.TenantID, running.ActorID, running.Job.ExamID, running.Job.ID)
	if err != nil || finished.Job.Status != "cancelled" || finished.Job.Result != nil {
		t.Fatalf("cancelled inference published a suggestion: %#v err=%v", finished.Job, err)
	}
	if next, err := store.claimRubricDraftJob(t.Context(), time.Minute, ""); err != nil || next == nil {
		t.Fatalf("finished cancellation did not release its slot: %#v err=%v", next, err)
	}
	if next, err := store.claimRubricDraftJob(t.Context(), time.Minute, ""); err != nil || next != nil {
		t.Fatalf("single released slot admitted multiple tasks: %#v err=%v", next, err)
	}
}

func TestRubricDraftJobsLocalModelOccupiesOnlyOneOfFiveGlobalSlots(t *testing.T) {
	store := NewMemoryStore()
	for i := 0; i < 3; i++ {
		record := rubricDraftJobFixture()
		record.Local = true
		seedRubricDraftJob(t, store, record)
	}
	for i := 0; i < 5; i++ {
		seedRubricDraftJob(t, store, rubricDraftJobFixture())
	}
	local, total := 0, 0
	for {
		record, err := store.claimRubricDraftJob(t.Context(), time.Minute, "")
		if err != nil {
			t.Fatal(err)
		}
		if record == nil {
			break
		}
		total++
		if record.Local {
			local++
		}
	}
	if local != 1 || total != 5 {
		t.Fatalf("local/global limits = %d/%d, want 1/5", local, total)
	}
}

func TestRubricDraftJobsDedupeQueueLimitAndOwnerIsolation(t *testing.T) {
	store := NewMemoryStore()
	first := rubricDraftJobFixture()
	seedRubricDraftJob(t, store, first)
	for i := 1; i < 20; i++ {
		record := first
		record.Job.ID, record.Job.RequestID, record.Job.QuestionID = uuid.NewString(), uuid.NewString(), uuid.NewString()
		seedRubricDraftJob(t, store, record)
	}
	duplicate := first
	duplicate.Job.ID, duplicate.Job.RequestID = uuid.NewString(), uuid.NewString()
	job, err := store.createRubricDraftJob(t.Context(), duplicate, true)
	if err != nil || job.ID != first.Job.ID {
		t.Fatalf("same active input must dedupe even when queue is full: %#v err=%v", job, err)
	}
	duplicate.Job.QuestionID = uuid.NewString()
	if _, err := store.createRubricDraftJob(t.Context(), duplicate, false); !errors.Is(err, ErrRubricDraftQueueFull) {
		t.Fatalf("21st queued task bypassed user quota: %v", err)
	}
	changed := first
	changed.Job.ID, changed.Fingerprint = uuid.NewString(), "changed-snapshot"
	if _, err := store.createRubricDraftJob(t.Context(), changed, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("request id reused with a different snapshot: %v", err)
	}
	changed.Job.RequestID = uuid.NewString()
	if _, err := store.createRubricDraftJob(t.Context(), changed, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("same question accepted competing active snapshots: %v", err)
	}
	for _, keys := range [][3]string{{uuid.NewString(), first.ActorID, first.Job.ExamID}, {first.TenantID, uuid.NewString(), first.Job.ExamID}, {first.TenantID, first.ActorID, uuid.NewString()}} {
		if _, err := store.getRubricDraftJob(t.Context(), keys[0], keys[1], keys[2], first.Job.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign identity read job: %v", err)
		}
		if _, err := store.cancelRubricDraftJob(t.Context(), keys[0], keys[1], keys[2], first.Job.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign identity cancelled job: %v", err)
		}
		if jobs, err := store.listRubricDraftJobs(t.Context(), keys[0], keys[1], keys[2]); err != nil || len(jobs) != 0 {
			t.Fatalf("foreign identity listed jobs: %#v err=%v", jobs, err)
		}
	}
	if job, err := store.cancelRubricDraftJob(t.Context(), first.TenantID, first.ActorID, first.Job.ExamID, first.Job.ID); err != nil || job.Status != "cancelled" {
		t.Fatalf("queued cancellation failed: %#v err=%v", job, err)
	}
	if claimed, err := store.claimRubricDraftJob(t.Context(), time.Minute, first.Job.ID); err != nil || claimed != nil {
		t.Fatalf("cancelled queued task reached execution: %#v err=%v", claimed, err)
	}
}

func TestRubricDraftJobsRejectLateLeaseResultsAndDoNotReplayInterruptedCalls(t *testing.T) {
	for _, cancelRunning := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelling=%t", cancelRunning), func(t *testing.T) {
			store := NewMemoryStore()
			record := rubricDraftJobFixture()
			seedRubricDraftJob(t, store, record)
			claimed, err := store.claimRubricDraftJob(t.Context(), time.Minute, record.Job.ID)
			if err != nil || claimed == nil {
				t.Fatal(err)
			}
			if cancelRunning {
				if _, err := store.cancelRubricDraftJob(t.Context(), record.TenantID, record.ActorID, record.Job.ExamID, record.Job.ID); err != nil {
					t.Fatal(err)
				}
			}
			store.mu.Lock()
			expired := store.rubricDraftJobs[record.Job.ID]
			expired.LeaseUntil = time.Now().Add(-time.Second)
			store.rubricDraftJobs[record.Job.ID] = expired
			store.mu.Unlock()
			if retry, err := store.claimRubricDraftJob(t.Context(), time.Minute, record.Job.ID); err != nil || retry != nil {
				t.Fatalf("expired inference was automatically replayed: %#v err=%v", retry, err)
			}
			if err := store.finishRubricDraftJob(t.Context(), *claimed, "succeeded", &MathRubricDraftResponse{}, "", time.Time{}); !errors.Is(err, ErrConflict) {
				t.Fatalf("late execution overwrote interruption state: %v", err)
			}
			current, err := store.getRubricDraftJob(t.Context(), record.TenantID, record.ActorID, record.Job.ExamID, record.Job.ID)
			wantStatus := "failed"
			if cancelRunning {
				wantStatus = "cancelled"
			}
			if err != nil || current.Job.Status != wantStatus || current.Job.Result != nil || current.Job.Attempt != 1 {
				t.Fatalf("interrupted task state: %#v err=%v want=%s", current.Job, err, wantStatus)
			}
		})
	}
}

func TestRubricDraftJobsListKeepsOldActiveJobsBeforeCompletedHistory(t *testing.T) {
	store := NewMemoryStore()
	active := rubricDraftJobFixture()
	seedRubricDraftJob(t, store, active)
	store.mu.Lock()
	for i := 0; i < 110; i++ {
		record := active
		record.Job.ID, record.Job.Status = uuid.NewString(), "succeeded"
		record.Job.CreatedAt = active.Job.CreatedAt.Add(time.Duration(i+1) * time.Second)
		store.rubricDraftJobs[record.Job.ID] = record
	}
	store.mu.Unlock()
	jobs, err := store.listRubricDraftJobs(t.Context(), active.TenantID, active.ActorID, active.Job.ExamID)
	if err != nil || len(jobs) != 100 || jobs[0].ID != active.Job.ID {
		t.Fatalf("completed history hid the pending question: count=%d err=%v", len(jobs), err)
	}
}

func newRubricDraftJobsFixture(t *testing.T, server *httptest.Server) (*MemoryStore, Question, *RubricDraftJobs, auth.AccessScope) {
	t.Helper()
	store, question := questionDraftFixture(t)
	scope := auth.AccessScope{TenantID: question.TenantID, ActorID: "teacher", TenantWide: true}
	reader := questionDraftExamReader(func(_ context.Context, got auth.AccessScope, id string) (exam.Exam, error) {
		if got.ActorID != scope.ActorID || got.TenantID != scope.TenantID || id != question.ExamID {
			return exam.Exam{}, ErrNotFound
		}
		return exam.Exam{ID: id, TenantID: scope.TenantID, Subject: "math", Status: "draft"}, nil
	})
	service := &DocumentImportService{parser: NewHTTPDocumentParser(server.URL, strings.Repeat("t", 32), 2*time.Second)}
	service.WithModelResolver(func(context.Context, string) (*DocumentModelConfig, error) {
		return &DocumentModelConfig{ModelName: "school-model", APIKey: "secret-job-test-key"}, nil
	})
	jobs := NewRubricDraftJobs(store, reader, service, 3*time.Second)
	return store, question, jobs, scope
}

func createRubricDraftJobForQuestion(t *testing.T, jobs *RubricDraftJobs, scope auth.AccessScope, question Question) RubricDraftJob {
	t.Helper()
	job, err := jobs.Create(t.Context(), scope, question.ExamID, question.ID, CreateRubricDraftJobInput{
		QuestionMathRubricDraftInput: QuestionMathRubricDraftInput{ExpectedQuestion: &question}, RequestID: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func executeRubricDraftJob(t *testing.T, jobs *RubricDraftJobs, id string) {
	t.Helper()
	record, err := jobs.store.claimRubricDraftJob(t.Context(), time.Minute, id)
	if err != nil || record == nil {
		t.Fatalf("claim fixture: %#v err=%v", record, err)
	}
	jobs.execute(t.Context(), *record, func(err error) { t.Error(err) })
}

func TestRubricDraftJobsPersistSuggestionWithoutWritingFormalRubric(t *testing.T) {
	server := questionDraftServer(t, nil)
	defer server.Close()
	store, question, jobs, scope := newRubricDraftJobsFixture(t, server)
	job := createRubricDraftJobForQuestion(t, jobs, scope, question)
	executeRubricDraftJob(t, jobs, job.ID)
	// 用新服务对象读取同一仓储，模拟页面刷新或后台执行实例切换。
	restored := NewRubricDraftJobs(store, jobs.exams, jobs.service, jobs.timeout)
	result, err := restored.Result(t.Context(), scope.TenantID, scope.ActorID, question.ExamID, job.ID)
	if err != nil || result.Status != "succeeded" || result.Result == nil || len(result.Result.SuggestedRubricCandidates) != 1 {
		t.Fatalf("durable suggestion was not restored: %#v err=%v", result, err)
	}
	stored, err := store.ListQuestions(t.Context(), question.TenantID, question.ExamID)
	if err != nil || stored[0].Rubric != nil || len(store.rubrics) != 0 {
		t.Fatalf("background generation wrote formal rubric: %#v err=%v", stored, err)
	}
	store.mu.RLock()
	record, _ := json.Marshal(store.rubricDraftJobs[job.ID])
	store.mu.RUnlock()
	response, _ := json.Marshal(result)
	if bytes.Contains(record, []byte("secret-job-test-key")) || bytes.Contains(response, []byte("secret-job-test-key")) {
		t.Fatal("model credential was persisted or returned to browser")
	}
	list, err := store.listRubricDraftJobs(t.Context(), scope.TenantID, scope.ActorID, question.ExamID)
	if err != nil || len(list) != 1 || list[0].Result != nil {
		t.Fatalf("polling list included large model response: %#v err=%v", list, err)
	}
}

func TestRubricDraftJobsRejectChangesBeforeExecutionAndAfterCompletion(t *testing.T) {
	for _, timing := range []string{"before-execution", "after-completion"} {
		t.Run(timing, func(t *testing.T) {
			var calls atomic.Int32
			server := questionDraftServer(t, func(QuestionCandidate, SolutionCandidate, *DocumentModelConfig) { calls.Add(1) })
			defer server.Close()
			store, question, jobs, scope := newRubricDraftJobsFixture(t, server)
			job := createRubricDraftJobForQuestion(t, jobs, scope, question)
			if timing == "after-completion" {
				executeRubricDraftJob(t, jobs, job.ID)
			}
			store.mu.Lock()
			changed := store.questions[question.ID]
			changed.Stem = "教师已更新题干"
			store.questions[question.ID] = changed
			store.mu.Unlock()
			if timing == "before-execution" {
				executeRubricDraftJob(t, jobs, job.ID)
			}
			result, err := jobs.Result(t.Context(), scope.TenantID, scope.ActorID, question.ExamID, job.ID)
			wantCalls := int32(0)
			if timing == "after-completion" {
				wantCalls = 1
			}
			if err != nil || result.Status != "stale" || result.Result != nil || calls.Load() != wantCalls {
				t.Fatalf("stale input remained applicable: %#v calls=%d err=%v", result, calls.Load(), err)
			}
		})
	}
}

func TestRubricDraftJobs429RetriesAreBounded(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	store, question, jobs, scope := newRubricDraftJobsFixture(t, server)
	job := createRubricDraftJobForQuestion(t, jobs, scope, question)
	for attempt := 1; attempt <= 3; attempt++ {
		executeRubricDraftJob(t, jobs, job.ID)
		current, err := store.getRubricDraftJob(t.Context(), scope.TenantID, scope.ActorID, question.ExamID, job.ID)
		want := "queued"
		if attempt == 3 {
			want = "failed"
		}
		if err != nil || current.Job.Status != want || current.Job.Attempt != attempt || calls.Load() != int32(attempt) {
			t.Fatalf("429 attempt %d: %#v calls=%d err=%v", attempt, current.Job, calls.Load(), err)
		}
		if attempt < 3 {
			if !current.AvailableAt.After(time.Now()) {
				t.Fatal("429 retry did not back off")
			}
			if early, err := store.claimRubricDraftJob(t.Context(), time.Minute, job.ID); err != nil || early != nil {
				t.Fatalf("429 retry ignored delay: %#v err=%v", early, err)
			}
			store.mu.Lock()
			current.AvailableAt = time.Now().Add(-time.Second)
			store.rubricDraftJobs[job.ID] = current
			store.mu.Unlock()
		}
	}
}

func TestRubricDraftJobsTemporaryExamReadFailureKeepsSuccessfulResult(t *testing.T) {
	server := questionDraftServer(t, nil)
	defer server.Close()
	store, question, jobs, scope := newRubricDraftJobsFixture(t, server)
	job := createRubricDraftJobForQuestion(t, jobs, scope, question)
	executeRubricDraftJob(t, jobs, job.ID)
	readFailure := errors.New("temporary exam database error")
	jobs.exams = questionDraftExamReader(func(context.Context, auth.AccessScope, string) (exam.Exam, error) {
		return exam.Exam{}, readFailure
	})
	if _, err := jobs.Result(t.Context(), scope.TenantID, scope.ActorID, question.ExamID, job.ID); !errors.Is(err, readFailure) {
		t.Fatalf("transient read failure was converted to permanent stale result: %v", err)
	}
	current, err := store.getRubricDraftJob(t.Context(), scope.TenantID, scope.ActorID, question.ExamID, job.ID)
	if err != nil || current.Job.Status != "succeeded" || current.Job.Result == nil {
		t.Fatalf("transient read failure destroyed completed suggestion: %#v err=%v", current.Job, err)
	}
}

func TestRubricDraftJobs429AfterCancellationDoesNotRequeue(t *testing.T) {
	var store *MemoryStore
	var job RubricDraftJob
	var scope auth.AccessScope
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := store.cancelRubricDraftJob(t.Context(), scope.TenantID, scope.ActorID, job.ExamID, job.ID); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	var question Question
	var jobs *RubricDraftJobs
	store, question, jobs, scope = newRubricDraftJobsFixture(t, server)
	job = createRubricDraftJobForQuestion(t, jobs, scope, question)
	executeRubricDraftJob(t, jobs, job.ID)
	current, err := store.getRubricDraftJob(t.Context(), scope.TenantID, scope.ActorID, question.ExamID, job.ID)
	if err != nil || current.Job.Status != "cancelled" || current.Job.Result != nil {
		t.Fatalf("429 response revived cancelled task: %#v err=%v", current.Job, err)
	}
}

func TestRubricDraftJobsManagedService502RetriesOnlyRateLimitCode(t *testing.T) {
	for _, item := range []struct {
		name, body, want string
	}{
		{"provider rate limit", `{"schema_version":"grading-agent-v1","error":{"code":"model_rate_limited","message":"sensitive-provider-secret","retryable":false}}`, "queued"},
		{"invalid model output", `{"error":{"code":"model_output_invalid","message":"sensitive-provider-secret","retryable":true}}`, "failed"},
		{"provider authentication", `{"error":{"code":"model_auth_failed","message":"sensitive-provider-secret","retryable":true}}`, "failed"},
		{"unstructured upstream text", `sensitive-provider-secret model_rate_limited`, "failed"},
	} {
		t.Run(item.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(item.body))
			}))
			defer server.Close()
			store, question, jobs, scope := newRubricDraftJobsFixture(t, server)
			job := createRubricDraftJobForQuestion(t, jobs, scope, question)
			executeRubricDraftJob(t, jobs, job.ID)
			current, err := store.getRubricDraftJob(t.Context(), scope.TenantID, scope.ActorID, question.ExamID, job.ID)
			if err != nil || current.Job.Status != item.want || current.Job.Attempt != 1 {
				t.Fatalf("managed service 502 classification: %#v err=%v", current.Job, err)
			}
			encoded, _ := json.Marshal(current)
			if bytes.Contains(encoded, []byte("sensitive-provider-secret")) {
				t.Fatal("provider error body reached persisted task or browser result")
			}
		})
	}
}

func TestRubricDraftJobsSuccessfulResultReusedUntilExplicitRegeneration(t *testing.T) {
	server := questionDraftServer(t, nil)
	defer server.Close()
	_, question, jobs, scope := newRubricDraftJobsFixture(t, server)
	first := createRubricDraftJobForQuestion(t, jobs, scope, question)
	executeRubricDraftJob(t, jobs, first.ID)
	if reused := createRubricDraftJobForQuestion(t, jobs, scope, question); reused.ID != first.ID || reused.Status != "succeeded" {
		t.Fatalf("ordinary duplicate click generated another billed round: %#v", reused)
	}
	input := CreateRubricDraftJobInput{QuestionMathRubricDraftInput: QuestionMathRubricDraftInput{ExpectedQuestion: &question}, RequestID: uuid.NewString(), Regenerate: true}
	regenerated, err := jobs.Create(t.Context(), scope, question.ExamID, question.ID, input)
	if err != nil || regenerated.ID == first.ID || regenerated.Status != "queued" {
		t.Fatalf("explicit regenerate did not create a new round: %#v err=%v", regenerated, err)
	}
	if replay, err := jobs.Create(t.Context(), scope, question.ExamID, question.ID, input); err != nil || replay.ID != regenerated.ID {
		t.Fatalf("uncertain POST retry duplicated regeneration: %#v err=%v", replay, err)
	}
}

// 先取消发起请求的 context，再由两个后台执行器共同消费六道题。
// 上游阻塞前五个调用，验证换题/断开不取消生成，第六题也不能突破共享占位。
func TestRubricDraftJobsRunSurvivesRequestCancellationAndTwoExecutorsShareFiveCalls(t *testing.T) {
	started := make(chan struct{}, 6)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var running, maximum atomic.Int32
	server := questionDraftServer(t, func(QuestionCandidate, SolutionCandidate, *DocumentModelConfig) {
		active := running.Add(1)
		for previous := maximum.Load(); active > previous; previous = maximum.Load() {
			if maximum.CompareAndSwap(previous, active) {
				break
			}
		}
		started <- struct{}{}
		<-release
		running.Add(-1)
	})
	defer server.Close()
	defer releaseOnce.Do(func() { close(release) })
	store, question, jobs, scope := newRubricDraftJobsFixture(t, server)
	requestCtx, disconnect := context.WithCancel(t.Context())
	for i := 0; i < 6; i++ {
		q := cloneQuestionDraftFixture(t, question)
		q.ID, q.QuestionNo = fmt.Sprintf("q%d", i+20), fmt.Sprint(i+20)
		q.AnswerKey.QuestionID, q.Solution.QuestionID = q.ID, q.ID
		store.mu.Lock()
		store.questions[q.ID] = q
		store.mu.Unlock()
		if _, err := jobs.Create(requestCtx, scope, q.ExamID, q.ID, CreateRubricDraftJobInput{
			QuestionMathRubricDraftInput: QuestionMathRubricDraftInput{ExpectedQuestion: &q}, RequestID: uuid.NewString(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	disconnect()
	workerCtx, stopWorkers := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	defer func() {
		stopWorkers()
		releaseOnce.Do(func() { close(release) })
		workers.Wait()
	}()
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			jobs.Run(workerCtx, func(err error) { t.Error(err) })
		}()
	}
	for i := 0; i < 5; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			releaseOnce.Do(func() { close(release) })
			t.Fatal("request cancellation prevented the accepted jobs from running")
		}
	}
	select {
	case <-started:
		t.Fatal("second executor exceeded the shared five-call limit")
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		list, err := store.listRubricDraftJobs(t.Context(), scope.TenantID, scope.ActorID, question.ExamID)
		if err != nil {
			t.Fatal(err)
		}
		completed := 0
		for _, job := range list {
			if job.Status == "succeeded" {
				completed++
			}
		}
		if completed == 6 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of six accepted jobs completed: %#v", completed, list)
		}
		time.Sleep(10 * time.Millisecond)
	}
	stopWorkers()
	workers.Wait()
	if maximum.Load() != 5 || len(store.rubrics) != 0 {
		t.Fatalf("upstream concurrency=%d, persisted rubrics=%d", maximum.Load(), len(store.rubrics))
	}
}
