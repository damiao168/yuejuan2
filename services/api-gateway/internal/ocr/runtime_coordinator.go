package ocr

import (
	"context"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

var errAtomicRuntimeUnavailable = errors.New("atomic OCR runtime coordination is unavailable")

type atomicRuntimeCoordinator interface {
	CreateTask(ctx context.Context, tenantID, submissionID, actorID string, input CreateTaskInput) (Task, error)
	CompleteTask(ctx context.Context, tenantID, taskID string, input CompleteTaskInput) (Task, error)
	FailTask(ctx context.Context, tenantID, taskID string, input FailTaskInput) (Task, error)
}

// 只有生产 PostgreSQL 或成对的内存存储支持原子协调；不匹配时拒绝降级为两次独立写入。
func newAtomicRuntimeCoordinator(source Store, runtime workerruntime.Store) atomicRuntimeCoordinator {
	switch sourceStore := source.(type) {
	case *PostgresStore:
		if _, ok := runtime.(*workerruntime.PostgresStore); ok {
			return &postgresRuntimeCoordinator{source: sourceStore}
		}
	case *MemoryStore:
		if runtimeStore, ok := runtime.(*workerruntime.MemoryStore); ok {
			return &memoryRuntimeCoordinator{source: sourceStore, runtime: runtimeStore}
		}
	}
	return nil
}

type memoryRuntimeCoordinator struct {
	source  *MemoryStore
	runtime *workerruntime.MemoryStore
	hook    func(string) error
}

func (c *memoryRuntimeCoordinator) runHook(point string) error {
	if c.hook == nil {
		return nil
	}
	return c.hook(point)
}

func (c *memoryRuntimeCoordinator) CreateTask(ctx context.Context, tenantID, submissionID, actorID string, input CreateTaskInput) (Task, error) {
	var sourceTask Task
	err := c.source.runAtomic(func(sourceTx *memoryTx) error {
		return c.runtime.RunAtomic(func(runtimeTx *workerruntime.MemoryTx) error {
			var err error
			sourceTask, err = sourceTx.CreateTask(ctx, tenantID, submissionID, actorID, input)
			if err != nil {
				return err
			}
			if err = c.runHook("create.after_source"); err != nil {
				return err
			}
			if _, err = runtimeTx.CreateTask(ctx, tenantID, actorID, runtimeCreateInput(sourceTask, input.SourceFileAssetIDs...)); err != nil {
				return err
			}
			return c.runHook("create.after_runtime")
		})
	})
	return sourceTask, err
}

// 调用时仍持有两侧事务锁；来源不匹配或读取失败时，必须先返回错误，不能更新来源任务。
func getOCRRuntimeTask(ctx context.Context, runtimeTx *workerruntime.MemoryTx, tenantID, runtimeTaskID, sourceTaskID string) (workerruntime.Task, error) {
	runtimeTask, err := runtimeTx.Get(ctx, tenantID, runtimeTaskID)
	if err != nil {
		return workerruntime.Task{}, err
	}
	if runtimeTask.SourceType != "ocr_task" || runtimeTask.SourceID != sourceTaskID {
		return workerruntime.Task{}, workerruntime.ErrInvalidInput
	}
	return runtimeTask, nil
}

func (c *memoryRuntimeCoordinator) CompleteTask(ctx context.Context, tenantID, taskID string, input CompleteTaskInput) (Task, error) {
	var sourceTask Task
	err := c.source.runAtomic(func(sourceTx *memoryTx) error {
		return c.runtime.RunAtomic(func(runtimeTx *workerruntime.MemoryTx) error {
			runtimeTask, err := getOCRRuntimeTask(ctx, runtimeTx, tenantID, input.RuntimeTaskID, taskID)
			if err != nil {
				return err
			}
			sourceTask, err = sourceTx.CompleteTask(ctx, tenantID, taskID, input)
			if err != nil {
				return err
			}
			if err = c.runHook("complete.after_source"); err != nil {
				return err
			}
			if _, err = runtimeTx.Complete(ctx, tenantID, runtimeTask.ID, runtimeCompleteInput(sourceTask, input.RuntimeLeaseToken)); err != nil {
				return err
			}
			return c.runHook("complete.after_runtime")
		})
	})
	return sourceTask, err
}

func (c *memoryRuntimeCoordinator) FailTask(ctx context.Context, tenantID, taskID string, input FailTaskInput) (Task, error) {
	var sourceTask Task
	err := c.source.runAtomic(func(sourceTx *memoryTx) error {
		return c.runtime.RunAtomic(func(runtimeTx *workerruntime.MemoryTx) error {
			runtimeTask, err := getOCRRuntimeTask(ctx, runtimeTx, tenantID, input.RuntimeTaskID, taskID)
			if err != nil {
				return err
			}
			sourceTask, err = sourceTx.FailTask(ctx, tenantID, taskID, input.ErrorMessage)
			if err != nil {
				return err
			}
			if err = c.runHook("fail.after_source"); err != nil {
				return err
			}
			_, err = runtimeTx.Fail(ctx, tenantID, runtimeTask.ID, workerruntime.FailInput{
				LeaseToken: input.RuntimeLeaseToken, Retryable: input.Retryable,
				ErrorCode: input.ErrorMessage, ErrorDetail: map[string]any{"source_type": "ocr_task"},
			})
			if err != nil {
				return err
			}
			return c.runHook("fail.after_runtime")
		})
	})
	return sourceTask, err
}
