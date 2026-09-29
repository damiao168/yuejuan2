// Package scorerelease owns the immutable public score boundary.  It does not
// recalculate grades: it snapshots already-confirmed final facts, applies a
// server-side release gate, and exposes a deliberately student-safe view.
package scorerelease

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound                  = errors.New("score release not found")
	ErrInvalidInput              = errors.New("invalid score release input")
	ErrInvalidTransition         = errors.New("invalid score release transition")
	ErrGateBlocked               = errors.New("score release gate is blocked")
	ErrForbidden                 = errors.New("score release access forbidden")
	ErrStaleSource               = errors.New("score release source is no longer current")
	ErrAnonymousPaperUnavailable = errors.New("anonymous high-score paper is unavailable")
)

const (
	StatusDraft     = "draft"
	StatusPublished = "published"

	SourceInitial   = "initial"
	SourceRegrade   = "regrade"
	SourceAppeal    = "appeal"
	SourceRollback  = "rollback"
	SourceMigration = "migration"
)

// AppealWindow is materialised with a release so that a later policy edit
// cannot silently change the period in which a student may question that
// specific published score.
type AppealWindow struct {
	Enabled            bool       `json:"enabled"`
	OpensAt            *time.Time `json:"opens_at,omitempty"`
	ClosesAt           *time.Time `json:"closes_at,omitempty"`
	AllowedReasonCodes []string   `json:"allowed_reason_codes,omitempty"`
}

// VisibilityPolicy is intentionally small. More granular A21 policy can be
// added without exposing internal scoring data from this package.
type VisibilityPolicy struct {
	ShowQuestionScores     bool `json:"show_question_scores"`
	ShowFeedback           bool `json:"show_feedback"`
	ShowRubricSummary      bool `json:"show_rubric_summary"`
	ShowCohortStatistics   bool `json:"show_cohort_statistics"`
	ShowScoreDistribution  bool `json:"show_score_distribution"`
	ShowPercentile         bool `json:"show_percentile"`
	ShowExactRank          bool `json:"show_exact_rank"`
	ShowQuestionStatistics bool `json:"show_question_statistics"`
	ShowKnowledgeAnalysis  bool `json:"show_knowledge_analysis"`
	ShowQuestionStem       bool `json:"show_question_stem"`
	ShowAnswers            bool `json:"show_answers"`
	ShowHighScorePaper     bool `json:"show_high_score_paper"`
}

type GateIssue struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Blocking    bool   `json:"blocking"`
	Count       int    `json:"count"`
	ActionRoute string `json:"action_route,omitempty"`
}

type Gate struct {
	Version     string         `json:"gate_version"`
	Passed      bool           `json:"passed"`
	Blocking    []GateIssue    `json:"blocking"`
	Warnings    []GateIssue    `json:"warnings"`
	Counts      map[string]int `json:"counts"`
	GeneratedAt time.Time      `json:"generated_at"`
}

type StudentExplanation struct {
	Feedback        string   `json:"feedback,omitempty"`
	RubricSummary   []string `json:"rubric_summary,omitempty"`
	QuestionType    string   `json:"question_type,omitempty"`
	Stem            string   `json:"stem,omitempty"`
	KnowledgePoints []string `json:"knowledge_points,omitempty"`
	CorrectAnswer   string   `json:"correct_answer,omitempty"`
	ActualAnswer    string   `json:"actual_answer,omitempty"`
}

type QuestionFact struct {
	QuestionID   string             `json:"question_id"`
	QuestionNo   string             `json:"question_no"`
	FinalGradeID string             `json:"final_grade_id"`
	Score        float64            `json:"score"`
	MaxScore     float64            `json:"max_score"`
	SourceType   string             `json:"source_type"`
	SourceID     string             `json:"source_id,omitempty"`
	Explanation  StudentExplanation `json:"explanation"`
}

type SubmissionFact struct {
	StudentID    string         `json:"student_id,omitempty"`
	SubmissionID string         `json:"submission_id"`
	TotalScore   float64        `json:"total_score"`
	MaxScore     float64        `json:"max_score"`
	Status       string         `json:"status"`
	Questions    []QuestionFact `json:"questions"`
}

type Release struct {
	ID                  string           `json:"id"`
	TenantID            string           `json:"tenant_id"`
	ExamID              string           `json:"exam_id"`
	Version             int              `json:"version"`
	Source              string           `json:"source"`
	Reason              string           `json:"reason"`
	Status              string           `json:"status"`
	IdempotencyKey      string           `json:"-"`
	VisibilityPolicy    VisibilityPolicy `json:"visibility_policy"`
	AppealWindow        AppealWindow     `json:"appeal_window"`
	GateSnapshot        Gate             `json:"gate_snapshot"`
	SourceReleaseID     string           `json:"source_release_id,omitempty"`
	SupersedesReleaseID string           `json:"supersedes_release_id,omitempty"`
	CreatedBy           string           `json:"created_by"`
	CreatedAt           time.Time        `json:"created_at"`
	PublishedBy         string           `json:"published_by,omitempty"`
	PublishedAt         *time.Time       `json:"published_at,omitempty"`
}

type ReleaseItem struct {
	ReleaseID    string  `json:"release_id"`
	StudentID    string  `json:"student_id,omitempty"`
	SubmissionID string  `json:"submission_id"`
	TotalScore   float64 `json:"total_score"`
	MaxScore     float64 `json:"max_score"`
	Status       string  `json:"status"`
	SnapshotHash string  `json:"snapshot_hash"`
}

type ReleaseQuestion struct {
	ReleaseID    string `json:"release_id"`
	SubmissionID string `json:"submission_id"`
	QuestionFact
}

type Detail struct {
	Release   Release           `json:"release"`
	Items     []ReleaseItem     `json:"items"`
	Questions []ReleaseQuestion `json:"questions,omitempty"`
}

type DiffQuestion struct {
	SubmissionID string  `json:"submission_id"`
	QuestionID   string  `json:"question_id"`
	OldScore     float64 `json:"old_score"`
	NewScore     float64 `json:"new_score"`
}

type DiffItem struct {
	SubmissionID string  `json:"submission_id"`
	OldTotal     float64 `json:"old_total"`
	NewTotal     float64 `json:"new_total"`
}

type Diff struct {
	BaseReleaseID string         `json:"base_release_id"`
	ReleaseID     string         `json:"release_id"`
	AffectedCount int            `json:"affected_count"`
	Items         []DiffItem     `json:"items"`
	Questions     []DiffQuestion `json:"questions"`
}

type CreateInput struct {
	Source           string           `json:"source"`
	Reason           string           `json:"reason"`
	IdempotencyKey   string           `json:"idempotency_key"`
	VisibilityPolicy VisibilityPolicy `json:"visibility_policy"`
	AppealWindow     AppealWindow     `json:"appeal_window"`
}

type RollbackInput struct {
	SourceReleaseID string `json:"source_release_id"`
	Reason          string `json:"reason"`
	IdempotencyKey  string `json:"idempotency_key"`
}

// RegradeChange is a reviewed, frozen correction for one question fact in a
// previously published release. It deliberately contains no current
// final-grade lookup: the source release remains the sole population source.
type RegradeChange struct {
	SubmissionID    string  `json:"submission_id"`
	QuestionID      string  `json:"question_id"`
	Score           float64 `json:"score"`
	MaxScore        float64 `json:"max_score"`
	ReviewedGradeID string  `json:"reviewed_grade_id,omitempty"`
}

type CreateRegradeInput struct {
	SourceReleaseID string          `json:"source_release_id"`
	QuestionID      string          `json:"question_id"`
	Reason          string          `json:"reason"`
	IdempotencyKey  string          `json:"idempotency_key"`
	Changes         []RegradeChange `json:"changes"`
}

// StudentResult deliberately has no final-grade IDs, source IDs, reviewer
// identities, quality warnings, model prompts, raw AI output or private notes.
type StudentResult struct {
	ExamID            string                  `json:"exam_id"`
	ReleaseID         string                  `json:"release_id"`
	ReleaseVersion    int                     `json:"release_version"`
	Exam              *StudentExam            `json:"exam,omitempty"`
	TotalScore        float64                 `json:"total_score"`
	MaxScore          float64                 `json:"max_score"`
	OverallTotalScore float64                 `json:"overall_total_score"`
	OverallMaxScore   float64                 `json:"overall_max_score"`
	ScoreRate         float64                 `json:"score_rate"`
	Reference         *StudentReference       `json:"reference,omitempty"`
	Rankings          *StudentRankings        `json:"rankings,omitempty"`
	PaperPages        []StudentPaperPage      `json:"paper_pages,omitempty"`
	HighScorePaper    *StudentHighScorePaper  `json:"high_score_paper,omitempty"`
	SubjectBalance    []StudentSubjectBalance `json:"subject_balance,omitempty"`
	Questions         []StudentQuestion       `json:"questions,omitempty"`
	AppealWindow      StudentAppealView       `json:"appeal_window"`
}

// StudentExam is public context for a released result. PublishedAt is taken
// from the immutable release, not from the mutable exam record.
type StudentExam struct {
	Name        string    `json:"name"`
	Subject     string    `json:"subject"`
	ExamType    string    `json:"exam_type"`
	PublishedAt time.Time `json:"published_at"`
}

// StudentReference contains aggregate facts only. No peer score or identity
// ever crosses the student boundary. Small cohorts return availability=false.
type StudentReference struct {
	Scope               string   `json:"scope"`
	SampleSize          int      `json:"sample_size"`
	StatisticsAvailable bool     `json:"statistics_available"`
	UnavailableReason   string   `json:"unavailable_reason,omitempty"`
	MeanScore           *float64 `json:"mean_score,omitempty"`
	MedianScore         *float64 `json:"median_score,omitempty"`
	Q1                  *float64 `json:"q1,omitempty"`
	Q3                  *float64 `json:"q3,omitempty"`
	MinScore            *float64 `json:"min_score,omitempty"`
	MaxScore            *float64 `json:"max_score,omitempty"`
	Percentile          *float64 `json:"percentile,omitempty"`
	Rank                *int     `json:"rank,omitempty"`
}

type StudentRankings struct {
	ClassRank int `json:"class_rank"`
	ClassSize int `json:"class_size"`
	GradeRank int `json:"grade_rank"`
	GradeSize int `json:"grade_size"`
}

type StudentSubjectBalance struct {
	Subject             string  `json:"subject"`
	StudentScoreRate    float64 `json:"student_score_rate"`
	SchoolMeanScoreRate float64 `json:"school_mean_score_rate"`
	SampleSize          int     `json:"sample_size"`
}

type StudentPaperPage struct {
	PageNo           int    `json:"page_no"`
	QuestionID       string `json:"question_id"`
	SubmissionPageID string `json:"submission_page_id,omitempty"`
}

type StudentHighScorePaper struct {
	Available  bool                    `json:"available"`
	TotalScore float64                 `json:"total_score,omitempty"`
	MaxScore   float64                 `json:"max_score,omitempty"`
	Pages      []StudentPaperPage      `json:"pages,omitempty"`
	ScoreMarks []StudentPaperScoreMark `json:"score_marks,omitempty"`
}

type StudentPaperScoreMark struct {
	QuestionID     string                `json:"question_id"`
	QuestionNo     string                `json:"question_no"`
	Score          float64               `json:"score"`
	MaxScore       float64               `json:"max_score"`
	PageNo         int                   `json:"page_no"`
	AnswerGeometry *StudentImageGeometry `json:"answer_geometry,omitempty"`
}

type StudentQuestion struct {
	QuestionID       string                    `json:"question_id"`
	QuestionNo       string                    `json:"question_no"`
	Subject          string                    `json:"subject,omitempty"`
	QuestionType     string                    `json:"question_type,omitempty"`
	Stem             string                    `json:"stem,omitempty"`
	Score            float64                   `json:"score"`
	MaxScore         float64                   `json:"max_score"`
	ScoreRate        float64                   `json:"score_rate"`
	KnowledgePoints  []string                  `json:"knowledge_points,omitempty"`
	Cohort           *StudentQuestionReference `json:"cohort,omitempty"`
	CorrectAnswer    string                    `json:"correct_answer,omitempty"`
	ActualAnswer     string                    `json:"actual_answer,omitempty"`
	PageNo           int                       `json:"page_no,omitempty"`
	SubmissionPageID string                    `json:"submission_page_id,omitempty"`
	AnswerGeometry   *StudentImageGeometry     `json:"answer_geometry,omitempty"`
	Feedback         string                    `json:"feedback,omitempty"`
	RubricSummary    []string                  `json:"rubric_summary,omitempty"`
}

type StudentQuestionReference struct {
	SampleSize      int      `json:"sample_size"`
	MeanScoreRate   float64  `json:"mean_score_rate"`
	FullScoreRate   float64  `json:"full_score_rate"`
	ZeroScoreRate   float64  `json:"zero_score_rate"`
	ClassMeanScore  *float64 `json:"class_mean_score,omitempty"`
	SchoolMeanScore *float64 `json:"school_mean_score,omitempty"`
	MedianScore     *float64 `json:"median_score,omitempty"`
}

type StudentImageGeometry struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// StudentQuestionImageSource 保存图片接口内部使用的资源 ID，不直接序列化给学生。
// 各查询负责核对学生、当前发布版本和展示策略，图片读取器再检查所需资产信息。
type StudentQuestionImageSource struct {
	AnswerSegmentID      string `json:"-"`
	AnonymousPageID      string `json:"-"`
	AnonymousFileAssetID string `json:"-"`
	ReleaseID            string `json:"-"`
}

type StudentAppealView struct {
	Open               bool       `json:"open"`
	ClosesAt           *time.Time `json:"closes_at,omitempty"`
	AllowedReasonCodes []string   `json:"allowed_reason_codes,omitempty"`
}

type Store interface {
	Create(context.Context, string, string, string, CreateInput) (Release, error)
	CreateRollback(context.Context, string, string, string, RollbackInput) (Release, error)
	CreateFromRegrade(context.Context, string, string, string, CreateRegradeInput) (Release, error)
	List(context.Context, string, string) ([]Release, error)
	Get(context.Context, string, string) (Detail, error)
	Diff(context.Context, string, string, string) (Diff, error)
	Gate(context.Context, string, string) (Gate, error)
	Publish(context.Context, string, string, string) (Release, error)
	CurrentPublished(context.Context, string, string) (Detail, error)
	StudentResult(context.Context, string, string, string) (StudentResult, error)
	StudentQuestion(context.Context, string, string, string, string) (StudentQuestion, error)
	StudentQuestionImage(context.Context, string, string, string, string) (StudentQuestionImageSource, error)
	StudentPaperPageImage(context.Context, string, string, string, string, bool) (StudentQuestionImageSource, error)
}
