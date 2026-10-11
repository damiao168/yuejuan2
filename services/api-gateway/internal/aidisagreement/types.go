// Package aidisagreement 在真实主观题 AI 建议与同一复核任务产生的人工评分事实不一致时，记录可审计的纯人工跟进。
// 它不会更新成绩，也不会暴露答案、Gold 内容或盲投种子材料。
package aidisagreement

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("AI-human disagreement not found")
	ErrInvalidInput = errors.New("invalid AI-human disagreement input")
	ErrConflict     = errors.New("AI-human disagreement revision conflict")
	ErrInvalidState = errors.New("AI-human disagreement state does not allow this action")
)

type Severity string

const (
	SeverityWarning Severity = "warning"
	SeveritySevere  Severity = "severe"
)

type Status string

const (
	StatusNeedsReview Status = "needs_review"
	StatusClassified  Status = "classified"
	StatusRouted      Status = "routed"
)

type DifferenceType string

const (
	DifferenceScore          DifferenceType = "score"
	DifferenceCriterion      DifferenceType = "criterion"
	DifferenceEvidence       DifferenceType = "evidence"
	DifferenceScoreCriterion DifferenceType = "score_and_criterion"
	DifferenceScoreEvidence  DifferenceType = "score_and_evidence"
	DifferenceCombined       DifferenceType = "combined"
)

type Taxonomy string

const (
	TaxonomyAIScoringError       Taxonomy = "ai_scoring_error"
	TaxonomyHumanScoringError    Taxonomy = "human_scoring_error"
	TaxonomyOCRError             Taxonomy = "ocr_error"
	TaxonomyParserError          Taxonomy = "parser_error"
	TaxonomyRubricAmbiguity      Taxonomy = "rubric_ambiguity"
	TaxonomyReferenceAnswerIssue Taxonomy = "reference_answer_issue"
	TaxonomyQuestionIssue        Taxonomy = "question_issue"
	TaxonomyInsufficientEvidence Taxonomy = "insufficient_evidence"
	TaxonomyAcceptableVariation  Taxonomy = "acceptable_variation"
)

func (t Taxonomy) Valid() bool {
	switch t {
	case TaxonomyAIScoringError, TaxonomyHumanScoringError, TaxonomyOCRError,
		TaxonomyParserError, TaxonomyRubricAmbiguity, TaxonomyReferenceAnswerIssue,
		TaxonomyQuestionIssue, TaxonomyInsufficientEvidence, TaxonomyAcceptableVariation:
		return true
	default:
		return false
	}
}

// ActualComparison 由 Store 从租户范围内的 ai_grade、human_grade 和 review_task 记录派生。
// 只保留聚合信息：证据用数量表示，不包含答案文本、图片 URL 或 Gold。
type ActualComparison struct {
	ExamID                  string
	QuestionID              string
	SubmissionID            string
	AnswerSegmentID         string
	AICandidateID           string
	HumanGradeID            string
	AISuggestedScore        float64
	HumanScore              float64
	MaxScore                float64
	AIConfidence            float64
	AIEvidenceCount         int
	AIMatchedCriterionCount int
	HumanCriterionCount     int
	RiskTier                string
}

type Disagreement struct {
	ID                 string          `json:"id"`
	TenantID           string          `json:"tenant_id,omitempty"`
	ExamID             string          `json:"exam_id"`
	QuestionID         string          `json:"question_id"`
	SubmissionID       string          `json:"submission_id"`
	AnswerSegmentID    string          `json:"answer_segment_id"`
	AICandidateID      string          `json:"ai_candidate_id"`
	HumanGradeID       string          `json:"human_grade_id"`
	AICandidateScore   float64         `json:"ai_candidate_score"`
	HumanScore         float64         `json:"human_score"`
	MaxScore           float64         `json:"max_score"`
	Delta              float64         `json:"delta"`
	AbsoluteDelta      float64         `json:"absolute_delta"`
	DifferenceType     DifferenceType  `json:"difference_type"`
	Severity           Severity        `json:"severity"`
	RiskTier           string          `json:"risk_tier"`
	TriggerRules       []string        `json:"trigger_rules"`
	EvidenceSummary    EvidenceSummary `json:"evidence_summary"`
	Status             Status          `json:"status"`
	Taxonomy           Taxonomy        `json:"taxonomy,omitempty"`
	ReviewerID         string          `json:"reviewer_id,omitempty"`
	ReviewedAt         *time.Time      `json:"reviewed_at,omitempty"`
	Notes              string          `json:"notes,omitempty"`
	RoutedReviewTaskID string          `json:"routed_review_task_id,omitempty"`
	RoutedBy           string          `json:"routed_by,omitempty"`
	Revision           int64           `json:"revision"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type EvidenceSummary struct {
	AIEvidenceCount         int `json:"ai_evidence_count"`
	AIMatchedCriterionCount int `json:"ai_matched_criterion_count"`
	HumanCriterionCount     int `json:"human_criterion_count"`
}

type CaptureInput struct {
	AICandidateID string `json:"ai_candidate_id"`
	HumanGradeID  string `json:"human_grade_id"`
}

type ClassifyInput struct {
	Taxonomy         Taxonomy `json:"taxonomy"`
	Notes            string   `json:"notes,omitempty"`
	ExpectedRevision int64    `json:"expected_revision"`
}

type RouteInput struct {
	ReviewTaskID     string `json:"review_task_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type Filter struct {
	ExamID     string
	QuestionID string
	Status     Status
	Severity   Severity
	Limit      int
}

// DatasetEntry 用于评测导出，已去标识化，不含学生作答、图片、评语、Gold 答案或复核者身份。
// LineageRef 是单向引用，特权服务可在内部关联回来源，数据集使用方不能用它识别学生。
type DatasetEntry struct {
	LineageRef       string         `json:"lineage_ref"`
	QuestionID       string         `json:"question_id"`
	RiskTier         string         `json:"risk_tier"`
	AICandidateScore float64        `json:"ai_candidate_score"`
	HumanScore       float64        `json:"human_score"`
	MaxScore         float64        `json:"max_score"`
	Delta            float64        `json:"delta"`
	Severity         Severity       `json:"severity"`
	DifferenceType   DifferenceType `json:"difference_type"`
	Taxonomy         Taxonomy       `json:"taxonomy"`
	TriggerRules     []string       `json:"trigger_rules"`
}

type Store interface {
	LoadActualComparison(context.Context, string, CaptureInput) (ActualComparison, error)
	Upsert(context.Context, string, Disagreement) (Disagreement, bool, error)
	Get(context.Context, string, string) (Disagreement, error)
	List(context.Context, string, Filter) ([]Disagreement, error)
	Classify(context.Context, string, string, string, ClassifyInput, time.Time) (Disagreement, error)
	Route(context.Context, string, string, string, RouteInput, time.Time) (Disagreement, error)
}
