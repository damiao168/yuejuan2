package review

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"errors"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

var (
	ErrNotFound          = errors.New("review resource not found")
	ErrInvalidInput      = errors.New("invalid review input")
	ErrForbidden         = errors.New("review action forbidden")
	ErrInvalidTransition = errors.New("invalid review task transition")
	ErrRevisionConflict  = errors.New("resource revision conflict")
)

type Context struct {
	ExamID             string                          `json:"exam_id"`
	SubmissionID       string                          `json:"submission_id"`
	AnswerSegmentID    string                          `json:"answer_segment_id"`
	AnonymousCode      string                          `json:"anonymous_code"`
	AssessmentSnapshot assessment.ExamQuestionSnapshot `json:"assessment_snapshot"`
	Question           paper.Question                  `json:"question"`
	Rubric             paper.Rubric                    `json:"rubric"`
	RawAnswer          string                          `json:"raw_answer"`
	OCRText            string                          `json:"ocr_text"`
	AISuggestion       map[string]any                  `json:"ai_suggestion"`
	AutomationResult   *AutomationResult               `json:"automation_result,omitempty"`
}

// AutomationResult exposes only the task-scoped recognition and rule-grading
// facts a reviewer needs to resolve an exception. It deliberately omits model
// administration data and any student identity beyond the task's anonymous code.
type AutomationResult struct {
	Source           string   `json:"source,omitempty"`
	RecognizedAnswer string   `json:"recognized_answer,omitempty"`
	Confidence       *float64 `json:"confidence,omitempty"`
	Decision         string   `json:"decision,omitempty"`
	StandardAnswer   any      `json:"standard_answer,omitempty"`
	RuleType         string   `json:"rule_type,omitempty"`
	Score            *float64 `json:"score,omitempty"`
	MaxScore         *float64 `json:"max_score,omitempty"`
	GradeSource      string   `json:"grade_source,omitempty"`
}

type ReviewTask struct {
	ID              string     `json:"id"`
	TenantID        string     `json:"tenant_id"`
	ExamID          string     `json:"exam_id"`
	QuestionID      string     `json:"question_id"`
	QuestionNo      string     `json:"question_no"`
	AnswerSegmentID string     `json:"answer_segment_id"`
	SubmissionID    string     `json:"submission_id"`
	AnonymousCode   string     `json:"anonymous_code"`
	Source          string     `json:"source"`
	Status          string     `json:"status"`
	Priority        int        `json:"priority"`
	AssignedTo      string     `json:"assigned_to,omitempty"`
	ReturnReason    string     `json:"return_reason,omitempty"`
	GradeRound      string     `json:"grade_round"`
	DueAt           *time.Time `json:"due_at,omitempty"`
	Revision        int64      `json:"revision"`
	CreatedBy       string     `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type HumanGrade struct {
	ID               string            `json:"id"`
	TenantID         string            `json:"tenant_id"`
	ReviewTaskID     string            `json:"review_task_id"`
	AnswerSegmentID  string            `json:"answer_segment_id"`
	ReviewerID       string            `json:"reviewer_id"`
	Score            float64           `json:"score"`
	MaxScore         float64           `json:"max_score"`
	RubricSelections []RubricSelection `json:"rubric_selections"`
	Comments         string            `json:"comments,omitempty"`
	PrivateNote      string            `json:"private_note,omitempty"`
	StudentFeedback  string            `json:"student_feedback,omitempty"`
	Reason           string            `json:"reason,omitempty"`
	GradeRound       string            `json:"grade_round"`
	AIGradeID        string            `json:"ai_grade_id,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
}

type RubricSelection struct {
	PointID string  `json:"point_id"`
	Score   float64 `json:"score"`
}

type CreateTaskInput struct {
	AnswerSegmentID string     `json:"answer_segment_id"`
	Source          string     `json:"source"`
	Priority        int        `json:"priority"`
	AssignedTo      string     `json:"assigned_to"`
	GradeRound      string     `json:"grade_round"`
	DueAt           *time.Time `json:"due_at"`
}

type AssignTaskInput struct {
	AssignedTo       string `json:"assigned_to"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type BatchAssignInput struct {
	TaskIDs           []string         `json:"task_ids"`
	AssignedTo        string           `json:"assigned_to"`
	ExpectedRevisions map[string]int64 `json:"expected_revisions"`
}

type SubmitGradeInput struct {
	Score            float64           `json:"score"`
	RubricSelections []RubricSelection `json:"rubric_selections"`
	Comments         string            `json:"comments"`
	PrivateNote      string            `json:"private_note"`
	StudentFeedback  string            `json:"student_feedback"`
	Reason           string            `json:"reason"`
	ExpectedRevision int64             `json:"expected_revision"`
}

type ReturnTaskInput struct {
	Reason             string `json:"reason"`
	ExpectedRevision   int64  `json:"expected_revision"`
	MustOwnActiveClaim bool   `json:"-"`
}

type ReviewDraft struct {
	ID               string            `json:"id"`
	ReviewTaskID     string            `json:"review_task_id"`
	ReviewerID       string            `json:"reviewer_id"`
	Score            *float64          `json:"score,omitempty"`
	RubricSelections []RubricSelection `json:"rubric_selections"`
	Comments         string            `json:"comments"`
	PrivateNote      string            `json:"private_note"`
	StudentFeedback  string            `json:"student_feedback"`
	ViewerState      map[string]any    `json:"viewer_state"`
	Revision         int               `json:"revision"`
	ClientUpdatedAt  *time.Time        `json:"client_updated_at,omitempty"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type SaveDraftInput struct {
	Score            *float64          `json:"score"`
	RubricSelections []RubricSelection `json:"rubric_selections"`
	Comments         string            `json:"comments"`
	PrivateNote      string            `json:"private_note"`
	StudentFeedback  string            `json:"student_feedback"`
	ViewerState      map[string]any    `json:"viewer_state"`
	ExpectedRevision int               `json:"expected_revision"`
	ClientUpdatedAt  *time.Time        `json:"client_updated_at"`
}

type DraftStore interface {
	GetDraft(context.Context, string, string, string) (ReviewDraft, error)
	SaveDraft(context.Context, string, string, string, SaveDraftInput) (ReviewDraft, error)
}

type NextTaskInput struct {
	ExamID     string `json:"exam_id"`
	QuestionID string `json:"question_id"`
}

type ClaimTaskOptions struct {
	AllowUnassigned bool
}

type Workspace struct {
	Task              ReviewTask `json:"task"`
	Context           Context    `json:"context"`
	SegmentImageURL   string     `json:"segment_image_url"`
	OriginalImageURL  string     `json:"original_image_url,omitempty"`
	OriginalFileID    string     `json:"-"`
	SegmentStatus     string     `json:"segment_status"`
	SegmentConfidence *float64   `json:"segment_confidence,omitempty"`
}

type WorkbenchStore interface {
	ClaimNextTask(context.Context, string, string, NextTaskInput, ClaimTaskOptions) (ReviewTask, error)
	GetWorkspace(context.Context, string, string) (Workspace, error)
	RenewTaskClaim(context.Context, string, string, string) error
	ReleaseTaskClaim(context.Context, string, string, string) (ReviewTask, error)
}

type SubmitResult struct {
	Task              ReviewTask         `json:"task"`
	Grade             HumanGrade         `json:"human_grade"`
	QuestionGradeID   string             `json:"question_grade_id,omitempty"`
	DoubleMarkSession *DoubleMarkSession `json:"double_mark_session,omitempty"`
	FinalGrade        *FinalGrade        `json:"final_grade,omitempty"`
	ArbitrationTask   *ArbitrationTask   `json:"arbitration_task,omitempty"`
}

type ListFilter struct {
	Status          string
	AssignedTo      string
	ExamID          string
	Limit           int
	CursorPriority  int
	CursorCreatedAt time.Time
	CursorID        string
	// Scope fields are populated by HTTP handlers from the server-derived
	// authentication scope.  They are intentionally plain values so the
	// review package remains independent of the auth package.
	ScopeMode      string
	ScopeActorID   string
	ScopeSchoolIDs []string
	ScopeExamIDs   []string
	ScopeTaskIDs   []string
}

// TaskAggregate describes the complete filtered queue. It is calculated with
// the same tenant, authorization and query filters as ListTasks, but ignores
// pagination so a client never mistakes the current page for the whole queue.
type TaskAggregate struct {
	TotalCount     int                     `json:"total_count"`
	CompletedCount int                     `json:"completed_count"`
	RemainingCount int                     `json:"remaining_count"`
	StatusCounts   map[string]int          `json:"status_counts"`
	Reviewers      []ReviewerTaskAggregate `json:"reviewers"`
}

type ReviewerTaskAggregate struct {
	ReviewerID     string `json:"reviewer_id"`
	TotalCount     int    `json:"total_count"`
	CompletedCount int    `json:"completed_count"`
	RemainingCount int    `json:"remaining_count"`
}

type DoubleMarkPolicy struct {
	ID                  string    `json:"id"`
	TenantID            string    `json:"tenant_id"`
	ExamID              string    `json:"exam_id"`
	QuestionID          string    `json:"question_id,omitempty"`
	Enabled             bool      `json:"enabled"`
	Threshold           float64   `json:"threshold"`
	ResolutionStrategy  string    `json:"resolution_strategy"`
	AllowSameArbitrator bool      `json:"allow_same_arbitrator"`
	CreatedBy           string    `json:"created_by"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type SetDoubleMarkPolicyInput struct {
	Enabled             bool    `json:"enabled"`
	Threshold           float64 `json:"threshold"`
	ResolutionStrategy  string  `json:"resolution_strategy"`
	AllowSameArbitrator bool    `json:"allow_same_arbitrator"`
}

type PolicyFilter struct {
	ExamID     string
	QuestionID string
}

type CreateDoubleMarkSessionInput struct {
	AnswerSegmentID  string     `json:"answer_segment_id"`
	FirstReviewerID  string     `json:"first_reviewer_id"`
	SecondReviewerID string     `json:"second_reviewer_id"`
	Priority         int        `json:"priority"`
	DueAt            *time.Time `json:"due_at"`
}

type DoubleMarkSession struct {
	ID                 string    `json:"id"`
	TenantID           string    `json:"tenant_id"`
	ExamID             string    `json:"exam_id"`
	QuestionID         string    `json:"question_id"`
	QuestionNo         string    `json:"question_no"`
	AnswerSegmentID    string    `json:"answer_segment_id"`
	SubmissionID       string    `json:"submission_id"`
	AnonymousCode      string    `json:"anonymous_code"`
	FirstReviewTaskID  string    `json:"first_review_task_id"`
	SecondReviewTaskID string    `json:"second_review_task_id"`
	FirstReviewerID    string    `json:"first_reviewer_id"`
	SecondReviewerID   string    `json:"second_reviewer_id"`
	Threshold          float64   `json:"threshold"`
	ResolutionStrategy string    `json:"resolution_strategy"`
	Status             string    `json:"status"`
	ScoreDifference    *float64  `json:"score_difference,omitempty"`
	FinalGradeID       string    `json:"final_grade_id,omitempty"`
	ArbitrationTaskID  string    `json:"arbitration_task_id,omitempty"`
	CreatedBy          string    `json:"created_by"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type DoubleMarkSessionFilter struct {
	Status          string
	AnswerSegmentID string
	// ExamID and QuestionID keep aggregate consumers from reading every
	// double-mark session in a tenant and filtering in application memory.
	ExamID     string
	QuestionID string
}

type CreateArbitrationTaskInput struct {
	DoubleMarkSessionID string `json:"double_mark_session_id"`
	AssignedTo          string `json:"assigned_to"`
	DifferenceReason    string `json:"difference_reason"`
}

type AssignArbitrationTaskInput struct {
	AssignedTo       string `json:"assigned_to"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type SubmitArbitrationInput struct {
	FinalScore       float64 `json:"final_score"`
	Reason           string  `json:"reason"`
	StudentFeedback  string  `json:"student_feedback"`
	ExpectedRevision int64   `json:"expected_revision"`
}

type ArbitrationTask struct {
	ID                  string        `json:"id"`
	TenantID            string        `json:"tenant_id"`
	DoubleMarkSessionID string        `json:"double_mark_session_id"`
	ExamID              string        `json:"exam_id"`
	QuestionID          string        `json:"question_id"`
	QuestionNo          string        `json:"question_no"`
	AnswerSegmentID     string        `json:"answer_segment_id"`
	SubmissionID        string        `json:"submission_id"`
	AnonymousCode       string        `json:"anonymous_code"`
	FirstReviewerID     string        `json:"first_reviewer_id"`
	SecondReviewerID    string        `json:"second_reviewer_id"`
	FirstScore          float64       `json:"first_score"`
	SecondScore         float64       `json:"second_score"`
	ScoreDifference     float64       `json:"score_difference"`
	DifferenceReason    string        `json:"difference_reason"`
	Status              string        `json:"status"`
	AssignedTo          string        `json:"assigned_to,omitempty"`
	FinalScore          *float64      `json:"final_score,omitempty"`
	Reason              string        `json:"reason,omitempty"`
	StudentFeedback     string        `json:"student_feedback,omitempty"`
	AllowSameArbitrator bool          `json:"allow_same_arbitrator"`
	Context             ReviewContext `json:"context"`
	Revision            int64         `json:"revision"`
	CreatedBy           string        `json:"created_by"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
}

type ReviewContext struct {
	RawAnswer    string         `json:"raw_answer,omitempty"`
	OCRText      string         `json:"ocr_text,omitempty"`
	AISuggestion map[string]any `json:"ai_suggestion,omitempty"`
}

type ArbitrationFilter struct {
	Status          string
	AssignedTo      string
	ExamID          string
	Limit           int
	CursorCreatedAt time.Time
	CursorID        string
	ScopeMode       string
	ScopeActorID    string
	ScopeSchoolIDs  []string
	ScopeExamIDs    []string
	ScopeTaskIDs    []string
}

type FinalGrade struct {
	ID                  string    `json:"id"`
	TenantID            string    `json:"tenant_id"`
	ExamID              string    `json:"exam_id"`
	QuestionID          string    `json:"question_id"`
	QuestionNo          string    `json:"question_no"`
	AnswerSegmentID     string    `json:"answer_segment_id"`
	SubmissionID        string    `json:"submission_id"`
	AnonymousCode       string    `json:"anonymous_code"`
	Score               float64   `json:"score"`
	MaxScore            float64   `json:"max_score"`
	Source              string    `json:"source"`
	DoubleMarkSessionID string    `json:"double_mark_session_id,omitempty"`
	ArbitrationTaskID   string    `json:"arbitration_task_id,omitempty"`
	ResolutionStrategy  string    `json:"resolution_strategy,omitempty"`
	Locked              bool      `json:"locked"`
	CreatedBy           string    `json:"created_by"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type Store interface {
	RecoverCommand(context.Context, string, string, string) (commandreceipt.Receipt, error)
	CreateTask(ctx context.Context, tenantID string, actorID string, input CreateTaskInput) (ReviewTask, error)
	ListTasks(ctx context.Context, tenantID string, filter ListFilter) ([]ReviewTask, error)
	AggregateTasks(ctx context.Context, tenantID string, filter ListFilter) (TaskAggregate, error)
	HasActiveAssignment(ctx context.Context, tenantID string, reviewerID string, answerSegmentID string) (bool, error)
	GetTask(ctx context.Context, tenantID string, id string) (ReviewTask, error)
	AssignTask(ctx context.Context, tenantID string, id string, actorID string, input AssignTaskInput) (ReviewTask, error)
	BatchAssignTasks(ctx context.Context, tenantID string, actorID string, input BatchAssignInput) ([]ReviewTask, error)
	SubmitGrade(ctx context.Context, tenantID string, id string, reviewerID string, input SubmitGradeInput) (SubmitResult, error)
	ReturnTask(ctx context.Context, tenantID string, id string, actorID string, input ReturnTaskInput) (ReviewTask, error)
	SetExamDoubleMarkPolicy(ctx context.Context, tenantID string, examID string, actorID string, input SetDoubleMarkPolicyInput) (DoubleMarkPolicy, error)
	SetQuestionDoubleMarkPolicy(ctx context.Context, tenantID string, questionID string, actorID string, input SetDoubleMarkPolicyInput) (DoubleMarkPolicy, error)
	ListDoubleMarkPolicies(ctx context.Context, tenantID string, filter PolicyFilter) ([]DoubleMarkPolicy, error)
	CreateDoubleMarkSession(ctx context.Context, tenantID string, actorID string, input CreateDoubleMarkSessionInput) (DoubleMarkSession, error)
	ListDoubleMarkSessions(ctx context.Context, tenantID string, filter DoubleMarkSessionFilter) ([]DoubleMarkSession, error)
	GetDoubleMarkSession(ctx context.Context, tenantID string, id string) (DoubleMarkSession, error)
	CreateArbitrationTask(ctx context.Context, tenantID string, actorID string, input CreateArbitrationTaskInput) (ArbitrationTask, error)
	ListArbitrationTasks(ctx context.Context, tenantID string, filter ArbitrationFilter) ([]ArbitrationTask, error)
	GetArbitrationTask(ctx context.Context, tenantID string, id string) (ArbitrationTask, error)
	AssignArbitrationTask(ctx context.Context, tenantID string, id string, actorID string, input AssignArbitrationTaskInput) (ArbitrationTask, error)
	SubmitArbitration(ctx context.Context, tenantID string, id string, arbitratorID string, input SubmitArbitrationInput) (ArbitrationTask, FinalGrade, error)
}

type ArbitrationSubmitResult struct {
	Task  ArbitrationTask `json:"arbitration_task"`
	Grade FinalGrade      `json:"final_grade"`
}
