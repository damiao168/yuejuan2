// Package qualitydashboard 构建学校管理员所用的考试级质量投影。
// 它只提供聚合结果，绝不返回答案内容、Gold 参考分数、考生身份或复核意见。
package qualitydashboard

import (
	"context"
	"errors"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/answergroup"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
)

var (
	ErrInvalidInput = errors.New("invalid quality dashboard input")
	ErrNotFound     = errors.New("quality dashboard resource not found")
)

type GateStatus string

const (
	GateReady            GateStatus = "ready"
	GateWarning          GateStatus = "warning"
	GateBlocked          GateStatus = "blocked"
	GateInsufficientData GateStatus = "insufficient_data"
)

// Ratio 始终携带计数基数。分母为零表示该比率有意保持不可用，不表示比率为零。
type Ratio struct {
	Numerator   int      `json:"numerator"`
	Denominator int      `json:"denominator"`
	SampleSize  int      `json:"sample_size"`
	Rate        *float64 `json:"rate,omitempty"`
}

type Question struct {
	ID            string  `json:"id"`
	QuestionNo    string  `json:"question_no"`
	ArchetypeCode string  `json:"archetype_code"`
	RiskTier      string  `json:"risk_tier"`
	MaxScore      float64 `json:"max_score"`
}

type Finding struct {
	Code        string     `json:"code"`
	Status      GateStatus `json:"status"`
	Reason      string     `json:"reason"`
	Owner       string     `json:"owner"`
	Resolution  string     `json:"resolution"`
	QuestionID  string     `json:"question_id"`
	QuestionNo  string     `json:"question_no"`
	GraderRef   string     `json:"grader_ref,omitempty"`
	IncidentRef string     `json:"incident_ref,omitempty"`
}

type GoldSummary struct {
	ActiveApproved int      `json:"active_approved"`
	Ready          bool     `json:"ready"`
	Gaps           []string `json:"gaps"`
	ScoreBands     []Band   `json:"score_bands"`
}

type Band struct {
	Band  string `json:"band"`
	Count int    `json:"count"`
}

type CalibrationGrader struct {
	GraderRef  string `json:"grader_ref"`
	Status     string `json:"status"`
	SampleSize int    `json:"sample_size"`
}

type CalibrationSummary struct {
	Configured bool                `json:"configured"`
	Completed  int                 `json:"completed"`
	Passed     int                 `json:"passed"`
	PassRate   Ratio               `json:"pass_rate"`
	Graders    []CalibrationGrader `json:"graders"`
}

type GraderSeedMetrics struct {
	GraderRef          string `json:"grader_ref"`
	SampleSize         int    `json:"sample_size"`
	ExactAgreement     Ratio  `json:"exact_agreement"`
	WithinOneAgreement Ratio  `json:"within_one_agreement"`
	CriterionAgreement Ratio  `json:"criterion_agreement"`
}

type SeedSummary struct {
	PolicyStatus       string              `json:"policy_status"`
	SampleSize         int                 `json:"sample_size"`
	ExactAgreement     Ratio               `json:"exact_agreement"`
	WithinOneAgreement Ratio               `json:"within_one_agreement"`
	CriterionAgreement Ratio               `json:"criterion_agreement"`
	Graders            []GraderSeedMetrics `json:"graders"`
}

type HumanAgreementSummary struct {
	SampleSize int   `json:"sample_size"`
	WithinRule Ratio `json:"within_rule_agreement"`
	OpenCases  int   `json:"open_cases"`
}

type GroupSummary struct {
	GroupCount       int      `json:"group_count"`
	MemberCount      int      `json:"member_count"`
	Homogeneity      *float64 `json:"homogeneity,omitempty"`
	OverrideRate     *float64 `json:"override_rate,omitempty"`
	OpenSampleGroups int      `json:"open_sample_groups"`
}

// DriftSummary 和 BackmarkSummary 是范围受限的扩展契约。
// 可接入 A11/A12，而无需本包依赖其具体存储 API 或迁移顺序。
type DriftSummary struct {
	OpenWarnings int        `json:"open_warnings"`
	OpenCritical int        `json:"open_critical"`
	Incidents    []Incident `json:"incidents"`
}

type Incident struct {
	ID        string `json:"id"`
	GraderRef string `json:"grader_ref"`
	Severity  string `json:"severity"`
	Status    string `json:"status"`
	Type      string `json:"type"`
}

type BackmarkSummary struct {
	OpenBatches    int   `json:"open_batches"`
	PendingItems   int   `json:"pending_items"`
	CompletedItems int   `json:"completed_items"`
	CorrectionRate Ratio `json:"correction_rate"`
}

type QuestionQuality struct {
	Question       Question              `json:"question"`
	Gate           GateStatus            `json:"gate"`
	Gold           GoldSummary           `json:"gold"`
	Calibration    CalibrationSummary    `json:"calibration"`
	Seed           SeedSummary           `json:"seed"`
	HumanAgreement HumanAgreementSummary `json:"human_human_agreement"`
	AnswerGroups   GroupSummary          `json:"answer_groups"`
	Drift          DriftSummary          `json:"drift"`
	Backmark       BackmarkSummary       `json:"backmark"`
	Findings       []Finding             `json:"findings"`
}

type Dashboard struct {
	ExamID      string            `json:"exam_id"`
	Gate        GateStatus        `json:"gate"`
	Blocking    []Finding         `json:"blocking"`
	Warnings    []Finding         `json:"warnings"`
	Questions   []QuestionQuality `json:"questions"`
	GeneratedAt time.Time         `json:"generated_at"`
}

type QuestionReader interface {
	ListQualityQuestions(context.Context, string, string) ([]Question, error)
}

type CalibrationReader interface {
	GetCalibrationSummary(context.Context, string, string, string) (CalibrationSummary, error)
}

type DriftReader interface {
	GetDriftSummary(context.Context, string, string, string) (DriftSummary, error)
}

type BackmarkReader interface {
	GetBackmarkSummary(context.Context, string, string, string) (BackmarkSummary, error)
}

type Sources struct {
	Questions   QuestionReader
	Gold        goldpaper.Store
	Calibration CalibrationReader
	Seeds       seedquality.Store
	Groups      answergroup.Store
	Review      review.Store
	Drift       DriftReader
	Backmark    BackmarkReader
}
