package server

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"

	"github.com/google/uuid"
)

const (
	subjectiveProbeQueue        = "subjective-grading"
	subjectiveProbeService      = "subjective-grading-worker"
	subjectiveProbePlatform     = "00000000-0000-0000-0000-000000000001"
	subjectiveProbeSchool       = "00000000-0000-0000-0000-000000000002"
	subjectiveProbeTaskCount    = 24
	subjectiveProbeExecution    = 75 * time.Millisecond
	subjectiveProbeLeaseSeconds = 60
)

// 用真实 PostgreSQL 领取和完成相同积压，固定等待模拟任务执行。该对比只测运行时租约和排队，不调用模型或部署 Worker 容器。
func TestSubjectiveWorkerConcurrencyWithPostgresTestDatabase(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	store := workerruntime.NewPostgresStore(db)
	for _, workers := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d_worker", workers), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			prefix := fmt.Sprintf("worker-concurrency-%d-", workers)
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			// 同一事务创建积压，任务的数据库创建时间一致，排队样本可以直接比较。
			for index := range subjectiveProbeTaskCount {
				_, err := workerruntime.CreateTaskInTx(ctx, tx, subjectiveProbeSchool, "", workerruntime.CreateTaskInput{
					TaskType: "ai_grade", QueueName: subjectiveProbeQueue, SourceType: "subjective_grading_run",
					SourceID: uuid.NewString(), PayloadSchemaVersion: "subjective-grade-v1",
					IdempotencyKey: fmt.Sprintf("%s%d", prefix, index),
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			before := db.Stats()
			started := time.Now()
			counts := runSubjectiveProbeWorkers(t, ctx, store, workers, prefix)
			drained := time.Since(started)
			after := db.Stats()
			var taskCount, completeCount, attemptCount, instances int
			var queueP95MS float64
			err = db.QueryRowContext(ctx, `
SELECT count(DISTINCT task.id), count(*) FILTER (WHERE task.status='succeeded'
  AND task.attempt_count=1 AND attempt.status='succeeded' AND attempt.completed_at IS NOT NULL),
  count(attempt.id), count(DISTINCT attempt.worker_instance_id),
  COALESCE(percentile_cont(0.95) WITHIN GROUP (
    ORDER BY extract(epoch FROM (attempt.started_at-task.created_at))*1000), 0)
FROM agent_worker_task task
LEFT JOIN agent_worker_task_attempt attempt ON attempt.tenant_id=task.tenant_id AND attempt.task_id=task.id
WHERE task.tenant_id=$1::uuid AND task.queue_name=$2 AND task.idempotency_key LIKE $3`,
				subjectiveProbeSchool, subjectiveProbeQueue, prefix+"%").Scan(&taskCount, &completeCount, &attemptCount, &instances, &queueP95MS)
			if err != nil {
				t.Fatal(err)
			}
			if taskCount != subjectiveProbeTaskCount || completeCount != taskCount || attemptCount != taskCount || instances != workers {
				t.Fatalf("unexpected completion: tasks=%d succeeded=%d attempts=%d workers=%d", taskCount, completeCount, attemptCount, instances)
			}
			// 只断言任务完整和租约唯一。受主机负载影响的耗时用于比较，避免 CI 将宿主机慢速误判为业务失败。
			t.Logf("workers=%d tasks=%d per_worker=%v synthetic_execution_ms=%d queue_p95_ms=%.1f drain_ms=%.1f tasks_per_second=%.1f db_waits=%d db_wait_ms=%.1f",
				workers, taskCount, counts, subjectiveProbeExecution.Milliseconds(), queueP95MS, float64(drained)/float64(time.Millisecond),
				float64(taskCount)/drained.Seconds(), after.WaitCount-before.WaitCount, float64(after.WaitDuration-before.WaitDuration)/float64(time.Millisecond))
		})
	}
}

func runSubjectiveProbeWorkers(t *testing.T, ctx context.Context, store *workerruntime.PostgresStore, workers int, prefix string) []int {
	t.Helper()
	counts := make([]int, workers)
	var firstClaim, finished sync.WaitGroup
	firstClaim.Add(workers)
	for worker := range workers {
		finished.Go(func() {
			input := workerruntime.ClaimInput{QueueName: subjectiveProbeQueue, WorkerService: subjectiveProbeService,
				WorkerInstanceID: fmt.Sprintf("%s%d", prefix, worker), Limit: 1, LeaseSeconds: subjectiveProbeLeaseSeconds}
			tasks, err := store.ClaimAcrossTenants(ctx, subjectiveProbePlatform, input)
			// 各实例持有首条不同租约再开始执行，避免快机器上首个实例独自清空队列。
			firstClaim.Done()
			firstClaim.Wait()
			if err != nil || len(tasks) != 1 {
				t.Errorf("%s initial claim: tasks=%d err=%v", input.WorkerInstanceID, len(tasks), err)
				return
			}
			for len(tasks) == 1 {
				if err := executeSubjectiveProbeTask(ctx, store, input, tasks[0]); err != nil {
					t.Errorf("%s execute: %v", input.WorkerInstanceID, err)
					return
				}
				counts[worker]++
				tasks, err = store.ClaimAcrossTenants(ctx, subjectiveProbePlatform, input)
				if err != nil {
					t.Errorf("%s claim: %v", input.WorkerInstanceID, err)
					return
				}
			}
		})
	}
	finished.Wait()
	return counts
}

func executeSubjectiveProbeTask(ctx context.Context, store *workerruntime.PostgresStore, input workerruntime.ClaimInput, task workerruntime.Task) error {
	if task.TenantID != subjectiveProbeSchool {
		return fmt.Errorf("claimed task from unexpected tenant %s", task.TenantID)
	}
	_, err := store.Heartbeat(ctx, task.TenantID, task.ID, workerruntime.HeartbeatInput{
		LeaseToken: task.LeaseToken, WorkerService: input.WorkerService, WorkerInstanceID: input.WorkerInstanceID,
		State: workerruntime.StatusRunning, LeaseSeconds: input.LeaseSeconds,
	})
	if err != nil {
		return err
	}
	select {
	case <-time.After(subjectiveProbeExecution):
	case <-ctx.Done():
		return ctx.Err()
	}
	_, err = store.Complete(ctx, task.TenantID, task.ID, workerruntime.CompleteInput{
		LeaseToken: task.LeaseToken, ResultSchemaVersion: "subjective-grade-v1",
		Result: map[string]any{"test": true}, DurationMS: int(subjectiveProbeExecution.Milliseconds()),
	})
	return err
}
