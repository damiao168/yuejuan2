package answergroup

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
)

var (
	ErrNotFound           = errors.New("answer group not found")
	ErrInvalidInput       = errors.New("invalid answer group input")
	ErrNoEligibleAnswers  = errors.New("no reliably textified answers are eligible for grouping")
	ErrSamplingIncomplete = errors.New("answer group sampling policy is not satisfied")
	ErrRevisionConflict   = errors.New("answer group revision conflict")
	ErrStateConflict      = errors.New("answer group state conflict")
	ErrRollbackConflict   = errors.New("answer group rollback reference mismatch")
)

const (
	ArchetypeExactText        = "exact_text"
	ArchetypeShortConstructed = "short_constructed"

	StatusSampling   = "sampling"
	StatusReady      = "ready_for_confirmation"
	StatusConfirmed  = "confirmed"
	StatusRolledBack = "rolled_back"

	SampleAccepted = "accepted"
	SampleRejected = "rejected"

	CandidateGroupScore       = "group_score"
	CandidateIndividualReview = "individual_review"
	CandidateActive           = "active"
	CandidateManualRequired   = "manual_required"
	CandidateRolledBack       = "rolled_back"
)

// SourceAnswer is an authoritative projection of the latest recorded answer
// and frozen question assessment. API callers cannot submit these facts.
type SourceAnswer struct {
	SubmissionID  string
	SegmentID     string
	SnapshotID    string
	ArchetypeCode string
	AnswerText    string
	Source        string
	Confidence    *float64
}

type Representation struct {
	Normalized string
	Features   map[string]struct{}
	Hash       string
}

// RepresentationProvider is deliberately replaceable. The default provider
// is deterministic text normalization and character shingles; it is not
// described as an embedding model or as evidence of semantic quality.
type RepresentationProvider interface {
	Version() string
	Represent(string) Representation
	Similarity(Representation, Representation) float64
}

type Policy struct {
	MinimumOCRConfidence float64
	ExactTextThreshold   float64
	ShortAnswerThreshold float64
	HighOutlierThreshold float64
	MaximumTextRunes     int
}

func DefaultPolicy() Policy {
	return Policy{
		MinimumOCRConfidence: 0.80,
		ExactTextThreshold:   0.90,
		ShortAnswerThreshold: 0.72,
		HighOutlierThreshold: 0.35,
		MaximumTextRunes:     4000,
	}
}

func (p Policy) valid() bool {
	return p.MinimumOCRConfidence >= 0 && p.MinimumOCRConfidence <= 1 &&
		p.ExactTextThreshold > 0 && p.ExactTextThreshold <= 1 &&
		p.ShortAnswerThreshold > 0 && p.ShortAnswerThreshold <= 1 &&
		p.HighOutlierThreshold > 0 && p.HighOutlierThreshold <= 1 &&
		p.MaximumTextRunes > 0
}

type Member struct {
	SubmissionID       string     `json:"submission_id"`
	SegmentID          string     `json:"segment_id"`
	Similarity         float64    `json:"similarity"`
	OutlierScore       float64    `json:"outlier_score"`
	Representative     bool       `json:"representative"`
	Boundary           bool       `json:"boundary"`
	Outlier            bool       `json:"outlier"`
	SampleStatus       string     `json:"sample_status,omitempty"`
	SampledBy          string     `json:"sampled_by,omitempty"`
	SampledAt          *time.Time `json:"sampled_at,omitempty"`
	RepresentationHash string     `json:"representation_hash"`
}

type Decision struct {
	ID                string         `json:"id"`
	ScoreCandidate    map[string]any `json:"score_candidate"`
	RubricSelection   map[string]any `json:"rubric_selection"`
	SampleSize        int            `json:"sample_size"`
	MinimumSample     int            `json:"minimum_sample"`
	ConfirmedBy       string         `json:"confirmed_by,omitempty"`
	ConfirmedAt       *time.Time     `json:"confirmed_at,omitempty"`
	Revision          int64          `json:"revision"`
	RollbackReference string         `json:"rollback_reference,omitempty"`
	RolledBackBy      string         `json:"rolled_back_by,omitempty"`
	RolledBackAt      *time.Time     `json:"rolled_back_at,omitempty"`
	RollbackReason    string         `json:"rollback_reason,omitempty"`
}

type Group struct {
	ID                         string    `json:"id"`
	TenantID                   string    `json:"tenant_id"`
	ExamID                     string    `json:"exam_id"`
	QuestionID                 string    `json:"question_id"`
	ExamQuestionSnapshotID     string    `json:"exam_question_snapshot_id"`
	AlgorithmVersion           string    `json:"algorithm_version"`
	RepresentationVersion      string    `json:"representation_version"`
	MemberCount                int       `json:"member_count"`
	RepresentativeSubmissionID string    `json:"representative_submission_id"`
	Homogeneity                float64   `json:"homogeneity"`
	Status                     string    `json:"status"`
	MinimumSample              int       `json:"minimum_sample"`
	ReviewedSampleCount        int       `json:"reviewed_sample_count"`
	CanConfirm                 bool      `json:"can_confirm"`
	Members                    []Member  `json:"members"`
	Decision                   *Decision `json:"decision,omitempty"`
	CreatedAt                  time.Time `json:"created_at"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

type Candidate struct {
	ID                string         `json:"id"`
	GroupID           string         `json:"group_id"`
	SubmissionID      string         `json:"submission_id"`
	SegmentID         string         `json:"segment_id"`
	DecisionRevision  int64          `json:"decision_revision"`
	Kind              string         `json:"kind"`
	ScoreCandidate    map[string]any `json:"score_candidate"`
	RubricSelection   map[string]any `json:"rubric_selection"`
	Status            string         `json:"status"`
	AlgorithmVersion  string         `json:"algorithm_version"`
	RollbackReference string         `json:"rollback_reference,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
}

type Metrics struct {
	GroupCount              int      `json:"group_count"`
	MemberCount             int      `json:"member_count"`
	GroupHomogeneity        float64  `json:"group_homogeneity"`
	BatchOverrideRate       float64  `json:"batch_override_rate"`
	HumanActionsSaved       int      `json:"human_actions_saved"`
	PostAuditErrorRate      *float64 `json:"post_audit_error_rate"`
	PostAuditEvidenceStatus string   `json:"post_audit_evidence_status"`
}

// TeacherReferenceCase is deliberately sourced only from an active,
// human-approved Gold Paper. It is not a nearest-neighbour result over raw
// student answers and does not expose source grade or reviewer identities.
type TeacherReferenceCase struct {
	GoldPaperID    string         `json:"gold_paper_id"`
	SubmissionID   string         `json:"submission_id"`
	Version        int            `json:"version"`
	ReferenceScore float64        `json:"reference_score"`
	MaxScore       float64        `json:"max_score"`
	Explanation    string         `json:"explanation"`
	TraitScores    map[string]any `json:"trait_scores"`
	ErrorTags      []string       `json:"error_tags"`
}

type BuildInput struct {
	AlgorithmVersion string `json:"algorithm_version,omitempty"`
}

type SampleReviewInput struct {
	Outcome string `json:"outcome"`
	Notes   string `json:"notes,omitempty"`
}

type DecisionInput struct {
	ScoreCandidate   map[string]any `json:"score_candidate"`
	RubricSelection  map[string]any `json:"rubric_selection"`
	ExpectedRevision int64          `json:"expected_revision"`
}

type ConfirmInput struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

type RollbackInput struct {
	RollbackReference string `json:"rollback_reference"`
	Reason            string `json:"reason"`
}

type Store interface {
	Build(context.Context, string, string, string, string, BuildInput) ([]Group, error)
	List(context.Context, string, string, string) ([]Group, error)
	Get(context.Context, string, string) (Group, error)
	ReviewSample(context.Context, string, string, string, string, SampleReviewInput) (Group, error)
	PutDecision(context.Context, string, string, string, DecisionInput) (Group, error)
	Confirm(context.Context, string, string, string, ConfirmInput) (Group, []Candidate, error)
	Rollback(context.Context, string, string, string, RollbackInput) (Group, []Candidate, error)
	Metrics(context.Context, string, string, string) (Metrics, error)
}

func eligible(answer SourceAnswer, policy Policy) bool {
	if answer.ArchetypeCode != ArchetypeExactText && answer.ArchetypeCode != ArchetypeShortConstructed {
		return false
	}
	text := strings.TrimSpace(answer.AnswerText)
	if text == "" || len([]rune(text)) > policy.MaximumTextRunes {
		return false
	}
	if answer.Source == "ocr_text" && (answer.Confidence == nil || *answer.Confidence < policy.MinimumOCRConfidence) {
		return false
	}
	return answer.Source == "ocr_text" || answer.Source == "manual_entry" || answer.Source == "imported_answer"
}

func minimumSample(memberCount int, homogeneity float64) int {
	if memberCount <= 0 {
		return 0
	}
	if memberCount <= 3 {
		return memberCount
	}
	count := int(math.Ceil(math.Sqrt(float64(memberCount))))
	if homogeneity < 0.88 {
		count++
	}
	if count > memberCount {
		return memberCount
	}
	return count
}

// 采样数量达到最低值还不够：所有离群成员必须复核且不能有拒绝样本，才允许确认。
func refreshReadiness(group *Group) {
	reviewed := 0
	rejected := false
	allRequiredOutliersReviewed := true
	for _, member := range group.Members {
		if member.SampleStatus != "" {
			reviewed++
		}
		if member.SampleStatus == SampleRejected {
			rejected = true
		}
		if member.Outlier && member.SampleStatus == "" {
			allRequiredOutliersReviewed = false
		}
	}
	group.ReviewedSampleCount = reviewed
	group.CanConfirm = group.MemberCount > 1 && reviewed >= group.MinimumSample && allRequiredOutliersReviewed && !rejected && group.Decision != nil && group.Decision.ConfirmedAt == nil
	if group.CanConfirm {
		group.Status = StatusReady
	} else if group.Status != StatusConfirmed && group.Status != StatusRolledBack {
		group.Status = StatusSampling
	}
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

func cloneGroup(value Group) Group {
	value.Members = append([]Member(nil), value.Members...)
	if value.Decision != nil {
		decision := *value.Decision
		decision.ScoreCandidate = cloneMap(decision.ScoreCandidate)
		decision.RubricSelection = cloneMap(decision.RubricSelection)
		value.Decision = &decision
	}
	return value
}

func cloneCandidates(values []Candidate) []Candidate {
	out := make([]Candidate, len(values))
	for index, value := range values {
		value.ScoreCandidate = cloneMap(value.ScoreCandidate)
		value.RubricSelection = cloneMap(value.RubricSelection)
		out[index] = value
	}
	return out
}

func sortGroups(values []Group) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].MemberCount != values[j].MemberCount {
			return values[i].MemberCount > values[j].MemberCount
		}
		return values[i].ID < values[j].ID
	})
}
