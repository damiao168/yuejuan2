package paper

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestBatch33ParseRetryRetainsTransactionalStateGates(t *testing.T) {
	readFailure := errors.New("read after committed retry")
	for _, test := range []paperParseRetryCase{
		{"stale generation", "failed", "ai_parse_failed", "failed", "paper_parse_failed", "failed", "paper_parse_failed", 2, false, false, false},
		{"missing task", "processing", "", "processing", "", "", "", 1, true, false, false},
		{"wrong failure stage", "failed", "ai_parse_failed", "failed", "paper_parse_failed", "failed", "ocr_failed", 1, false, false, false},
		{"queued duplicate", "processing", "", "processing", "", "queued", "", 1, false, true, false},
		{"leased duplicate", "processing", "", "processing", "", "leased", "", 1, false, true, false},
		{"running duplicate", "processing", "", "processing", "", "running", "", 1, false, true, false},
		{"reviewed success", "review_required", "", "review_required", "", "succeeded", "", 1, false, true, false},
		{"applied success", "applied", "", "applied", "", "succeeded", "", 1, false, true, false},
		{"failed parse", "failed", "ai_parse_failed", "failed", "paper_parse_failed", "failed", "paper_parse_failed", 1, false, false, true},
		{"dead letter parse", "failed", "ai_parse_failed", "failed", "paper_parse_failed", "dead_letter", "paper_parse_failed", 1, false, false, true},
		{"cancelled parse", "cancelled", "paper_import_cancelled", "cancelled", "paper_import_cancelled", "cancelled", "", 1, false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertPaperParseRetryTransactionGates(t, test, readFailure)
		})
	}
}

func TestBatch33HistoricalParseReceiptRejectsMismatchedEvidence(t *testing.T) {
	for _, test := range []struct {
		name, revision, status, taskID, hash string
	}{
		{"different revision", "old", "review_required", "task", "hash"},
		{"unfinished run", "revision", "processing", "task", "hash"},
		{"different task", "revision", "review_required", "other", "hash"},
		{"different result", "revision", "applied", "task", "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, script := openPaperBatch10Store(t, []paperBatch10Step{{kind: "query", fragment: "FROM paper_import_run", columns: 4, rows: [][]driver.Value{{test.revision, test.status, test.taskID, test.hash}}}})
			tx, err := store.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			binding := PaperImportRunBinding{ImportID: "import", RunID: "run", Generation: 1, SourceRevision: "revision", InputID: "input"}
			completion := workerruntime.CompleteInput{LeaseToken: "lease", ResultSchemaVersion: "paper-import-parse-result-v2", Result: map[string]any{"generation": 1}, DurationMS: 1}
			err = confirmHistoricalPaperImportParseResult(context.Background(), tx, "tenant", "task", binding, completion, "hash")
			_ = tx.Rollback()
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("historical receipt accepted mismatched evidence: %v", err)
			}
			assertPaperBatch10Proof(t, script, "rollback", test.name)
		})
	}
}

func TestBatch33StageBindingRejectsWrongRunBeforeStage(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol int64
		runID    string
		stage    bool
	}{
		{"wrong protocol", 1, "run", false},
		{"wrong run", 2, "other", false},
		{"wrong stage", 2, "run", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			steps := []paperBatch10Step{{kind: "query", fragment: "paper_import_generation,0),task_protocol_version", columns: 3, rows: [][]driver.Value{{test.runID, int64(1), test.protocol}}}}
			if test.stage {
				steps = append(steps, paperBatch10Step{kind: "query", fragment: "SELECT task_type=$3 AND source_type=$4 AND source_id=$5::uuid", columns: 1, rows: [][]driver.Value{{false}}})
			}
			store, script := openPaperBatch10Store(t, steps)
			tx, err := store.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			err = validatePaperImportStageBinding(context.Background(), tx, "tenant", "task", "run", 1, paperImportTaskStage{taskType: "paper_parse", sourceType: "paper_import_parse", sourceID: "input"})
			_ = tx.Rollback()
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("stage binding accepted a different identity: %v", err)
			}
			assertPaperBatch10Proof(t, script, "rollback", test.name)
		})
	}
}

type batch33DispatchStore struct {
	repaired            bool
	repairErr, claimErr error
	claimCalls          int
	owner               string
}

func (s *batch33DispatchStore) ReconcileExpiredPaperImportTask(context.Context) (bool, error) {
	return s.repaired, s.repairErr
}

func (s *batch33DispatchStore) ClaimPendingPaperImportDispatch(_ context.Context, owner string, _ time.Duration) (PaperImportJob, bool, error) {
	s.claimCalls++
	s.owner = owner
	return PaperImportJob{}, false, s.claimErr
}

func (*batch33DispatchStore) FailPendingPaperImportDispatch(context.Context, PaperImportJob, string, string) error {
	return errors.New("unexpected dispatch failure")
}

func TestBatch33ExecutorKeepsDispatchPriorityAndClaimedWork(t *testing.T) {
	failed := errors.New("dispatch failure")
	for _, test := range []struct {
		name           string
		dispatch       batch33DispatchStore
		worked         bool
		wantErr        error
		wantClaimCalls int
	}{
		{"repaired", batch33DispatchStore{repaired: true}, true, nil, 0},
		{"repair error", batch33DispatchStore{repairErr: failed}, false, failed, 0},
		{"dispatch claim error", batch33DispatchStore{claimErr: failed}, false, failed, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			// runtime 故意为空，提前返回必须发生在解析任务 claim 之前。
			executor := &ParseTaskExecutor{dispatches: &test.dispatch, instanceID: "parser", leaseSeconds: 300}
			worked, err := executor.RunOnce(context.Background())
			if worked != test.worked || !errors.Is(err, test.wantErr) || test.dispatch.claimCalls != test.wantClaimCalls {
				t.Fatalf("dispatch priority = %v, %v, %d", worked, err, test.dispatch.claimCalls)
			}
		})
	}
	runtime := workerruntime.NewMemoryStore()
	ctx := context.Background()
	created, err := runtime.CreateTask(ctx, "tenant", "actor", workerruntime.CreateTaskInput{TaskType: "layout", QueueName: "paper-parse", SourceType: "paper_import_job", SourceID: "import", IdempotencyKey: "batch33-invalid-stage", PayloadSchemaVersion: "v1", MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	dispatch := &batch33DispatchStore{}
	executor := &ParseTaskExecutor{dispatches: dispatch, runtime: runtime, instanceID: "parser", leaseSeconds: 300}
	worked, err := executor.RunOnce(ctx)
	if !worked || err != nil || dispatch.claimCalls != 1 || !strings.HasPrefix(dispatch.owner, "parser:") {
		t.Fatalf("parse task was skipped after empty dispatch: %v, %v, %#v", worked, err, dispatch)
	}
	got, err := runtime.Get(ctx, "tenant", created.ID)
	if err != nil || got.Status == workerruntime.StatusQueued {
		t.Fatalf("claimed invalid-stage task remained queued: %#v, %v", got, err)
	}
}

type paperParseRetryCase struct {
	name                           string
	jobStatus, jobError            string
	runStatus, runError            string
	taskStatus, taskError          string
	generation                     int64
	missingTask, accepted, requeue bool
}

func assertPaperParseRetryTransactionGates(t *testing.T, test paperParseRetryCase, readFailure error) {
	t.Helper()
	steps := paperParseRetrySQLSteps(test, readFailure)
	store, script := openPaperBatch10Store(t, steps)
	_, err := store.RetryPaperImportParseGeneration(context.Background(), "tenant", "import", 1)
	want := ErrConflict
	end := "rollback"
	if test.accepted || test.requeue {
		want, end = readFailure, ""
	}
	if !errors.Is(err, want) {
		t.Fatalf("retry error = %v, want %v", err, want)
	}
	assertPaperBatch10Proof(t, script, end, err.Error())
	if test.accepted || test.requeue {
		wantEvents := []string{"begin", "query:FOR UPDATE OF j,r", "query:FOR UPDATE OF task"}
		if test.requeue {
			wantEvents = append(wantEvents, "exec:UPDATE agent_worker_task", "exec:UPDATE paper_import_job", "exec:UPDATE paper_import_run", "exec:UPDATE paper_import_source")
		}
		wantEvents = append(wantEvents, "commit", "query:FROM paper_import_job WHERE")
		if !reflect.DeepEqual(script.events, wantEvents) {
			t.Fatalf("retry transaction sequence = %#v, want %#v", script.events, wantEvents)
		}
	}
}

// 先准备锁定代次和任务的SQL，再按状态决定重新排队或提交后的回执读取失败。
func paperParseRetrySQLSteps(test paperParseRetryCase, readFailure error) []paperBatch10Step {
	steps := []paperBatch10Step{{kind: "query", fragment: "FOR UPDATE OF j,r", columns: 6, rows: [][]driver.Value{{test.jobStatus, test.jobError, test.generation, "run", test.runStatus, test.runError}}}}
	if test.generation == 1 {
		task := paperBatch10Step{kind: "query", fragment: "FOR UPDATE OF task", columns: 3}
		if !test.missingTask {
			task.rows = [][]driver.Value{{"task", test.taskStatus, test.taskError}}
		}
		steps = append(steps, task)
	}
	if test.requeue {
		for _, fragment := range []string{"UPDATE agent_worker_task", "UPDATE paper_import_job", "UPDATE paper_import_run", "UPDATE paper_import_source"} {
			steps = append(steps, paperBatch10Step{kind: "exec", fragment: fragment})
		}
	}
	if test.accepted || test.requeue {
		// 已提交后只让读取失败，保护“写入/幂等确认已成功”与“最终响应读取失败”的边界。
		steps = append(steps, paperBatch10Step{kind: "query", fragment: "FROM paper_import_job WHERE", err: readFailure})
	}
	return steps
}
