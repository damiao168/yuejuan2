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

// Decide 对每个运行项保持幂等，并记录用于决策的每项输入事实的精简副本，防止后续策略或评测变化使历史外部模型调用看似按新规则获准。
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
	// 此检查刻意放在策略查询之前。策略缺失或格式错误不能掩盖风险等级、题型本身禁止外部 AI 的原因。
	if hardProhibited(input.AssessmentSnapshot, input.RequestedMode) {
		decision.Reasons = append(decision.Reasons, reason("hard_risk_archetype_prohibition", "R3 开放题不得由单次模型快速确认或形成最终分数", true))
		return s.store.CreateOrGetDecision(ctx, tenantID, decision)
	}
	// 图像 v3 的 AI_ASSIST 只生成教师待确认建议。冻结策略和完整学生观察构成该路线的准入，
	// 评测与校准仍决定自动确认资格；缺少它们不能把合法建议任务永久阻塞。
	if imageTeacherSuggestion(input) {
		if !input.RubricComplete {
			decision.Reasons = append(decision.Reasons, reason("rubric_incomplete", "冻结评分标准不完整，不能调用 AI 评分", true))
		} else if materialEvidenceRequired(input.AssessmentSnapshot) && !input.EvidenceAvailable {
			decision.Reasons = append(decision.Reasons, reason("material_evidence_missing", "材料题缺少可核验的材料证据引用，模型必须弃权", true))
		} else {
			decision.Decision, decision.ExternalAIAllowed = assessment.ScoringAIAssist, true
			decision.Reasons = append(decision.Reasons, reason("image_teacher_suggestion", "按冻结量规生成图像阅卷建议，最终分数须由教师确认", false))
			if !input.Evaluation.Approved {
				decision.Reasons = append(decision.Reasons, reason("suggestion_evaluation_unapproved", "当前模型与提示词尚无已批准评测，不具备自动确认资格", false))
			}
			if !input.Calibration.Available {
				decision.Reasons = append(decision.Reasons, reason("suggestion_uncalibrated", "当前建议未经校准，须由教师确认", false))
			}
		}
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
	if blocked, denied := admissionEvidenceBlock(input, policy); denied {
		decision.Reasons = append(decision.Reasons, blocked)
		return s.store.CreateOrGetDecision(ctx, tenantID, decision)
	}

	applyAdmittedScoringMode(input, policy, &decision)
	return s.store.CreateOrGetDecision(ctx, tenantID, decision)
}

// 同时缺少多项证据时只记录原流程中最先阻止准入的一项，不能改变历史审计原因的优先级。
func admissionEvidenceBlock(input DecisionInput, policy Policy) (Reason, bool) {
	imageObservation := input.InputProtocol == "grading-image-v3" && input.InputVersionID != "" && input.StudentObservationAvailable && input.RequestedMode == assessment.ScoringAIAssist
	// 图像观察不是 OCR 或符号核验。新协议只以真实观察代替这两项前置，评测、校准和最终确认约束保持原样。
	if !imageObservation && qualityBelow(input.OCRQuality, policy.MinOCRQuality) {
		return reason("ocr_quality_below_policy", "文字识别质量不足，禁止模型猜测不可读答案", true), true
	}
	if !imageObservation && qualityBelow(input.ParserQuality, policy.MinParserQuality) {
		return reason("parser_quality_below_policy", "结构化解析质量不足，必须转人工原图阅卷", true), true
	}
	if !input.RubricComplete {
		return reason("rubric_incomplete", "冻结评分标准不完整，不能调用 AI 评分", true), true
	}
	if materialEvidenceRequired(input.AssessmentSnapshot) && !input.EvidenceAvailable {
		return reason("material_evidence_missing", "材料题缺少可核验的材料证据引用，模型必须弃权", true), true
	}
	if !input.Evaluation.Approved || input.Evaluation.SampleCount < policy.MinEvalN || input.Evaluation.SevereErrorRate > policy.MaxSevereErrorRate {
		return reason("approved_evaluation_insufficient", "未达到已批准评测样本量或严重误差门槛", true), true
	}
	if !input.Calibration.Available {
		return reason("calibration_unavailable", "当前模型、提示词和部署缺少可用校准证据", true), true
	}
	return Reason{}, false
}

func imageTeacherSuggestion(input DecisionInput) bool {
	return input.InputProtocol == "grading-image-v3" && input.InputVersionID != "" && input.StudentObservationAvailable &&
		input.RequestedMode == assessment.ScoringAIAssist && input.AssessmentSnapshot.ScoringPolicySnapshot.Mode == assessment.ScoringAIAssist
}

func applyAdmittedScoringMode(input DecisionInput, policy Policy, decision *Decision) {
	// 结构化数学题尽量使用确定性处理；此检查先于允许的 AI 模式判断，调用方不能仅通过请求 LLM 模式绕过规则评分。
	if input.InputProtocol != "grading-image-v3" && prefersRuleAuto(input.AssessmentSnapshot) && containsMode(policy.AllowedModes, assessment.ScoringRuleAuto) {
		decision.Decision = assessment.ScoringRuleAuto
		decision.OutputConstraint.FinalScoreAuthority = "server_rubric_and_deterministic_rule"
		decision.Reasons = append(decision.Reasons, reason("prefer_structured_rule", "数学结构表达式可由确定性规则优先判定，不调用 LLM", false))
		return
	}
	if !containsMode(policy.AllowedModes, input.RequestedMode) {
		decision.Reasons = append(decision.Reasons, reason("requested_mode_not_allowed", "请求的评分模式未获当前准入策略批准", true))
		return
	}
	decision.Decision = input.RequestedMode
	if input.RequestedMode == assessment.ScoringRuleAuto {
		decision.OutputConstraint.FinalScoreAuthority = "server_rubric_and_deterministic_rule"
		decision.Reasons = append(decision.Reasons, reason("rule_auto_allowed", "已满足规则评分准入条件", false))
		return
	}
	decision.ExternalAIAllowed = input.RequestedMode == assessment.ScoringAIAssist || input.RequestedMode == assessment.ScoringAIFastConfirm
	if decision.ExternalAIAllowed {
		decision.OutputConstraint.FinalScoreAuthority = "server_rubric_or_human_confirmation"
		decision.Reasons = append(decision.Reasons, reason("ai_criteria_evidence_only", "AI 仅可返回结构化评分点与证据；最终分数由服务端 Rubric 计算或人工确认", false))
	}
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
		"input_protocol": input.InputProtocol, "input_version_id": input.InputVersionID, "student_observation_available": input.StudentObservationAvailable,
		"assessment_snapshot_id": input.AssessmentSnapshot.ID, "assessment_content_hash": input.AssessmentSnapshot.ContentHash,
		"subject_code": input.AssessmentSnapshot.SubjectCode, "education_stage": input.AssessmentSnapshot.EducationStage,
		"archetype_code": input.AssessmentSnapshot.ArchetypeCode, "risk_tier": input.AssessmentSnapshot.RiskTier,
		"requested_mode": input.RequestedMode, "ocr_quality": quality(input.OCRQuality), "parser_quality": quality(input.ParserQuality),
		"rubric_complete": input.RubricComplete, "evidence_available": input.EvidenceAvailable,
		"evaluation": input.Evaluation, "calibration": input.Calibration,
	}
	// 通过 JSON 往返转换，使保存的审计记录脱离调用方持有的所有映射和指针，同时不保留原始答案或图片内容。
	raw, _ := json.Marshal(result)
	var copied map[string]any
	_ = json.Unmarshal(raw, &copied)
	return copied
}
