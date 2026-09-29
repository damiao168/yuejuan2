package report

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"errors"
)

var (
	ErrNotFound     = errors.New("report resource not found")
	ErrInvalidInput = errors.New("invalid report input")
	ErrForbidden    = errors.New("report action forbidden")
)

type EmptyState struct {
	Empty  bool   `json:"empty"`
	Reason string `json:"reason,omitempty"`
}

// Metric 用 Available 区分无样本与真实零值；比例型指标的 Value 为小数，不是百分数。
type Metric struct {
	Available   bool    `json:"available"`
	Value       float64 `json:"value"`
	Numerator   int     `json:"numerator,omitempty"`
	Denominator int     `json:"denominator,omitempty"`
	Reason      string  `json:"reason,omitempty"`
}

type ScoreDistributionBucket struct {
	Label string `json:"label"`
	Min   int    `json:"min"`
	Max   int    `json:"max"`
	Count int    `json:"count"`
}

type ScoreStats struct {
	Count         int                       `json:"count"`
	Average       float64                   `json:"average"`
	Highest       float64                   `json:"highest"`
	Lowest        float64                   `json:"lowest"`
	Median        float64                   `json:"median"`
	Stddev        float64                   `json:"stddev"`
	PassRate      float64                   `json:"pass_rate"`
	ExcellentRate float64                   `json:"excellent_rate"`
	Distribution  []ScoreDistributionBucket `json:"distribution"`
}

type FeedbackItem struct {
	QuestionID string `json:"question_id,omitempty"`
	QuestionNo string `json:"question_no,omitempty"`
	Source     string `json:"source"`
	Text       string `json:"text"`
}

type ErrorClue struct {
	QuestionID string `json:"question_id,omitempty"`
	QuestionNo string `json:"question_no,omitempty"`
	Source     string `json:"source"`
	Text       string `json:"text"`
	Count      int    `json:"count,omitempty"`
}

type KnowledgeMastery struct {
	KnowledgePoint string  `json:"knowledge_point"`
	Score          float64 `json:"score"`
	MaxScore       float64 `json:"max_score"`
	MasteryRate    float64 `json:"mastery_rate"`
	QuestionCount  int     `json:"question_count"`
}

type StudentQuestionReport struct {
	QuestionID      string         `json:"question_id"`
	QuestionNo      string         `json:"question_no"`
	QuestionType    string         `json:"question_type"`
	Score           float64        `json:"score"`
	MaxScore        float64        `json:"max_score"`
	ScoreRate       float64        `json:"score_rate"`
	KnowledgePoints []string       `json:"knowledge_points"`
	TeacherFeedback []FeedbackItem `json:"teacher_feedback,omitempty"`
	AIFeedback      []FeedbackItem `json:"ai_feedback,omitempty"`
	ErrorClues      []ErrorClue    `json:"error_clues,omitempty"`
}

type StudentReport struct {
	ExamID           string                  `json:"exam_id"`
	StudentID        string                  `json:"student_id"`
	SubmissionID     string                  `json:"submission_id,omitempty"`
	AnonymousCode    string                  `json:"anonymous_code,omitempty"`
	Empty            *EmptyState             `json:"empty,omitempty"`
	TotalScore       float64                 `json:"total_score"`
	MaxScore         float64                 `json:"max_score"`
	ScoreRate        float64                 `json:"score_rate"`
	Questions        []StudentQuestionReport `json:"questions"`
	KnowledgeMastery []KnowledgeMastery      `json:"knowledge_mastery"`
	TeacherFeedback  []FeedbackItem          `json:"teacher_feedback"`
	AIFeedback       []FeedbackItem          `json:"ai_feedback"`
	ErrorClues       []ErrorClue             `json:"error_clues"`
}

type QuestionWeakness struct {
	QuestionID string  `json:"question_id"`
	QuestionNo string  `json:"question_no"`
	WrongCount int     `json:"wrong_count"`
	ScoreRate  float64 `json:"score_rate"`
}

type ClassReport struct {
	ClassID                string             `json:"class_id"`
	ClassName              string             `json:"class_name"`
	StudentCount           int                `json:"student_count"`
	Stats                  ScoreStats         `json:"stats"`
	FrequentWrongQuestions []QuestionWeakness `json:"frequent_wrong_questions"`
	WeakKnowledgePoints    []KnowledgeMastery `json:"weak_knowledge_points"`
}

type ClassComparison struct {
	ClassID       string  `json:"class_id"`
	ClassName     string  `json:"class_name"`
	StudentCount  int     `json:"student_count"`
	Average       float64 `json:"average"`
	Median        float64 `json:"median"`
	PassRate      float64 `json:"pass_rate"`
	ExcellentRate float64 `json:"excellent_rate"`
}

type OverviewReport struct {
	ExamID             string             `json:"exam_id"`
	Empty              *EmptyState        `json:"empty,omitempty"`
	StudentCount       int                `json:"student_count"`
	PublishedCount     int                `json:"published_count"`
	Stats              ScoreStats         `json:"stats"`
	ClassComparisons   []ClassComparison  `json:"class_comparisons"`
	QuestionScoreRates []QuestionAnalysis `json:"question_score_rates"`
}

type OptionCount struct {
	Option string `json:"option"`
	Count  int    `json:"count"`
}

type QuestionAnalysis struct {
	QuestionID         string        `json:"question_id"`
	QuestionNo         string        `json:"question_no"`
	QuestionType       string        `json:"question_type"`
	MaxScore           float64       `json:"max_score"`
	AverageScore       float64       `json:"average_score"`
	ScoreRate          float64       `json:"score_rate"`
	CorrectRate        float64       `json:"correct_rate"`
	Difficulty         float64       `json:"difficulty"`
	Discrimination     float64       `json:"discrimination"`
	KnowledgePoints    []string      `json:"knowledge_points"`
	OptionDistribution []OptionCount `json:"option_distribution,omitempty"`
	OptionEmpty        *EmptyState   `json:"option_empty,omitempty"`
	FrequentErrors     []ErrorClue   `json:"frequent_errors,omitempty"`
}

type GradingQualityReport struct {
	ExamID                   string `json:"exam_id"`
	TotalSegments            int    `json:"total_segments"`
	AIGradeCount             int    `json:"ai_grade_count"`
	AIAcceptedCount          int    `json:"ai_accepted_count"`
	AIAdoptionRate           Metric `json:"ai_adoption_rate"`
	HumanComparableCount     int    `json:"human_comparable_count"`
	HumanModifiedCount       int    `json:"human_modified_count"`
	HumanModificationRate    Metric `json:"human_modification_rate"`
	DoubleMarkSessionCount   int    `json:"double_mark_session_count"`
	AverageDoubleMarkDiff    Metric `json:"average_double_mark_diff"`
	MaxDoubleMarkDiff        Metric `json:"max_double_mark_diff"`
	ArbitrationCount         int    `json:"arbitration_count"`
	OCRTaskCount             int    `json:"ocr_task_count"`
	OCRFailedCount           int    `json:"ocr_failed_count"`
	OCRFailureRate           Metric `json:"ocr_failure_rate"`
	LowConfidenceReviewCount int    `json:"low_confidence_review_count"`
}

type ExportResult struct {
	Filename    string
	ContentType string
	Content     []byte
	RowCount    int
	Watermark   string
	ReportID    string
}

type Store interface {
	RecoverCommand(context.Context, string, string, string) (commandreceipt.Receipt, error)
	StudentReport(ctx context.Context, tenantID string, examID string, studentID string) (StudentReport, error)
	Overview(ctx context.Context, tenantID string, examID string) (OverviewReport, error)
	ClassReports(ctx context.Context, tenantID string, examID string) ([]ClassReport, error)
	QuestionAnalysis(ctx context.Context, tenantID string, examID string) ([]QuestionAnalysis, error)
	GradingQuality(ctx context.Context, tenantID string, examID string) (GradingQualityReport, error)
	Export(ctx context.Context, tenantID string, examID string, actorID string) (ExportResult, error)
}

type submissionRecord struct {
	ID            string
	StudentID     string
	ClassID       string
	ClassName     string
	AnonymousCode string
	TotalScore    float64
	MaxScore      float64
}

type gradeRecord struct {
	SubmissionID    string
	StudentID       string
	ClassID         string
	ClassName       string
	QuestionID      string
	QuestionNo      string
	QuestionType    string
	AnswerSegmentID string
	Score           float64
	MaxScore        float64
	Source          string
	KnowledgePoints []string
	AnswerPayload   map[string]any
	AIScore         *float64
	HumanScore      *float64
	AIFeedback      []FeedbackItem
	TeacherFeedback []FeedbackItem
	ErrorClues      []ErrorClue
}

type dataset struct {
	ExamID      string
	Submissions []submissionRecord
	Grades      []gradeRecord
}
