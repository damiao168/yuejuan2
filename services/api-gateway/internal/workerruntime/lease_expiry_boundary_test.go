package workerruntime

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// 固定比较时刻，不靠 sleep 猜测边界；落库检查与内存执行授权必须都只接受尚未到期的租约。
func TestLeaseExpiryBoundaryMatchesMemoryExecutionAuthorization(t *testing.T) {
	expires := time.Date(2026, time.October, 15, 0, 0, 0, 0, time.UTC)
	for _, status := range []string{StatusLeased, StatusRunning} {
		for _, delta := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
			t.Run(fmt.Sprintf("%s/%s", status, delta), func(t *testing.T) {
				task := Task{ID: "task", TenantID: "tenant", Status: status, LeaseToken: "lease", LeaseExpiresAt: &expires, WorkerService: "worker", WorkerInstanceID: "instance"}
				store := NewMemoryStore()
				store.tasks[task.ID] = task
				now := expires.Add(delta)
				err := validateTaskLeaseAt(task, task.LeaseToken, now)
				_, authErr := store.AuthorizeLease(context.Background(), task.ID, task.LeaseToken, task.WorkerService, task.WorkerInstanceID, now)
				if delta < 0 {
					if err != nil || authErr != nil {
						t.Fatalf("unexpired lease rejected: validation=%v authorization=%v", err, authErr)
					}
				} else if !errors.Is(err, ErrLeaseExpired) || !errors.Is(authErr, ErrLeaseMismatch) {
					t.Fatalf("expired lease accepted: validation=%v authorization=%v", err, authErr)
				}
			})
		}
	}
}

// 数据库驱动只提供锁后时钟恰好等于到期时刻；任何 UPDATE 都是多余操作，必须回滚且不返回部分任务。
func TestPostgresMutationsRejectExactLeaseExpiryAfterLock(t *testing.T) {
	now := time.Date(2026, time.October, 15, 0, 0, 0, 0, time.UTC)
	for _, status := range []string{StatusLeased, StatusRunning} {
		for _, operation := range []string{"heartbeat", "complete", "fail", "completion lock"} {
			t.Run(status+"/"+operation, func(t *testing.T) {
				state := &batch48RuntimeSQL{steps: []batch48RuntimeStep{
					{kind: "query", fragment: "FOR UPDATE", args: []driver.Value{"tenant", "task"}, columns: 35, rows: [][]driver.Value{batch48RuntimeTaskRow(status, now, now)}},
					{kind: "query", fragment: "SELECT clock_timestamp()", args: []driver.Value{}, columns: 1, rows: [][]driver.Value{{now}}},
				}}
				db := batch48RuntimeDB(t, state)
				store := NewPostgresStore(db)
				var task Task
				var err error
				switch operation {
				case "heartbeat":
					task, err = store.Heartbeat(t.Context(), "tenant", "task", HeartbeatInput{LeaseToken: "lease", WorkerService: "worker", WorkerInstanceID: "instance", LeaseSeconds: 60})
				case "complete":
					task, err = store.Complete(t.Context(), "tenant", "task", CompleteInput{LeaseToken: "lease", ResultSchemaVersion: "result.v1", Result: map[string]any{"accepted": true}})
				case "fail":
					task, err = store.Fail(t.Context(), "tenant", "task", FailInput{LeaseToken: "lease", ErrorCode: "provider_failed", Retryable: true})
				case "completion lock":
					tx, beginErr := db.BeginTx(t.Context(), nil)
					if beginErr != nil {
						t.Fatal(beginErr)
					}
					task, err = LockTaskForCompletionInTx(t.Context(), tx, "tenant", "task", "lease")
					_ = tx.Rollback()
				}
				if !errors.Is(err, ErrLeaseExpired) || !reflect.DeepEqual(task, Task{}) || state.next != len(state.steps) || !reflect.DeepEqual(state.events, []string{"begin", "query", "query", "rollback"}) {
					t.Fatalf("exact expiry mutated task: result=%#v error=%v steps=%d events=%v", task, err, state.next, state.events)
				}
			})
		}
	}
}
