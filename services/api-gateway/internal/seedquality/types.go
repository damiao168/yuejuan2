package seedquality

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"errors"
	"time"
)

var (
	ErrNotFound            = errors.New("seed quality resource not found")
	ErrInvalidInput        = errors.New("invalid seed quality input")
	ErrConflict            = errors.New("seed quality state conflict")
	ErrGoldSetMissing      = errors.New("active approved Gold Set is required")
	ErrGoldSetChanged      = errors.New("active Gold Set changed after policy configuration")
	ErrQualificationNeeded = errors.New("current grader qualification is required")
	ErrSeedTaskForbidden   = errors.New("seed task does not belong to grader")
)

type PolicyStatus string

const (
	PolicyActive PolicyStatus = "active"
	PolicyPaused PolicyStatus = "paused"
)

// Policy 受题目作用域限制。ActiveGoldFingerprint 将采样绑定到启用策略时已经复核并批准的 Gold 版本。
type Policy struct {
	ID                    string       `json:"id"`
	ExamID                string       `json:"exam_id"`
	QuestionID            string       `json:"question_id"`
	Rate                  float64      `json:"rate"`
	MinInterval           int          `json:"min_interval"`
	MaxInterval           int          `json:"max_interval"`
	ActiveGoldFingerprint string       `json:"active_gold_fingerprint"`
	Status                PolicyStatus `json:"status"`
	Revision              int64        `json:"revision"`
	CreatedBy             string       `json:"created_by"`
	CreatedAt             time.Time    `json:"created_at"`
	UpdatedAt             time.Time    `json:"updated_at"`
}

type PutPolicyInput struct {
	Rate             float64      `json:"rate"`
	MinInterval      int          `json:"min_interval"`
	MaxInterval      int          `json:"max_interval"`
	Status           PolicyStatus `json:"status"`
	ExpectedRevision int64        `json:"expected_revision"`
}

// Task 的结构与普通评分工作项相似。其 JSON 省略 Gold 标识、参考分数、提交 ID 以及它是 Seed 的事实。
// 复核组装入口可通过正常领取响应返回它，并将其不透明 ID 路由回 TrySubmit。
type Task struct {
	ID             string    `json:"id"`
	ExamID         string    `json:"exam_id"`
	QuestionID     string    `json:"question_id"`
	QuestionNo     string    `json:"question_no"`
	AnonymousCode  string    `json:"anonymous_code"`
	AnswerImageURL string    `json:"answer_image_url"`
	Source         string    `json:"source"`
	Status         string    `json:"status"`
	Priority       int       `json:"priority"`
	AssignedTo     string    `json:"assigned_to"`
	MaxScore       float64   `json:"max_score"`
	Revision       int64     `json:"revision"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	GoldPaperID      string         `json:"-"`
	GoldVersion      int            `json:"-"`
	SnapshotID       string         `json:"-"`
	ReferenceScore   float64        `json:"-"`
	ExpectedCriteria map[string]any `json:"-"`
	ArchetypeCode    string         `json:"-"`
	SourceImageURL   string         `json:"-"`
}

type SubmitInput struct {
	Score            float64        `json:"score"`
	RubricSelections map[string]any `json:"rubric_selections"`
	ExpectedRevision int64          `json:"expected_revision"`
}

// SubmitReceipt 不泄漏参考答案，也不泄漏已完成条目是否为 Seed。
// 质量观察只属于管理员可见事实。
type SubmitReceipt struct {
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
}

type ObservationKind string

const (
	ObservationTrait     ObservationKind = "trait"
	ObservationCriterion ObservationKind = "criterion"
)

type Observation struct {
	ID             string  `json:"id"`
	ExamID         string  `json:"exam_id"`
	QuestionID     string  `json:"question_id"`
	GraderID       string  `json:"grader_id"`
	GoldPaperID    string  `json:"gold_paper_id"`
	GoldVersion    int     `json:"gold_version"`
	SubmittedScore float64 `json:"submitted_score"`
	ReferenceScore float64 `json:"reference_score"`
	// MaxScore 是仅供服务器使用的质量上下文。
	// 它使 A11 的中间分数切片保持准确，同时不在阅卷路由中暴露任何 Gold 事实。
	MaxScore             float64         `json:"-"`
	RubricSelections     map[string]any  `json:"rubric_selections"`
	AbsoluteError        float64         `json:"error"`
	RubricAgreement      *float64        `json:"rubric_agreement,omitempty"`
	ObservationKind      ObservationKind `json:"observation_kind"`
	TraitObservation     map[string]any  `json:"trait_observation,omitempty"`
	CriterionObservation map[string]any  `json:"criterion_observation,omitempty"`
	ObservedAt           time.Time       `json:"observed_at"`
}

type ObservationFilter struct {
	Access     auth.ExamCollectionAccess
	ExamID     string
	QuestionID string
	GraderID   string
	Limit      int
}

type GoldSample struct {
	GoldPaperID      string
	GoldVersion      int
	SnapshotID       string
	AnswerImageURL   string
	ReferenceScore   float64
	MaxScore         float64
	ExpectedCriteria map[string]any
	ArchetypeCode    string
}

type IssueDecision struct {
	Policy       Policy
	QuestionNo   string
	GraderID     string
	Gold         GoldSample
	Probability  float64
	NextInterval int
	Now          time.Time
}

type Store interface {
	RecoverCommand(context.Context, string, string, string) (commandreceipt.Receipt, error)
	PutPolicy(context.Context, string, string, string, string, PutPolicyInput, string) (Policy, error)
	GetPolicy(context.Context, string, string, string) (Policy, error)
	AdvanceAndMaybeCreate(context.Context, string, IssueDecision) (Task, bool, error)
	GetTask(context.Context, string, string) (Task, error)
	CompleteTask(context.Context, string, string, string, SubmitInput, Observation) (Task, Observation, error)
	ListObservations(context.Context, string, ObservationFilter) ([]Observation, error)
}
