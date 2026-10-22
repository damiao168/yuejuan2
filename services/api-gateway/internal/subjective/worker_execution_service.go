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
// 准备阶段在同一事务内确认当前租约并标记 processing，再检查版本、证据和准入。
// 成功结果的回放由 completion 入口处理；此入口不能恢复成功任务的执行权限。
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
	processingStore, ok := s.store.(interface {
		MarkWorkerProcessing(context.Context, string, string, string, string, workerExecutionRuntime) (GradingRun, workerruntime.Task, error)
	})
	if !ok {
		return prepared, ErrInvalidInput
	}
	run, task, err = processingStore.MarkWorkerProcessing(ctx, input.TenantID, run.ID, input.TaskID, input.LeaseToken, s.runtime)
	if err != nil {
		return prepared, err
	}
	if input.Result != nil && !workerResultMatchesRun(run, *input.Result) {
		return prepared, &WorkerResultVersionError{}
	}
	var value Context
	if loader, ok := s.store.(interface {
		LoadRunContext(context.Context, string, GradingRun) (Context, error)
	}); ok {
		value, err = loader.LoadRunContext(ctx, input.TenantID, run)
	} else {
		value, err = s.store.LoadContext(ctx, input.TenantID, run.AnswerSegmentID)
	}
	if err != nil {
		if errors.Is(err, ErrWorkerResultVersionConflict) {
			s.failPrepared(ctx, input, run.ID, RunConflict, "result_version_conflict", nil)
		}
		return prepared, err
	}
	// 队列期间题目快照、答案或冻结细则可能更新；旧运行不能以新内容调用模型或接受迟到结果。
	if !runContextVersionMatches(run, value) {
		s.failPrepared(ctx, input, run.ID, RunConflict, "result_version_conflict", nil)
		return prepared, &WorkerResultVersionError{}
	}
	if s.prepareMath != nil {
		if err := s.prepareMath(ctx, input.TenantID, &value); err != nil {
			s.failMathEvidence(ctx, input, run, task, err)
			return prepared, err
		}
	}
	if err := ensureRunMathBinding(run, value); err != nil {
		s.failPrepared(ctx, input, run.ID, RunConflict, "math_evidence_version_conflict", nil)
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
		s.failPrepared(ctx, input, run.ID, RunFailed, "ai_eligibility_abstained", map[string]any{"decision_id": decision.ID})
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

func runContextVersionMatches(run GradingRun, value Context) bool {
	return run.ExamQuestionSnapshotID == value.AssessmentSnapshot.ID && run.AnswerVersion == value.AnswerVersion &&
		run.QuestionID == value.Question.ID && run.RubricVersion == value.Rubric.Version
}

func (s *WorkerExecutionService) failMathEvidence(ctx context.Context, input WorkerPrepareInput, run GradingRun, _ workerruntime.Task, cause error) {
	status, code := RunFailed, "math_evidence_unavailable"
	if isMathRevisionConflict(cause) {
		status, code = RunConflict, "math_evidence_version_conflict"
	} else if !errors.Is(cause, ErrActiveCropUnavailable) {
		return
	}
	s.failPrepared(ctx, input, run.ID, status, code, nil)
}

// 准入检查和模型调用可能跨过租约更新；旧尝试的错误只能在仍持有租约时提交到运行事实。
func (s *WorkerExecutionService) failPrepared(ctx context.Context, input WorkerPrepareInput, runID, status, code string, detail map[string]any) {
	if store, ok := s.store.(atomicWorkerFailureStore); ok {
		_, _ = store.FailWorkerAtomic(ctx, input.TenantID, runID, WorkerFailureInput{TaskID: input.TaskID,
			LeaseToken: input.LeaseToken, ErrorCode: code, ErrorDetail: detail, DurationMS: input.DurationMS}, status, s.runtime)
	}
}
