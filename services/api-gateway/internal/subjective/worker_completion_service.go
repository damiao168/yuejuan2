package subjective

import (
	"context"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

var (
	ErrWorkerCompletionRejected = errors.New("subjective worker completion rejected")
	ErrWorkerFailureRejected    = errors.New("subjective worker failure rejected")
)

type workerCompletionStore interface {
	GetRun(context.Context, string, string) (GradingRun, error)
	GetGradeByAdapterRequestID(context.Context, string, string) (Grade, error)
	CreateGrade(context.Context, string, string, Grade) (Grade, error)
	UpdateRun(context.Context, string, string, UpdateRunInput) (GradingRun, error)
}

type workerCompletionRuntime interface {
	Get(context.Context, string, string) (workerruntime.Task, error)
	Complete(context.Context, string, string, workerruntime.CompleteInput) (workerruntime.Task, error)
	Fail(context.Context, string, string, workerruntime.FailInput) (workerruntime.Task, error)
}

type WorkerCompletionService struct {
	store   workerCompletionStore
	runtime workerCompletionRuntime
}

type WorkerCompletionResult struct {
	Run   GradingRun
	Grade Grade
	Task  workerruntime.Task
}

type WorkerFailureResult struct {
	Run  GradingRun
	Task workerruntime.Task
}

func NewWorkerCompletionService(store workerCompletionStore, runtime workerCompletionRuntime) *WorkerCompletionService {
	return &WorkerCompletionService{store: store, runtime: runtime}
}

func (s *WorkerCompletionService) Complete(ctx context.Context, tenantID, actorID string, prepared WorkerExecutionContext, input WorkerResultInput, output AdapterOutput) (WorkerCompletionResult, error) {
	grade, err := s.store.GetGradeByAdapterRequestID(ctx, tenantID, prepared.Run.RequestID)
	if errors.Is(err, ErrNotFound) {
		grade, err = s.store.CreateGrade(ctx, tenantID, actorID, successfulGrade(prepared.Context, prepared.Policy, prepared.Run.ID, output))
	}
	if err != nil {
		return WorkerCompletionResult{}, err
	}
	completed, err := s.runtime.Complete(ctx, tenantID, input.TaskID, workerruntime.CompleteInput{
		LeaseToken: input.LeaseToken, ResultSchemaVersion: input.ResultSchemaVersion,
		Result: map[string]any{"run_id": prepared.Run.ID, "grade_id": grade.ID}, DurationMS: input.DurationMS,
	})
	if err != nil {
		return WorkerCompletionResult{}, errors.Join(ErrWorkerCompletionRejected, err)
	}
	updated, err := s.store.UpdateRun(ctx, tenantID, prepared.Run.ID, UpdateRunInput{Status: RunSucceeded, GradeID: grade.ID, AttemptCount: completed.AttemptCount})
	if err != nil {
		return WorkerCompletionResult{}, err
	}
	return WorkerCompletionResult{Run: updated, Grade: grade, Task: completed}, nil
}

// Reject records a non-retryable settlement failure. Its callers preserve the
// former best-effort behavior by intentionally ignoring storage failures here.
func (s *WorkerCompletionService) Reject(ctx context.Context, tenantID string, prepared WorkerExecutionContext, input WorkerResultInput, runStatus, code string, cause error) {
	detail := map[string]any{}
	if cause != nil {
		detail["reason"] = cause.Error()
	}
	_, _ = s.runtime.Fail(ctx, tenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: false, ErrorCode: code, ErrorDetail: detail, DurationMS: input.DurationMS})
	_, _ = s.store.UpdateRun(ctx, tenantID, prepared.Run.ID, UpdateRunInput{Status: runStatus, ErrorCode: code, AttemptCount: prepared.Task.AttemptCount})
}

func (s *WorkerCompletionService) Fail(ctx context.Context, tenantID, runID string, input WorkerFailureInput) (WorkerFailureResult, error) {
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return WorkerFailureResult{}, err
	}
	task, err := s.runtime.Get(ctx, tenantID, input.TaskID)
	if err != nil || task.SourceType != "subjective_grading_run" || task.SourceID != run.ID {
		return WorkerFailureResult{}, ErrWorkerTaskMismatch
	}
	updatedTask, err := s.runtime.Fail(ctx, tenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: input.Retryable, ErrorCode: input.ErrorCode, ErrorDetail: input.ErrorDetail, DurationMS: input.DurationMS})
	if err != nil {
		return WorkerFailureResult{}, errors.Join(ErrWorkerFailureRejected, err)
	}
	status := RunQueued
	if updatedTask.Status == workerruntime.StatusFailed || updatedTask.Status == workerruntime.StatusDeadLetter {
		status = RunFailed
	} else if updatedTask.Status == workerruntime.StatusLeased || updatedTask.Status == workerruntime.StatusRunning {
		status = RunProcessing
	}
	updatedRun, err := s.store.UpdateRun(ctx, tenantID, run.ID, UpdateRunInput{Status: status, ErrorCode: input.ErrorCode, AttemptCount: updatedTask.AttemptCount})
	if err != nil {
		return WorkerFailureResult{}, err
	}
	return WorkerFailureResult{Run: updatedRun, Task: updatedTask}, nil
}
