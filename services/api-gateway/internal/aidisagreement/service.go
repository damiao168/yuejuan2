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

// Service 仅负责观察与投影。采集、分类和路由都不能更新 question_grade、final_grade、成绩发布或模型结果。
type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store) *Service {
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }}
}

// Capture 从 Store 加载租户范围内的两条真实来源事实。没有实质差异时返回 created=false；唯一的来源配对键使重试和重复复核提交保持幂等。
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

// ObserveHumanGrade 是复核处理器的专用连接点。
// 单轮复核成功提交后，传入服务端关联的 HumanGrade.AIGradeID 和 HumanGrade.ID；AI 关联为空或任务是盲投 Seed 时不要调用。
// PostgreSQL 存储在写入前会重新核验两条记录。
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

// Route 将问题关联到租户范围内已有的人工复核任务。它不会生成最终成绩、修改当前分数或派发 AI 重试；A19 后续可使用分类结果中的跟进候选项。
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
		// 未分类的记录不能作为评测目标，避免调用方误将未解决的差异导出为模型错误。
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

// RecommendedFollowUps 提供人工决策参考，不执行动作；它不会创建重评任务或修改评分细则。
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
	// 共用引擎舍入后的分差可忽略，且评分标准、证据摘要也可比时，不产生分歧项。
	if absDelta < scoreEpsilon && !evidenceGap && !criterionGap {
		return Disagreement{}, false
	}
	difference := comparisonDifferenceType(absDelta, evidenceGap, criterionGap)
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
	// 严重分差的判定刻意保守。R3 获得最高队列优先级，但小幅差异不会被标记为严重。
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

// 多种差异并存时保留原来的分类优先级；证据和评分点缺失仍可独立产生分歧。
func comparisonDifferenceType(absDelta float64, evidenceGap, criterionGap bool) DifferenceType {
	switch {
	case absDelta >= scoreEpsilon && evidenceGap && criterionGap:
		return DifferenceCombined
	case absDelta >= scoreEpsilon && evidenceGap:
		return DifferenceScoreEvidence
	case absDelta >= scoreEpsilon && criterionGap:
		return DifferenceScoreCriterion
	case evidenceGap:
		return DifferenceEvidence
	case criterionGap:
		return DifferenceCriterion
	default:
		return DifferenceScore
	}
}

func disagreementID(aiID, humanID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(aiID+"\x00"+humanID)).String()
}

func lineageRef(tenantID, id string) string {
	sum := sha256.Sum256([]byte("a17\x00" + tenantID + "\x00" + id))
	return hex.EncodeToString(sum[:16])
}

func rounded(value float64) float64 { return math.Round(value*1e6) / 1e6 }
