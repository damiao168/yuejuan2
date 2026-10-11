package goldpaper

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
)

var (
	ErrNotFound           = errors.New("gold paper not found")
	ErrInvalidInput       = errors.New("invalid gold paper input")
	ErrConflict           = errors.New("gold paper conflict")
	ErrAlreadyApproved    = errors.New("gold paper version already approved")
	ErrApprovedImmutable  = errors.New("approved gold paper version is immutable")
	ErrSourceGradeMissing = errors.New("gold paper source grade is missing")
)

type Status string

const (
	StatusPendingApproval Status = "pending_approval"
	StatusActive          Status = "active"
	StatusRetired         Status = "retired"
)

type Source struct {
	ExamID            string
	QuestionID        string
	SubmissionID      string
	SnapshotID        string
	SubjectCode       string
	ArchetypeCode     string
	RiskTier          string
	MaxScore          float64
	RubricSnapshot    map[string]any
	AvailableGradeIDs []string
	AnswerSegmentID   string
}

type Version struct {
	ID                     string         `json:"id"`
	Version                int            `json:"version"`
	ExamQuestionSnapshotID string         `json:"exam_question_snapshot_id"`
	ReferenceScore         float64        `json:"reference_score"`
	MaxScore               float64        `json:"max_score"`
	RubricSnapshot         map[string]any `json:"rubric_snapshot"`
	Explanation            string         `json:"explanation"`
	TraitScores            map[string]any `json:"trait_scores"`
	ErrorTags              []string       `json:"error_tags"`
	SourceGradeIDs         []string       `json:"source_grade_ids"`
	NominatedBy            string         `json:"nominated_by"`
	ApprovedBy             string         `json:"approved_by,omitempty"`
	ApprovedAt             *time.Time     `json:"approved_at,omitempty"`
	CreatedAt              time.Time      `json:"created_at"`
}

// GoldPaper 不暴露 student_id、candidate_no、学生姓名、复核者私有备注或原始 AI 提示词。
// SubmissionID 是假名化的来源引用，供单独授权的答案图片端点使用。
type GoldPaper struct {
	ID               string     `json:"id"`
	ExamID           string     `json:"exam_id"`
	QuestionID       string     `json:"question_id"`
	SubmissionID     string     `json:"submission_id"`
	AnswerImageURL   string     `json:"answer_image_url,omitempty"`
	ActiveVersion    int        `json:"active_version,omitempty"`
	Status           Status     `json:"status"`
	SubjectCode      string     `json:"subject_code"`
	ArchetypeCode    string     `json:"archetype_code"`
	RiskTier         string     `json:"risk_tier"`
	NominatedBy      string     `json:"nominated_by"`
	RetiredAt        *time.Time `json:"retired_at,omitempty"`
	RetirementReason string     `json:"retirement_reason,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Versions         []Version  `json:"versions"`
}

type NominateInput struct {
	SubmissionID   string         `json:"submission_id"`
	ReferenceScore float64        `json:"reference_score"`
	Explanation    string         `json:"explanation"`
	TraitScores    map[string]any `json:"trait_scores"`
	ErrorTags      []string       `json:"error_tags"`
	SourceGradeIDs []string       `json:"source_grade_ids"`
}

type CreateVersionInput struct {
	ReferenceScore float64        `json:"reference_score"`
	Explanation    string         `json:"explanation"`
	TraitScores    map[string]any `json:"trait_scores"`
	ErrorTags      []string       `json:"error_tags"`
	SourceGradeIDs []string       `json:"source_grade_ids"`
}

type RetireInput struct {
	Reason string `json:"reason"`
}

type ListFilter struct {
	ExamID     string
	QuestionID string
	Status     Status
}

type ScoreBandCoverage struct {
	Band  string `json:"band"`
	Count int    `json:"count"`
}

type Coverage struct {
	ExamID              string              `json:"exam_id"`
	QuestionID          string              `json:"question_id"`
	SubjectCode         string              `json:"subject_code"`
	ArchetypeCode       string              `json:"archetype_code"`
	RiskTier            string              `json:"risk_tier"`
	ActiveApprovedCount int                 `json:"active_approved_count"`
	ScoreBands          []ScoreBandCoverage `json:"score_bands"`
	TraitPatterns       []string            `json:"trait_patterns"`
	ErrorTags           []string            `json:"error_tags"`
	Gaps                []string            `json:"gaps"`
	Ready               bool                `json:"ready"`
}

type Store interface {
	Nominate(context.Context, string, string, string, string, NominateInput) (GoldPaper, error)
	CreateVersion(context.Context, string, string, string, CreateVersionInput) (GoldPaper, error)
	Approve(context.Context, string, string, string, int) (GoldPaper, error)
	Retire(context.Context, string, string, string, string) (GoldPaper, error)
	Get(context.Context, string, string) (GoldPaper, error)
	List(context.Context, string, ListFilter) ([]GoldPaper, error)
	Coverage(context.Context, string, string, string) (Coverage, error)
	ListActiveApproved(context.Context, string, string, string) ([]GoldPaper, error)
}

// ActiveApprovedReader 是校准、种子和 AI 评测功能使用的有限依赖；调用方不得将待批准或已退役版本视为可用 Gold Set。
type ActiveApprovedReader interface {
	ListActiveApproved(context.Context, string, string, string) ([]GoldPaper, error)
}

func normalizeInput(input CreateVersionInput) CreateVersionInput {
	input.Explanation = strings.TrimSpace(input.Explanation)
	if input.TraitScores == nil {
		input.TraitScores = map[string]any{}
	}
	seen := map[string]struct{}{}
	tags := make([]string, 0, len(input.ErrorTags))
	for _, raw := range input.ErrorTags {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	input.ErrorTags = tags
	seen = map[string]struct{}{}
	grades := make([]string, 0, len(input.SourceGradeIDs))
	for _, raw := range input.SourceGradeIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		grades = append(grades, id)
	}
	input.SourceGradeIDs = grades
	return input
}

func validateInput(input CreateVersionInput, maxScore float64) error {
	if math.IsNaN(input.ReferenceScore) || math.IsInf(input.ReferenceScore, 0) ||
		input.ReferenceScore < 0 || input.ReferenceScore > maxScore ||
		input.Explanation == "" || len(input.Explanation) > 8000 ||
		len(input.SourceGradeIDs) == 0 {
		return ErrInvalidInput
	}
	return nil
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func cloneVersion(value Version) Version {
	value.RubricSnapshot = cloneMap(value.RubricSnapshot)
	value.TraitScores = cloneMap(value.TraitScores)
	value.ErrorTags = append([]string(nil), value.ErrorTags...)
	value.SourceGradeIDs = append([]string(nil), value.SourceGradeIDs...)
	return value
}

func cloneGold(value GoldPaper) GoldPaper {
	value.Versions = append([]Version(nil), value.Versions...)
	for index := range value.Versions {
		value.Versions[index] = cloneVersion(value.Versions[index])
	}
	return value
}
