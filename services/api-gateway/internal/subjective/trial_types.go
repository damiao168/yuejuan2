package subjective

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/grading"
)

var ErrTrialConflict = errors.New("scoring trial state or version conflict")
var ErrTrialIndependentSamplesRequired = errors.Join(ErrInvalidInput, errors.New("at least five independent samples without prior AI exposure are required"))
var ErrTrialExpansionLimitExceeded = errors.Join(ErrInvalidInput, errors.New("a trial expansion may contain at most 1000 answers"))

type TrialSampleInput struct {
	SegmentID  string   `json:"segment_id"`
	HumanScore float64  `json:"human_score"`
	Roles      []string `json:"roles"`
}

// 0 分也是有效人工基准；省略或 null 必须拒绝，不能把 float64 的默认值当成教师已录入。
func (sample *TrialSampleInput) UnmarshalJSON(raw []byte) error {
	var input struct {
		SegmentID  string   `json:"segment_id"`
		HumanScore *float64 `json:"human_score"`
		Roles      []string `json:"roles"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return err
	}
	if input.HumanScore == nil {
		return ErrInvalidInput
	}
	sample.SegmentID, sample.HumanScore, sample.Roles = input.SegmentID, *input.HumanScore, input.Roles
	return nil
}

type CreateTrialInput struct {
	CommandID  string             `json:"command_id"`
	QuestionID string             `json:"question_id"`
	Samples    []TrialSampleInput `json:"samples"`
}

type TrialDecisionInput struct {
	Decision         string `json:"decision"`
	Reason           string `json:"reason"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type TrialSampleResult struct {
	MaterialReceipt *MaterialReceiptSummary `json:"material_receipt,omitempty"`
	Status          string                  `json:"status"`
	SuggestedScore  *float64                `json:"suggested_score,omitempty"`
	GradeID         string                  `json:"grade_id,omitempty"`
	Evidence        []grading.Evidence      `json:"evidence"`
	RiskFlags       []string                `json:"risk_flags"`
	LatencyMS       *int64                  `json:"latency_ms,omitempty"`
	TotalTokens     *int64                  `json:"total_tokens,omitempty"`
	ErrorCode       string                  `json:"error_code,omitempty"`
}

type TrialSample struct {
	SegmentID  string             `json:"segment_id"`
	HumanScore float64            `json:"human_score"`
	Roles      []string           `json:"roles"`
	MaxScore   float64            `json:"max_score"`
	Result     *TrialSampleResult `json:"result,omitempty"`
}

// ScoringTrial 固定人工基准与实际样本批次。结果只读该批次的 run/grade，不能借用正式成绩或历史建议。
type ScoringTrial struct {
	ID                  string        `json:"id"`
	TenantID            string        `json:"-"`
	RunID               string        `json:"run_id"`
	ExamID              string        `json:"exam_id"`
	QuestionID          string        `json:"question_id"`
	CommandID           string        `json:"command_id"`
	Status              string        `json:"status"`
	Revision            int64         `json:"revision"`
	Fingerprint         string        `json:"fingerprint"`
	SampleBatchID       string        `json:"sample_batch_id"`
	ExpansionBatchID    string        `json:"expansion_batch_id,omitempty"`
	Samples             []TrialSample `json:"samples"`
	RemainingSegmentIDs []string      `json:"remaining_segment_ids"`
	DecidedBy           string        `json:"decided_by,omitempty"`
	DecisionReason      string        `json:"decision_reason,omitempty"`
	CreatedBy           string        `json:"created_by"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
	RequestHash         string        `json:"-"`
}

// TrialPersistence 是旧批次存储的可选扩展；生产和内存实现都把基准与样本批次放在同一事务/锁内。
type TrialPersistence interface {
	CreateScoringTrial(context.Context, string, string, string, CreateTrialInput, ModelPolicy) (ScoringTrial, error)
	ListScoringTrials(context.Context, string, string, ModelPolicy) ([]ScoringTrial, error)
	DecideScoringTrial(context.Context, string, string, string, string, TrialDecisionInput, ModelPolicy) (ScoringTrial, error)
	PrepareScoringTrialEnqueue(context.Context, string, string, string, ModelPolicy) (ScoringTrial, error)
	AuthorizeTrialBatch(context.Context, string, string, ModelPolicy) error
}
