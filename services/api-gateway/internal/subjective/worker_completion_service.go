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

type atomicWorkerCompletionStore interface {
	CompleteWorkerAtomic(context.Context, string, string, WorkerExecutionContext, WorkerResultInput, Grade, workerCompletionRuntime) (WorkerCompletionResult, error)
}

type WorkerFailureResult struct {
	Run  GradingRun
	Task workerruntime.Task
}

func NewWorkerCompletionService(store workerCompletionStore, runtime workerCompletionRuntime) *WorkerCompletionService {
	return &WorkerCompletionService{store: store, runtime: runtime}
}

// Complete 把当前租约、成绩和运行终态作为一次提交；任一步失败都不能留下旧 worker 的成绩。
func (s *WorkerCompletionService) Complete(ctx context.Context, tenantID, actorID string, prepared WorkerExecutionContext, input WorkerResultInput, output AdapterOutput) (WorkerCompletionResult, error) {
	if result, replay, err := s.Replay(ctx, tenantID, actorID, prepared.Run.ID, input); replay || err != nil {
		return result, err
	}
	store, ok := s.store.(atomicWorkerCompletionStore)
	if !ok {
		return WorkerCompletionResult{}, errors.Join(ErrWorkerCompletionRejected, ErrInvalidInput)
	}
	return store.CompleteWorkerAtomic(ctx, tenantID, actorID, prepared, input, successfulGrade(prepared.Context, prepared.Policy, prepared.Run.ID, output), s.runtime)
}

// Replay 只用于完成入口。已接受的相同结果沿用保存成绩，不重新调用模型或改写冻结证据。
func (s *WorkerCompletionService) Replay(ctx context.Context, tenantID, actorID, runID string, input WorkerResultInput) (WorkerCompletionResult, bool, error) {
	task, err := s.runtime.Get(ctx, tenantID, input.TaskID)
	if err != nil {
		return WorkerCompletionResult{}, false, err
	}
	if task.Status != workerruntime.StatusSucceeded {
		return WorkerCompletionResult{}, false, nil
	}
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return WorkerCompletionResult{}, true, err
	}
	if input.LeaseToken == "" || task.LeaseToken != input.LeaseToken || task.SourceType != "subjective_grading_run" || task.SourceID != run.ID {
		return WorkerCompletionResult{}, true, errors.Join(ErrWorkerCompletionRejected, workerruntime.ErrLeaseMismatch)
	}
	grade, err := s.store.GetGradeByAdapterRequestID(ctx, tenantID, run.RequestID)
	if err != nil {
		return WorkerCompletionResult{}, true, err
	}
	prepared := WorkerExecutionContext{Run: run, Task: task}
	if err := validateWorkerCompletion(run, task, prepared, input, grade); err != nil {
		return WorkerCompletionResult{}, true, err
	}
	store, ok := s.store.(atomicWorkerCompletionStore)
	if !ok {
		return WorkerCompletionResult{}, true, ErrInvalidInput
	}
	result, err := store.CompleteWorkerAtomic(ctx, tenantID, actorID, prepared, input, grade, s.runtime)
	return result, true, err
}

// Reject 原子记录不可重试的结算失败；调用方只有在返回 nil 后才能宣称已记录任务失败。
func (s *WorkerCompletionService) Reject(ctx context.Context, tenantID string, prepared WorkerExecutionContext, input WorkerResultInput, runStatus, code string, cause error) error {
	detail := map[string]any{}
	kind := "schema_invalid"
	if errors.Is(cause, ErrMathHumanReviewRequired) || isMathRevisionConflict(cause) {
		kind = "evidence_invalid"
	}
	if diagnostics := diagnosticsFromOutput(input.Output, kind); diagnostics != nil {
		detail["execution_diagnostics"] = diagnostics
	}
	if cause != nil {
		detail["reason"] = cause.Error()
	}
	if store, ok := s.store.(atomicWorkerFailureStore); ok {
		_, err := store.FailWorkerAtomic(ctx, tenantID, prepared.Run.ID, WorkerFailureInput{TaskID: input.TaskID,
			LeaseToken: input.LeaseToken, ErrorCode: code, ErrorDetail: detail, DurationMS: input.DurationMS}, runStatus, s.runtime)
		return err
	}
	return ErrInvalidInput
}

func (s *WorkerCompletionService) Fail(ctx context.Context, tenantID, runID string, input WorkerFailureInput) (WorkerFailureResult, error) {
	store, ok := s.store.(atomicWorkerFailureStore)
	if !ok {
		return WorkerFailureResult{}, ErrInvalidInput
	}
	return store.FailWorkerAtomic(ctx, tenantID, runID, input, "", s.runtime)
}
