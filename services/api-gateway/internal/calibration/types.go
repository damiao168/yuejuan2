package calibration

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"errors"
	"time"
)

var (
	ErrNotFound              = errors.New("calibration resource not found")
	ErrInvalidInput          = errors.New("invalid calibration input")
	ErrConflict              = errors.New("calibration state conflict")
	ErrPolicyMissing         = errors.New("calibration policy is not configured")
	ErrGoldSetIncomplete     = errors.New("active approved Gold Set is insufficient")
	ErrSampleNotInSession    = errors.New("gold paper is not part of this calibration session")
	ErrQualificationRequired = errors.New("current grader qualification is required")
)

type SessionStatus string

const (
	SessionInProgress  SessionStatus = "in_progress"
	SessionPassed      SessionStatus = "passed"
	SessionFailed      SessionStatus = "failed"
	SessionInvalidated SessionStatus = "invalidated"
)

type QualificationStatus string

const (
	QualificationQualified QualificationStatus = "qualified"
	QualificationExpired   QualificationStatus = "expired"
	QualificationRevoked   QualificationStatus = "revoked"
)

// Policy 按考试题目存储，属于数据而非包常量；两分简答题和长篇作文无法共用一个有意义的全国统一阈值。
type Policy struct {
	ID                        string    `json:"id"`
	ExamID                    string    `json:"exam_id"`
	QuestionID                string    `json:"question_id"`
	ArchetypeCode             string    `json:"archetype_code"`
	MaxScore                  float64   `json:"max_score"`
	MinimumSamples            int       `json:"minimum_samples"`
	MaximumMAE                float64   `json:"maximum_mae"`
	MinimumExactAgreement     float64   `json:"minimum_exact_agreement"`
	MinimumWithinOneAgreement float64   `json:"minimum_within_one_agreement"`
	MinimumCriterionAgreement *float64  `json:"minimum_criterion_agreement,omitempty"`
	MaximumSevereRate         float64   `json:"maximum_severe_rate"`
	SevereErrorThreshold      float64   `json:"severe_error_threshold"`
	QualificationValidityDays int       `json:"qualification_validity_days"`
	Revision                  int64     `json:"revision"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

type PutPolicyInput struct {
	ArchetypeCode             string   `json:"archetype_code"`
	MaxScore                  float64  `json:"max_score"`
	MinimumSamples            int      `json:"minimum_samples"`
	MaximumMAE                float64  `json:"maximum_mae"`
	MinimumExactAgreement     float64  `json:"minimum_exact_agreement"`
	MinimumWithinOneAgreement float64  `json:"minimum_within_one_agreement"`
	MinimumCriterionAgreement *float64 `json:"minimum_criterion_agreement,omitempty"`
	MaximumSevereRate         float64  `json:"maximum_severe_rate"`
	SevereErrorThreshold      float64  `json:"severe_error_threshold"`
	QualificationValidityDays int      `json:"qualification_validity_days"`
	ExpectedRevision          int64    `json:"expected_revision"`
}

type GoldSample struct {
	Question       *paper.Question `json:"question,omitempty"`
	GoldPaperID    string          `json:"gold_paper_id"`
	GoldVersion    int             `json:"gold_version"`
	SubmissionID   string          `json:"submission_id"`
	AnswerImageURL string          `json:"answer_image_url,omitempty"`
	MaxScore       float64         `json:"max_score"`
	RubricSnapshot map[string]any  `json:"rubric_snapshot"`
}

// goldReference 在会话内冻结，不序列化给阅卷者；参考分数和预期评分细则得分只在提交后的作答结果中返回。
type goldReference struct {
	GoldSample
	SnapshotID       string         `json:"snapshot_id"`
	RiskTier         string         `json:"risk_tier"`
	ReferenceScore   float64        `json:"reference_score"`
	ExpectedCriteria map[string]any `json:"expected_criteria"`
}

type Metrics struct {
	SampleCount            int      `json:"sample_count"`
	CriterionSampleCount   int      `json:"criterion_sample_count"`
	MAE                    float64  `json:"mae"`
	ExactAgreement         float64  `json:"exact_agreement"`
	WithinOneAgreement     float64  `json:"within_one_agreement"`
	CriterionAgreement     *float64 `json:"criterion_agreement,omitempty"`
	SevereDisagreementRate float64  `json:"severe_disagreement_rate"`
}

type Session struct {
	ID                 string        `json:"id"`
	ExamID             string        `json:"exam_id"`
	QuestionID         string        `json:"question_id"`
	GraderID           string        `json:"grader_id"`
	SnapshotID         string        `json:"exam_question_snapshot_id"`
	ArchetypeCode      string        `json:"archetype_code"`
	RiskTier           string        `json:"risk_tier"`
	GoldSetHash        string        `json:"gold_set_hash"`
	Status             SessionStatus `json:"status"`
	Samples            []GoldSample  `json:"samples"`
	SubmittedCount     int           `json:"submitted_count"`
	Metrics            *Metrics      `json:"metrics,omitempty"`
	StartedAt          time.Time     `json:"started_at"`
	CompletedAt        *time.Time    `json:"completed_at,omitempty"`
	InvalidatedAt      *time.Time    `json:"invalidated_at,omitempty"`
	InvalidationReason string        `json:"invalidation_reason,omitempty"`
}

type CreateSessionInput struct {
	GraderID string `json:"grader_id,omitempty"`
}

type SubmitAttemptInput struct {
	GoldPaperID      string         `json:"gold_paper_id"`
	SubmittedScore   float64        `json:"submitted_score"`
	RubricSelections map[string]any `json:"rubric_selections"`
}

type CriterionDifference struct {
	Criterion string `json:"criterion"`
	Expected  any    `json:"expected,omitempty"`
	Submitted any    `json:"submitted,omitempty"`
}

type Attempt struct {
	ID                   string                `json:"id"`
	SessionID            string                `json:"session_id"`
	GoldPaperID          string                `json:"gold_paper_id"`
	GoldVersion          int                   `json:"gold_version"`
	SubmittedScore       float64               `json:"submitted_score"`
	ReferenceScore       float64               `json:"reference_score"`
	RubricSelections     map[string]any        `json:"rubric_selections"`
	AbsoluteError        float64               `json:"absolute_error"`
	ExactMatch           bool                  `json:"exact_match"`
	WithinOne            bool                  `json:"within_one"`
	SevereDisagreement   bool                  `json:"severe_disagreement"`
	CriterionCorrect     int                   `json:"criterion_correct"`
	CriterionCount       int                   `json:"criterion_count"`
	CriterionDifferences []CriterionDifference `json:"criterion_differences"`
	CreatedAt            time.Time             `json:"created_at"`
}

type Qualification struct {
	ID                   string              `json:"id"`
	ExamID               string              `json:"exam_id"`
	QuestionID           string              `json:"question_id"`
	GraderID             string              `json:"grader_id"`
	Status               QualificationStatus `json:"status"`
	GoldSetHash          string              `json:"gold_set_hash"`
	CalibrationSessionID string              `json:"calibration_session_id"`
	ValidUntil           time.Time           `json:"valid_until"`
	Metrics              Metrics             `json:"metrics"`
	CreatedAt            time.Time           `json:"created_at"`
	UpdatedAt            time.Time           `json:"updated_at"`
}

type Store interface {
	PutPolicy(context.Context, string, string, string, PutPolicyInput) (Policy, error)
	GetPolicy(context.Context, string, string, string) (Policy, error)
	CreateSession(context.Context, string, Session, Policy, []goldReference) (Session, error)
	GetSession(context.Context, string, string) (Session, Policy, []goldReference, []Attempt, error)
	CreateAttempt(context.Context, string, string, Attempt) (Attempt, error)
	CompleteSession(context.Context, string, Session, Qualification) (Session, Qualification, error)
	GetQualification(context.Context, string, string, string, string) (Qualification, error)
	InvalidateQualification(context.Context, string, string, string) (Qualification, error)
}

// QualificationReader 是 R3 领取门禁使用的有限服务端依赖；实现返回资格前须对照当前 Gold Set 校验。
type QualificationReader interface {
	GetQualification(context.Context, string, string, string, string) (Qualification, error)
}

type QualificationGate interface {
	RequireQualification(context.Context, string, string, string, string) error
}
