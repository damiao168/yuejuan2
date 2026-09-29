package modelgovernance

import (
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	EvaluationEvidenceProtocolFixture     = "protocol_fixture"
	EvaluationEvidenceAuthorizedFrozenSet = "authorized_frozen_set"
	EvaluationStatusDraft                 = "draft"
	EvaluationStatusCompleted             = "completed"
	EvaluationStatusInvalidated           = "invalidated"
	maxEvaluationSamples                  = 100000
	maxEvaluationRepeats                  = 20
	maxEvaluationTextLength               = 128
)

var sha256Hex = regexp.MustCompile(`^[a-f0-9]{64}$`)

type EvaluationRun struct {
	ID               string                `json:"id"`
	TenantID         string                `json:"tenant_id,omitempty"`
	Key              string                `json:"run_key"`
	DisplayName      string                `json:"display_name"`
	DatasetReference string                `json:"dataset_reference"`
	DatasetSHA256    string                `json:"dataset_sha256"`
	AuthorizationRef string                `json:"authorization_reference,omitempty"`
	EvidenceClass    string                `json:"evidence_class"`
	Subject          string                `json:"subject"`
	Grade            string                `json:"grade"`
	QuestionType     string                `json:"question_type"`
	Modality         string                `json:"modality"`
	SampleCount      int                   `json:"sample_count"`
	RepeatCount      int                   `json:"repeat_count"`
	Status           string                `json:"status"`
	Candidates       []EvaluationCandidate `json:"candidates"`
	CompletedAt      *time.Time            `json:"completed_at,omitempty"`
	InvalidatedAt    *time.Time            `json:"invalidated_at,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
}

type EvaluationListFilter struct {
	Limit           int
	CursorCreatedAt time.Time
	CursorID        string
}

type EvaluationCandidate struct {
	ID                     string            `json:"id"`
	TenantID               string            `json:"tenant_id,omitempty"`
	RunID                  string            `json:"run_id"`
	DeploymentID           string            `json:"deployment_id"`
	ModelConfigID          string            `json:"model_config_id,omitempty"`
	ProviderKey            string            `json:"provider_key"`
	DeploymentKey          string            `json:"deployment_key"`
	ModelName              string            `json:"model_name"`
	ModelVersion           string            `json:"model_version"`
	PromptVersion          string            `json:"prompt_version"`
	RubricVersion          string            `json:"rubric_version"`
	EvaluatedSamples       int               `json:"evaluated_samples"`
	TeacherReviewedSamples int               `json:"teacher_reviewed_samples"`
	TeacherAcceptedSamples int               `json:"teacher_accepted_samples"`
	SeriousErrorSamples    int               `json:"serious_error_samples"`
	EvidenceValidSamples   int               `json:"evidence_valid_samples"`
	RepeatComparisons      int               `json:"repeat_comparisons"`
	StableRepeatSamples    int               `json:"stable_repeat_samples"`
	P95LatencyMS           int64             `json:"p95_latency_ms"`
	TotalCostMicros        int64             `json:"total_cost_micros"`
	Metrics                EvaluationMetrics `json:"metrics"`
	CreatedAt              time.Time         `json:"created_at"`
}

type EvaluationMetrics struct {
	TeacherAcceptanceRate float64 `json:"teacher_acceptance_rate"`
	SeriousErrorRate      float64 `json:"serious_error_rate"`
	EvidenceValidityRate  float64 `json:"evidence_validity_rate"`
	StabilityRate         float64 `json:"stability_rate"`
	AverageCostMicros     float64 `json:"average_cost_micros"`
}

type EvaluationRunInput struct {
	TenantID         string `json:"tenant_id,omitempty"`
	Key              string `json:"run_key"`
	DisplayName      string `json:"display_name"`
	DatasetReference string `json:"dataset_reference"`
	DatasetSHA256    string `json:"dataset_sha256"`
	AuthorizationRef string `json:"authorization_reference,omitempty"`
	EvidenceClass    string `json:"evidence_class"`
	Subject          string `json:"subject"`
	Grade            string `json:"grade"`
	QuestionType     string `json:"question_type"`
	Modality         string `json:"modality"`
	SampleCount      int    `json:"sample_count"`
	RepeatCount      int    `json:"repeat_count"`
	Reason           string `json:"reason"`
}

type EvaluationCandidateInput struct {
	DeploymentID           string `json:"deployment_id"`
	ModelConfigID          string `json:"model_config_id"`
	PromptVersion          string `json:"prompt_version"`
	RubricVersion          string `json:"rubric_version"`
	EvaluatedSamples       int    `json:"evaluated_samples"`
	TeacherReviewedSamples int    `json:"teacher_reviewed_samples"`
	TeacherAcceptedSamples int    `json:"teacher_accepted_samples"`
	SeriousErrorSamples    int    `json:"serious_error_samples"`
	EvidenceValidSamples   int    `json:"evidence_valid_samples"`
	RepeatComparisons      int    `json:"repeat_comparisons"`
	StableRepeatSamples    int    `json:"stable_repeat_samples"`
	P95LatencyMS           int64  `json:"p95_latency_ms"`
	TotalCostMicros        int64  `json:"total_cost_micros"`
	Reason                 string `json:"reason"`
}

type EvaluationTransitionInput struct {
	Reason string `json:"reason"`
}

// 评测运行先固定证据类型、数据摘要和样本上限，防止把未经授权的数据当成质量结论。
func ValidateEvaluationRunInput(input EvaluationRunInput) error {
	if !governanceKey.MatchString(strings.TrimSpace(input.Key)) ||
		!safeEvaluationText(input.DisplayName) ||
		!governanceKey.MatchString(strings.TrimSpace(input.DatasetReference)) ||
		!sha256Hex.MatchString(strings.TrimSpace(input.DatasetSHA256)) ||
		(input.EvidenceClass != EvaluationEvidenceProtocolFixture &&
			input.EvidenceClass != EvaluationEvidenceAuthorizedFrozenSet) ||
		(input.EvidenceClass == EvaluationEvidenceProtocolFixture &&
			strings.TrimSpace(input.AuthorizationRef) != "") ||
		(input.EvidenceClass == EvaluationEvidenceAuthorizedFrozenSet &&
			!governanceKey.MatchString(strings.TrimSpace(input.AuthorizationRef))) ||
		!safeEvaluationText(input.Subject) ||
		!safeEvaluationText(input.Grade) ||
		!governanceKey.MatchString(strings.TrimSpace(input.QuestionType)) ||
		(input.Modality != "text" && input.Modality != "image") ||
		input.SampleCount < 1 ||
		input.SampleCount > maxEvaluationSamples ||
		input.RepeatCount < 1 ||
		input.RepeatCount > maxEvaluationRepeats ||
		strings.TrimSpace(input.Reason) == "" {
		return ErrInvalidEvaluation
	}
	return nil
}

func ValidateEvaluationCandidateInput(input EvaluationCandidateInput, run EvaluationRun) error {
	maxComparisons := run.SampleCount * (run.RepeatCount - 1)
	if (strings.TrimSpace(input.DeploymentID) == "") == (strings.TrimSpace(input.ModelConfigID) == "") ||
		!safeEvaluationText(input.PromptVersion) ||
		!safeEvaluationText(input.RubricVersion) ||
		input.EvaluatedSamples != run.SampleCount ||
		input.TeacherReviewedSamples < 0 ||
		input.TeacherReviewedSamples > input.EvaluatedSamples ||
		input.TeacherAcceptedSamples < 0 ||
		input.TeacherAcceptedSamples > input.TeacherReviewedSamples ||
		input.SeriousErrorSamples < 0 ||
		input.SeriousErrorSamples > input.EvaluatedSamples ||
		input.EvidenceValidSamples < 0 ||
		input.EvidenceValidSamples > input.EvaluatedSamples ||
		input.RepeatComparisons < 0 ||
		input.RepeatComparisons > maxComparisons ||
		input.StableRepeatSamples < 0 ||
		input.StableRepeatSamples > input.RepeatComparisons ||
		(run.RepeatCount == 1 && (input.RepeatComparisons != 0 || input.StableRepeatSamples != 0)) ||
		(run.RepeatCount > 1 && input.RepeatComparisons == 0) ||
		input.P95LatencyMS < 0 ||
		input.TotalCostMicros < 0 ||
		strings.TrimSpace(input.Reason) == "" {
		return ErrInvalidEvaluation
	}
	if run.EvidenceClass == EvaluationEvidenceAuthorizedFrozenSet &&
		input.TeacherReviewedSamples != input.EvaluatedSamples {
		return ErrInvalidEvaluation
	}
	if run.EvidenceClass == EvaluationEvidenceProtocolFixture &&
		(input.TeacherReviewedSamples != 0 || input.TeacherAcceptedSamples != 0) {
		return ErrInvalidEvaluation
	}
	return nil
}

// 指标分母保持原始样本口径；没有可比较样本时返回 0，避免伪造稳定性或接受率。
func PopulateEvaluationMetrics(candidate EvaluationCandidate) EvaluationCandidate {
	candidate.Metrics = EvaluationMetrics{
		TeacherAcceptanceRate: ratio(candidate.TeacherAcceptedSamples, candidate.TeacherReviewedSamples),
		SeriousErrorRate:      ratio(candidate.SeriousErrorSamples, candidate.EvaluatedSamples),
		EvidenceValidityRate:  ratio(candidate.EvidenceValidSamples, candidate.EvaluatedSamples),
		StabilityRate:         ratio(candidate.StableRepeatSamples, candidate.RepeatComparisons),
		AverageCostMicros:     ratio64(candidate.TotalCostMicros, candidate.EvaluatedSamples),
	}
	return candidate
}

func safeEvaluationText(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > maxEvaluationTextLength {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func ratio(numerator int, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func ratio64(numerator int64, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
