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

// EligibilityAbstainedError retains the admission decision for callers that
// need to report or audit it without coupling the workflow to HTTP concerns.
type EligibilityAbstainedError struct {
	Decision aieligibility.Decision
}

func (e *EligibilityAbstainedError) Error() string { return ErrAIEligibilityAbstained.Error() }
func (e *EligibilityAbstainedError) Unwrap() error { return ErrAIEligibilityAbstained }

// WorkerResultVersionError distinguishes the math envelope constraint while
// retaining one application-level conflict category for handlers.
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

// Prepare owns the shared ExecuteWorker/CompleteWorker admission path. Its
// mutation order intentionally matches the former handlers: the run is marked
// processing before result-version, context, evidence, and eligibility checks.
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
