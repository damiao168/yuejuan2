package subjective

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/grading"
)

// ImageRoleResult 保存每次真实调用；Output 只含结构化观察、采分点与用量，不含供应商私有思考正文。
type ImageRoleResult struct {
	Role      string                 `json:"role"`
	Status    string                 `json:"status"`
	Model     grading.GradingAIModel `json:"model"`
	Output    *AdapterOutput         `json:"output,omitempty"`
	ErrorCode string                 `json:"error_code,omitempty"`
}

type ImagePanelExecution struct {
	ConfiguredMode        string            `json:"configured_mode"`
	ExecutedRoles         []string          `json:"executed_roles"`
	IndependentModelCount int               `json:"independent_model_count"`
	Decision              string            `json:"decision"`
	ReasonCodes           []string          `json:"reason_codes"`
	RawScore              *float64          `json:"raw_score,omitempty"`
	FinalScore            *float64          `json:"final_score,omitempty"`
	PrimaryThreshold      *float64          `json:"primary_threshold,omitempty"`
	TeacherThreshold      *float64          `json:"teacher_threshold,omitempty"`
	RoleResults           []ImageRoleResult `json:"role_results"`
}

// 小题只沿用首轮观察使用的单个模型，不读取或执行大题的分差规则。
func imagePlanForQuestion(plan *grading.GradingAIPlan, questionType string) *grading.GradingAIPlan {
	if plan == nil || grading.GradingAIPlanAppliesToQuestion(questionType) || plan.EffectiveMode == "single" || plan.EffectiveMode == "unconfigured" {
		return plan
	}
	single := *plan
	single.EffectiveMode, single.IndependentModelCount = "single", 1
	if primary, ok := plan.ResolvedRoles["primary_a"]; ok {
		single.SingleModel = &primary
	}
	return &single
}

func imagePlanRoleModel(plan *grading.GradingAIPlan, role string) (grading.GradingAIModel, bool) {
	if plan == nil {
		return grading.GradingAIModel{}, false
	}
	if role == "single" && plan.EffectiveMode == "single" && plan.SingleModel != nil {
		return *plan.SingleModel, true
	}
	if role != "single" && (plan.EffectiveMode == "dual" || plan.EffectiveMode == "dual_conditional_arbiter") {
		model, ok := plan.ResolvedRoles[role]
		if role == "arbiter" && plan.EffectiveMode != "dual_conditional_arbiter" {
			return grading.GradingAIModel{}, false
		}
		return model, ok
	}
	return grading.GradingAIModel{}, false
}

func (h *Handler) executeImageRole(ctx context.Context, tenantID string, input AdapterInput, role string, identity grading.GradingAIModel) ImageRoleResult {
	result := ImageRoleResult{Role: role, Status: "failed", Model: identity}
	base, ok := h.adapter.(*HTTPAdapter)
	if !ok || h.imageModelByIDResolver == nil {
		result.ErrorCode = "visual_model_unavailable"
		return result
	}
	model, err := h.imageModelByIDResolver(ctx, tenantID, identity.ModelConfigID, identity.BindingRevision)
	if err != nil || model == nil || model.ConfigRevision != identity.ConfigRevision || model.ModelName != identity.ModelName || model.ModelVersion != identity.ModelVersion {
		result.ErrorCode = "visual_model_changed"
		return result
	}
	adapter := *base
	adapter.policy.ModelVersion, adapter.policy.PromptVersion = identity.ModelVersion, ImageComparisonPromptVersion
	adapter.adapterType, adapter.providerKey, adapter.deploymentKey, adapter.deploymentRegion = model.AdapterType, model.ProviderKey, model.ConfigRevision, "managed"
	input.RequestID, input.AgentRole = input.RequestID+":"+role, role
	input.ImageModel, input.ModelPolicy = model, adapter.Policy()
	output, err := adapter.Grade(ctx, input)
	result.Output = &output
	if err != nil {
		result.ErrorCode = "image_role_failed"
		var failure *GradingAgentError
		if errors.As(err, &failure) && failure.Code != "" {
			result.ErrorCode = failure.Code
		}
		return result
	}
	result.Status = "succeeded"
	return result
}

// executeImagePlan 共用父任务租约。A/B 互不传递结果；C 也只看原图、冻结观察和量规，不接收已有分数。
// 先按未取整分比较阈值，避免每个角色提前取整导致额外仲裁或掩盖分歧。
func (h *Handler) executeImagePlan(ctx context.Context, tenantID string, prepared WorkerExecutionContext, input AdapterInput) (AdapterOutput, error) {
	plan := imagePlanForQuestion(prepared.Context.AIPlan, prepared.Context.Question.QuestionType)
	if plan == nil {
		return AdapterOutput{}, ErrInvalidInput
	}
	prepared.Context.AIPlan = plan
	execution := &ImagePanelExecution{ConfiguredMode: plan.EffectiveMode, RoleResults: []ImageRoleResult{}}
	if plan.EffectiveMode == "single" {
		identity, ok := imagePlanRoleModel(plan, "single")
		if !ok {
			return AdapterOutput{}, ErrInvalidInput
		}
		execution.RoleResults = append(execution.RoleResults, h.executeImageRole(ctx, tenantID, input, "single", identity))
	} else if plan.EffectiveMode == "dual" || plan.EffectiveMode == "dual_conditional_arbiter" {
		if plan.DecisionConfig.PrimaryAllowedDiffPoints == nil || plan.DecisionConfig.TeacherAllowedDiffPoints == nil {
			return AdapterOutput{}, ErrInvalidInput
		}
		type roleResult struct {
			index  int
			result ImageRoleResult
		}
		results := make(chan roleResult, 2)
		for index, role := range []string{"primary_a", "primary_b"} {
			identity, ok := imagePlanRoleModel(plan, role)
			if !ok {
				return AdapterOutput{}, ErrInvalidInput
			}
			go func(index int, role string, identity grading.GradingAIModel) {
				results <- roleResult{index: index, result: h.executeImageRole(ctx, tenantID, input, role, identity)}
			}(index, role, identity)
		}
		execution.RoleResults = make([]ImageRoleResult, 2)
		for range 2 {
			result := <-results
			execution.RoleResults[result.index] = result.result
		}
		a, b := execution.RoleResults[0], execution.RoleResults[1]
		if a.Status == "succeeded" && b.Status == "succeeded" && a.Output != nil && b.Output != nil && imageAllowedDifferenceExceeded(math.Abs(a.Output.SuggestedScore-b.Output.SuggestedScore), *plan.DecisionConfig.PrimaryAllowedDiffPoints) && plan.EffectiveMode == "dual_conditional_arbiter" {
			identity, _ := imagePlanRoleModel(plan, "arbiter")
			execution.RoleResults = append(execution.RoleResults, h.executeImageRole(ctx, tenantID, input, "arbiter", identity))
		}
	} else {
		return AdapterOutput{}, &GradingAgentError{Code: "visual_model_not_configured"}
	}
	var output AdapterOutput
	for _, role := range execution.RoleResults {
		if role.Status == "succeeded" && role.Output != nil {
			output = *role.Output
			break
		}
	}
	if output.SchemaVersion == "" {
		execution.Decision, execution.ReasonCodes, execution.ExecutedRoles = "teacher_review", []string{"all_image_roles_failed"}, []string{}
		identities := map[string]bool{}
		for _, role := range execution.RoleResults {
			if role.Output != nil {
				execution.ExecutedRoles = append(execution.ExecutedRoles, role.Role)
				identities[strings.ToLower(role.Model.ModelName)+":"+strings.ToLower(role.Model.ModelVersion)] = true
			}
		}
		execution.IndependentModelCount = len(identities)
		output.RawOutput = map[string]any{"panel_execution": imagePanelPublicOutput(execution), "score_prefill_allowed": false}
		return output, &GradingAgentError{Code: "image_roles_failed"}
	}
	output.PanelExecution = execution
	output.RequestID, output.ModelVersion, output.PromptVersion = prepared.Run.RequestID, prepared.Policy.ModelVersion, prepared.Policy.PromptVersion
	if err := settleImagePanelOutput(prepared.Context, prepared.Policy, &output); err != nil {
		return output, err
	}
	return output, nil
}

func imageScoringBasis(value Context) map[string]any {
	basis := map[string]any{"rubric_points": value.Rubric.Points}
	if value.ReferenceContext != nil {
		basis["standard_answer"] = value.ReferenceContext.StandardAnswer
		basis["solution_text"] = value.ReferenceContext.SolutionText
	}
	if value.Question.Solution != nil && (basis["solution_text"] == nil || basis["solution_text"] == "") {
		basis["solution_text"] = value.Question.Solution.RawText
	}
	return basis
}

func imageRawOutput(value Context, judgments []ImageCriterionJudgment) map[string]any {
	return map[string]any{"schema_version": ImageProtocolVersion, "input_version_id": value.InputVersionID,
		"assessment_snapshot_id": value.AssessmentSnapshot.ID, "observation": value.Observation, "judgments": judgments,
		"observation_prompt_version": value.Observation.PromptVersion, "comparison_prompt_version": ImageComparisonPromptVersion,
		"scoring_basis": imageScoringBasis(value)}
}

// settleImagePanelOutput 不接受回调中的汇总分、角色分或采分点分值，全部从冻结量规和每次独立判断重建。
// 不可读、uncertain 或任一角色失败时保留候选与证据，FinalScore 留空，不能把失败当作零分。
func settleImagePanelOutput(value Context, policy ModelPolicy, output *AdapterOutput) error {
	if output == nil || output.PanelExecution == nil || value.AIPlan == nil || value.Observation == nil {
		return ErrInvalidModelOutput
	}
	execution := output.PanelExecution
	plan := imagePlanForQuestion(value.AIPlan, value.Question.QuestionType)
	if execution.ConfiguredMode != plan.EffectiveMode {
		return ErrInvalidModelOutput
	}
	seen := map[string]bool{}
	byRole := map[string]*AdapterOutput{}
	execution.ExecutedRoles, execution.ReasonCodes = []string{}, []string{}
	execution.RawScore, execution.FinalScore = nil, nil
	execution.PrimaryThreshold, execution.TeacherThreshold = nil, nil
	if plan.EffectiveMode == "dual" || plan.EffectiveMode == "dual_conditional_arbiter" {
		if plan.DecisionConfig.PrimaryAllowedDiffPoints == nil || plan.DecisionConfig.TeacherAllowedDiffPoints == nil {
			return ErrInvalidModelOutput
		}
		execution.PrimaryThreshold, execution.TeacherThreshold = plan.DecisionConfig.PrimaryAllowedDiffPoints, plan.DecisionConfig.TeacherAllowedDiffPoints
	}
	identities := map[string]bool{}
	for index := range execution.RoleResults {
		result := &execution.RoleResults[index]
		identity, ok := imagePlanRoleModel(plan, result.Role)
		if !ok || seen[result.Role] || result.Model != identity {
			return ErrInvalidModelOutput
		}
		seen[result.Role] = true
		if result.Output != nil {
			execution.ExecutedRoles = append(execution.ExecutedRoles, result.Role)
			identities[strings.ToLower(identity.ModelName)+":"+strings.ToLower(identity.ModelVersion)] = true
		}
		if result.Status == "failed" {
			if strings.TrimSpace(result.ErrorCode) == "" {
				return ErrInvalidModelOutput
			}
			if result.Output != nil && (result.Output.RequestID != output.RequestID+":"+result.Role || result.Output.Telemetry.Adapter != identity.AdapterType || result.Output.Telemetry.Provider != identity.ProviderKey || result.Output.Telemetry.Deployment != identity.ConfigRevision) {
				return ErrInvalidModelOutput
			}
			execution.ReasonCodes = append(execution.ReasonCodes, result.Role+"_failed")
			continue
		}
		if result.Status != "succeeded" || result.Output == nil || result.Output.PanelExecution != nil {
			return ErrInvalidModelOutput
		}
		roleOutput := result.Output
		if roleOutput.ModelVersion != identity.ModelVersion || roleOutput.PromptVersion != ImageComparisonPromptVersion || roleOutput.Telemetry.Adapter != identity.AdapterType || roleOutput.Telemetry.Provider != identity.ProviderKey || roleOutput.Telemetry.Deployment != identity.ConfigRevision || roleOutput.RequestID != output.RequestID+":"+result.Role {
			return ErrInvalidModelOutput
		}
		roleOutput.Observation = value.Observation
		if err := projectImageJudgments(roleOutput, AdapterInput{SegmentID: value.SegmentID, Question: value.Question, Rubric: value.Rubric, CropSHA256: value.CropSHA256, Observation: value.Observation}); err != nil {
			return err
		}
		roleOutput.RawOutput = imageRawOutput(value, roleOutput.ImageJudgments)
		if ValidateOutput(*roleOutput, value) != nil || validateOutputExecutionDiagnostics(*roleOutput) != nil {
			return ErrInvalidModelOutput
		}
		for _, judgment := range roleOutput.ImageJudgments {
			if judgment.Status == "uncertain" {
				execution.ReasonCodes = appendFlag(execution.ReasonCodes, "criterion_evidence_uncertain")
			}
		}
		for _, member := range value.Observation.Members {
			if member.QuestionID == value.Question.ID && (member.Presence == "unreadable" || member.Presence == "uncertain" || member.Association != "matched") {
				execution.ReasonCodes = appendFlag(execution.ReasonCodes, "student_observation_unreadable")
			}
		}
		byRole[result.Role] = roleOutput
	}
	execution.IndependentModelCount = len(identities)
	if plan.EffectiveMode == "single" {
		if len(seen) != 1 || !seen["single"] {
			return ErrInvalidModelOutput
		}
	} else if !seen["primary_a"] || !seen["primary_b"] {
		return ErrInvalidModelOutput
	}
	var candidate *AdapterOutput
	for _, role := range []string{"primary_a", "single", "primary_b", "arbiter"} {
		if byRole[role] != nil {
			candidate = byRole[role]
			break
		}
	}
	if candidate == nil {
		return ErrInvalidModelOutput
	}
	base := *candidate
	base.RequestID, base.ModelVersion, base.PromptVersion = output.RequestID, policy.ModelVersion, policy.PromptVersion
	base.PanelExecution = execution
	execution.Decision = "teacher_review"
	if len(execution.ReasonCodes) == 0 {
		if plan.EffectiveMode == "single" {
			raw := candidate.SuggestedScore
			execution.RawScore, execution.FinalScore = &raw, &raw
			execution.Decision = "single_suggestion"
		} else {
			a, b := byRole["primary_a"], byRole["primary_b"]
			if a == nil || b == nil {
				return ErrInvalidModelOutput
			}
			raw := (a.SuggestedScore + b.SuggestedScore) / 2
			if !imageAllowedDifferenceExceeded(math.Abs(a.SuggestedScore-b.SuggestedScore), *execution.PrimaryThreshold) {
				if seen["arbiter"] {
					return ErrInvalidModelOutput
				}
				execution.Decision = "primary_consensus"
			} else if c := byRole["arbiter"]; c != nil {
				raw = .25*a.SuggestedScore + .25*b.SuggestedScore + .5*c.SuggestedScore
				execution.Decision = "arbiter_weighted"
				span := math.Max(math.Max(a.SuggestedScore, b.SuggestedScore), c.SuggestedScore) - math.Min(math.Min(a.SuggestedScore, b.SuggestedScore), c.SuggestedScore)
				if imageAllowedDifferenceExceeded(math.Abs(c.SuggestedScore-(a.SuggestedScore+b.SuggestedScore)/2), *execution.TeacherThreshold) || imageAllowedDifferenceExceeded(span, 2*(*execution.TeacherThreshold)) {
					execution.ReasonCodes = append(execution.ReasonCodes, "arbiter_difference_exceeds_teacher_threshold")
					execution.Decision = "teacher_review"
				}
			} else {
				execution.ReasonCodes = append(execution.ReasonCodes, "arbiter_unavailable_for_difference")
			}
			if execution.Decision != "teacher_review" {
				final := imageCeilFinal(raw, value.Question.Score)
				execution.RawScore, execution.FinalScore = &raw, &final
			}
		}
	}
	if execution.FinalScore != nil {
		base.SuggestedScore = *execution.FinalScore
	}
	base.RawOutput = imageRawOutput(value, base.ImageJudgments)
	base.RawOutput["panel_execution"] = imagePanelPublicOutput(execution)
	base.RawOutput["score_prefill_allowed"] = execution.FinalScore != nil
	base.NeedsHumanReview = true
	for _, code := range execution.ReasonCodes {
		base.RiskFlags = appendFlag(base.RiskFlags, code)
	}
	*output = base
	return nil
}

// 管理员设置的是允许分差；达到允许值仍可汇总，只有超过允许值和浮点容差才升级。
func imageAllowedDifferenceExceeded(difference, allowed float64) bool {
	return difference > allowed && math.Abs(difference-allowed) > 1e-9
}

func imageCeilFinal(raw, maximum float64) float64 {
	if nearest := math.Round(raw); math.Abs(raw-nearest) <= 1e-9 {
		raw = nearest
	}
	return math.Min(maximum, math.Max(0, math.Ceil(raw)))
}

func imagePanelPublicOutput(execution *ImagePanelExecution) map[string]any {
	raw, _ := json.Marshal(execution)
	var public map[string]any
	_ = json.Unmarshal(raw, &public)
	rows := []map[string]any{}
	for _, result := range execution.RoleResults {
		row := map[string]any{"role": result.Role, "status": result.Status, "model": result.Model}
		if result.ErrorCode != "" {
			row["error_code"] = result.ErrorCode
		}
		if result.Output != nil {
			row["output"] = result.Output
			if result.Status == "succeeded" {
				row["score"], row["confidence"] = result.Output.SuggestedScore, result.Output.Confidence
				row["matched_points"], row["missing_points"], row["evidence"], row["raw_output"] = result.Output.MatchedPoints, result.Output.MissingPoints, result.Output.Evidence, result.Output.RawOutput
			}
		}
		rows = append(rows, row)
	}
	public["role_results"] = rows
	return public
}
