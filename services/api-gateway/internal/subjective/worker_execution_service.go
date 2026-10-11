package subjective

import (
	"context"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

var (
	ErrWorkerTaskMismatch          = errors.New("subjective worker task mismatch")
	ErrWorkerLeaseMismatch         = errors.New("subjective worker lease mismatch")
	ErrWorkerResultVersionConflict = errors.New("subjective worker result version conflict")
	ErrWorkerMathBindingConflict   = errors.New("subjective worker math binding conflict")
	ErrAIEligibilityAbstained      = errors.New("subjective ai eligibility abstained")
)

// EligibilityAbstainedError 保留准入决定，供调用方报告或审计，而无需将流程耦合到 HTTP。
type EligibilityAbstainedError struct {
	Decision aieligibility.Decision
}

func (e *EligibilityAbstainedError) Error() string { return ErrAIEligibilityAbstained.Error() }
func (e *EligibilityAbstainedError) Unwrap() error { return ErrAIEligibilityAbstained }

// WorkerResultVersionError 区分数学结果封装约束，同时为 handler 保留统一的应用级冲突类别。
type WorkerResultVersionError struct {
	MathSchema bool
}

func (e *WorkerResultVersionError) Error() string { return ErrWorkerResultVersionConflict.Error() }
func (e *WorkerResultVersionError) Unwrap() error { return ErrWorkerResultVersionConflict }

type workerExecutionStore interface {
	GetRun(context.Context, string, string) (GradingRun, error)
	UpdateRun(context.Context, string, string, UpdateRunInput) (GradingRun, error)
	LoadContext(context.Context, string, string) (Context, error)
}

type workerExecutionRuntime interface {
	Get(context.Context, string, string) (workerruntime.Task, error)
	Fail(context.Context, string, string, workerruntime.FailInput) (workerruntime.Task, error)
}

type prepareMathEvidenceFunc func(context.Context, string, *Context) error
type decideEligibilityFunc func(context.Context, string, string, Context, ModelPolicy) (aieligibility.Decision, bool, error)

type WorkerExecutionService struct {
	store             workerExecutionStore
	runtime           workerExecutionRuntime
	prepareMath       prepareMathEvidenceFunc
	decideEligibility decideEligibilityFunc
}

type WorkerPrepareInput struct {
	TenantID   string
	RunID      string
	TaskID     string
	LeaseToken string
	DurationMS int
	Result     *WorkerResultInput
}

type WorkerExecutionContext struct {
	Run      GradingRun
	Task     workerruntime.Task
	Context  Context
	Policy   ModelPolicy
	Decision aieligibility.Decision
	MathRun  bool
}

func NewWorkerExecutionService(store workerExecutionStore, runtime workerExecutionRuntime, prepareMath prepareMathEvidenceFunc, decideEligibility decideEligibilityFunc) *WorkerExecutionService {
	return &WorkerExecutionService{store: store, runtime: runtime, prepareMath: prepareMath, decideEligibility: decideEligibility}
}

// Prepare 负责 ExecuteWorker/CompleteWorker 共用的准入路径。
// 变更顺序与此前 handler 保持一致：先把运行标记为 processing，再检查结果版本、上下文、证据和准入资格。
// worker 准备阶段先确认租约并标记 processing，再检查版本、证据和准入；失败路径会同步结束任务和运行。
func (s *WorkerExecutionService) Prepare(ctx context.Context, input WorkerPrepareInput) (WorkerExecutionContext, error) {
	prepared := WorkerExecutionContext{}
	run, err := s.store.GetRun(ctx, input.TenantID, input.RunID)
	if err != nil {
		return prepared, err
	}
	task, err := s.runtime.Get(ctx, input.TenantID, input.TaskID)
	if err != nil || task.SourceType != "subjective_grading_run" || task.SourceID != run.ID {
		return prepared, ErrWorkerTaskMismatch
	}
	if input.LeaseToken == "" || task.LeaseToken != input.LeaseToken || (task.Status != workerruntime.StatusLeased && task.Status != workerruntime.StatusRunning) {
		return prepared, ErrWorkerLeaseMismatch
	}
	if _, err := s.store.UpdateRun(ctx, input.TenantID, run.ID, UpdateRunInput{Status: RunProcessing, AttemptCount: task.AttemptCount}); err != nil {
		return prepared, err
	}
	if input.Result != nil && !workerResultMatchesRun(run, *input.Result) {
		return prepared, &WorkerResultVersionError{}
	}
	value, err := s.store.LoadContext(ctx, input.TenantID, run.AnswerSegmentID)
	if err != nil {
		return prepared, err
	}
	if s.prepareMath != nil {
		if err := s.prepareMath(ctx, input.TenantID, &value); err != nil {
			s.failMathEvidence(ctx, input, run, task, err)
			return prepared, err
		}
	}
	if err := ensureRunMathBinding(run, value); err != nil {
		_, _ = s.runtime.Fail(ctx, input.TenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: false, ErrorCode: "math_evidence_version_conflict", DurationMS: input.DurationMS})
		_, _ = s.store.UpdateRun(ctx, input.TenantID, run.ID, UpdateRunInput{Status: RunConflict, ErrorCode: "math_evidence_version_conflict", AttemptCount: task.AttemptCount})
		return prepared, errors.Join(ErrWorkerMathBindingConflict, err)
	}
	policy := ModelPolicy{ModelVersion: run.ModelVersion, PromptVersion: run.PromptVersion, MinConfidence: run.MinConfidence}
	decision := aieligibility.Decision{}
	allowed := true
	if s.decideEligibility != nil {
		decision, allowed, err = s.decideEligibility(ctx, input.TenantID, run.ID, value, policy)
		if err != nil {
			return prepared, err
		}
	}
	if !allowed {
		_, _ = s.runtime.Fail(ctx, input.TenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: false, ErrorCode: "ai_eligibility_abstained", ErrorDetail: map[string]any{"decision_id": decision.ID}, DurationMS: input.DurationMS})
		_, _ = s.store.UpdateRun(ctx, input.TenantID, run.ID, UpdateRunInput{Status: RunFailed, ErrorCode: "ai_eligibility_abstained", AttemptCount: task.AttemptCount})
		return prepared, &EligibilityAbstainedError{Decision: decision}
	}
	mathRun := run.MathScoringVersion != ""
	if input.Result != nil && mathRun && input.Result.ResultSchemaVersion != "math-grade-v2" {
		return prepared, &WorkerResultVersionError{MathSchema: true}
	}
	return WorkerExecutionContext{Run: run, Task: task, Context: value, Policy: policy, Decision: decision, MathRun: mathRun}, nil
}

func workerResultMatchesRun(run GradingRun, input WorkerResultInput) bool {
	return input.ResultSchemaVersion != "" && input.DurationMS >= 0 && input.Output.RequestID == run.RequestID &&
		input.Output.ModelVersion == run.ModelVersion && input.Output.PromptVersion == run.PromptVersion && input.Output.RubricVersion == run.RubricVersion
}

func (s *WorkerExecutionService) failMathEvidence(ctx context.Context, input WorkerPrepareInput, run GradingRun, task workerruntime.Task, cause error) {
	status, code := RunFailed, "math_evidence_unavailable"
	if isMathRevisionConflict(cause) {
		status, code = RunConflict, "math_evidence_version_conflict"
	} else if !errors.Is(cause, ErrActiveCropUnavailable) {
		return
	}
	_, _ = s.runtime.Fail(ctx, input.TenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: false, ErrorCode: code, DurationMS: input.DurationMS})
	_, _ = s.store.UpdateRun(ctx, input.TenantID, run.ID, UpdateRunInput{Status: status, ErrorCode: code, AttemptCount: task.AttemptCount})
}
