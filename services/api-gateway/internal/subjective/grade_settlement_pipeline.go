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

// Settle applies the common synchronous/asynchronous result policy in one
// place. Persistence is deliberately left to the caller so transaction and
// idempotency behavior remain unchanged.
func (p *GradeSettlementPipeline) Settle(ctx context.Context, input GradeSettlementInput, output *AdapterOutput) error {
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
