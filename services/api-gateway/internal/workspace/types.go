package workspace

import "time"

type Stage struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	State       string `json:"state"`
	ActionRoute string `json:"action_route"`
}

type StageProgress struct {
	Stage     string `json:"stage"`
	Status    string `json:"status"`
	Completed *int   `json:"completed,omitempty"`
	Total     *int   `json:"total,omitempty"`
	Unit      string `json:"unit,omitempty"`
	Summary   string `json:"summary"`
}

type Notice struct {
	Code        string `json:"code"`
	Title       string `json:"title"`
	Message     string `json:"message"`
	Severity    string `json:"severity"`
	ActionLabel string `json:"action_label,omitempty"`
	ActionRoute string `json:"action_route,omitempty"`
}

type Counts struct {
	PaperCount                  int `json:"paper_count"`
	QuestionCount               int `json:"question_count"`
	SubmissionCount             int `json:"submission_count"`
	FailedSubmissionCount       int `json:"failed_submission_count"`
	QualityIssueSubmissionCount int `json:"quality_issue_submission_count"`
	UnmatchedSubmissionCount    int `json:"unmatched_submission_count"`
	PendingReviewCount          int `json:"pending_review_count"`
	PendingArbitrationCount     int `json:"pending_arbitration_count"`
}

type NextAction struct {
	Code        string `json:"code"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Route       string `json:"route"`
	Priority    string `json:"priority"`
}

type SubjectSummary struct {
	Code                    string         `json:"code"`
	Label                   string         `json:"label"`
	TotalScore              float64        `json:"total_score"`
	QuestionCount           int            `json:"question_count"`
	ConfiguredQuestionCount int            `json:"configured_question_count"`
	FrozenQuestionCount     int            `json:"frozen_question_count"`
	RiskTierSource          string         `json:"risk_tier_source,omitempty"`
	QuestionTypes           map[string]int `json:"question_types"`
}

type SubjectExam struct {
	ExamID     string  `json:"exam_id"`
	Subject    string  `json:"subject"`
	TotalScore float64 `json:"total_score"`
}

type Projection struct {
	ExamID          string          `json:"exam_id"`
	ExamName        string          `json:"exam_name"`
	ExamSessionName string          `json:"exam_session_name,omitempty"`
	SubjectExams    []SubjectExam   `json:"subject_exams"`
	ExamStatus      string          `json:"exam_status"`
	Revision        int64           `json:"revision"`
	Stage           string          `json:"stage"`
	Stages          []Stage         `json:"stages"`
	StageProgress   []StageProgress `json:"stage_progress"`
	Blockers        []Notice        `json:"blockers"`
	Warnings        []Notice        `json:"warnings"`
	Counts          Counts          `json:"counts"`
	NextActions     []NextAction    `json:"next_actions"`
	RiskTier        string          `json:"risk_tier"`
	SubjectSummary  SubjectSummary  `json:"subject_summary"`
	UpdatedAt       time.Time       `json:"updated_at"`
}
