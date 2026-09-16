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

// AdmissionEvidence is a conservative projection of a completed offline run.
// It deliberately contains no answer, Gold content, or model output.  The
// caller still applies its own tenant policy thresholds before admitting an
// external scoring call.
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

// Complete calculates every metric only from observations that were actually
// recorded for this immutable run. It does not fabricate a missing model
// response or infer a teacher reference from OCR/model output.
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

// ListObservations exposes the already-sanitized, opaque aligned observations
// to evidence consumers such as A15 calibration. It does not expose answer
// text, images, OCR output, or Gold content.
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

// AdmissionEvidenceFor finds the newest completed, non-invalidated offline
// run for the exact model/prompt/rubric and returns metrics calculated only
// from observations in the requested subject/archetype intersection.  It is
// intentionally fail-closed: absence of an aligned run is not an approval.
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
		if item.PageMatchCorrect != nil {
			summary.PageMatchAccuracy.ObservedCount++
			if *item.PageMatchCorrect {
				pageCorrect++
			}
		}
		if item.CropIoU != nil {
			summary.MeanCropIoU.ObservedCount++
			cropTotal += *item.CropIoU
		}
		if item.TranscriptionCER != nil {
			summary.MeanTranscriptionCER.ObservedCount++
			cerTotal += *item.TranscriptionCER
		}
		if item.FormulaExact != nil {
			summary.FormulaExactRate.ObservedCount++
			if *item.FormulaExact {
				formulaCorrect++
			}
		}
		if item.RubricAgreement != nil {
			summary.MeanRubricCriterionAgreement.ObservedCount++
			rubricTotal += *item.RubricAgreement
		}
		if item.NeedsHumanReview {
			reviewed++
		}
		if item.ErrorSource != ErrorNone {
			risky++
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
		if item.ModelScore < scoreEpsilon && item.ReferenceScore > scoreEpsilon {
			falseZero++
		}
		if item.ModelScore >= item.MaxScore-scoreEpsilon && item.ReferenceScore < item.MaxScore-scoreEpsilon {
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
		// The score is intentionally transparent: observed normalized error plus
		// bounded metadata friction. It is a hard-case triage hint, never an
		// estimate of a student's ability or a final production decision.
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

func conservativeQWK(items []Observation) (float64, bool, string) {
	if len(items) < 2 {
		return 0, false, "requires_at_least_two_aligned_observations"
	}
	maxScore := items[0].MaxScore
	if maxScore < 1 || maxScore > 100 || math.Abs(maxScore-math.Round(maxScore)) > scoreEpsilon {
		return 0, false, "requires_single_integer_score_scale"
	}
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
			return 0, false, "requires_single_integer_score_scale"
		}
		observed[int(math.Round(item.ReferenceScore))][int(math.Round(item.ModelScore))]++
	}
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
		// Keep the persisted contract deliberately bounded. An exotic sparse
		// distribution is reported as unavailable rather than silently clipped.
		return 0, false, "outside_conservative_qwk_range"
	}
	return qwk, true, ""
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
