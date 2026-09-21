package aidisagreement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/gradingdisagreement"

	"github.com/google/uuid"
)

const scoreEpsilon = 1e-9

// Service is intentionally an observation/projection service. Capture,
// classification and routing can never update question_grade, final_grade,
// score-release or a model result.
type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store) *Service {
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }}
}

// Capture loads the two real, tenant-scoped source facts from the Store. It
// returns created=false when there is no material discrepancy. The unique
// source-pair key makes retries and duplicate review submits idempotent.
func (s *Service) Capture(ctx context.Context, tenantID string, input CaptureInput) (item Disagreement, created bool, err error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(input.AICandidateID) == "" || strings.TrimSpace(input.HumanGradeID) == "" {
		return Disagreement{}, false, ErrInvalidInput
	}
	comparison, err := s.store.LoadActualComparison(ctx, tenantID, input)
	if err != nil {
		return Disagreement{}, false, err
	}
	candidate, material := calculate(comparison, s.now().UTC())
	if !material {
		return Disagreement{}, false, nil
	}
	stored, created, err := s.store.Upsert(ctx, tenantID, candidate)
	if err != nil {
		return Disagreement{}, false, err
	}
	return stored, created, nil
}

// ObserveHumanGrade is the narrow review-handler connection point. Pass the
// server-linked HumanGrade.AIGradeID and HumanGrade.ID after a successful
// single-round review commit; do not call it for blank AI links or blind Seed
// tasks. The PostgreSQL store re-checks both records before writing anything.
func (s *Service) ObserveHumanGrade(ctx context.Context, tenantID, aiCandidateID, humanGradeID string) (Disagreement, bool, error) {
	return s.Capture(ctx, tenantID, CaptureInput{AICandidateID: aiCandidateID, HumanGradeID: humanGradeID})
}

func (s *Service) Get(ctx context.Context, tenantID, id string) (Disagreement, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(id) == "" {
		return Disagreement{}, ErrInvalidInput
	}
	return s.store.Get(ctx, tenantID, id)
}

func (s *Service) List(ctx context.Context, tenantID string, filter Filter) ([]Disagreement, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidInput
	}
	if (filter.Status != "" && filter.Status != StatusNeedsReview && filter.Status != StatusClassified && filter.Status != StatusRouted) ||
		(filter.Severity != "" && filter.Severity != SeverityWarning && filter.Severity != SeveritySevere) {
		return nil, ErrInvalidInput
	}
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 50
	}
	return s.store.List(ctx, tenantID, filter)
}

func (s *Service) Classify(ctx context.Context, tenantID, id, reviewerID string, input ClassifyInput) (Disagreement, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(id) == "" || strings.TrimSpace(reviewerID) == "" ||
		!input.Taxonomy.Valid() || input.ExpectedRevision <= 0 || utf8.RuneCountInString(strings.TrimSpace(input.Notes)) > 2000 {
		return Disagreement{}, ErrInvalidInput
	}
	return s.store.Classify(ctx, tenantID, id, reviewerID, input, s.now().UTC())
}

// Route attaches the issue to an existing tenant-scoped human review task.
// It never creates a final grade, changes a current score, or dispatches an
// AI retry. A19 can later consume the classification's follow-up candidate.
func (s *Service) Route(ctx context.Context, tenantID, id, actorID string, input RouteInput) (Disagreement, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(id) == "" || strings.TrimSpace(actorID) == "" ||
		strings.TrimSpace(input.ReviewTaskID) == "" || input.ExpectedRevision <= 0 {
		return Disagreement{}, ErrInvalidInput
	}
	return s.store.Route(ctx, tenantID, id, actorID, input, s.now().UTC())
}

func (s *Service) Dataset(ctx context.Context, tenantID string, filter Filter) ([]DatasetEntry, error) {
	items, err := s.List(ctx, tenantID, filter)
	if err != nil {
		return nil, err
	}
	entries := make([]DatasetEntry, 0, len(items))
	for _, item := range items {
		// Unclassified records are not an evaluation target. The caller cannot
		// accidentally export an unresolved difference as a model error.
		if item.Taxonomy == "" {
			continue
		}
		entries = append(entries, DatasetEntry{
			LineageRef: lineageRef(tenantID, item.ID), QuestionID: item.QuestionID, RiskTier: item.RiskTier,
			AICandidateScore: item.AICandidateScore, HumanScore: item.HumanScore, MaxScore: item.MaxScore,
			Delta: item.Delta, Severity: item.Severity, DifferenceType: item.DifferenceType,
			Taxonomy: item.Taxonomy, TriggerRules: append([]string(nil), item.TriggerRules...),
		})
	}
	return entries, nil
}

// RecommendedFollowUps exposes a human decision aid rather than enacting an
// action. In particular, it does not create regrade jobs or mutate a rubric.
func RecommendedFollowUps(t Taxonomy) []string {
	switch t {
	case TaxonomyAIScoringError:
		return []string{"evaluation_dataset_candidate"}
	case TaxonomyHumanScoringError:
		return []string{"human_review_follow_up"}
	case TaxonomyOCRError:
		return []string{"ocr_correction_candidate", "evaluation_dataset_candidate"}
	case TaxonomyParserError:
		return []string{"parser_correction_candidate", "evaluation_dataset_candidate"}
	case TaxonomyRubricAmbiguity, TaxonomyReferenceAnswerIssue:
		return []string{"rubric_version_proposal", "regrade_candidate"}
	case TaxonomyQuestionIssue:
		return []string{"question_quality_review", "regrade_candidate"}
	case TaxonomyInsufficientEvidence:
		return []string{"human_review_follow_up"}
	default:
		return []string{}
	}
}

func calculate(source ActualComparison, at time.Time) (Disagreement, bool) {
	delta, absDelta, normalized := gradingdisagreement.ScoreDelta(source.AISuggestedScore, source.HumanScore, source.MaxScore)
	evidenceGap := source.AIEvidenceCount == 0
	criterionGap := source.AIMatchedCriterionCount != source.HumanCriterionCount
	// Exact score with otherwise comparable criterion/evidence summaries does
	// not make a useful disagreement item. It would dilute the human queue.
	if absDelta < scoreEpsilon && !evidenceGap && !criterionGap {
		return Disagreement{}, false
	}
	difference := DifferenceScore
	switch {
	case absDelta >= scoreEpsilon && evidenceGap && criterionGap:
		difference = DifferenceCombined
	case absDelta >= scoreEpsilon && evidenceGap:
		difference = DifferenceScoreEvidence
	case absDelta >= scoreEpsilon && criterionGap:
		difference = DifferenceScoreCriterion
	case evidenceGap:
		difference = DifferenceEvidence
	case criterionGap:
		difference = DifferenceCriterion
	}
	rules := []string{}
	if absDelta >= scoreEpsilon {
		rules = append(rules, "score_delta")
	}
	if evidenceGap {
		rules = append(rules, "missing_ai_evidence")
	}
	if criterionGap {
		rules = append(rules, "criterion_count_mismatch")
	}
	if absDelta >= 1 && source.AIConfidence >= .80 {
		rules = append(rules, "high_confidence_contradiction")
	}
	if source.HumanScore > scoreEpsilon && source.HumanScore < source.MaxScore-scoreEpsilon {
		rules = append(rules, "middle_score_diagnostic")
	}
	severity := SeverityWarning
	if source.MaxScore <= 0 {
		normalized = absDelta / math.Max(source.MaxScore, 1)
	}
	// A severe score gap is intentionally conservative. R3 then receives the
	// highest queue priority but a small variation is never labelled severe.
	if normalized >= .40 || (source.RiskTier == "R3" && absDelta >= 1) {
		severity = SeveritySevere
		rules = append(rules, "severe_score_delta")
	}
	if source.RiskTier == "R3" && severity == SeveritySevere {
		rules = append(rules, "r3_priority")
	}
	sort.Strings(rules)
	return Disagreement{
		ID: disagreementID(source.AICandidateID, source.HumanGradeID), ExamID: source.ExamID, QuestionID: source.QuestionID,
		SubmissionID: source.SubmissionID, AnswerSegmentID: source.AnswerSegmentID, AICandidateID: source.AICandidateID,
		HumanGradeID: source.HumanGradeID, AICandidateScore: source.AISuggestedScore, HumanScore: source.HumanScore,
		MaxScore: source.MaxScore, Delta: rounded(delta), AbsoluteDelta: rounded(absDelta), DifferenceType: difference,
		Severity: severity, RiskTier: source.RiskTier, TriggerRules: rules,
		EvidenceSummary: EvidenceSummary{AIEvidenceCount: source.AIEvidenceCount, AIMatchedCriterionCount: source.AIMatchedCriterionCount, HumanCriterionCount: source.HumanCriterionCount},
		Status:          StatusNeedsReview, Revision: 1, CreatedAt: at, UpdatedAt: at,
	}, true
}

func disagreementID(aiID, humanID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(aiID+"\x00"+humanID)).String()
}

func lineageRef(tenantID, id string) string {
	sum := sha256.Sum256([]byte("a17\x00" + tenantID + "\x00" + id))
	return hex.EncodeToString(sum[:16])
}

func rounded(value float64) float64 { return math.Round(value*1e6) / 1e6 }
