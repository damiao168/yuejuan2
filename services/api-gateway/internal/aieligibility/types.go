// Package aieligibility 为所有外部 AI 评分调用提供服务端准入控制。它与 subjective 分离；后者仅在本包记录决策后执行请求。
package aieligibility

import (
	"context"
	"errors"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

var (
	ErrNotFound       = errors.New("AI eligibility resource not found")
	ErrInvalidInput   = errors.New("invalid AI eligibility input")
	ErrPolicyConflict = errors.New("AI eligibility policy version conflict")
)

type PolicyStatus string

const (
	PolicyActive   PolicyStatus = "active"
	PolicyDisabled PolicyStatus = "disabled"
)

func (s PolicyStatus) Valid() bool { return s == PolicyActive || s == PolicyDisabled }

// Policy 是租户所有、按版本管理的单个评估维度策略。策略不指定模型；模型专属的实证证据放在 DecisionInput 中，使每次实际评分运行可审计。
type Policy struct {
	ID                 string                    `json:"id"`
	TenantID           string                    `json:"tenant_id"`
	SubjectCode        assessment.SubjectCode    `json:"subject_code"`
	EducationStage     assessment.EducationStage `json:"education_stage"`
	ArchetypeCode      string                    `json:"archetype_code"`
	RiskTier           assessment.RiskTier       `json:"risk_tier"`
	MinOCRQuality      float64                   `json:"min_ocr_quality"`
	MinParserQuality   float64                   `json:"min_parser_quality"`
	MinEvalN           int                       `json:"min_eval_n"`
	MaxSevereErrorRate float64                   `json:"max_severe_error_rate"`
	AllowedModes       []assessment.ScoringMode  `json:"allowed_modes"`
	Version            int                       `json:"version"`
	Status             PolicyStatus              `json:"status"`
	CreatedAt          time.Time                 `json:"created_at"`
}

type PutPolicyInput struct {
	SubjectCode        assessment.SubjectCode    `json:"subject_code"`
	EducationStage     assessment.EducationStage `json:"education_stage"`
	ArchetypeCode      string                    `json:"archetype_code"`
	RiskTier           assessment.RiskTier       `json:"risk_tier"`
	MinOCRQuality      float64                   `json:"min_ocr_quality"`
	MinParserQuality   float64                   `json:"min_parser_quality"`
	MinEvalN           int                       `json:"min_eval_n"`
	MaxSevereErrorRate float64                   `json:"max_severe_error_rate"`
	AllowedModes       []assessment.ScoringMode  `json:"allowed_modes"`
	Status             PolicyStatus              `json:"status"`
	ExpectedVersion    int                       `json:"expected_version"`
}

type EvaluationEvidence struct {
	Approved        bool    `json:"approved"`
	SampleCount     int     `json:"sample_count"`
	SevereErrorRate float64 `json:"severe_error_rate"`
	EvaluationRef   string  `json:"evaluation_ref,omitempty"`
}

// CalibrationEvidence 只说明模型、提示词和部署能否用于当前冻结的题目上下文，不暴露 Gold 答案、校准提交或教师质量记录。
type CalibrationEvidence struct {
	Available      bool   `json:"available"`
	CalibrationRef string `json:"calibration_ref,omitempty"`
}

type DecisionInput struct {
	// 仅内部图像 v3 入口提供；观察绑定与裁图哈希由调用方先校验，不能由 HTTP 客户端声称已完成。
	InputProtocol               string `json:"input_protocol,omitempty"`
	InputVersionID              string `json:"input_version_id,omitempty"`
	StudentObservationAvailable bool   `json:"student_observation_available,omitempty"`
	// RunItemID 是幂等和审计键，由调用方提供且视为不透明 ID；它可以表示主观题评分运行、工作项或试运行项。
	RunItemID          string                          `json:"run_item_id"`
	AssessmentSnapshot assessment.ExamQuestionSnapshot `json:"assessment_snapshot"`
	RequestedMode      assessment.ScoringMode          `json:"requested_mode"`
	OCRQuality         *float64                        `json:"ocr_quality,omitempty"`
	ParserQuality      *float64                        `json:"parser_quality,omitempty"`
	RubricComplete     bool                            `json:"rubric_complete"`
	EvidenceAvailable  bool                            `json:"evidence_available"`
	Evaluation         EvaluationEvidence              `json:"evaluation"`
	Calibration        CalibrationEvidence             `json:"calibration"`
}

type Reason struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
}

// OutputConstraint 明确产品边界：即使允许调用 AI，模型也只返回评分标准和证据观察；分数由服务端按冻结的评分细则派生，或由人工流程裁定。
type OutputConstraint struct {
	CriteriaEvidenceOnly bool   `json:"criteria_evidence_only"`
	AllowModelFinalScore bool   `json:"allow_model_final_score"`
	FinalScoreAuthority  string `json:"final_score_authority"`
	// MaxSevereErrorRisk 从当前启用的不可变策略复制，供 A15 记录模型置信度候选值时使用。它不是分数阈值，也不授予最终评分权。
	MaxSevereErrorRisk float64 `json:"max_severe_error_risk"`
}

type Decision struct {
	ID                string                 `json:"id"`
	TenantID          string                 `json:"tenant_id"`
	RunItemID         string                 `json:"run_item_id"`
	PolicyID          string                 `json:"policy_id,omitempty"`
	PolicyVersion     int                    `json:"policy_version"`
	Decision          assessment.ScoringMode `json:"decision"`
	ExternalAIAllowed bool                   `json:"external_ai_allowed"`
	InputSnapshot     map[string]any         `json:"input_snapshot"`
	Reasons           []Reason               `json:"reasons"`
	OutputConstraint  OutputConstraint       `json:"output_constraint"`
	CreatedAt         time.Time              `json:"created_at"`
}

func (d Decision) CanCallExternalAI() bool { return d.ExternalAIAllowed }

// Gate 是评分流水线所需的唯一依赖。调用适配器前必须先调用 Decide，再使用 CanCallExternalAI；处理器或工作器不应重新实现风险逻辑。
type Gate interface {
	Decide(context.Context, string, DecisionInput) (Decision, error)
}

type Store interface {
	PutPolicy(context.Context, string, PutPolicyInput) (Policy, error)
	GetActivePolicy(context.Context, string, assessment.SubjectCode, assessment.EducationStage, string, assessment.RiskTier) (Policy, error)
	CreateOrGetDecision(context.Context, string, Decision) (Decision, error)
	GetDecision(context.Context, string, string) (Decision, error)
}
