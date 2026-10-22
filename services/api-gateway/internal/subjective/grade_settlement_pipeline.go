package subjective

import (
	"context"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
)

var (
	ErrGradeOutputValidation = errors.New("grade output validation failed")
	ErrGradeCalibration      = errors.New("grade calibration failed")
)

type GradeSettlementError struct {
	Kind  error
	Cause error
}

func (e *GradeSettlementError) Error() string { return e.Cause.Error() }
func (e *GradeSettlementError) Unwrap() error { return e.Cause }
func (e *GradeSettlementError) Is(target error) bool {
	return target == e.Kind || errors.Is(e.Cause, target)
}

type mathSettlementFunc func(context.Context, string, Context, ModelPolicy, *AdapterOutput) error
type calibrationSettlementFunc func(context.Context, string, string, Context, ModelPolicy, aieligibility.Decision, *AdapterOutput) error

type GradeSettlementPipeline struct {
	settleMath        mathSettlementFunc
	recordCalibration calibrationSettlementFunc
}

type GradeSettlementInput struct {
	TenantID string
	RunID    string
	Context  Context
	Policy   ModelPolicy
	Decision aieligibility.Decision
	MathRun  bool
}

func NewGradeSettlementPipeline(settleMath mathSettlementFunc, recordCalibration calibrationSettlementFunc) *GradeSettlementPipeline {
	return &GradeSettlementPipeline{settleMath: settleMath, recordCalibration: recordCalibration}
}

// Settle 在一处应用同步和异步共用的结果策略。
// 持久化留给调用方，以保持事务和幂等行为不变。
func (p *GradeSettlementPipeline) Settle(ctx context.Context, input GradeSettlementInput, output *AdapterOutput) error {
	if input.Context.InputVersionID != "" {
		// 回调携带的分数、正文和观察都不可信；只能用已保存观察重建冻结量规的建议。
		if output == nil || output.SchemaVersion != ImageProtocolVersion {
			return ErrInvalidModelOutput
		}
		if input.Context.AIPlan != nil {
			if err := settleImagePanelOutput(input.Context, input.Policy, output); err != nil {
				return err
			}
			// 多评聚合是本场冻结规则，不沿用单模型校准通过结论；AI_ASSIST 仍由教师确认。
			ApplyPromptGuard(output, InspectPromptInjection(input.Context.AnswerText))
			ApplyReviewPolicy(output, input.Context, input.Policy)
			return nil
		}
		output.Observation = input.Context.Observation
		if err := projectImageJudgments(output, AdapterInput{SegmentID: input.Context.SegmentID, Question: input.Context.Question, Rubric: input.Context.Rubric, CropSHA256: input.Context.CropSHA256, Observation: input.Context.Observation}); err != nil {
			return err
		}
		output.RawOutput = imageRawOutput(input.Context, output.ImageJudgments)
	}
	if input.MathRun {
		if p.settleMath == nil {
			return ErrInvalidModelOutput
		}
		if err := p.settleMath(ctx, input.TenantID, input.Context, input.Policy, output); err != nil {
			return err
		}
	} else if output == nil || ValidateOutput(*output, input.Context) != nil {
		if output != nil {
			ApplyPromptGuard(output, InspectPromptInjection(input.Context.AnswerText))
		}
		return &GradeSettlementError{Kind: ErrGradeOutputValidation, Cause: ErrInvalidModelOutput}
	}

	guard := InspectPromptInjection(input.Context.AnswerText)
	ApplyPromptGuard(output, guard)
	if !input.MathRun {
		DeriveSuggestedScore(output)
		if p.recordCalibration != nil {
			if err := p.recordCalibration(ctx, input.TenantID, input.RunID, input.Context, input.Policy, input.Decision, output); err != nil {
				return &GradeSettlementError{Kind: ErrGradeCalibration, Cause: err}
			}
		}
	}
	ApplyReviewPolicy(output, input.Context, input.Policy)
	return nil
}
