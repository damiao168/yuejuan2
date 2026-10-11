package gradingevaluation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

const scoreEpsilon = 1e-9

type Service struct {
	store Store
	now   func() time.Time
}

// AdmissionEvidence 是已完成离线运行的保守投影，不包含答案、Gold 内容或模型输出；调用方允许外部评分调用前，仍须应用自身租户策略的阈值。
type AdmissionEvidence struct {
	Approved        bool
	SampleCount     int
	SevereErrorRate float64
	EvaluationRef   string
}

func NewService(store Store) *Service {
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) CreateRun(ctx context.Context, tenantID, actorID string, input CreateRunInput) (Run, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || !validRunInput(input) {
		return Run{}, ErrInvalidInput
	}
	return s.store.CreateRun(ctx, tenantID, actorID, normalizeRunInput(input))
}

func (s *Service) AddObservation(ctx context.Context, tenantID, runID string, input AddObservationInput) (Observation, error) {
	input = normalizeObservationInput(input)
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(runID) == "" || !validObservationInput(input) {
		return Observation{}, ErrInvalidInput
	}
	return s.store.AddObservation(ctx, tenantID, runID, input)
}

// Complete 仅根据当前不可变运行实际记录的观察计算各项指标，不伪造缺失模型响应，也不从 OCR 或模型输出推断教师参考。
// 完成时把当前观测数量作为期望值传给存储层，由存储层再次校验，防止计算期间悄悄新增样本。
func (s *Service) Complete(ctx context.Context, tenantID, runID string) (Run, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(runID) == "" {
		return Run{}, ErrInvalidInput
	}
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Status != RunDraft {
		return Run{}, ErrStateConflict
	}
	items, err := s.store.ListObservations(ctx, tenantID, runID)
	if err != nil {
		return Run{}, err
	}
	if len(items) == 0 {
		return Run{}, ErrInvalidInput
	}
	computedAt := s.now().UTC()
	return s.store.ReplaceComputed(ctx, tenantID, runID, len(items), sliceMetrics(runID, items, computedAt), responseDifficulty(runID, items, computedAt), computedAt)
}

// CompletePanel 验证每条去标识化 A/B/C 观察后，关闭仅模型组评测的运行。同一运行不能混用旧版单模型观察和模型组观察，以保留指标含义。
func (s *Service) CompletePanel(ctx context.Context, tenantID, runID string) (Run, PanelMetrics, error) {
	if s == nil || s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(runID) == "" {
		return Run{}, PanelMetrics{}, ErrInvalidInput
	}
	panelStore, ok := s.store.(PanelEvaluationStore)
	if !ok {
		return Run{}, PanelMetrics{}, ErrInvalidInput
	}
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return Run{}, PanelMetrics{}, err
	}
	if run.Status != RunDraft {
		return Run{}, PanelMetrics{}, ErrStateConflict
	}
	legacy, err := s.store.ListObservations(ctx, tenantID, runID)
	if err != nil {
		return Run{}, PanelMetrics{}, err
	}
	items, err := panelStore.ListPanelObservations(ctx, tenantID, runID)
	if err != nil {
		return Run{}, PanelMetrics{}, err
	}
	if len(legacy) != 0 || len(items) == 0 {
		return Run{}, PanelMetrics{}, ErrInvalidInput
	}
	metrics, err := CalculatePanelMetrics(items)
	if err != nil {
		return Run{}, PanelMetrics{}, err
	}
	completed, err := s.store.ReplaceComputed(ctx, tenantID, runID, len(items), nil, nil, s.now().UTC())
	return completed, metrics, err
}

func (s *Service) GetRun(ctx context.Context, tenantID, runID string) (Run, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(runID) == "" {
		return Run{}, ErrInvalidInput
	}
	return s.store.GetRun(ctx, tenantID, runID)
}

func (s *Service) ListRuns(ctx context.Context, tenantID string, filter RunFilter) ([]Run, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidInput
	}
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 50
	}
	return s.store.ListRuns(ctx, tenantID, filter)
}

// ListObservations 为 A15 校准等证据使用方提供已清理的不透明对齐观察，不暴露答案文本、图片、OCR 输出或 Gold 内容。
func (s *Service) ListObservations(ctx context.Context, tenantID, runID string) ([]Observation, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(runID) == "" {
		return nil, ErrInvalidInput
	}
	return s.store.ListObservations(ctx, tenantID, runID)
}

func (s *Service) ListSliceMetrics(ctx context.Context, tenantID, runID string) ([]SliceMetric, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(runID) == "" {
		return nil, ErrInvalidInput
	}
	return s.store.ListSliceMetrics(ctx, tenantID, runID)
}

func (s *Service) ListResponseDifficulty(ctx context.Context, tenantID, runID string) ([]ResponseDifficulty, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(runID) == "" {
		return nil, ErrInvalidInput
	}
	return s.store.ListResponseDifficulty(ctx, tenantID, runID)
}

func (s *Service) QualitySummary(ctx context.Context, tenantID, runID string) (QualitySummary, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(runID) == "" {
		return QualitySummary{}, ErrInvalidInput
	}
	items, err := s.store.ListObservations(ctx, tenantID, runID)
	if err != nil {
		return QualitySummary{}, err
	}
	return calculateQualitySummary(items), nil
}

func (s *Service) Invalidate(ctx context.Context, tenantID, runID, reason string) (Run, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(runID) == "" || strings.TrimSpace(reason) == "" {
		return Run{}, ErrInvalidInput
	}
	return s.store.InvalidateRun(ctx, tenantID, runID, strings.TrimSpace(reason), s.now().UTC())
}

// AdmissionEvidenceFor 查找精确模型、提示词和评分细则对应的最新已完成且未失效离线运行，只根据所请求学科与题型交集中的观察计算指标。
// 找不到对齐运行时拒绝放行，缺失不等于批准。
func (s *Service) AdmissionEvidenceFor(ctx context.Context, tenantID, modelReference, promptVersion, rubricVersion string, subject assessment.SubjectCode, archetype string) (AdmissionEvidence, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(modelReference) == "" || strings.TrimSpace(promptVersion) == "" || strings.TrimSpace(rubricVersion) == "" || !subject.Valid() || !assessment.IsQuestionArchetype(archetype) {
		return AdmissionEvidence{}, ErrInvalidInput
	}
	runs, err := s.store.ListRuns(ctx, tenantID, RunFilter{Limit: 200})
	if err != nil {
		return AdmissionEvidence{}, err
	}
	for _, run := range runs {
		if run.Status != RunCompleted || run.ModelReference != modelReference || run.PromptVersion != promptVersion || run.RubricVersion != rubricVersion {
			continue
		}
		observations, observationErr := s.store.ListObservations(ctx, tenantID, run.ID)
		if observationErr != nil {
			return AdmissionEvidence{}, observationErr
		}
		aligned := make([]Observation, 0, len(observations))
		for _, item := range observations {
			itemSubject, ok := assessment.NormalizeSubjectCode(item.Subject)
			if ok && itemSubject == subject && item.Archetype == archetype {
				aligned = append(aligned, item)
			}
		}
		if len(aligned) == 0 {
			continue
		}
		metrics := calculateMetrics(aligned)
		return AdmissionEvidence{Approved: true, SampleCount: metrics.SampleCount, SevereErrorRate: metrics.SevereErrorRate, EvaluationRef: run.ID}, nil
	}
	return AdmissionEvidence{}, nil
}

func sliceMetrics(runID string, observations []Observation, computedAt time.Time) []SliceMetric {
	dimensions := []struct {
		name  string
		value func(Observation) string
	}{
		{SliceSubject, func(item Observation) string { return item.Subject }},
		{SliceArchetype, func(item Observation) string { return item.Archetype }},
		{SliceScoreBand, func(item Observation) string { return item.ReferenceScoreBand }},
		{SliceOCRQuality, func(item Observation) string { return item.OCRQuality }},
		{SliceAnswerLength, func(item Observation) string { return item.AnswerLength }},
		{SliceRubricComplexity, func(item Observation) string { return item.RubricComplexity }},
		{SliceErrorSource, func(item Observation) string { return string(item.ErrorSource) }},
	}
	result := make([]SliceMetric, 0, len(dimensions)*3)
	for _, dimension := range dimensions {
		groups := map[string][]Observation{}
		for _, item := range observations {
			groups[dimension.value(item)] = append(groups[dimension.value(item)], item)
		}
		values := make([]string, 0, len(groups))
		for value := range groups {
			values = append(values, value)
		}
		sort.Strings(values)
		for _, value := range values {
			result = append(result, SliceMetric{
				ID: metricID(runID, dimension.name, value), RunID: runID, Dimension: dimension.name,
				Value: value, Metrics: calculateMetrics(groups[value]), ComputedAt: computedAt,
			})
		}
	}
	return result
}

func calculateQualitySummary(items []Observation) QualitySummary {
	summary := QualitySummary{SampleCount: len(items), ErrorAttribution: []ErrorAttribution{}}
	var pageCorrect, formulaCorrect, reviewed, risky, riskyReviewed int
	var cropTotal, cerTotal, rubricTotal float64
	attribution := map[ErrorSource]*ErrorAttribution{}
	for _, item := range items {
		observeQualityRate(item.PageMatchCorrect, &summary.PageMatchAccuracy, &pageCorrect)
		observeQualityMean(item.CropIoU, &summary.MeanCropIoU, &cropTotal)
		observeQualityMean(item.TranscriptionCER, &summary.MeanTranscriptionCER, &cerTotal)
		observeQualityRate(item.FormulaExact, &summary.FormulaExactRate, &formulaCorrect)
		observeQualityMean(item.RubricAgreement, &summary.MeanRubricCriterionAgreement, &rubricTotal)
		if item.NeedsHumanReview {
			reviewed++
		}
		// 无错误但需复核的样本只计入总复核率，不计入风险错误召回率。
		if item.ErrorSource != ErrorNone {
			risky++
			recordErrorAttribution(attribution, item)
			if item.NeedsHumanReview {
				riskyReviewed++
			}
		}
	}
	summary.PageMatchAccuracy.Rate = ratioPointer(pageCorrect, summary.PageMatchAccuracy.ObservedCount)
	summary.MeanCropIoU.Mean = meanPointer(cropTotal, summary.MeanCropIoU.ObservedCount)
	summary.MeanTranscriptionCER.Mean = meanPointer(cerTotal, summary.MeanTranscriptionCER.ObservedCount)
	summary.FormulaExactRate.Rate = ratioPointer(formulaCorrect, summary.FormulaExactRate.ObservedCount)
	summary.MeanRubricCriterionAgreement.Mean = meanPointer(rubricTotal, summary.MeanRubricCriterionAgreement.ObservedCount)
	if len(items) > 0 {
		summary.HumanReviewRate = rounded(float64(reviewed) / float64(len(items)))
	}
	if risky > 0 {
		summary.RiskyErrorRoutingRecall = rounded(float64(riskyReviewed) / float64(risky))
	}
	// 来源占比以全量样本为分母，来源召回率只用该来源的错误样本；按来源排序保持输出稳定。
	sources := make([]string, 0, len(attribution))
	for source := range attribution {
		sources = append(sources, string(source))
	}
	sort.Strings(sources)
	for _, rawSource := range sources {
		entry := attribution[ErrorSource(rawSource)]
		if len(items) > 0 {
			entry.Rate = rounded(float64(entry.Count) / float64(len(items)))
		}
		entry.HumanRoutingRecall = rounded(float64(entry.HumanReviewCount) / float64(entry.Count))
		summary.ErrorAttribution = append(summary.ErrorAttribution, *entry)
	}
	return summary
}

// 缺失质量观测不进入分母；实际观察到的 false 和零值仍计入覆盖数量。
func observeQualityRate(value *bool, metric *CoveredRate, correct *int) {
	if value == nil {
		return
	}
	metric.ObservedCount++
	if *value {
		*correct += 1
	}
}

func observeQualityMean(value *float64, metric *CoveredMean, total *float64) {
	if value == nil {
		return
	}
	metric.ObservedCount++
	*total += *value
}

func recordErrorAttribution(attribution map[ErrorSource]*ErrorAttribution, item Observation) {
	entry := attribution[item.ErrorSource]
	if entry == nil {
		entry = &ErrorAttribution{Source: item.ErrorSource}
		attribution[item.ErrorSource] = entry
	}
	entry.Count++
	if severeError(item) {
		entry.SevereErrorCount++
	}
	if item.NeedsHumanReview {
		entry.HumanReviewCount++
	}
}

func ratioPointer(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	value := rounded(float64(numerator) / float64(denominator))
	return &value
}

func meanPointer(total float64, count int) *float64 {
	if count == 0 {
		return nil
	}
	value := rounded(total / float64(count))
	return &value
}

func calculateMetrics(items []Observation) Metrics {
	metrics := Metrics{SampleCount: len(items)}
	if len(items) == 0 {
		metrics.QWKUnavailableReason = "no_observations"
		return metrics
	}
	var exact, withinOne, severe, falseZero, falseFull int
	for _, item := range items {
		diff := math.Abs(item.ModelScore - item.ReferenceScore)
		metrics.MAE += diff
		if diff < scoreEpsilon {
			exact++
		}
		if diff <= 1+scoreEpsilon {
			withinOne++
		}
		if severeError(item) {
			severe++
		}
		if falseZeroError(item) {
			falseZero++
		}
		if falseFullError(item) {
			falseFull++
		}
	}
	n := float64(len(items))
	metrics.MAE = rounded(metrics.MAE / n)
	metrics.ExactRate = rounded(float64(exact) / n)
	metrics.WithinOneRate = rounded(float64(withinOne) / n)
	metrics.SevereErrorRate = rounded(float64(severe) / n)
	metrics.FalseZeroRate = rounded(float64(falseZero) / n)
	metrics.FalseFullRate = rounded(float64(falseFull) / n)
	if qwk, ok, reason := conservativeQWK(items); ok {
		metrics.QWK, metrics.QWKAvailable = &qwk, true
	} else {
		metrics.QWKUnavailableReason = reason
	}
	return metrics
}

func responseDifficulty(runID string, observations []Observation, computedAt time.Time) []ResponseDifficulty {
	result := make([]ResponseDifficulty, 0, len(observations))
	for _, item := range observations {
		normalizedError := math.Min(1, math.Abs(item.ModelScore-item.ReferenceScore)/item.MaxScore)
		// 该分值的计算保持透明：已观察到的归一化误差加上有上限的元数据差异项。它只提示疑难案例分流，不估计学生能力，也不作最终生产决策。
		friction := 0.0
		if item.OCRQuality == "low" {
			friction += .15
		}
		if item.AnswerLength == "long" {
			friction += .05
		}
		if item.RubricComplexity == "high" {
			friction += .10
		}
		score := math.Min(1, normalizedError+friction)
		band := "low"
		if score >= .60 {
			band = "high"
		} else if score >= .25 {
			band = "medium"
		}
		result = append(result, ResponseDifficulty{
			ID: metricID(runID, "response", item.ResponseKey), RunID: runID, ResponseKey: item.ResponseKey,
			ResponseFingerprint: item.ResponseFingerprint, DifficultyScore: rounded(score), DifficultyBand: band,
			NormalizedError: rounded(normalizedError), SevereError: severeError(item), OCRQuality: item.OCRQuality,
			AnswerLength: item.AnswerLength, RubricComplexity: item.RubricComplexity,
			EvidenceNote: "offline_aligned_score_difference", ComputedAt: computedAt,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].DifficultyScore == result[j].DifficultyScore {
			return result[i].ResponseKey < result[j].ResponseKey
		}
		return result[i].DifficultyScore > result[j].DifficultyScore
	})
	return result
}

// QWK 只在量表可对齐且样本足够时计算；量表混用或信息不足返回不可用原因，不猜测指标。
func conservativeQWK(items []Observation) (float64, bool, string) {
	if len(items) < 2 {
		return 0, false, "requires_at_least_two_aligned_observations"
	}
	maxScore := items[0].MaxScore
	if maxScore < 1 || maxScore > 100 || math.Abs(maxScore-math.Round(maxScore)) > scoreEpsilon {
		return 0, false, "requires_single_integer_score_scale"
	}
	observed, aligned := qwkObservedDistribution(items, maxScore)
	if !aligned {
		return 0, false, "requires_single_integer_score_scale"
	}
	maxClass := len(observed) - 1
	n := float64(len(items))
	row, column := make([]float64, maxClass+1), make([]float64, maxClass+1)
	for i := 0; i <= maxClass; i++ {
		for j := 0; j <= maxClass; j++ {
			row[i] += observed[i][j]
			column[j] += observed[i][j]
		}
	}
	var weightedObserved, weightedExpected float64
	denominator := float64(maxClass * maxClass)
	for i := 0; i <= maxClass; i++ {
		for j := 0; j <= maxClass; j++ {
			weight := float64((i-j)*(i-j)) / denominator
			weightedObserved += weight * observed[i][j]
			weightedExpected += weight * row[i] * column[j] / n
		}
	}
	if weightedExpected < scoreEpsilon {
		return 0, false, "undefined_for_constant_score_distribution"
	}
	qwk := rounded(1 - weightedObserved/weightedExpected)
	if qwk < -1 || qwk > 1 {
		// 持久化约定有明确边界；异常稀疏分布报告为不可用，不静默截断。
		return 0, false, "outside_conservative_qwk_range"
	}
	return qwk, true, ""
}

// 外层已校验整数满分；每条观察仍须对齐量表、整数得分和范围，不能用取整掩盖不对齐的样本。
func qwkObservedDistribution(items []Observation, maxScore float64) ([][]float64, bool) {
	maxClass := int(math.Round(maxScore))
	observed := make([][]float64, maxClass+1)
	for i := range observed {
		observed[i] = make([]float64, maxClass+1)
	}
	for _, item := range items {
		if math.Abs(item.MaxScore-maxScore) > scoreEpsilon ||
			math.Abs(item.ReferenceScore-math.Round(item.ReferenceScore)) > scoreEpsilon ||
			math.Abs(item.ModelScore-math.Round(item.ModelScore)) > scoreEpsilon ||
			item.ReferenceScore < 0 || item.ReferenceScore > maxScore || item.ModelScore < 0 || item.ModelScore > maxScore {
			return nil, false
		}
		observed[int(math.Round(item.ReferenceScore))][int(math.Round(item.ModelScore))]++
	}
	return observed, true
}

func scoreBand(reference, max float64) string {
	if reference < scoreEpsilon {
		return "zero"
	}
	if reference >= max-scoreEpsilon {
		return "full"
	}
	return "partial"
}

func severeError(item Observation) bool {
	return math.Abs(item.ModelScore-item.ReferenceScore) >= math.Max(1, item.MaxScore*.4)-scoreEpsilon
}

// 假零分和假满分沿用评分误差容差；参考分位于相同端点容差内时不记作端点误判。
func falseZeroError(item Observation) bool {
	return item.ModelScore < scoreEpsilon && item.ReferenceScore > scoreEpsilon
}

func falseFullError(item Observation) bool {
	return item.ModelScore >= item.MaxScore-scoreEpsilon && item.ReferenceScore < item.MaxScore-scoreEpsilon
}

func rounded(value float64) float64 { return math.Round(value*1e6) / 1e6 }

func metricID(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:16])
}

func validRunInput(input CreateRunInput) bool {
	return validKey(input.Key) && safeText(input.DisplayName, 128) && safeText(input.ModelReference, 256) &&
		safeText(input.PromptVersion, 128) && safeText(input.RubricVersion, 128) && validKey(input.DatasetReference) && validSHA(input.DatasetSHA256)
}

func validObservationInput(input AddObservationInput) bool {
	if !validKey(input.ResponseKey) || !validSHA(input.ResponseFingerprint) ||
		(input.ReferenceKind != ReferenceGold && input.ReferenceKind != ReferenceHumanAdjudicated) ||
		!validSliceValue(input.Subject) || !validSliceValue(input.Archetype) ||
		(input.OCRQuality != "high" && input.OCRQuality != "medium" && input.OCRQuality != "low" && input.OCRQuality != "unknown") ||
		(input.AnswerLength != "short" && input.AnswerLength != "medium" && input.AnswerLength != "long" && input.AnswerLength != "unknown") ||
		(input.RubricComplexity != "low" && input.RubricComplexity != "medium" && input.RubricComplexity != "high" && input.RubricComplexity != "unknown") ||
		math.IsNaN(input.ReferenceScore) || math.IsNaN(input.ModelScore) || math.IsNaN(input.MaxScore) ||
		math.IsInf(input.ReferenceScore, 0) || math.IsInf(input.ModelScore, 0) || math.IsInf(input.MaxScore, 0) ||
		input.MaxScore <= 0 || input.ReferenceScore < 0 || input.ModelScore < 0 || input.ReferenceScore > input.MaxScore || input.ModelScore > input.MaxScore ||
		!validOptionalRate(input.CropIoU) || !validOptionalRate(input.TranscriptionCER) || !validOptionalRate(input.RubricAgreement) ||
		!validErrorSource(input.ErrorSource) || input.ReferenceReviewers < 0 ||
		(input.ReferenceKind == ReferenceHumanAdjudicated && (input.ReferenceReviewers < 2 || !input.ReferenceAdjudicated)) {
		return false
	}
	return true
}

func normalizeRunInput(input CreateRunInput) CreateRunInput {
	input.Key, input.DisplayName, input.ModelReference = strings.TrimSpace(input.Key), strings.TrimSpace(input.DisplayName), strings.TrimSpace(input.ModelReference)
	input.PromptVersion, input.RubricVersion = strings.TrimSpace(input.PromptVersion), strings.TrimSpace(input.RubricVersion)
	input.DatasetReference, input.DatasetSHA256 = strings.TrimSpace(input.DatasetReference), strings.ToLower(strings.TrimSpace(input.DatasetSHA256))
	return input
}

func normalizeObservationInput(input AddObservationInput) AddObservationInput {
	input.ResponseKey, input.ResponseFingerprint = strings.TrimSpace(input.ResponseKey), strings.ToLower(strings.TrimSpace(input.ResponseFingerprint))
	input.Subject, input.Archetype = strings.TrimSpace(input.Subject), strings.TrimSpace(input.Archetype)
	if input.ReferenceReviewers == 0 {
		input.ReferenceReviewers = 1
	}
	if input.ErrorSource == "" {
		if math.Abs(input.ModelScore-input.ReferenceScore) < scoreEpsilon {
			input.ErrorSource = ErrorNone
		} else {
			input.ErrorSource = ErrorUnattributed
		}
	}
	return input
}

func validOptionalRate(value *float64) bool {
	return value == nil || (!math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= 1)
}

func validErrorSource(value ErrorSource) bool {
	switch value {
	case "", ErrorNone, ErrorImageQuality, ErrorPageMatching, ErrorAnswerCrop, ErrorHandwritingOCR, ErrorFormulaRecognition,
		ErrorAnswerStructuring, ErrorRubric, ErrorModelScoring, ErrorScoreCalculation, ErrorSystem, ErrorUnattributed:
		return true
	default:
		return false
	}
}

func validKey(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for i, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '.' || char == '_' || char == '-') || (i == 0 && !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9')) {
			return false
		}
	}
	return true
}

func safeText(value string, max int) bool {
	length := utf8.RuneCountInString(strings.TrimSpace(value))
	return length > 0 && length <= max
}
func validSliceValue(value string) bool { return validKey(value) }
func validSHA(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'f' || char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}
