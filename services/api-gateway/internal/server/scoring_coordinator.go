package server

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"time"
)

// 后台维护上下文允许枚举租户，但每次来源读写仍显式限定任务的 tenant_id。
// 关闭时先停协调器，再由 Infrastructure 逆序释放数据库。
func (i *Infrastructure) startScoringCoordinator(coordinator *grading.ScoringCoordinator) {
	if coordinator == nil {
		return
	}
	ctx, cancel := context.WithCancel(db.WithTenantMaintenance(context.Background()))
	done := make(chan struct{})
	go func() {
		defer close(done)
		coordinator.Run(ctx, func(err error) {
			i.Logger.Error(ctx, "scoring coordinator failed", map[string]any{"event": "scoring_coordinator_failed", "error": err.Error()})
		})
	}()
	i.cleanup = append(i.cleanup, func() error {
		cancel()
		select {
		case <-done:
			return nil
		case <-time.After(6 * time.Second):
			return context.DeadlineExceeded
		}
	})
}
