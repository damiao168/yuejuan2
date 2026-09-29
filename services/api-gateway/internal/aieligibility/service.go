package aieligibility

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"

	"github.com/google/uuid"
)

type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store) *Service {
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) PutPolicy(ctx context.Context, tenantID string, input PutPolicyInput) (Policy, error) {
	if tenantID == "" || !validPolicy(input) {
		return Policy{}, ErrInvalidInput
	}
	return s.store.PutPolicy(ctx, tenantID, normalizePolicyInput(input))
}

func (s *Service) GetActivePolicy(ctx context.Context, tenantID string, subject assessment.SubjectCode, stage assessment.EducationStage, archetype string, risk assessment.RiskTier) (Policy, error) {
	if tenantID == "" || !subject.Valid() || !stage.Valid() || !assessment.IsQuestionArchetype(archetype) || !risk.Valid() {
		return Policy{}, ErrInvalidInput
	}
	return s.store.GetActivePolicy(ctx, tenantID, subject, stage, archetype, risk)
}

// Decide is idempotent per run item. It records a compact copy of every input
// fact used for the outcome, so later policy/evaluation changes cannot make a
// historical external model call appear to have been admitted under new rules.
func (s *Service) Decide(ctx context.Context, tenantID string, input DecisionInput) (Decision, error) {
	if tenantID == "" || strings.TrimSpace(input.RunItemID) == "" || input.AssessmentSnapshot.ID == "" ||
		!input.AssessmentSnapshot.SubjectCode.Valid() || !input.AssessmentSnapshot.EducationStage.Valid() ||
		!assessment.IsQuestionArchetype(input.AssessmentSnapshot.ArchetypeCode) || !input.AssessmentSnapshot.RiskTier.Valid() {
		return Decision{}, ErrInvalidInput
	}
	if input.RequestedMode == "" {
		input.RequestedMode = input.AssessmentSnapshot.ScoringPolicySnapshot.Mode
	}
	if !input.RequestedMode.Valid() || !validQuality(input.OCRQuality) || !validQuality(input.ParserQuality) ||
		!validEvidence(input.Evaluation) {
		return Decision{}, ErrInvalidInput
	}
	if existing, err := s.store.GetDecision(ctx, tenantID, input.RunItemID); err == nil {
		return existing, nil
	} else if err != ErrNotFound {
		return Decision{}, err
	}

	decision := Decision{ID: uuid.NewString(), TenantID: tenantID, RunItemID: input.RunItemID,
		Decision: assessment.ScoringHumanPrimary, InputSnapshot: inputSnapshot(input), Reasons: []Reason{},
		OutputConstraint: OutputConstraint{CriteriaEvidenceOnly: true, AllowModelFinalScore: false, FinalScoreAuthority: "human_review"}, CreatedAt: s.now()}
	// This is deliberately before the policy lookup. A missing or malformed
	// policy must never hide the reason that the risk/archetype itself is a
	// non-negotiable external-AI prohibition.
	if hardProhibited(input.AssessmentSnapshot, input.RequestedMode) {
		decision.Reasons = append(decision.Reasons, reason("hard_risk_archetype_prohibition", "R3 开放题不得由单次模型快速确认或形成最终分数", true))
		return s.store.CreateOrGetDecision(ctx, tenantID, decision)
	}

	policy, policyErr := s.store.GetActivePolicy(ctx, tenantID, input.AssessmentSnapshot.SubjectCode,
		input.AssessmentSnapshot.EducationStage, input.AssessmentSnapshot.ArchetypeCode, input.AssessmentSnapshot.RiskTier)
	if policyErr == nil {
		decision.PolicyID, decision.PolicyVersion = policy.ID, policy.Version
		decision.OutputConstraint.MaxSevereErrorRisk = policy.MaxSevereErrorRate
	} else if policyErr == ErrNotFound {
		decision.Reasons = append(decision.Reasons, reason("policy_missing", "没有适用于该学科、学段、题型和风险级别的启用准入策略", true))
		return s.store.CreateOrGetDecision(ctx, tenantID, decision)
	} else {
		return Decision{}, policyErr
	}

	if input.RequestedMode == assessment.ScoringManualOnly || input.RequestedMode == assessment.ScoringHumanPrimary || input.RequestedMode == assessment.ScoringDualHuman {
		decision.Decision = input.RequestedMode
		decision.Reasons = append(decision.Reasons, reason("requested_human_mode", "题目冻结评分策略要求人工评分流程", false))
		return s.store.CreateOrGetDecision(ctx, tenantID, decision)
	}
	if qualityBelow(input.OCRQuality, policy.MinOCRQuality) {
		decision.Reasons = append(decision.Reasons, reason("ocr_quality_below_policy", "文字识别质量不足，禁止模型猜测不可读答案", true))
		return s.store.CreateOrGetDecision(ctx, tenantID, decision)
	}
	if qualityBelow(input.ParserQuality, policy.MinParserQuality) {
		decision.Reasons = append(decision.Reasons, reason("parser_quality_below_policy", "结构化解析质量不足，必须转人工原图阅卷", true))
		return s.store.CreateOrGetDecision(ctx, tenantID, decision)
	}
	if !input.RubricComplete {
		decision.Reasons = append(decision.Reasons, reason("rubric_incomplete", "冻结评分标准不完整，不能调用 AI 评分", true))
		return s.store.CreateOrGetDecision(ctx, tenantID, decision)
	}
	if materialEvidenceRequired(input.AssessmentSnapshot) && !input.EvidenceAvailable {
		decision.Reasons = append(decision.Reasons, reason("material_evidence_missing", "材料题缺少可核验的材料证据引用，模型必须弃权", true))
		return s.store.CreateOrGetDecision(ctx, tenantID, decision)
	}
	if !input.Evaluation.Approved || input.Evaluation.SampleCount < policy.MinEvalN || input.Evaluation.SevereErrorRate > policy.MaxSevereErrorRate {
		decision.Reasons = append(decision.Reasons, reason("approved_evaluation_insufficient", "未达到已批准评测样本量或严重误差门槛", true))
		return s.store.CreateOrGetDecision(ctx, tenantID, decision)
	}
	if !input.Calibration.Available {
		decision.Reasons = append(decision.Reasons, reason("calibration_unavailable", "当前模型、提示词和部署缺少可用校准证据", true))
		return s.store.CreateOrGetDecision(ctx, tenantID, decision)
	}

	// Structured math is deterministic whenever possible. This is evaluated
	// before allowed AI modes, so a caller cannot bypass rule scoring merely by
	// requesting an LLM mode.
	if prefersRuleAuto(input.AssessmentSnapshot) && containsMode(policy.AllowedModes, assessment.ScoringRuleAuto) {
		decision.Decision = assessment.ScoringRuleAuto
		decision.OutputConstraint.FinalScoreAuthority = "server_rubric_and_deterministic_rule"
		decision.Reasons = append(decision.Reasons, reason("prefer_structured_rule", "数学结构表达式可由确定性规则优先判定，不调用 LLM", false))
		return s.store.CreateOrGetDecision(ctx, tenantID, decision)
	}
	if !containsMode(policy.AllowedModes, input.RequestedMode) {
		decision.Reasons = append(decision.Reasons, reason("requested_mode_not_allowed", "请求的评分模式未获当前准入策略批准", true))
		return s.store.CreateOrGetDecision(ctx, tenantID, decision)
	}
	decision.Decision = input.RequestedMode
	if input.RequestedMode == assessment.ScoringRuleAuto {
		decision.OutputConstraint.FinalScoreAuthority = "server_rubric_and_deterministic_rule"
		decision.Reasons = append(decision.Reasons, reason("rule_auto_allowed", "已满足规则评分准入条件", false))
		return s.store.CreateOrGetDecision(ctx, tenantID, decision)
	}
	decision.ExternalAIAllowed = input.RequestedMode == assessment.ScoringAIAssist || input.RequestedMode == assessment.ScoringAIFastConfirm
	if decision.ExternalAIAllowed {
		decision.OutputConstraint.FinalScoreAuthority = "server_rubric_or_human_confirmation"
		decision.Reasons = append(decision.Reasons, reason("ai_criteria_evidence_only", "AI 仅可返回结构化评分点与证据；最终分数由服务端 Rubric 计算或人工确认", false))
	}
	return s.store.CreateOrGetDecision(ctx, tenantID, decision)
}

func (s *Service) GetDecision(ctx context.Context, tenantID, runItemID string) (Decision, error) {
	if tenantID == "" || strings.TrimSpace(runItemID) == "" {
		return Decision{}, ErrInvalidInput
	}
	return s.store.GetDecision(ctx, tenantID, runItemID)
}

func hardProhibited(snapshot assessment.ExamQuestionSnapshot, requested assessment.ScoringMode) bool {
	return snapshot.RiskTier == assessment.RiskR3 && snapshot.ArchetypeCode == "extended_response" &&
		(requested == assessment.ScoringAIFastConfirm || requested == assessment.ScoringRuleAuto)
}

func materialEvidenceRequired(snapshot assessment.ExamQuestionSnapshot) bool {
	switch snapshot.SubjectCode {
	case assessment.SubjectHistory, assessment.SubjectEthicsPolitics:
		return snapshot.ArchetypeCode == "short_constructed" || snapshot.ArchetypeCode == "extended_response"
	default:
		return false
	}
}

func prefersRuleAuto(snapshot assessment.ExamQuestionSnapshot) bool {
	return snapshot.SubjectCode == assessment.SubjectMathematics && snapshot.ArchetypeCode == "numeric_expression"
}

func validPolicy(input PutPolicyInput) bool {
	if !input.SubjectCode.Valid() || !input.EducationStage.Valid() || !assessment.IsQuestionArchetype(input.ArchetypeCode) || !input.RiskTier.Valid() || !input.Status.Valid() || input.MinEvalN < 1 || input.ExpectedVersion < 0 ||
		!validRate(input.MinOCRQuality) || !validRate(input.MinParserQuality) || !validRate(input.MaxSevereErrorRate) || len(input.AllowedModes) == 0 {
		return false
	}
	seen := map[assessment.ScoringMode]bool{}
	for _, mode := range input.AllowedModes {
		if !mode.Valid() || seen[mode] || (input.RiskTier == assessment.RiskR3 && input.ArchetypeCode == "extended_response" && (mode == assessment.ScoringAIFastConfirm || mode == assessment.ScoringRuleAuto)) {
			return false
		}
		seen[mode] = true
	}
	return true
}

func normalizePolicyInput(input PutPolicyInput) PutPolicyInput {
	input.AllowedModes = append([]assessment.ScoringMode(nil), input.AllowedModes...)
	sort.Slice(input.AllowedModes, func(i, j int) bool { return input.AllowedModes[i] < input.AllowedModes[j] })
	return input
}
func validRate(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}
func validQuality(value *float64) bool { return value == nil || validRate(*value) }
func validEvidence(value EvaluationEvidence) bool {
	return value.SampleCount >= 0 && validRate(value.SevereErrorRate)
}

// 缺失质量数据不等于零阈值已达标；即使策略阈值为 0，未知质量也必须阻止调用。
func qualityBelow(value *float64, threshold float64) bool { return value == nil || *value < threshold }
func containsMode(values []assessment.ScoringMode, target assessment.ScoringMode) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func reason(code, message string, blocking bool) Reason {
	return Reason{Code: code, Message: message, Blocking: blocking}
}

func inputSnapshot(input DecisionInput) map[string]any {
	quality := func(value *float64) any {
		if value == nil {
			return nil
		}
		return *value
	}
	result := map[string]any{
		"assessment_snapshot_id": input.AssessmentSnapshot.ID, "assessment_content_hash": input.AssessmentSnapshot.ContentHash,
		"subject_code": input.AssessmentSnapshot.SubjectCode, "education_stage": input.AssessmentSnapshot.EducationStage,
		"archetype_code": input.AssessmentSnapshot.ArchetypeCode, "risk_tier": input.AssessmentSnapshot.RiskTier,
		"requested_mode": input.RequestedMode, "ocr_quality": quality(input.OCRQuality), "parser_quality": quality(input.ParserQuality),
		"rubric_complete": input.RubricComplete, "evidence_available": input.EvidenceAvailable,
		"evaluation": input.Evaluation, "calibration": input.Calibration,
	}
	// Round-trip through JSON makes the stored audit record detached from every
	// caller-owned map/pointer without retaining raw answer or image content.
	raw, _ := json.Marshal(result)
	var copied map[string]any
	_ = json.Unmarshal(raw, &copied)
	return copied
}
