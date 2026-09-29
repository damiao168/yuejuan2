package appeal

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound          = errors.New("appeal resource not found")
	ErrInvalidInput      = errors.New("invalid appeal input")
	ErrForbidden         = errors.New("appeal action forbidden")
	ErrInvalidTransition = errors.New("invalid appeal transition")
	ErrUnpublishedGrade  = errors.New("grade is not published")
	ErrRevisionConflict  = errors.New("appeal revision conflict")
)

// Appeal 保留旧版按可变成绩处理的申诉；基于发布快照的新流程使用 PublishedQuestionAppeal。
type Appeal struct {
	ID                   string            `json:"id"`
	TenantID             string            `json:"tenant_id"`
	ExamID               string            `json:"exam_id"`
	ExamName             string            `json:"exam_name,omitempty"`
	Subject              string            `json:"subject,omitempty"`
	SubmissionID         string            `json:"submission_id"`
	SubmissionGradeID    string            `json:"submission_grade_id"`
	AnonymousCode        string            `json:"anonymous_code,omitempty"`
	StudentID            string            `json:"student_id,omitempty"`
	TargetType           string            `json:"target_type"`
	FinalGradeID         string            `json:"final_grade_id,omitempty"`
	QuestionID           string            `json:"question_id,omitempty"`
	QuestionNo           string            `json:"question_no,omitempty"`
	DeductionPointID     string            `json:"deduction_point_id,omitempty"`
	Reason               string            `json:"reason"`
	Attachment           map[string]any    `json:"attachment,omitempty"`
	Status               string            `json:"status"`
	ResultReason         string            `json:"result_reason,omitempty"`
	AssignedTo           string            `json:"assigned_to,omitempty"`
	Recommendation       string            `json:"teacher_recommendation,omitempty"`
	RecommendationReason string            `json:"teacher_recommendation_reason,omitempty"`
	RecommendedScore     *float64          `json:"teacher_recommended_score,omitempty"`
	RecommendationBy     string            `json:"teacher_recommendation_by,omitempty"`
	RecommendationAt     *time.Time        `json:"teacher_recommendation_at,omitempty"`
	ReviewedBy           string            `json:"reviewed_by,omitempty"`
	ReviewedAt           *time.Time        `json:"reviewed_at,omitempty"`
	ClosedBy             string            `json:"closed_by,omitempty"`
	ClosedAt             *time.Time        `json:"closed_at,omitempty"`
	CreatedBy            string            `json:"created_by,omitempty"`
	Revision             int64             `json:"revision"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
	Evidence             *AppealEvidence   `json:"evidence,omitempty"`
	Adjustments          []ScoreAdjustment `json:"adjustments,omitempty"`
}

type AppealEvidence struct {
	RawAnswer   string           `json:"raw_answer,omitempty"`
	OCRText     string           `json:"ocr_text,omitempty"`
	AIGrades    []map[string]any `json:"ai_grades,omitempty"`
	HumanGrades []map[string]any `json:"human_grades,omitempty"`
	Rubric      map[string]any   `json:"rubric,omitempty"`
	FinalGrade  map[string]any   `json:"final_grade,omitempty"`
}

type ScoreAdjustment struct {
	ID                string    `json:"id"`
	TenantID          string    `json:"tenant_id"`
	AppealID          string    `json:"appeal_id"`
	ExamID            string    `json:"exam_id"`
	SubmissionID      string    `json:"submission_id"`
	SubmissionGradeID string    `json:"submission_grade_id"`
	FinalGradeID      string    `json:"final_grade_id"`
	QuestionID        string    `json:"question_id"`
	QuestionNo        string    `json:"question_no"`
	PreviousScore     float64   `json:"previous_score"`
	AdjustedScore     float64   `json:"adjusted_score"`
	Delta             float64   `json:"delta"`
	Reason            string    `json:"reason"`
	AdjustedBy        string    `json:"adjusted_by,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

type CreateAppealInput struct {
	ExamID           string         `json:"exam_id"`
	StudentID        string         `json:"student_id"`
	TargetType       string         `json:"target_type"`
	FinalGradeID     string         `json:"final_grade_id"`
	DeductionPointID string         `json:"deduction_point_id"`
	Reason           string         `json:"reason"`
	Attachment       map[string]any `json:"attachment"`
}

type ReviewAppealInput struct {
	Status           string   `json:"status"`
	Reason           string   `json:"reason"`
	AssignedTo       string   `json:"assigned_to"`
	FinalGradeID     string   `json:"final_grade_id"`
	AdjustedScore    *float64 `json:"adjusted_score"`
	ExpectedRevision int64    `json:"expected_revision"`
}

type AssignAppealInput struct {
	AssignedTo       string `json:"assigned_to"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type SubmitRecommendationInput struct {
	Recommendation   string   `json:"recommendation"`
	Reason           string   `json:"reason"`
	RecommendedScore *float64 `json:"recommended_score"`
	ExpectedRevision int64    `json:"expected_revision"`
}

type CloseAppealInput struct {
	Reason           string `json:"reason"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type ListFilter struct {
	ExamID          string
	StudentID       string
	Status          string
	AssignedTo      string
	Limit           int
	CursorCreatedAt time.Time
	CursorID        string
}

type StatisticsFilter struct {
	ExamID string
}

type AppealStatistics struct {
	Total              int            `json:"total"`
	ByStatus           map[string]int `json:"by_status"`
	ScoreAdjustedCount int            `json:"score_adjusted_count"`
	AverageHandleHours float64        `json:"average_handle_hours"`
}

type Store interface {
	CreateAppeal(ctx context.Context, tenantID string, actorID string, input CreateAppealInput) (Appeal, error)
	ListAppeals(ctx context.Context, tenantID string, filter ListFilter) ([]Appeal, error)
	GetAppeal(ctx context.Context, tenantID string, id string) (Appeal, error)
	AssignAppeal(ctx context.Context, tenantID string, id string, actorID string, input AssignAppealInput) (Appeal, error)
	SubmitRecommendation(ctx context.Context, tenantID string, id string, actorID string, input SubmitRecommendationInput) (Appeal, error)
	ReviewAppeal(ctx context.Context, tenantID string, id string, actorID string, input ReviewAppealInput) (Appeal, *ScoreAdjustment, error)
	CloseAppeal(ctx context.Context, tenantID string, id string, actorID string, input CloseAppealInput) (Appeal, error)
	Statistics(ctx context.Context, tenantID string, filter StatisticsFilter) (AppealStatistics, error)
}

func Statuses() []string {
	return []string{"submitted", "under_review", "need_more_info", "accepted", "rejected", "score_adjusted", "closed"}
}
