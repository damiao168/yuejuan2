// Package scorerelease 管理不可变的公开成绩边界。
// 它不重新计算成绩，而是为已确认的最终事实建立快照、应用服务器端发布门禁，并公开面向学生的受限视图。
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
	ErrStudentReleaseChanged     = errors.New("student published release changed")
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

// AppealWindow 随发布实例化，避免后续策略编辑静默改变学生对该次已发布分数提出疑问的期限。
type AppealWindow struct {
	Enabled            bool       `json:"enabled"`
	OpensAt            *time.Time `json:"opens_at,omitempty"`
	ClosesAt           *time.Time `json:"closes_at,omitempty"`
	AllowedReasonCodes []string   `json:"allowed_reason_codes,omitempty"`
}

// VisibilityPolicy 保持精简，可添加更细的 A21 策略，而不从本包暴露内部评分数据。
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

// RegradeChange 是对之前已发布版本中一条题目事实的已复核、冻结修正。
// 它不查询当前最终成绩；来源发布仍是群体的唯一来源。
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

// StudentResult 不包含最终成绩 ID、来源 ID、复核人员身份、质量警告、模型提示词、原始 AI 输出或私有笔记。
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
	SessionSummary    *StudentSessionSummary  `json:"session_summary,omitempty"`
	Questions         []StudentQuestion       `json:"questions,omitempty"`
	AppealWindow      StudentAppealView       `json:"appeal_window"`
}

// StudentExam 是已发布结果的公开上下文。PublishedAt 取自不可变发布，不取自可变考试记录。
type StudentExam struct {
	Name        string    `json:"name"`
	Subject     string    `json:"subject"`
	ExamType    string    `json:"exam_type"`
	PublishedAt time.Time `json:"published_at"`
}

// StudentReference 只包含聚合事实。任何同学分数或身份都不能越过学生边界。
// 小群体返回 availability=false。
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
	ClassRank              int    `json:"class_rank"`
	ClassSize              int    `json:"class_size"`
	GradeRank              int    `json:"grade_rank"`
	GradeSize              int    `json:"grade_size"`
	ClassUnavailableReason string `json:"class_unavailable_reason,omitempty"`
	GradeUnavailableReason string `json:"grade_unavailable_reason,omitempty"`
}

// StudentSessionSummary 仅列出本人在考次中已冻结的应考科目；未发布科目不带分数字段。
type StudentSessionSummary struct {
	PublishedCount int                     `json:"published_count"`
	ExpectedCount  int                     `json:"expected_count"`
	Subjects       []StudentSessionSubject `json:"subjects"`
}
type StudentSessionSubject struct {
	ExamID              string     `json:"exam_id"`
	Subject             string     `json:"subject"`
	Name                string     `json:"name"`
	Status              string     `json:"status"`
	Score               *float64   `json:"score,omitempty"`
	MaxScore            *float64   `json:"max_score,omitempty"`
	PublishedAt         *time.Time `json:"published_at,omitempty"`
	StatisticsAvailable bool       `json:"statistics_available"`
	StatisticsReason    string     `json:"statistics_reason,omitempty"`
	MeanScoreRate       *float64   `json:"mean_score_rate,omitempty"`
	SampleSize          int        `json:"sample_size,omitempty"`
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
	QuestionID              string                    `json:"question_id"`
	QuestionNo              string                    `json:"question_no"`
	Subject                 string                    `json:"subject,omitempty"`
	QuestionType            string                    `json:"question_type,omitempty"`
	Stem                    string                    `json:"stem,omitempty"`
	Score                   float64                   `json:"score"`
	MaxScore                float64                   `json:"max_score"`
	ScoreRate               float64                   `json:"score_rate"`
	KnowledgePoints         []string                  `json:"knowledge_points,omitempty"`
	Cohort                  *StudentQuestionReference `json:"cohort,omitempty"`
	CohortUnavailableReason string                    `json:"cohort_unavailable_reason,omitempty"`
	CorrectAnswer           string                    `json:"correct_answer,omitempty"`
	ActualAnswer            string                    `json:"actual_answer,omitempty"`
	PageNo                  int                       `json:"page_no,omitempty"`
	SubmissionPageID        string                    `json:"submission_page_id,omitempty"`
	AnswerGeometry          *StudentImageGeometry     `json:"answer_geometry,omitempty"`
	Feedback                string                    `json:"feedback,omitempty"`
	RubricSummary           []string                  `json:"rubric_summary,omitempty"`
}

type StudentQuestionReference struct {
	ClassUnavailableReason string   `json:"class_unavailable_reason,omitempty"`
	SampleSize             int      `json:"sample_size"`
	MeanScoreRate          float64  `json:"mean_score_rate"`
	FullScoreRate          float64  `json:"full_score_rate"`
	ZeroScoreRate          float64  `json:"zero_score_rate"`
	ClassMeanScore         *float64 `json:"class_mean_score,omitempty"`
	SchoolMeanScore        *float64 `json:"school_mean_score,omitempty"`
	MedianScore            *float64 `json:"median_score,omitempty"`
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
	State              string     `json:"state"`
	OpensAt            *time.Time `json:"opens_at,omitempty"`
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
	StudentQuestion(context.Context, string, string, string, string, ...string) (StudentQuestion, error)
	StudentQuestionImage(context.Context, string, string, string, string, ...string) (StudentQuestionImageSource, error)
	StudentPaperPageImage(context.Context, string, string, string, string, bool, ...string) (StudentQuestionImageSource, error)
}
