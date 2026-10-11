package subjective

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

type PanelAgentBinding struct {
	Adapter       LLMGradingAdapter
	Policy        ModelPolicy
	StrengthRank  int
	ModelConfigID string
	ModelName     string
	ProviderKey   string
	AdapterType   string
	BaseURL       string
}

type PanelAgents struct {
	PrimaryA PanelAgentBinding
	PrimaryB PanelAgentBinding
	Arbiter  PanelAgentBinding
}

type PanelResult struct {
	Panel    GradingPanel `json:"panel"`
	PrimaryA Grade        `json:"primary_a"`
	PrimaryB Grade        `json:"primary_b"`
	Arbiter  *Grade       `json:"arbiter,omitempty"`
}

type panelAgentResult struct {
	role  string
	run   GradingRun
	grade Grade
	err   error
}

type PanelModelApprovalReader interface {
	ListModelApprovals(context.Context, string) ([]modelgovernance.ModelApproval, error)
}

type PanelOrchestrator struct {
	store        PanelPersistence
	agents       PanelAgents
	admission    decideEligibilityFunc
	policies     PanelPolicyStore
	approvals    PanelModelApprovalReader
	approvedOnly bool
	math         *panelMathRuntime
}

func (o *PanelOrchestrator) WithApprovedPolicyStore(store PanelPolicyStore) *PanelOrchestrator {
	if o != nil {
		o.policies = store
		o.approvedOnly = true
	}
	return o
}

func (o *PanelOrchestrator) WithManagedApprovals(reader PanelModelApprovalReader) *PanelOrchestrator {
	if o != nil {
		o.approvals = reader
	}
	return o
}

// GradeWithApprovedPolicy 是采取保守拒绝的生产入口。
// 影子实验仍可使用显式冻结配置调用 Grade；此路径只接受已批准的准确学段、学科和题型原型策略中的阈值，
// 且该策略必须有已完成评估运行作为依据。
func (o *PanelOrchestrator) GradeWithApprovedPolicy(ctx context.Context, tenantID, actorID string, gradingContext Context) (PanelResult, error) {
	if o == nil || o.policies == nil || o.approvals == nil || !gradingContext.AssessmentSnapshot.EducationStage.Valid() ||
		!gradingContext.AssessmentSnapshot.SubjectCode.Valid() || strings.TrimSpace(gradingContext.AssessmentSnapshot.ArchetypeCode) == "" {
		return PanelResult{}, ErrPanelPolicyRequired
	}
	policy, err := o.policies.FindApprovedPanelPolicy(
		ctx, tenantID, gradingContext.AssessmentSnapshot.EducationStage,
		gradingContext.AssessmentSnapshot.SubjectCode, gradingContext.AssessmentSnapshot.ArchetypeCode,
	)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return PanelResult{}, ErrPanelPolicyRequired
		}
		return PanelResult{}, err
	}
	if policy.Status != PanelPolicyApproved || policy.EvaluationRunID == "" || policy.ReadinessReport == nil || !policy.ReadinessReport.Ready ||
		policy.DecisionConfig.PolicyVersion != policy.PolicyVersion || policy.ReadinessPolicy.PolicyVersion != policy.PolicyVersion ||
		policy.ModelSetReference != PanelModelSetReference(o.agents) {
		return PanelResult{}, ErrPanelPolicyRequired
	}
	if err := o.requireManagedApprovals(ctx, tenantID, gradingContext); err != nil {
		return PanelResult{}, err
	}
	return o.gradeWithConfig(ctx, tenantID, actorID, gradingContext, policy.DecisionConfig)
}

func (o *PanelOrchestrator) requireManagedApprovals(ctx context.Context, tenantID string, gradingContext Context) error {
	items, err := o.approvals.ListModelApprovals(ctx, tenantID)
	if err != nil {
		return err
	}
	modality := "text"
	if strings.TrimSpace(gradingContext.AnswerText) == "" && len(gradingContext.AnswerImageRef) > 0 {
		modality = "image"
	}
	for _, binding := range []PanelAgentBinding{o.agents.PrimaryA, o.agents.PrimaryB, o.agents.Arbiter} {
		if binding.ModelConfigID == "" {
			return ErrPanelPolicyRequired
		}
		scope := modelgovernance.ModelApprovalScope{
			ModelConfigID: binding.ModelConfigID,
			ModelVersion:  binding.Policy.ModelVersion,
			PromptVersion: binding.Policy.PromptVersion,
			RubricVersion: gradingContext.Rubric.Version,
			Subject:       gradingContext.Subject,
			Grade:         gradingContext.GradeLevel,
			QuestionType:  gradingContext.Question.QuestionType,
			Modality:      modality,
		}
		if _, err := modelgovernance.RequireActiveModelApproval(items, scope, time.Now().UTC()); err != nil {
			return ErrPanelPolicyRequired
		}
	}
	return nil
}

// 数学运行时只由 Handler 安装；Handler 负责既有的证据、有效裁剪图和冻结规则结算治理路径。
type panelMathRuntime struct {
	prepare func(context.Context, string, *Context) error
	input   func(context.Context, string, string, Context, ModelPolicy, PromptGuard, aieligibility.OutputConstraint) (AdapterInput, error)
	settle  mathSettlementFunc
}

func NewPanelOrchestrator(store PanelPersistence, agents PanelAgents, admission decideEligibilityFunc) (*PanelOrchestrator, error) {
	if store == nil || admission == nil || validatePanelAgents(agents) != nil {
		return nil, ErrPanelConfiguration
	}
	return &PanelOrchestrator{store: store, agents: agents, admission: admission}, nil
}

func validatePanelAgents(agents PanelAgents) error {
	bindings := []PanelAgentBinding{agents.PrimaryA, agents.PrimaryB, agents.Arbiter}
	for _, binding := range bindings {
		if binding.Adapter == nil || ValidatePolicy(binding.Policy) != nil || binding.StrengthRank <= 0 {
			return ErrPanelConfiguration
		}
		if governed, ok := binding.Adapter.(GovernedPolicyProvider); ok && governed.Policy() != binding.Policy {
			return ErrPanelConfiguration
		}
	}
	if agents.Arbiter.StrengthRank <= agents.PrimaryA.StrengthRank || agents.Arbiter.StrengthRank <= agents.PrimaryB.StrengthRank {
		return ErrPanelConfiguration
	}
	if agents.Arbiter.Policy.ModelVersion == agents.PrimaryA.Policy.ModelVersion ||
		agents.Arbiter.Policy.ModelVersion == agents.PrimaryB.Policy.ModelVersion {
		return ErrPanelConfiguration
	}
	return nil
}

func (o *PanelOrchestrator) Grade(ctx context.Context, tenantID, actorID string, gradingContext Context, config PanelDecisionConfig) (PanelResult, error) {
	if o == nil || o.approvedOnly {
		return PanelResult{}, ErrPanelPolicyRequired
	}
	return o.gradeWithConfig(ctx, tenantID, actorID, gradingContext, config)
}

// 两个主评并行但互不共享结果；只有主评出现分歧才调用盲仲裁，无法安全收敛时转人工。
func (o *PanelOrchestrator) gradeWithConfig(ctx context.Context, tenantID, actorID string, gradingContext Context, config PanelDecisionConfig) (PanelResult, error) {
	config = NormalizePanelDecisionConfig(config)
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(actorID) == "" || validateCreatePanel(CreatePanelInput{
		AnswerSegmentID: gradingContext.SegmentID, QuestionID: gradingContext.Question.ID,
		AnswerVersion: gradingContext.AnswerVersion,
		RubricVersion: gradingContext.Rubric.Version, MaxScore: gradingContext.Question.Score, DecisionConfig: config,
	}) != nil {
		return PanelResult{}, ErrPanelConfiguration
	}
	panel, err := o.store.GetOrCreatePanel(ctx, tenantID, actorID, CreatePanelInput{
		AnswerSegmentID: gradingContext.SegmentID, QuestionID: gradingContext.Question.ID,
		AnswerVersion: gradingContext.AnswerVersion,
		RubricVersion: gradingContext.Rubric.Version, MaxScore: gradingContext.Question.Score, DecisionConfig: config,
	})
	if err != nil {
		return PanelResult{}, err
	}
	if panel.Status == PanelHumanReview || panel.Status == PanelResolved || panel.Status == PanelFailed {
		return o.loadTerminalPanelResult(ctx, tenantID, panel)
	}
	if o.math != nil && mathSubject(gradingContext) {
		if err := o.math.prepare(ctx, tenantID, &gradingContext); err != nil || gradingContext.MathEvidence == nil {
			return o.routeHuman(ctx, tenantID, actorID, PanelResult{Panel: panel},
				UpdatePanelInput{TriggerCodes: []string{"math_evidence_unavailable"}})
		}
	}
	primaryA, primaryB := o.runPrimaryAgents(ctx, tenantID, actorID, panel, gradingContext)
	return o.resolvePrimaryResults(ctx, tenantID, actorID, panel, gradingContext, primaryA, primaryB)
}

func (o *PanelOrchestrator) runPrimaryAgents(ctx context.Context, tenantID, actorID string, panel GradingPanel, gradingContext Context) (panelAgentResult, panelAgentResult) {
	results := make(chan panelAgentResult, 2)
	for _, item := range []struct {
		role    string
		binding PanelAgentBinding
	}{{AgentRolePrimaryA, o.agents.PrimaryA}, {AgentRolePrimaryB, o.agents.PrimaryB}} {
		item := item
		go func() {
			run, grade, runErr := o.executeBlindAgent(ctx, tenantID, actorID, panel, gradingContext, item.role, item.binding)
			results <- panelAgentResult{role: item.role, run: run, grade: grade, err: runErr}
		}()
	}
	// 两个主评必须收齐后再判断重放或失败，避免另一角色仍运行时提前转换评审组状态。
	var primaryA, primaryB panelAgentResult
	for range 2 {
		result := <-results
		if result.role == AgentRolePrimaryA {
			primaryA = result
		} else {
			primaryB = result
		}
	}
	return primaryA, primaryB
}

func (o *PanelOrchestrator) resolvePrimaryResults(ctx context.Context, tenantID, actorID string, panel GradingPanel, gradingContext Context, primaryA, primaryB panelAgentResult) (PanelResult, error) {
	// 重试只能沿用评审组已保存的阈值，不能用本次调用的配置改写冻结决定。
	config := panel.DecisionConfig
	if errors.Is(primaryA.err, ErrPanelRunInProgress) || errors.Is(primaryB.err, ErrPanelRunInProgress) {
		latest, lookupErr := o.store.GetPanel(ctx, tenantID, panel.ID)
		if lookupErr != nil {
			return PanelResult{}, lookupErr
		}
		return PanelResult{Panel: latest, PrimaryA: primaryA.grade, PrimaryB: primaryB.grade}, ErrPanelRunInProgress
	}
	baseUpdate := UpdatePanelInput{Status: PanelComparing, PrimaryARunID: primaryA.run.ID, PrimaryBRunID: primaryB.run.ID}
	if primaryA.err != nil || primaryB.err != nil {
		baseUpdate.TriggerCodes = []string{"primary_agent_failure"}
		return o.routeHuman(ctx, tenantID, actorID,
			PanelResult{Panel: panel, PrimaryA: primaryA.grade, PrimaryB: primaryB.grade}, baseUpdate)
	}
	scoreA, scoreB := primaryA.grade.SuggestedScore, primaryB.grade.SuggestedScore
	decision := ComparePrimaryGrades(primaryA.grade, primaryB.grade, gradingContext.Rubric, config)
	baseUpdate.ScoreA, baseUpdate.ScoreB = &scoreA, &scoreB
	baseUpdate.ScoreGap = floatPointer(decision.Facts.NormalizedScoreGap)
	baseUpdate.CriterionGap = floatPointer(decision.Facts.NormalizedCriterionGap)
	baseUpdate.RequiredPointConflict = decision.Facts.RequiredPointConflict
	baseUpdate.EvidenceConflict = decision.Facts.EvidenceConflict
	baseUpdate.ConfidenceConflict = decision.Facts.ConfidenceGap >= config.ConfidenceGapThreshold
	baseUpdate.TriggerCodes = decision.TriggerCodes
	panel, err := o.store.UpdatePanel(ctx, tenantID, panel.ID, baseUpdate)
	if err != nil {
		return PanelResult{}, err
	}
	if decision.PrimaryConsensus && !decision.RequiresArbitration {
		resolved := scoreA
		panel, err = o.store.UpdatePanel(ctx, tenantID, panel.ID, mergePanelUpdate(baseUpdate, UpdatePanelInput{
			Status: PanelResolved, ResolvedScore: &resolved, ResolutionSource: ResolutionPrimaryConsensus,
		}))
		return PanelResult{Panel: panel, PrimaryA: primaryA.grade, PrimaryB: primaryB.grade}, err
	}
	panel, err = o.store.UpdatePanel(ctx, tenantID, panel.ID, mergePanelUpdate(baseUpdate, UpdatePanelInput{Status: PanelArbitrationPending}))
	if err != nil {
		return PanelResult{}, err
	}
	// 仲裁方接收仅根据冻结题目、评分细则和学生证据新构建的请求，不含 A/B 分数或输出。
	arbiterRun, arbiterGrade, arbiterErr := o.executeBlindAgent(ctx, tenantID, actorID, panel, gradingContext, AgentRoleArbiter, o.agents.Arbiter)
	if arbiterErr != nil {
		baseUpdate.ArbiterRunID = arbiterRun.ID
		baseUpdate.TriggerCodes = uniqueSorted(append(baseUpdate.TriggerCodes, "arbiter_failure"))
		return o.routeHuman(ctx, tenantID, actorID,
			PanelResult{Panel: panel, PrimaryA: primaryA.grade, PrimaryB: primaryB.grade}, baseUpdate)
	}
	scoreC := arbiterGrade.SuggestedScore
	baseUpdate.ArbiterRunID, baseUpdate.ScoreC = arbiterRun.ID, &scoreC
	agreement := maxFloat(ArbiterAgrees(arbiterGrade, primaryA.grade, gradingContext.Rubric), ArbiterAgrees(arbiterGrade, primaryB.grade, gradingContext.Rubric))
	if arbiterGrade.Confidence < config.ArbiterMinConfidence || containsHardRisk(arbiterGrade.RiskFlags, config.HardRiskCodes) || agreement < config.ArbiterAgreementThreshold {
		baseUpdate.TriggerCodes = uniqueSorted(append(baseUpdate.TriggerCodes, "arbiter_unresolved"))
		return o.routeHuman(ctx, tenantID, actorID,
			PanelResult{Panel: panel, PrimaryA: primaryA.grade, PrimaryB: primaryB.grade, Arbiter: &arbiterGrade}, baseUpdate)
	}
	panel, err = o.store.UpdatePanel(ctx, tenantID, panel.ID, mergePanelUpdate(baseUpdate, UpdatePanelInput{
		Status: PanelResolved, ResolvedScore: &scoreC, ResolutionSource: ResolutionArbiter,
	}))
	return PanelResult{Panel: panel, PrimaryA: primaryA.grade, PrimaryB: primaryB.grade, Arbiter: &arbiterGrade}, err
}

// 全部角色运行完成后，影子观察仍可能持久化失败。
// 重放时恢复已保存且绑定角色的成绩，避免再次调用模型，或静默把已解决的评审组当成空结果。
func (o *PanelOrchestrator) loadTerminalPanelResult(ctx context.Context, tenantID string, panel GradingPanel) (PanelResult, error) {
	result := PanelResult{Panel: panel}
	for _, item := range []struct {
		role  string
		runID string
		grade *Grade
	}{
		{AgentRolePrimaryA, panel.PrimaryARunID, &result.PrimaryA},
		{AgentRolePrimaryB, panel.PrimaryBRunID, &result.PrimaryB},
	} {
		if item.runID == "" {
			continue
		}
		grade, err := o.loadTerminalRoleGrade(ctx, tenantID, panel, item.role, item.runID)
		if err != nil {
			return PanelResult{}, err
		}
		*item.grade = grade
	}
	if panel.ArbiterRunID != "" {
		grade, err := o.loadTerminalRoleGrade(ctx, tenantID, panel, AgentRoleArbiter, panel.ArbiterRunID)
		if err != nil {
			return PanelResult{}, err
		}
		if grade.ID != "" {
			result.Arbiter = &grade
		}
	}
	return result, nil
}

func (o *PanelOrchestrator) loadTerminalRoleGrade(ctx context.Context, tenantID string, panel GradingPanel, role, runID string) (Grade, error) {
	run, err := o.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return Grade{}, err
	}
	requestID := fmt.Sprintf("panel:%s:%s", panel.ID, role)
	if run.PanelID != panel.ID || run.AgentRole != role || run.RequestID != requestID ||
		run.AnswerSegmentID != panel.AnswerSegmentID || run.AnswerVersion != panel.AnswerVersion ||
		run.QuestionID != panel.QuestionID || run.RubricVersion != panel.RubricVersion {
		return Grade{}, ErrIdempotencyConflict
	}
	if run.Status != RunSucceeded {
		return Grade{}, nil
	}
	_, grade, err := o.loadSucceededPanelRun(ctx, tenantID, requestID, run)
	return grade, err
}

func (o *PanelOrchestrator) executeBlindAgent(ctx context.Context, tenantID, actorID string, panel GradingPanel, gradingContext Context, role string, binding PanelAgentBinding) (GradingRun, Grade, error) {
	requestID := fmt.Sprintf("panel:%s:%s", panel.ID, role)
	runInput := runInputFor(gradingContext, "", binding.Policy, requestID)
	runInput.PanelID, runInput.AgentRole = panel.ID, role
	run, err := o.store.GetOrCreateRun(ctx, tenantID, actorID, runInput)
	if err != nil {
		return GradingRun{}, Grade{}, err
	}
	if run.ModelVersion != binding.Policy.ModelVersion || run.PromptVersion != binding.Policy.PromptVersion ||
		run.MinConfidence != binding.Policy.MinConfidence || run.AnswerVersion != gradingContext.AnswerVersion {
		return run, Grade{}, ErrIdempotencyConflict
	}
	if err := ensureRunMathBinding(run, gradingContext); err != nil {
		return run, Grade{}, err
	}
	if run.Status == RunSucceeded && run.GradeID != "" {
		return o.loadSucceededPanelRun(ctx, tenantID, requestID, run)
	}
	run, claimed, err := o.store.ClaimPanelRun(ctx, tenantID, run.ID)
	if err != nil {
		return run, Grade{}, err
	}
	if !claimed {
		switch run.Status {
		case RunSucceeded:
			return o.loadSucceededPanelRun(ctx, tenantID, requestID, run)
		case RunProcessing, RunQueued:
			return run, Grade{}, ErrPanelRunInProgress
		default:
			return run, Grade{}, ErrPanelAgentFailure
		}
	}
	decision, allowed, err := o.admission(ctx, tenantID, run.ID, gradingContext, binding.Policy)
	if err != nil || !allowed || !decision.CanCallExternalAI() || !decision.OutputConstraint.CriteriaEvidenceOnly ||
		decision.OutputConstraint.AllowModelFinalScore || decision.OutputConstraint.FinalScoreAuthority != "server_rubric_and_deterministic_rule" {
		_, _ = o.store.UpdateRun(ctx, tenantID, run.ID, UpdateRunInput{Status: RunFailed, ErrorCode: "panel_eligibility_abstained"})
		return run, Grade{}, ErrAIEligibilityAbstained
	}
	input := newBlindPanelInput(requestID, gradingContext, binding.Policy, role)
	input.OutputConstraint = decision.OutputConstraint
	mathRun := gradingContext.MathEvidence != nil
	input, failureCode, err := o.preparePanelMathInput(ctx, tenantID, gradingContext, binding, input)
	if err != nil {
		_, _ = o.store.UpdateRun(ctx, tenantID, run.ID, UpdateRunInput{Status: RunFailed, ErrorCode: failureCode})
		return run, Grade{}, err
	}
	input, err = isolatePanelAdapterInput(input)
	if err != nil {
		_, _ = o.store.UpdateRun(ctx, tenantID, run.ID, UpdateRunInput{Status: RunFailed, ErrorCode: "panel_input_isolation_failed"})
		return run, Grade{}, ErrPanelConfiguration
	}
	output, err := binding.Adapter.Grade(ctx, input)
	if err != nil {
		_, _ = o.store.UpdateRun(ctx, tenantID, run.ID, UpdateRunInput{Status: RunFailed, ErrorCode: "panel_agent_failed"})
		return run, Grade{}, fmt.Errorf("%w: %s", ErrPanelAgentFailure, role)
	}
	output.RequestID = requestID
	if output.Mock || output.ModelVersion != binding.Policy.ModelVersion || output.PromptVersion != binding.Policy.PromptVersion {
		_, _ = o.store.UpdateRun(ctx, tenantID, run.ID, UpdateRunInput{Status: RunFailed, ErrorCode: "panel_model_binding_mismatch"})
		return run, Grade{}, ErrInvalidModelOutput
	}
	if mathRun {
		// v2 agent 只返回标准项候选。基于冻结规则的数学评分还会在持久化前重新检查产物修订号。
		err = o.math.settle(ctx, tenantID, gradingContext, binding.Policy, &output)
	} else {
		// 绝不信任模型生成的评分点分数或总分。
		err = ProjectPanelRubricScore(&output, gradingContext.Rubric)
		if err == nil {
			err = ValidateOutput(output, gradingContext)
		}
	}
	if err != nil {
		_, _ = o.store.UpdateRun(ctx, tenantID, run.ID, UpdateRunInput{Status: RunFailed, ErrorCode: "invalid_model_output"})
		return run, Grade{}, ErrInvalidModelOutput
	}
	ApplyPromptGuard(&output, input.PromptGuard)
	ApplyReviewPolicy(&output, gradingContext, binding.Policy)
	gradeInput := successfulGrade(gradingContext, binding.Policy, run.ID, output)
	gradeInput.ModelConfigID = binding.ModelConfigID
	gradeInput.ModelName = binding.ModelName
	grade, err := o.store.CreateGrade(ctx, tenantID, actorID, gradeInput)
	if err != nil {
		return run, Grade{}, err
	}
	run, err = o.store.UpdateRun(ctx, tenantID, run.ID, UpdateRunInput{Status: RunSucceeded, GradeID: grade.ID})
	return run, grade, err
}

func (o *PanelOrchestrator) preparePanelMathInput(ctx context.Context, tenantID string, gradingContext Context, binding PanelAgentBinding, input AdapterInput) (AdapterInput, string, error) {
	if gradingContext.MathEvidence == nil {
		return input, "", nil
	}
	mathAdapter, ok := binding.Adapter.(interface{ SupportsPanelMathV2() bool })
	if !ok || !mathAdapter.SupportsPanelMathV2() || o.math == nil || o.math.input == nil || o.math.settle == nil {
		return AdapterInput{}, "panel_math_v2_unavailable", ErrPanelConfiguration
	}
	mathInput, err := o.math.input(ctx, tenantID, input.RequestID, gradingContext, binding.Policy, input.PromptGuard, input.OutputConstraint)
	if err != nil {
		return AdapterInput{}, "panel_math_evidence_unavailable", err
	}
	mathInput.AgentRole = input.AgentRole
	return mathInput, "", nil
}

// A/B 并发运行，C 只能接收冻结来源材料。交给任何适配器前深拷贝导出请求字段，
// 避免嵌套 map、评分点或数学证据携带其他评审方的修改。
func isolatePanelAdapterInput(input AdapterInput) (AdapterInput, error) {
	crop := input.ActiveCrop
	input.ActiveCrop = nil // 图像字节单独复制，不编码为 JSON。
	encoded, err := json.Marshal(input)
	if err != nil {
		return AdapterInput{}, err
	}
	var isolated AdapterInput
	if err := json.Unmarshal(encoded, &isolated); err != nil {
		return AdapterInput{}, err
	}
	if crop != nil {
		copyCrop := *crop
		copyCrop.Data = append([]byte(nil), crop.Data...)
		isolated.ActiveCrop = &copyCrop
	}
	return isolated, nil
}

func (o *PanelOrchestrator) loadSucceededPanelRun(ctx context.Context, tenantID, requestID string, run GradingRun) (GradingRun, Grade, error) {
	if run.GradeID == "" {
		return run, Grade{}, ErrIdempotencyConflict
	}
	grade, err := o.store.GetGradeByAdapterRequestID(ctx, tenantID, requestID)
	if err == nil && grade.ID == run.GradeID && grade.RunID == run.ID {
		return run, grade, nil
	}
	return run, Grade{}, ErrIdempotencyConflict
}

func newBlindPanelInput(requestID string, gradingContext Context, policy ModelPolicy, role string) AdapterInput {
	return AdapterInput{
		RequestID: requestID, SegmentID: gradingContext.SegmentID, Subject: gradingContext.Subject,
		GradeLevel: gradingContext.GradeLevel, AgentRole: role, Question: gradingContext.Question, Rubric: gradingContext.Rubric,
		AnswerText: gradingContext.AnswerText, AnswerImageRef: cloneMap(gradingContext.AnswerImageRef),
		OCRConfidence: gradingContext.OCRConfidence, AssessmentSnapshot: gradingContext.AssessmentSnapshot,
		ModelPolicy: policy, PromptGuard: InspectPromptInjection(gradingContext.AnswerText),
		OutputConstraint: aieligibility.OutputConstraint{CriteriaEvidenceOnly: true, AllowModelFinalScore: false, FinalScoreAuthority: "server_rubric_and_deterministic_rule"},
		MathEvidence:     gradingContext.MathEvidence,
		ReferenceContext: gradingContext.ReferenceContext,
	}
}

func (o *PanelOrchestrator) routeHuman(ctx context.Context, tenantID, actorID string, result PanelResult, update UpdatePanelInput) (PanelResult, error) {
	// 先保存人工状态，再建任务并关联任务 ID；前两步失败返回空结果，关联失败仍返回已取得的成绩。
	update.Status, update.ResolutionSource = PanelHumanReview, ResolutionHuman
	panel, err := o.store.UpdatePanel(ctx, tenantID, result.Panel.ID, update)
	if err != nil {
		return PanelResult{}, err
	}
	taskID, err := o.store.RoutePanelHumanReview(ctx, tenantID, actorID, panel)
	if err != nil {
		return PanelResult{}, err
	}
	update.ReviewTaskID = taskID
	panel, err = o.store.UpdatePanel(ctx, tenantID, panel.ID, update)
	result.Panel = panel
	if err != nil {
		return result, err
	}
	// 转交人工复核属于成功的受治理结果，不作为 agent 错误暴露给调用方；原因仍通过触发码表示。
	return result, nil
}

func mergePanelUpdate(base, overlay UpdatePanelInput) UpdatePanelInput {
	base.Status = overlay.Status
	if overlay.ArbiterRunID != "" {
		base.ArbiterRunID = overlay.ArbiterRunID
	}
	if overlay.ScoreC != nil {
		base.ScoreC = overlay.ScoreC
	}
	if overlay.ResolvedScore != nil {
		base.ResolvedScore = overlay.ResolvedScore
	}
	if overlay.ResolutionSource != "" {
		base.ResolutionSource = overlay.ResolutionSource
	}
	return base
}

func floatPointer(value float64) *float64 { return &value }
func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}
