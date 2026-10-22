// Package processing 提供学校运营所用的标准页面处理投影。
// 捕获、图像质量、OCR、配准和分段等来源系统仍为权威来源；本包只存储它们精简、可解释的运行状态。
package processing

import (
	"context"
	"errors"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
)

var (
	ErrNotFound       = errors.New("processing resource not found")
	ErrInvalidInput   = errors.New("invalid processing input")
	ErrStateConflict  = errors.New("processing state conflict")
	ErrRetryForbidden = errors.New("processing exception is not retryable")
)

type Stage string

const (
	StageReceived       Stage = "RECEIVED"
	StageValidated      Stage = "VALIDATED"
	StageQualityChecked Stage = "QUALITY_CHECKED"
	StageRegistered     Stage = "REGISTERED"
	StageIdentified     Stage = "IDENTIFIED"
	StageParsed         Stage = "PARSED"
	StageSegmented      Stage = "SEGMENTED"
	StageReady          Stage = "READY"
)

func (s Stage) Valid() bool {
	switch s {
	case StageReceived, StageValidated, StageQualityChecked, StageRegistered, StageIdentified, StageParsed, StageSegmented, StageReady:
		return true
	default:
		return false
	}
}

type IssueCode string

const (
	IssueMissingIdentity    IssueCode = "BLOCKED_MISSING_IDENTITY"
	IssueMissingPage        IssueCode = "BLOCKED_MISSING_PAGE"
	IssueBadAlignment       IssueCode = "BLOCKED_BAD_ALIGNMENT"
	IssueLowImageQuality    IssueCode = "BLOCKED_LOW_IMAGE_QUALITY"
	IssueOCRLowConfidence   IssueCode = "OCR_LOW_CONFIDENCE"
	IssueMathParseFailed    IssueCode = "MATH_PARSE_FAILED"
	IssueChemistryParseFail IssueCode = "CHEMISTRY_PARSE_FAILED"
	IssueTableParseFailed   IssueCode = "TABLE_PARSE_FAILED"
	IssueDiagramParseFailed IssueCode = "DIAGRAM_PARSE_FAILED"
	IssueSegmentationFailed IssueCode = "SEGMENTATION_FAILED"
)

func (c IssueCode) Valid() bool {
	switch c {
	case "", IssueMissingIdentity, IssueMissingPage, IssueBadAlignment, IssueLowImageQuality, IssueOCRLowConfidence,
		IssueMathParseFailed, IssueChemistryParseFail, IssueTableParseFailed, IssueDiagramParseFailed, IssueSegmentationFailed:
		return true
	default:
		return false
	}
}

type Severity string

const (
	SeverityP0 Severity = "P0"
	SeverityP1 Severity = "P1"
	SeverityP2 Severity = "P2"
	SeverityP3 Severity = "P3"
)

func (s Severity) Valid() bool {
	return s == SeverityP0 || s == SeverityP1 || s == SeverityP2 || s == SeverityP3
}

type ExceptionStatus string

const (
	ExceptionOpen     ExceptionStatus = "open"
	ExceptionAssigned ExceptionStatus = "assigned"
	ExceptionResolved ExceptionStatus = "resolved"
)

func (s ExceptionStatus) Valid() bool {
	return s == ExceptionOpen || s == ExceptionAssigned || s == ExceptionResolved
}

type ParserQuality struct {
	Text               *float64 `json:"text_quality,omitempty"`
	MathExpression     *float64 `json:"math_expression_quality,omitempty"`
	ChemicalExpression *float64 `json:"chemical_expression_quality,omitempty"`
	TableStructure     *float64 `json:"table_structure_quality,omitempty"`
	Diagram            *float64 `json:"diagram_quality,omitempty"`
}

// For 返回与评估上下文相关的保守质量信号。
// 专用解析器质量缺失时保持 nil，让 A14 放弃判断；绝不静默替换为文本 OCR 质量。
func (q ParserQuality) For(subject assessment.SubjectCode, archetype string) *float64 {
	if archetype == "diagram_graph" {
		return q.Diagram
	}
	if archetype == "table_experiment" {
		return q.TableStructure
	}
	switch subject {
	case assessment.SubjectMathematics, assessment.SubjectPhysics:
		if archetype == "numeric_expression" || archetype == "structured_steps" {
			return q.MathExpression
		}
	case assessment.SubjectChemistry:
		if archetype == "numeric_expression" || archetype == "structured_steps" {
			return q.ChemicalExpression
		}
	}
	return q.Text
}

type PageState struct {
	PageID           string        `json:"page_id"`
	SubmissionID     string        `json:"submission_id"`
	ExamID           string        `json:"exam_id"`
	CurrentStage     Stage         `json:"current_stage"`
	Blocking         bool          `json:"blocking"`
	IssueCode        IssueCode     `json:"issue_code,omitempty"`
	Retryable        bool          `json:"retryable"`
	RetrySourceType  string        `json:"retry_source_type,omitempty"`
	RetrySourceID    string        `json:"retry_source_id,omitempty"`
	ParserQuality    ParserQuality `json:"parser_quality"`
	SourceObservedAt time.Time     `json:"source_observed_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
}

type StageCount struct {
	Stage Stage `json:"stage"`
	Count int   `json:"count"`
}

type IssueCount struct {
	Code  IssueCode `json:"code"`
	Count int       `json:"count"`
}

type Summary struct {
	ExamID       string       `json:"exam_id"`
	TotalPages   int          `json:"total_pages"`
	ReadyPages   int          `json:"ready_pages"`
	BlockedPages int          `json:"blocked_pages"`
	PendingPages int          `json:"pending_pages"`
	ByStage      []StageCount `json:"by_stage"`
	Issues       []IssueCount `json:"issues"`
	GeneratedAt  time.Time    `json:"generated_at"`
}

type Exception struct {
	ID                 string          `json:"id"`
	ExamID             string          `json:"exam_id"`
	PageID             string          `json:"page_id"`
	SourceType         string          `json:"source_type"`
	SourceID           string          `json:"source_id"`
	Code               IssueCode       `json:"code"`
	Severity           Severity        `json:"severity"`
	Blocking           bool            `json:"blocking"`
	Status             ExceptionStatus `json:"status"`
	AssignedTo         string          `json:"assigned_to,omitempty"`
	AssignedToName     string          `json:"assigned_to_name,omitempty"`
	AssignedToUsername string          `json:"assigned_to_username,omitempty"`
	CandidateNo        string          `json:"candidate_no,omitempty"`
	PageNo             int             `json:"page_no,omitempty"`
	Details            map[string]any  `json:"details"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	ResolvedAt         *time.Time      `json:"resolved_at,omitempty"`
	Resolution         string          `json:"resolution,omitempty"`
	RetrySourceType    string          `json:"retry_source_type,omitempty"`
	RetrySourceID      string          `json:"retry_source_id,omitempty"`
}

type ExceptionFilter struct {
	ExamID   string
	Severity Severity
	Stage    Stage
	Subject  string
	Status   ExceptionStatus
	Limit    int
	CursorAt time.Time
	CursorID string
	// Access 由 HTTP 认证范围派生；请求筛选只能缩小范围，不能把班级继承的学校 ID 当成全校权限。
	Access auth.ExamCollectionAccess
}

type ListResult struct {
	Exceptions  []Exception `json:"exceptions"`
	NextCursor  string      `json:"next_cursor,omitempty"`
	HasMore     bool        `json:"has_more"`
	ProjectedAt *time.Time  `json:"projected_at,omitempty"`
}

type AssignInput struct {
	AssigneeID string `json:"assignee_id"`
}

type ResolveInput struct {
	Resolution string `json:"resolution"`
}

type RetryTarget struct {
	ExceptionID string `json:"exception_id"`
	SourceType  string `json:"source_type"`
	SourceID    string `json:"source_id"`
}

type Store interface {
	Summary(context.Context, string, string) (Summary, error)
	ListExceptions(context.Context, string, ExceptionFilter) (ListResult, error)
	GetException(context.Context, string, string) (Exception, error)
	AssignException(context.Context, string, string, string, AssignInput) (Exception, error)
	ResolveException(context.Context, string, string, string, ResolveInput) (Exception, error)
	RetryTarget(context.Context, string, string) (RetryTarget, error)
	ParserQualityForSegment(context.Context, string, string, assessment.SubjectCode, string) (*float64, error)
}

// ProjectionRefresh 标识后台投影器领取的一个持久化来源版本。
// RequestedVersion 防止较旧刷新确认并发发生的来源变化。
type ProjectionRefresh struct {
	claimID          string
	TenantID         string
	ExamID           string
	RequestedVersion int64
	AttemptCount     int
}

type ProjectionStore interface {
	ClaimProjection(context.Context, string, time.Duration) (ProjectionRefresh, bool, error)
	ApplyProjection(context.Context, string, ProjectionRefresh) error
	FailProjection(context.Context, string, ProjectionRefresh, string, time.Duration) error
}
