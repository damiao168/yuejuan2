package grading

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrGradingAIPlanChanged = errors.New("exam grading AI plan changed")
var ErrGradingAIPlanConfirmationRequired = errors.New("exam grading AI plan confirmation required")
var ErrGradingAIThresholdRequired = errors.New("exam grading AI allowed difference required")
var ErrGradingAIModelRequired = errors.New("exam grading AI model required")

type GradingAIRoleSelection struct {
	ModelConfigID string `json:"model_config_id"`
	StrengthRank  int    `json:"strength_rank"`
}

type GradingAIDecisionConfig struct {
	PrimaryAllowedDiffPoints *float64 `json:"primary_allowed_diff_points"`
	TeacherAllowedDiffPoints *float64 `json:"teacher_allowed_diff_points"`
	AggregationVersion       string   `json:"aggregation_version"`
	Rounding                 string   `json:"rounding"`
}

type GradingAIPlanInput struct {
	ExpectedRevision      string                            `json:"expected_revision"`
	FallbackModelConfigID string                            `json:"fallback_model_config_id"`
	Roles                 map[string]GradingAIRoleSelection `json:"roles"`
	DecisionConfig        GradingAIDecisionConfig           `json:"decision_config"`
}

// GradingAIModel 只保存可公开的模型身份；连接地址和凭据不进入考试方案或运行快照。
// BindingRevision 固定当时学校选择的版本种子，之后换选不能改变旧运行的模型绑定。
type GradingAIModel struct {
	ModelConfigID     string `json:"model_config_id"`
	DisplayName       string `json:"display_name"`
	ProviderKey       string `json:"provider_key"`
	AdapterType       string `json:"adapter_type"`
	ModelName         string `json:"model_name"`
	ModelVersion      string `json:"model_version"`
	ConfigRevision    string `json:"config_revision"`
	BindingRevision   string `json:"binding_revision"`
	StrengthRank      int    `json:"strength_rank,omitempty"`
	VisionProbeStatus string `json:"vision_probe_status,omitempty"`
	VisionProbeReason string `json:"vision_probe_reason,omitempty"`
}

type GradingAIPlan struct {
	Version               int                               `json:"version"`
	Revision              string                            `json:"revision"`
	FallbackModelConfigID string                            `json:"fallback_model_config_id"`
	Roles                 map[string]GradingAIRoleSelection `json:"roles"`
	DecisionConfig        GradingAIDecisionConfig           `json:"decision_config"`
	EffectiveMode         string                            `json:"effective_mode"`
	ReasonCodes           []string                          `json:"reason_codes"`
	ResolvedRoles         map[string]GradingAIModel         `json:"resolved_roles"`
	SingleModel           *GradingAIModel                   `json:"single_model,omitempty"`
	IndependentModelCount int                               `json:"independent_model_count"`
	ConfirmedBy           string                            `json:"confirmed_by,omitempty"`
	ConfirmedAt           *time.Time                        `json:"confirmed_at,omitempty"`
}

type GradingAIPlanDetail struct {
	Plan            GradingAIPlan    `json:"plan"`
	AvailableModels []GradingAIModel `json:"available_models"`
	CanConfigure    bool             `json:"can_configure"`
}

type GradingVisionProbe struct {
	ModelConfigID     string `json:"model_config_id"`
	VisionProbeStatus string `json:"vision_probe_status"`
	VisionProbeReason string `json:"vision_probe_reason"`
	CacheHit          bool   `json:"cache_hit"`
}

// GradingModelConfigRevision 与图像协议共用连接指纹。种子来自冻结方案，不能在执行时改读当前学校选择。
func GradingModelConfigRevision(tenantID, bindingRevision, configID, adapterType, baseURL, modelName, modelVersion string, updatedAt time.Time) string {
	raw, _ := json.Marshal([]string{tenantID, bindingRevision, configID, updatedAt.UTC().Format(time.RFC3339Nano), adapterType, baseURL, modelName, modelVersion})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func normalizeGradingAIPlanInput(input GradingAIPlanInput) (GradingAIPlanInput, error) {
	input.ExpectedRevision = strings.TrimSpace(input.ExpectedRevision)
	input.FallbackModelConfigID = strings.TrimSpace(input.FallbackModelConfigID)
	if input.ExpectedRevision != "absent" && uuid.Validate(input.ExpectedRevision) != nil {
		return input, ErrInvalidInput
	}
	if input.FallbackModelConfigID != "" && uuid.Validate(input.FallbackModelConfigID) != nil {
		return input, ErrInvalidInput
	}
	for role, selected := range input.Roles {
		if role != "primary_a" && role != "primary_b" && role != "arbiter" {
			return input, ErrInvalidInput
		}
		selected.ModelConfigID = strings.TrimSpace(selected.ModelConfigID)
		if selected.ModelConfigID != "" && uuid.Validate(selected.ModelConfigID) != nil || selected.StrengthRank < 0 || selected.StrengthRank > 100 {
			return input, ErrInvalidInput
		}
		input.Roles[role] = selected
	}
	for _, threshold := range []*float64{input.DecisionConfig.PrimaryAllowedDiffPoints, input.DecisionConfig.TeacherAllowedDiffPoints} {
		if threshold != nil && (math.IsNaN(*threshold) || math.IsInf(*threshold, 0) || *threshold < 0 || *threshold > 1000) {
			return input, ErrInvalidInput
		}
	}
	primary, teacher := input.DecisionConfig.PrimaryAllowedDiffPoints, input.DecisionConfig.TeacherAllowedDiffPoints
	if primary != nil && teacher != nil && *primary > *teacher || input.DecisionConfig.AggregationVersion != "dual-conditional-arbiter-v1" || input.DecisionConfig.Rounding != "ceil_final" {
		return input, ErrInvalidInput
	}
	if input.Roles == nil {
		input.Roles = map[string]GradingAIRoleSelection{}
	}
	return input, nil
}

// GradingAIPlanAppliesToQuestion 将多评方案限定为大题；选择、判断和填空继续使用原有路径。
func GradingAIPlanAppliesToQuestion(questionType string) bool {
	return questionType == "short_answer" || questionType == "calculation" || questionType == "essay" || questionType == "discussion"
}

func gradingAIPlanThresholdRequirement(plan GradingAIPlan) error {
	if (plan.EffectiveMode == "dual" || plan.EffectiveMode == "dual_conditional_arbiter") && (plan.DecisionConfig.PrimaryAllowedDiffPoints == nil || plan.DecisionConfig.TeacherAllowedDiffPoints == nil) {
		return ErrGradingAIThresholdRequired
	}
	return nil
}

func gradingAIPlanStartRequirement(plan GradingAIPlan, hasLargeImageAI bool) error {
	if !hasLargeImageAI {
		return nil
	}
	if plan.EffectiveMode == "unconfigured" {
		return ErrGradingAIModelRequired
	}
	if plan.Revision == "absent" || plan.Revision == "" {
		return ErrGradingAIPlanConfirmationRequired
	}
	return gradingAIPlanThresholdRequirement(plan)
}

func gradingAIModelIdentity(model GradingAIModel) string {
	return strings.ToLower(strings.TrimSpace(model.ModelName)) + ":" + strings.ToLower(strings.TrimSpace(model.ModelVersion))
}

// resolveGradingAIPlan 只根据本校可用连接判定实际模式；重复配置不能冒充多个独立模型。
// 未配置完整评审组时继续选择明确的备用模型，保留降级原因供管理员和教师核对。
func resolveGradingAIPlan(plan GradingAIPlan, models []GradingAIModel) GradingAIPlan {
	plan.Version = 1
	plan.EffectiveMode = "single"
	plan.ReasonCodes = []string{}
	plan.ResolvedRoles = map[string]GradingAIModel{}
	plan.SingleModel = nil
	available := map[string]GradingAIModel{}
	for _, model := range models {
		available[model.ModelConfigID] = model
	}
	for _, role := range []string{"primary_a", "primary_b", "arbiter"} {
		selected := plan.Roles[role]
		model, ok := available[selected.ModelConfigID]
		if !ok {
			continue
		}
		model.StrengthRank = selected.StrengthRank
		plan.ResolvedRoles[role] = model
	}
	a, hasA := plan.ResolvedRoles["primary_a"]
	b, hasB := plan.ResolvedRoles["primary_b"]
	c, hasC := plan.ResolvedRoles["arbiter"]
	if hasA && hasB && gradingAIModelIdentity(a) != gradingAIModelIdentity(b) {
		plan.EffectiveMode = "dual"
		plan.IndependentModelCount = 2
		if hasC && gradingAIModelIdentity(c) != gradingAIModelIdentity(a) && gradingAIModelIdentity(c) != gradingAIModelIdentity(b) {
			if c.StrengthRank > a.StrengthRank && c.StrengthRank > b.StrengthRank && a.StrengthRank > 0 && b.StrengthRank > 0 {
				plan.EffectiveMode = "dual_conditional_arbiter"
				plan.IndependentModelCount = 3
			} else {
				plan.ReasonCodes = append(plan.ReasonCodes, "arbiter_strength_not_higher")
			}
		} else if hasC {
			plan.ReasonCodes = append(plan.ReasonCodes, "three_agent_models_not_distinct")
		} else {
			plan.ReasonCodes = append(plan.ReasonCodes, "three_agent_configuration_incomplete")
		}
	} else if hasA && hasB {
		plan.ReasonCodes = append(plan.ReasonCodes, "primary_models_not_distinct")
	} else {
		plan.ReasonCodes = append(plan.ReasonCodes, "primary_configuration_incomplete")
	}
	if model, ok := available[plan.FallbackModelConfigID]; ok {
		plan.SingleModel = &model
	}
	if plan.SingleModel == nil {
		for _, role := range []string{"primary_a", "primary_b", "arbiter"} {
			if model, ok := plan.ResolvedRoles[role]; ok {
				plan.SingleModel = &model
				break
			}
		}
	}
	if plan.EffectiveMode == "single" {
		plan.IndependentModelCount = 0
		if plan.SingleModel != nil {
			plan.IndependentModelCount = 1
		} else {
			plan.EffectiveMode = "unconfigured"
			plan.ReasonCodes = append(plan.ReasonCodes, "grading_model_unconfigured")
		}
	}
	return plan
}
