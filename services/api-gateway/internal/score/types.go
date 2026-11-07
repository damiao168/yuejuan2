package score

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"errors"
	"time"
)

var (
	ErrNotFound                = errors.New("score resource not found")
	ErrInvalidInput            = errors.New("invalid score input")
	ErrInvalidTransition       = errors.New("invalid score transition")
	ErrQualityGateFailed       = errors.New("score quality gate failed")
	ErrManualAttendanceRetired = errors.New("manual attendance is retired; attendance is derived from matched submissions")
)

type FinalGrade struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	ExamID          string    `json:"exam_id"`
	QuestionID      string    `json:"question_id"`
	QuestionNo      string    `json:"question_no"`
	AnswerSegmentID string    `json:"answer_segment_id"`
	SubmissionID    string    `json:"submission_id"`
	AnonymousCode   string    `json:"anonymous_code"`
	Score           float64   `json:"score"`
	MaxScore        float64   `json:"max_score"`
	Source          string    `json:"source"`
	Status          string    `json:"status"`
	Locked          bool      `json:"locked"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type SubmissionGrade struct {
	ID            string       `json:"id"`
	TenantID      string       `json:"tenant_id"`
	ExamID        string       `json:"exam_id"`
	SubmissionID  string       `json:"submission_id"`
	StudentID     string       `json:"student_id,omitempty"`
	AnonymousCode string       `json:"anonymous_code"`
	TotalScore    float64      `json:"total_score"`
	MaxScore      float64      `json:"max_score"`
	Status        string       `json:"status"`
	Locked        bool         `json:"locked"`
	ConfirmedBy   string       `json:"confirmed_by,omitempty"`
	ConfirmedAt   *time.Time   `json:"confirmed_at,omitempty"`
	PublishedBy   string       `json:"published_by,omitempty"`
	PublishedAt   *time.Time   `json:"published_at,omitempty"`
	Revision      int64        `json:"revision"`
	CreatedBy     string       `json:"created_by"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
	Items         []FinalGrade `json:"items,omitempty"`
}

type GradeListFilter struct {
	Status              string
	Query               string
	Limit               int
	CursorAnonymousCode string
	CursorID            string
}

type GradeListResult struct {
	Grades        []SubmissionGrade
	Total         int
	FilteredTotal int
	AllLocked     bool
}

type QualityIssue struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
	Count    int    `json:"count"`
}

type QualityReport struct {
	Passed bool           `json:"passed"`
	Issues []QualityIssue `json:"issues"`
}

type RosterEntry struct {
	Key               string   `json:"key"`
	StudentID         string   `json:"student_id,omitempty"`
	StudentNo         string   `json:"student_no,omitempty"`
	StudentName       string   `json:"student_name,omitempty"`
	ClassID           string   `json:"class_id,omitempty"`
	ClassName         string   `json:"class_name,omitempty"`
	SubmissionID      string   `json:"submission_id,omitempty"`
	CandidateNo       string   `json:"candidate_no,omitempty"`
	Status            string   `json:"status"`
	ResolutionCode    string   `json:"resolution_code"`
	ExpectedPageCount int      `json:"expected_page_count"`
	ActualPageCount   int      `json:"actual_page_count"`
	TotalScore        *float64 `json:"total_score,omitempty"`
	MaxScore          *float64 `json:"max_score,omitempty"`
	// 旧响应字段保留兼容；当前名册不再填入人工标记，出勤只从冻结名册和答卷关系派生。
	AttendanceReason string     `json:"attendance_reason,omitempty"`
	MarkedBy         string     `json:"marked_by,omitempty"`
	MarkedAt         *time.Time `json:"marked_at,omitempty"`
}

type RosterSummary struct {
	Expected     int `json:"expected"`
	Received     int `json:"received"`
	Graded       int `json:"graded"`
	Absent       int `json:"absent"`
	Unresolved   int `json:"unresolved"`
	MissingPages int `json:"missing_pages"`
	Unidentified int `json:"unidentified"`
}

type RosterReport struct {
	Entries []RosterEntry `json:"entries"`
	Summary RosterSummary `json:"summary"`
}

type AttendanceInput struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type FinalizeResult struct {
	Status            string            `json:"status"`
	CreatedFinals     int               `json:"created_finals"`
	SubmissionGrades  []SubmissionGrade `json:"submission_grades"`
	Quality           QualityReport     `json:"quality"`
	AvailableStatuses []string          `json:"available_statuses"`
}

type ConfirmInput struct {
	Reason string `json:"reason"`
}

type PublishInput struct {
	Reason string `json:"reason"`
}

type PublishResult struct {
	Status           string            `json:"status"`
	SubmissionGrades []SubmissionGrade `json:"submission_grades"`
	Quality          QualityReport     `json:"quality"`
	PublishedAt      time.Time         `json:"published_at"`
}

type ExportResult struct {
	Filename    string
	ContentType string
	Content     []byte
	RowCount    int
	Watermark   string
}

type Store interface {
	RecoverCommand(context.Context, string, string, string) (commandreceipt.Receipt, error)
	FinalizeExam(ctx context.Context, tenantID string, examID string, actorID string) (FinalizeResult, error)
	ListExamGrades(ctx context.Context, tenantID string, examID string, filter GradeListFilter) (GradeListResult, error)
	CheckQuality(ctx context.Context, tenantID string, examID string, requirePendingPublish bool) (QualityReport, error)
	ConfirmGrades(ctx context.Context, tenantID string, examID string, actorID string, input ConfirmInput) ([]SubmissionGrade, error)
	PublishGrades(ctx context.Context, tenantID string, examID string, actorID string, input PublishInput) (PublishResult, error)
	GetStudentGrade(ctx context.Context, tenantID string, studentID string, examID string) (SubmissionGrade, error)
	ExportGradesCSV(ctx context.Context, tenantID string, examID string, actorID string) (ExportResult, error)
	ListRoster(ctx context.Context, tenantID string, examID string) (RosterReport, error)
	SetAttendance(ctx context.Context, tenantID string, examID string, studentID string, actorID string, input AttendanceInput) (RosterReport, error)
}

func Statuses() []string {
	return []string{"calculating", "pending_confirmation", "confirmed", "pending_publish", "published", "locked"}
}
