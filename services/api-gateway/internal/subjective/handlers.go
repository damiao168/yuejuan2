package subjective

import (
	"context"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
	"edugrade-enterprise/services/api-gateway/internal/modelcalibration"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type Handler struct {
	store         Store
	adapter       LLMGradingAdapter
	audit         auth.AuditRecorder
	runtime       workerruntime.Store
	eligibility   EligibilityGate
	evaluation    EvaluationEvidenceProvider
	calibration   CalibrationEvidenceProvider
	parserQuality ParserQualityProvider
	mathV2Enabled bool
	mathAdapter   LLMGradingAdapter
	mathEvidence  MathEvidenceSource
	activeCrops   *ActiveCropResolver
}

func NewHandler(store Store, adapter LLMGradingAdapter, audit auth.AuditRecorder) *Handler {
	return &Handler{store: store, adapter: adapter, audit: audit}
}

func (h *Handler) WithWorkerRuntimeStore(runtime workerruntime.Store) *Handler {
	h.runtime = runtime
	return h
}

func (h *Handler) WithEligibilityGate(gate EligibilityGate) *Handler {
	h.eligibility = gate
	return h
}

// EvaluationEvidenceProvider supplies only the aggregate, aligned offline
// evidence needed by A14. It cannot expose answers or make a model eligible
// by itself; the admission policy remains the final gate.
type EvaluationEvidenceProvider interface {
	AdmissionEvidenceFor(context.Context, string, string, string, string, assessment.SubjectCode, string) (gradingevaluation.AdmissionEvidence, error)
}

func (h *Handler) WithEvaluationEvidence(provider EvaluationEvidenceProvider) *Handler {
	h.evaluation = provider
	return h
}

type CalibrationEvidenceProvider interface {
	Approved(context.Context, string, modelcalibration.Axis) (modelcalibration.ApprovedEvidence, error)
	RecordCandidate(context.Context, string, modelcalibration.RecordCandidateInput) (modelcalibration.Candidate, error)
}

func (h *Handler) WithCalibrationEvidence(provider CalibrationEvidenceProvider) *Handler {
	h.calibration = provider
	return h
}

// ParserQualityProvider supplies specialised parser quality only for the
// answer segment being considered. It must return nil when that evidence is
// absent so the AI admission policy can abstain rather than treat OCR text
// quality as a mathematical, chemical, diagram, or table parse.
type ParserQualityProvider interface {
	ParserQualityForSegment(context.Context, string, string, assessment.SubjectCode, string) (*float64, error)
}

func (h *Handler) WithParserQuality(provider ParserQualityProvider) *Handler {
	h.parserQuality = provider
	return h
}

func (h *Handler) workerExecutionService() *WorkerExecutionService {
	return NewWorkerExecutionService(h.store, h.runtime, h.prepareMathEvidence, h.decideEligibility)
}

func (h *Handler) gradeSettlementPipeline() *GradeSettlementPipeline {
	return NewGradeSettlementPipeline(h.settleMathOutput, h.recordCalibrationCandidate)
}

func (h *Handler) workerCompletionService() *WorkerCompletionService {
	return NewWorkerCompletionService(h.store, h.runtime)
}
