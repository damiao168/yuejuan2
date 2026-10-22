package submission

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound          = errors.New("submission not found")
	ErrInvalidInput      = errors.New("invalid submission input")
	ErrDuplicatePage     = errors.New("submission page already exists")
	ErrInvalidTransition = errors.New("invalid submission status transition")
	ErrSubmissionLocked  = errors.New("submission is locked")
	ErrRevisionConflict  = errors.New("submission revision conflict")
)

type Submission struct {
	ID                string           `json:"id"`
	TenantID          string           `json:"tenant_id"`
	ExamID            string           `json:"exam_id"`
	StudentID         string           `json:"student_id,omitempty"`
	CandidateNo       string           `json:"candidate_no,omitempty"`
	SourceType        string           `json:"source_type"`
	Status            string           `json:"status"`
	ExpectedPageCount int              `json:"expected_page_count"`
	ActualPageCount   int              `json:"actual_page_count"`
	QualityStatus     string           `json:"quality_status"`
	QualityIssues     []QualityIssue   `json:"quality_issues"`
	CollectedBy       string           `json:"collected_by"`
	Revision          int64            `json:"revision"`
	CreatedAt         time.Time        `json:"created_at"`
	Pages             []SubmissionPage `json:"pages,omitempty"`
	Summary           map[string]any   `json:"summary,omitempty"`
}

type SubmissionPage struct {
	ID                    string         `json:"id"`
	TenantID              string         `json:"tenant_id"`
	SubmissionID          string         `json:"submission_id"`
	FileAssetID           string         `json:"file_asset_id"`
	PageNo                int            `json:"page_no"`
	Status                string         `json:"status"`
	LatestQualityRunID    string         `json:"latest_quality_run_id,omitempty"`
	NormalizedFileAssetID string         `json:"normalized_file_asset_id,omitempty"`
	QualityStatus         string         `json:"quality_status"`
	QualityOverride       map[string]any `json:"quality_override"`
	QualityIssues         []QualityIssue `json:"quality_issues"`
	CreatedAt             time.Time      `json:"created_at"`
}

// AnswerRegion 是向 OCR worker 提供的 answer_segment 隐私受限子集，不包含学生身份或答案文本。
type AnswerRegion struct {
	ID           string    `json:"answer_segment_id"`
	QuestionID   string    `json:"question_id"`
	QuestionNo   string    `json:"question_no"`
	QuestionType string    `json:"question_type"`
	BBox         []float64 `json:"bbox"`
	PageID       string    `json:"submission_page_id"`
	// 配准或模板坐标不能直接应用于原始页面。
	RequiresFullPage bool `json:"-"`
}

type QualityIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type CreateSubmissionInput struct {
	StudentID         string `json:"student_id"`
	CandidateNo       string `json:"candidate_no"`
	SourceType        string `json:"source_type"`
	ExpectedPageCount int    `json:"expected_page_count"`
}

type AddPageInput struct {
	FileAssetID string `json:"file_asset_id"`
	PageNo      int    `json:"page_no"`
}

type UpdateStatusInput struct {
	Status           string `json:"status"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type ListFilter struct {
	Limit           int
	CursorCreatedAt time.Time
	CursorID        string
}

type QualityResult struct {
	Valid  bool           `json:"valid"`
	Issues []QualityIssue `json:"issues"`
}

type ApplyPageQualityInput struct {
	SourceFileAssetID     string
	SubmissionID          string
	PageID                string
	LatestQualityRunID    string
	NormalizedFileAssetID string
	QualityStatus         string
	QualityIssues         []QualityIssue
}

type OverridePageQualityInput struct {
	Reason string `json:"reason"`
}

type Store interface {
	Create(ctx context.Context, tenantID string, examID string, actorID string, input CreateSubmissionInput) (Submission, error)
	ListByExam(ctx context.Context, tenantID string, examID string, filter ListFilter) ([]Submission, error)
	Get(ctx context.Context, tenantID string, id string) (Submission, error)
	AddPage(ctx context.Context, tenantID string, submissionID string, actorID string, input AddPageInput) (SubmissionPage, error)
	ReplacePage(ctx context.Context, tenantID string, submissionID string, actorID string, pageNo int, input AddPageInput) (SubmissionPage, error)
	ListPages(ctx context.Context, tenantID string, submissionID string) ([]SubmissionPage, error)
	ApplyPageQualityResult(ctx context.Context, tenantID string, input ApplyPageQualityInput) (SubmissionPage, error)
	OverridePageQuality(ctx context.Context, tenantID string, pageID string, actorID string, input OverridePageQualityInput) (SubmissionPage, error)
	RunQualityCheck(ctx context.Context, tenantID string, submissionID string, actorID string) (QualityResult, error)
	UpdateStatus(ctx context.Context, tenantID string, submissionID string, actorID string, status string, expectedRevision int64) (Submission, error)
}

// AnswerRegionLister 对兼容旧版的存储是可选接口。不可用时，OCR 输入回退到整页推断。
type AnswerRegionLister interface {
	ListAnswerRegions(ctx context.Context, tenantID string, submissionID string) ([]AnswerRegion, error)
}

// ExamBatchLister 避免仪表盘和报告汇总为每场考试单独查询提交。
// 实现必须在 SQL 中保留租户条件。
type ExamBatchLister interface {
	ListByExams(ctx context.Context, tenantID string, examIDs []string) (map[string][]Submission, error)
}
