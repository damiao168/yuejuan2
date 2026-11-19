package server

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

// startWorkerExhaustionRecovery 用网关进程的维护上下文扫描失败来源。所有结算仍按记录租户执行，
// 也不依赖 Worker 继续轮询。关停时先取消扫描，数据库连接在它退出后才释放。
func (i *Infrastructure) startWorkerExhaustionRecovery() {
	ctx, cancel := context.WithCancel(db.WithTenantMaintenance(context.Background()))
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		var after workerruntime.ExhaustedSourceTask
		for ctx.Err() == nil {
			var err error
			after, err = reconcileWorkerExhaustion(ctx, i.DB, after)
			if err != nil && ctx.Err() == nil {
				i.Logger.Error(ctx, "worker exhaustion recovery failed", map[string]any{"event": "worker_exhaustion_recovery_failed", "error": err.Error()})
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	i.cleanup = append(i.cleanup, func() error {
		cancel()
		select {
		case <-done:
			return nil
		case <-time.After(5 * time.Second):
			return context.DeadlineExceeded
		}
	})
}

func reconcileWorkerExhaustion(ctx context.Context, sqlDB *sql.DB, after workerruntime.ExhaustedSourceTask) (workerruntime.ExhaustedSourceTask, error) {
	const limit = 50
	runtime := workerruntime.NewPostgresStore(sqlDB)
	captures := capture.NewPostgresStore(sqlDB)
	scores := subjective.NewPostgresStore(sqlDB)
	tasks, err := runtime.ListExhaustedSourceTasks(ctx, after, limit)
	if err != nil {
		return after, err
	}
	var first error
	for _, task := range tasks {
		// 即使维护扫描可见其他租户，写事务也只用来源记录的租户权限。
		sourceCtx, cancel := context.WithTimeout(db.WithTenant(ctx, task.TenantID), 3*time.Second)
		switch task.SourceType {
		case "capture_file":
			err = captures.ReconcileExhaustedFileTask(sourceCtx, task)
		case "subjective_grading_run":
			err = scores.ReconcileExhaustedGradingTask(sourceCtx, task)
		}
		cancel()
		if err != nil && first == nil {
			first = fmt.Errorf("reconcile exhausted %s task %s: %w", task.SourceType, task.ID, err)
		}
	}
	if len(tasks) == limit {
		return tasks[len(tasks)-1], first
	}
	return workerruntime.ExhaustedSourceTask{}, first
}
