package subjective

import (
	"context"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type workerFailureRuntime interface {
	Get(context.Context, string, string) (workerruntime.Task, error)
	Fail(context.Context, string, string, workerruntime.FailInput) (workerruntime.Task, error)
}

type atomicWorkerFailureStore interface {
	FailWorkerAtomic(context.Context, string, string, WorkerFailureInput, string, workerFailureRuntime) (WorkerFailureResult, error)
}

func workerFailureRunInput(task workerruntime.Task, input WorkerFailureInput, forcedStatus string) UpdateRunInput {
	status := RunQueued
	if task.Status == workerruntime.StatusFailed || task.Status == workerruntime.StatusDeadLetter {
		status = RunFailed
	}
	if forcedStatus != "" {
		status = forcedStatus
	}
	diagnostics, _ := diagnosticsFromFailureDetail(input.ErrorDetail)
	return UpdateRunInput{Status: status, ErrorCode: input.ErrorCode, AttemptCount: task.AttemptCount, ExecutionDiagnostics: diagnostics}
}

// FailWorkerAtomic 同时结束本次尝试和更新来源运行；旧租约被拒绝时不能污染新尝试的运行状态。
func (s *MemoryStore) FailWorkerAtomic(ctx context.Context, tenantID, runID string, input WorkerFailureInput, forcedStatus string, runtime workerFailureRuntime) (result WorkerFailureResult, err error) {
	diagnostics, validateErr := diagnosticsFromFailureDetail(input.ErrorDetail)
	if validateErr != nil {
		return result, validateErr
	}
	if !diagnosticsAllowRetry(diagnostics) {
		input.Retryable = false
	}
	if err := validateCompletionCapability(ctx, tenantID, WorkerResultInput{TaskID: input.TaskID, LeaseToken: input.LeaseToken}); err != nil {
		return result, err
	}
	runtimeStore, ok := runtime.(*workerruntime.MemoryStore)
	if !ok {
		return result, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	oldRuns := make(map[string]GradingRun, len(s.runs))
	for key, value := range s.runs {
		oldRuns[key] = value
	}
	committed := false
	defer func() {
		if !committed {
			s.runs = oldRuns
		}
	}()
	err = runtimeStore.RunAtomic(func(tx *workerruntime.MemoryTx) error {
		run, err := s.getRunLocked(tenantID, runID)
		if err != nil {
			return err
		}
		if !workerRunActive(run) {
			return errors.Join(ErrWorkerFailureRejected, workerruntime.ErrInvalidTransition)
		}
		task, err := tx.Get(ctx, tenantID, input.TaskID)
		if err != nil || task.SourceType != "subjective_grading_run" || task.SourceID != run.ID {
			return ErrWorkerTaskMismatch
		}
		if err := validateCompletionWorker(ctx, task); err != nil {
			return err
		}
		task, err = tx.Fail(ctx, tenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken,
			Retryable: input.Retryable, ErrorCode: input.ErrorCode, ErrorDetail: input.ErrorDetail, DurationMS: input.DurationMS})
		if err != nil {
			return errors.Join(ErrWorkerFailureRejected, err)
		}
		run, err = s.updateRunLocked(tenantID, runID, workerFailureRunInput(task, input, forcedStatus))
		if err != nil {
			return err
		}
		result = WorkerFailureResult{Run: run, Task: task}
		return nil
	})
	committed = err == nil
	return result, err
}

// FailWorkerAtomic 与成功结算采用相同事务边界，失败回调的租约和运行更新必须一起生效。
func (s *PostgresStore) FailWorkerAtomic(ctx context.Context, tenantID, runID string, input WorkerFailureInput, forcedStatus string, _ workerFailureRuntime) (WorkerFailureResult, error) {
	diagnostics, validateErr := diagnosticsFromFailureDetail(input.ErrorDetail)
	if validateErr != nil {
		return WorkerFailureResult{}, validateErr
	}
	if !diagnosticsAllowRetry(diagnostics) {
		input.Retryable = false
	}
	if err := validateCompletionCapability(ctx, tenantID, WorkerResultInput{TaskID: input.TaskID, LeaseToken: input.LeaseToken}); err != nil {
		return WorkerFailureResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkerFailureResult{}, err
	}
	defer tx.Rollback()
	run, err := getRunInDB(ctx, tx, tenantID, runID, true)
	if err != nil {
		return WorkerFailureResult{}, err
	}
	if !workerRunActive(run) {
		return WorkerFailureResult{}, errors.Join(ErrWorkerFailureRejected, workerruntime.ErrInvalidTransition)
	}
	task, err := workerruntime.LockTaskForCompletionInTx(ctx, tx, tenantID, input.TaskID, input.LeaseToken)
	if err != nil {
		return WorkerFailureResult{}, errors.Join(ErrWorkerFailureRejected, err)
	}
	if task.SourceType != "subjective_grading_run" || task.SourceID != run.ID {
		return WorkerFailureResult{}, ErrWorkerTaskMismatch
	}
	if err := validateCompletionWorker(ctx, task); err != nil {
		return WorkerFailureResult{}, err
	}
	if input.ErrorDetail["panel_execution"] != nil {
		if err := recordFailedImagePanelCallsInTx(ctx, tx, tenantID, run, input.ErrorDetail["panel_execution"]); err != nil {
			return WorkerFailureResult{}, err
		}
	}
	task, err = workerruntime.FailTaskInTx(ctx, tx, tenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken,
		Retryable: input.Retryable, ErrorCode: input.ErrorCode, ErrorDetail: input.ErrorDetail, DurationMS: input.DurationMS})
	if err != nil {
		return WorkerFailureResult{}, errors.Join(ErrWorkerFailureRejected, err)
	}
	run, err = updateRunInDB(ctx, tx, tenantID, runID, workerFailureRunInput(task, input, forcedStatus))
	if err != nil {
		return WorkerFailureResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkerFailureResult{}, err
	}
	return WorkerFailureResult{Run: run, Task: task}, nil
}
