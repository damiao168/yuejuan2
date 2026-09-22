package subjective

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
)

type PanelAgentBinding struct {
	Adapter       LLMGradingAdapter
	Policy        ModelPolicy
	StrengthRank  int
	ModelConfigID string
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

type PanelOrchestrator struct {
	store        PanelPersistence
	agents       PanelAgents
	admission    decideEligibilityFunc
	policies     PanelPolicyStore
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

// GradeWithApprovedPolicy is the fail-closed production entry point. Shadow
// experiments may still call Grade with an explicit frozen config, while this
// path accepts thresholds only from an approved exact stage/subject/archetype
// policy backed by a completed evaluation run.
func (o *PanelOrchestrator) GradeWithApprovedPolicy(ctx context.Context, tenantID, actorID string, gradingContext Context) (PanelResult, error) {
	if o == nil || o.policies == nil || !gradingContext.AssessmentSnapshot.EducationStage.Valid() ||
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
	return o.gradeWithConfig(ctx, tenantID, actorID, gradingContext, policy.DecisionConfig)
}

// The math runtime is installed only by Handler, which owns the existing
// evidence, active-crop and frozen-rule settlement governance path.
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
	config = panel.DecisionConfig
	if panel.Status == PanelHumanReview || panel.Status == PanelResolved || panel.Status == PanelFailed {
		return o.loadTerminalPanelResult(ctx, tenantID, panel)
	}
	if o.math != nil && mathSubject(gradingContext) {
		if err := o.math.prepare(ctx, tenantID, &gradingContext); err != nil || gradingContext.MathEvidence == nil {
			return o.routeHuman(ctx, tenantID, actorID, panel,
				UpdatePanelInput{TriggerCodes: []string{"math_evidence_unavailable"}}, Grade{}, Grade{}, nil, err)
		}
	}
	type agentResult struct {
		role  string
		run   GradingRun
		grade Grade
		err   error
	}
	results := make(chan agentResult, 2)
	for _, item := range []struct {
		role    string
		binding PanelAgentBinding
	}{{AgentRolePrimaryA, o.agents.PrimaryA}, {AgentRolePrimaryB, o.agents.PrimaryB}} {
		item := item
		go func() {
			run, grade, runErr := o.executeBlindAgent(ctx, tenantID, actorID, panel, gradingContext, item.role, item.binding)
			results <- agentResult{role: item.role, run: run, grade: grade, err: runErr}
		}()
	}
	var primaryA, primaryB agentResult
	for range 2 {
		result := <-results
		if result.role == AgentRolePrimaryA {
			primaryA = result
		} else {
			primaryB = result
		}
	}
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
		return o.routeHuman(ctx, tenantID, actorID, panel, baseUpdate, primaryA.grade, primaryB.grade, nil, errors.Join(primaryA.err, primaryB.err))
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
	panel, err = o.store.UpdatePanel(ctx, tenantID, panel.ID, baseUpdate)
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
	// The arbiter receives a freshly constructed request from the frozen
	// question/rubric/student evidence only. No A/B score or output is present.
	arbiterRun, arbiterGrade, arbiterErr := o.executeBlindAgent(ctx, tenantID, actorID, panel, gradingContext, AgentRoleArbiter, o.agents.Arbiter)
	if arbiterErr != nil {
		baseUpdate.ArbiterRunID = arbiterRun.ID
		baseUpdate.TriggerCodes = uniqueSorted(append(baseUpdate.TriggerCodes, "arbiter_failure"))
		return o.routeHuman(ctx, tenantID, actorID, panel, baseUpdate, primaryA.grade, primaryB.grade, nil, arbiterErr)
	}
	scoreC := arbiterGrade.SuggestedScore
	baseUpdate.ArbiterRunID, baseUpdate.ScoreC = arbiterRun.ID, &scoreC
	agreement := maxFloat(ArbiterAgrees(arbiterGrade, primaryA.grade, gradingContext.Rubric), ArbiterAgrees(arbiterGrade, primaryB.grade, gradingContext.Rubric))
	if arbiterGrade.Confidence < config.ArbiterMinConfidence || containsHardRisk(arbiterGrade.RiskFlags, config.HardRiskCodes) || agreement < config.ArbiterAgreementThreshold {
		baseUpdate.TriggerCodes = uniqueSorted(append(baseUpdate.TriggerCodes, "arbiter_unresolved"))
		return o.routeHuman(ctx, tenantID, actorID, panel, baseUpdate, primaryA.grade, primaryB.grade, &arbiterGrade, nil)
	}
	panel, err = o.store.UpdatePanel(ctx, tenantID, panel.ID, mergePanelUpdate(baseUpdate, UpdatePanelInput{
		Status: PanelResolved, ResolvedScore: &scoreC, ResolutionSource: ResolutionArbiter,
	}))
	return PanelResult{Panel: panel, PrimaryA: primaryA.grade, PrimaryB: primaryB.grade, Arbiter: &arbiterGrade}, err
}

// A Shadow observation may fail to persist after all role runs have completed.
// Recover the saved, role-bound grades on replay instead of invoking a model
// again or silently treating an already-resolved panel as an empty result.
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
	if mathRun {
		mathAdapter, ok := binding.Adapter.(interface{ SupportsPanelMathV2() bool })
		if !ok || !mathAdapter.SupportsPanelMathV2() || o.math == nil || o.math.input == nil || o.math.settle == nil {
			_, _ = o.store.UpdateRun(ctx, tenantID, run.ID, UpdateRunInput{Status: RunFailed, ErrorCode: "panel_math_v2_unavailable"})
			return run, Grade{}, ErrPanelConfiguration
		}
		input, err = o.math.input(ctx, tenantID, requestID, gradingContext, binding.Policy, input.PromptGuard, decision.OutputConstraint)
		if err != nil {
			_, _ = o.store.UpdateRun(ctx, tenantID, run.ID, UpdateRunInput{Status: RunFailed, ErrorCode: "panel_math_evidence_unavailable"})
			return run, Grade{}, err
		}
		input.AgentRole = role
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
		// The v2 agent returns only criterion candidates. Frozen-rule math
		// scoring also rechecks the artifact revision before persistence.
		err = o.math.settle(ctx, tenantID, gradingContext, binding.Policy, &output)
	} else {
		// Never trust a model-authored point or total score.
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
	grade, err := o.store.CreateGrade(ctx, tenantID, actorID, successfulGrade(gradingContext, binding.Policy, run.ID, output))
	if err != nil {
		return run, Grade{}, err
	}
	run, err = o.store.UpdateRun(ctx, tenantID, run.ID, UpdateRunInput{Status: RunSucceeded, GradeID: grade.ID})
	return run, grade, err
}

// A/B run concurrently, and C must receive only the frozen source material.
// Deep-copy the exported request fields before handing them to any adapter so
// nested maps, rubric points or math evidence cannot carry peer mutations.
func isolatePanelAdapterInput(input AdapterInput) (AdapterInput, error) {
	crop := input.ActiveCrop
	input.ActiveCrop = nil // image bytes are copied separately, never JSON encoded
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
	}
}

func (o *PanelOrchestrator) routeHuman(ctx context.Context, tenantID, actorID string, panel GradingPanel, update UpdatePanelInput, primaryA, primaryB Grade, arbiter *Grade, cause error) (PanelResult, error) {
	update.Status, update.ResolutionSource = PanelHumanReview, ResolutionHuman
	panel, err := o.store.UpdatePanel(ctx, tenantID, panel.ID, update)
	if err != nil {
		return PanelResult{}, err
	}
	taskID, err := o.store.RoutePanelHumanReview(ctx, tenantID, actorID, panel)
	if err != nil {
		return PanelResult{}, err
	}
	update.ReviewTaskID = taskID
	panel, err = o.store.UpdatePanel(ctx, tenantID, panel.ID, update)
	result := PanelResult{Panel: panel, PrimaryA: primaryA, PrimaryB: primaryB, Arbiter: arbiter}
	if err != nil {
		return result, err
	}
	// A routed human review is a successful governed outcome, not an agent
	// error exposed to the caller. The cause remains represented by trigger codes.
	_ = cause
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
