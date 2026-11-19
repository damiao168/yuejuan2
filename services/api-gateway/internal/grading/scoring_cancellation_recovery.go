package grading

import (
	"context"
	"errors"
	"fmt"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func cancelScoringWorkerTasks(ctx context.Context, runtime workerruntime.Store, tenantID string, taskIDs []string) error {
	for _, taskID := range taskIDs {
		_, err := runtime.Cancel(ctx, tenantID, taskID)
		// 事务内发现任务已结束时 Cancel 返回无效转换。完成先到和另一实例先取消都可以继续收尾。
		if err != nil && !errors.Is(err, workerruntime.ErrInvalidTransition) {
			return fmt.Errorf("cancel scoring worker task %s: %w", taskID, err)
		}
	}
	return nil
}

const scoringCancellationPageSize = 20

type pendingScoringCancellation struct {
	tenantID, runID string
	updatedAt       time.Time
}

func (c *ScoringCoordinator) resumeScoringCancellations(ctx context.Context, after pendingScoringCancellation) (pendingScoringCancellation, error) {
	// 后台仅接续已获授权且已持久化的取消，每次写入仍按记录的租户和运行 ID 限定。
	var afterTime any
	if after.runID != "" {
		afterTime = after.updatedAt
	}
	rows, err := c.store.db.QueryContext(ctx, `
SELECT r.tenant_id::text,r.id::text,r.updated_at
FROM scoring_run r JOIN tenant t ON t.id=r.tenant_id
WHERE r.status='cancelling' AND r.deleted_at IS NULL AND t.status='active' AND t.deleted_at IS NULL
  AND ($1::timestamptz IS NULL OR (r.updated_at,r.id)>($1::timestamptz,NULLIF($2,'')::uuid))
ORDER BY r.updated_at,r.id LIMIT $3`, afterTime, after.runID, scoringCancellationPageSize)
	if err != nil {
		return after, err
	}
	defer rows.Close()
	var pending []pendingScoringCancellation
	for rows.Next() {
		var run pendingScoringCancellation
		if err := rows.Scan(&run.tenantID, &run.runID, &run.updatedAt); err != nil {
			return after, err
		}
		pending = append(pending, run)
	}
	if err := rows.Err(); err != nil {
		return after, err
	}
	if err := rows.Close(); err != nil {
		return after, err
	}

	// 游标越过受阻记录，到末页后从头重试，避免为调整扫描顺序再次等待同一行锁。
	var next pendingScoringCancellation
	if len(pending) == scoringCancellationPageSize {
		next = pending[len(pending)-1]
	}
	var first error
	for _, run := range pending {
		if ctx.Err() != nil {
			break
		}
		runCtx, cancel := context.WithTimeout(db.WithTenant(ctx, run.tenantID), 10*time.Second)
		err := c.resumeScoringCancellation(runCtx, run.tenantID, run.runID)
		cancel()
		if err != nil && first == nil {
			first = fmt.Errorf("resume scoring cancellation %s: %w", run.runID, err)
		}
	}
	return next, first
}

func (c *ScoringCoordinator) resumeScoringCancellation(ctx context.Context, tenantID, runID string) error {
	run, tasks, err := c.store.BeginScoringRunCancellation(ctx, tenantID, runID)
	if err != nil || run.Status == "cancelled" {
		return err
	}
	if err := cancelScoringWorkerTasks(ctx, c.runtime, tenantID, tasks); err != nil {
		return err
	}
	_, err = c.store.FinalizeScoringRunCancellation(ctx, tenantID, runID)
	return err
}
