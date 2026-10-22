package subjective

import (
	"context"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
	"edugrade-enterprise/services/api-gateway/internal/modelcalibration"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type referenceMaterialSource interface {
	Resolve(context.Context, string, string, string, *paper.ReferenceMaterialBinding) (paper.ReferenceMaterials, []paper.ResolvedReferenceImage, error)
	ScoringRunSnapshot(context.Context, auth.AccessScope, string, string) (string, string, error)
	FrozenImage(context.Context, string, string, string, string, string) ([]byte, error)
	ForSnapshot(context.Context, string, string, string, string) (paper.ReferenceMaterials, error)
}

type Handler struct {
	imageModelResolver         func(context.Context, string) (*ImageModelConfig, error)
	imageModelByIDResolver     func(context.Context, string, string, string) (*ImageModelConfig, error)
	referenceMaterials         referenceMaterialSource
	referenceMaterialPreflight ReferenceMaterialPreflight
	store                      Store
	adapter                    LLMGradingAdapter
	audit                      auth.AuditRecorder
	runtime                    workerruntime.Store
	eligibility                EligibilityGate
	evaluation                 EvaluationEvidenceProvider
	calibration                CalibrationEvidenceProvider
	parserQuality              ParserQualityProvider
	mathV2Enabled              bool
	mathAdapter                LLMGradingAdapter
	mathEvidence               MathEvidenceSource
	activeCrops                *ActiveCropResolver
}

func NewHandler(store Store, adapter LLMGradingAdapter, audit auth.AuditRecorder) *Handler {
	return &Handler{store: store, adapter: adapter, audit: audit}
}
func (h *Handler) WithReferenceMaterials(s *paper.ReferenceMaterialsService) *Handler {
	if s == nil {
		h.referenceMaterials = nil
	} else {
		h.referenceMaterials = s
	}
	return h
}

// ReferenceMaterialPreflight 只核对当前部署与 image 审批；不创建运行、执行许可或供应商请求。
// 预检通过后配置仍可能变化，实际消费许可时必须再次按运行身份检查。
type ReferenceMaterialPreflight func(context.Context, string, Context, ModelPolicy, string) error

func (h *Handler) WithReferenceMaterialPreflight(preflight ReferenceMaterialPreflight) *Handler {
	h.referenceMaterialPreflight = preflight
	return h
}

func (h *Handler) WithWorkerRuntimeStore(runtime workerruntime.Store) *Handler {
	h.runtime = runtime
	return h
}

func (h *Handler) WithEligibilityGate(gate EligibilityGate) *Handler {
	h.eligibility = gate
	return h
}

// NewShadowPanelOrchestrator 将评审组调用接入旧同步流程和 worker 评分流程所用的同一 A14 准入路径。
// 生产门禁缺失时拒绝执行；此处不改变既有 HTTP 路由。
func (h *Handler) NewShadowPanelOrchestrator(agents PanelAgents) (*PanelOrchestrator, error) {
	if h == nil || h.eligibility == nil {
		return nil, ErrPanelConfiguration
	}
	store, ok := h.store.(PanelPersistence)
	if !ok {
		return nil, ErrPanelConfiguration
	}
	panel, err := NewPanelOrchestrator(store, agents, h.decideEligibility)
	if err != nil {
		return nil, err
	}
	if h.mathV2Enabled {
		panel.math = &panelMathRuntime{
			prepare: h.prepareMathEvidence,
			input:   h.buildPanelMathInput,
			settle:  h.settleMathOutput,
		}
	}
	return panel, nil
}

// NewApprovedPanelOrchestrator 只启用有评估依据的策略入口，不改变或自动启用既有评分路由。
func (h *Handler) NewApprovedPanelOrchestrator(agents PanelAgents, policies PanelPolicyStore, approvals PanelModelApprovalReader) (*PanelOrchestrator, error) {
	if policies == nil || approvals == nil {
		return nil, ErrPanelConfiguration
	}
	panel, err := h.NewShadowPanelOrchestrator(agents)
	if err != nil {
		return nil, err
	}
	return panel.WithApprovedPolicyStore(policies).WithManagedApprovals(approvals), nil
}

// EvaluationEvidenceProvider 只提供 A14 所需的聚合、对齐离线证据。
// 它不能暴露答案，也不能自行使模型获得资格；准入策略仍是最终门禁。
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

// ParserQualityProvider 只提供当前待评答案分段的专用解析器质量。
// 证据缺失时必须返回 nil，使 AI 准入策略放弃判断，而不把 OCR 文本质量当作数学、化学、图示或表格解析质量。
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
