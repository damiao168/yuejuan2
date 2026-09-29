package server

import (
	"context"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/imagequality"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type workerSourceLeaseRenewer struct {
	imageQuality imagequality.Store
}

// 只有 image_quality_run 还维护独立来源租约；其他任务由 worker runtime 自身续租，避免重复写入不同状态机。
func (r workerSourceLeaseRenewer) RenewSourceLease(ctx context.Context, task workerruntime.Task) error {
	if task.SourceType != "image_quality_run" {
		return nil
	}
	if r.imageQuality == nil || task.LeaseExpiresAt == nil {
		return workerruntime.ErrInvalidInput
	}
	_, err := r.imageQuality.RenewLease(
		ctx,
		task.TenantID,
		task.SourceID,
		task.WorkerInstanceID,
		task.LeaseToken,
		*task.LeaseExpiresAt,
		task.AttemptCount,
	)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, imagequality.ErrNotFound):
		return workerruntime.ErrNotFound
	case errors.Is(err, imagequality.ErrInvalidInput):
		return workerruntime.ErrInvalidInput
	case errors.Is(err, imagequality.ErrLeaseExpired):
		return workerruntime.ErrLeaseExpired
	case errors.Is(err, imagequality.ErrLeaseMismatch):
		return workerruntime.ErrLeaseMismatch
	case errors.Is(err, imagequality.ErrInvalidTransition):
		return workerruntime.ErrInvalidTransition
	case errors.Is(err, imagequality.ErrConflict):
		return workerruntime.ErrConflict
	default:
		return err
	}
}
