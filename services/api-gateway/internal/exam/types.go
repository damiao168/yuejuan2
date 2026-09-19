package exam

import (
	"context"
	"errors"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

var (
	ErrNotFound          = errors.New("exam not found")
	ErrInvalidTransition = errors.New("invalid exam status transition")
	ErrLocked            = errors.New("exam is locked")
	ErrInvalidInput      = errors.New("invalid exam input")
	ErrRevisionConflict  = errors.New("exam revision conflict")
	ErrScopeForbidden    = errors.New("exam access scope forbidden")
	ErrCandidatesFrozen  = errors.New("exam candidates are frozen")
	ErrCommandConflict   = errors.New("exam session command was reused with different input")
)

type Exam struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	SchoolID       string    `json:"school_id"`
	SessionID      string    `json:"exam_session_id,omitempty"`
	SessionName    string    `json:"exam_session_name,omitempty"`
	SessionGradeID string    `json:"exam_session_grade_id,omitempty"`
	Name           string    `json:"name"`
	Subject        string    `json:"subject"`
	ExamType       string    `json:"exam_type"`
	TotalScore     float64   `json:"total_score"`
	Status         string    `json:"status"`
	GradingMode    string    `json:"grading_mode"`
	AppealEnabled  bool      `json:"appeal_enabled"`
	PublishPolicy  string    `json:"publish_policy"`
	CreatedBy      string    `json:"created_by"`
	ClassIDs       []string  `json:"class_ids"`
	Revision       int64     `json:"revision"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ExamSession is the grade-level parent of one or more subject exams. Existing
// grading workflows continue to operate on the child Exam IDs.
type ExamSession struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	SchoolID        string    `json:"school_id"`
	GradeID         string    `json:"grade_id"`
	TemplateID      string    `json:"template_id,omitempty"`
	TemplateVersion int       `json:"template_version,omitempty"`
	Name            string    `json:"name"`
	ExamType        string    `json:"exam_type"`
	Status          string    `json:"status"`
	GradingMode     string    `json:"grading_mode"`
	AppealEnabled   bool      `json:"appeal_enabled"`
	PublishPolicy   string    `json:"publish_policy"`
	CreatedBy       string    `json:"created_by"`
	CommandID       string    `json:"-"`
	Revision        int64     `json:"revision"`
	Exams           []Exam    `json:"exams"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type BlueprintSectionInput struct {
	Title            string  `json:"title"`
	QuestionType     string  `json:"question_type"`
	QuestionCount    int     `json:"question_count"`
	ScorePerQuestion float64 `json:"score_per_question"`
}

type SessionSubjectInput struct {
	Subject         string                  `json:"subject"`
	TotalScore      float64                 `json:"total_score"`
	DurationMinutes int                     `json:"duration_minutes"`
	CandidateRule   string                  `json:"candidate_rule"`
	ClassIDs        []string                `json:"class_ids"`
	Sections        []BlueprintSectionInput `json:"sections"`
}

type CreateSessionInput struct {
	SchoolID      string                `json:"school_id"`
	GradeID       string                `json:"grade_id"`
	TemplateID    string                `json:"template_id,omitempty"`
	Name          string                `json:"name"`
	ExamType      string                `json:"exam_type"`
	GradingMode   string                `json:"grading_mode"`
	AppealEnabled *bool                 `json:"appeal_enabled"`
	PublishPolicy string                `json:"publish_policy"`
	ClassIDs      []string              `json:"class_ids"`
	Subjects      []SessionSubjectInput `json:"subjects"`
	CommandID     string                `json:"command_id,omitempty"`
}

type SessionStore interface {
	CreateExamSession(ctx context.Context, scope auth.AccessScope, createdBy string, input CreateSessionInput) (ExamSession, error)
}

type ExamSessionCommandResult struct {
	CommandID  string       `json:"command_id"`
	Status     string       `json:"status"`
	HTTPStatus int          `json:"http_status,omitempty"`
	ErrorCode  string       `json:"error_code,omitempty"`
	Session    *ExamSession `json:"exam_session,omitempty"`
}

type SessionCommandStore interface {
	RecoverExamSessionCommand(ctx context.Context, scope auth.AccessScope, createdBy, commandID string) (ExamSessionCommandResult, error)
}

type CreateInput struct {
	SchoolID      string   `json:"school_id"`
	Name          string   `json:"name"`
	Subject       string   `json:"subject"`
	ExamType      string   `json:"exam_type"`
	TotalScore    float64  `json:"total_score"`
	GradingMode   string   `json:"grading_mode"`
	AppealEnabled *bool    `json:"appeal_enabled"`
	PublishPolicy string   `json:"publish_policy"`
	ClassIDs      []string `json:"class_ids"`
}

type UpdateInput struct {
	SchoolID         *string   `json:"school_id"`
	Name             *string   `json:"name"`
	Subject          *string   `json:"subject"`
	ExamType         *string   `json:"exam_type"`
	TotalScore       *float64  `json:"total_score"`
	GradingMode      *string   `json:"grading_mode"`
	AppealEnabled    *bool     `json:"appeal_enabled"`
	PublishPolicy    *string   `json:"publish_policy"`
	ClassIDs         *[]string `json:"class_ids"`
	ExpectedRevision int64     `json:"expected_revision"`
}

type ListFilter struct {
	Status   string
	SchoolID string
	Limit    int
	CursorAt time.Time
	CursorID string
}

// CandidateRefreshResult describes how the editable exam roster changed when
// it was rebuilt from the currently active class enrollments.
type CandidateRefreshResult struct {
	BeforeCount  int `json:"before_count"`
	AfterCount   int `json:"after_count"`
	AddedCount   int `json:"added_count"`
	RemovedCount int `json:"removed_count"`
}

type Store interface {
	CreateExam(ctx context.Context, scope auth.AccessScope, createdBy string, input CreateInput) (Exam, error)
	ListExams(ctx context.Context, scope auth.AccessScope, filter ListFilter) ([]Exam, error)
	GetExam(ctx context.Context, scope auth.AccessScope, id string) (Exam, error)
	UpdateExam(ctx context.Context, scope auth.AccessScope, id string, input UpdateInput) (Exam, error)
	UpdateStatus(ctx context.Context, scope auth.AccessScope, id string, status string, expectedRevision int64) (Exam, error)
	RefreshCandidateSnapshot(ctx context.Context, scope auth.AccessScope, id string) (CandidateRefreshResult, error)
}
