package modelcalibration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
)

const epsilon = 1e-9

type Service struct {
	store       Store
	evaluations EvaluationReader
	now         func() time.Time
}

func NewService(store Store, evaluations EvaluationReader) *Service {
	return &Service{store: store, evaluations: evaluations, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Create(ctx context.Context, tenantID, actorID string, input CreateInput) (Calibration, error) {
	if s.store == nil || s.evaluations == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(actorID) == "" || !validCreate(input) {
		return Calibration{}, ErrInvalidInput
	}
	input = normalizeCreate(input)
	run, err := s.evaluations.GetRun(ctx, tenantID, input.EvaluationRunID)
	if err != nil {
		return Calibration{}, mapEvaluationError(err)
	}
	if run.Status != "completed" || run.ModelReference != input.Axis.ModelReference || run.PromptVersion != input.Axis.PromptVersion || run.RubricVersion != input.Axis.RubricVersion {
		return Calibration{}, ErrEvaluationRequired
	}
	return s.store.Create(ctx, tenantID, actorID, input)
}

// AddEvidence accepts a confidence value only after the corresponding opaque
// response is found in a completed A16 run. Correctness and severe-error facts
// are derived from the aligned scores, never supplied by the caller.
func (s *Service) AddEvidence(ctx context.Context, tenantID, calibrationID string, input AddEvidenceInput) (CalibrationEvidence, error) {
	if s.store == nil || s.evaluations == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(calibrationID) == "" || !validEvidenceInput(input) {
		return CalibrationEvidence{}, ErrInvalidInput
	}
	calibration, err := s.store.Get(ctx, tenantID, calibrationID)
	if err != nil {
		return CalibrationEvidence{}, err
	}
	if calibration.Status != StatusDraft {
		return CalibrationEvidence{}, ErrStateConflict
	}
	run, err := s.evaluations.GetRun(ctx, tenantID, calibration.EvaluationRunID)
	if err != nil {
		return CalibrationEvidence{}, mapEvaluationError(err)
	}
	if run.Status != "completed" || !sameProvenance(calibration.Axis, run) {
		return CalibrationEvidence{}, ErrEvaluationRequired
	}
	observations, err := s.evaluations.ListObservations(ctx, tenantID, calibration.EvaluationRunID)
	if err != nil {
		return CalibrationEvidence{}, mapEvaluationError(err)
	}
	var matched *EvaluationObservation
	for index := range observations {
		if observations[index].ResponseKey == strings.TrimSpace(input.ResponseKey) {
			matched = &observations[index]
			break
		}
	}
	if matched == nil || !matchesAxis(calibration.Axis, *matched) {
		return CalibrationEvidence{}, ErrEvidenceUnavailable
	}
	evidence := CalibrationEvidence{
		CalibrationID: calibration.ID, EvaluationRunID: calibration.EvaluationRunID, ResponseKey: matched.ResponseKey,
		RawConfidence: rounded(input.RawConfidence), Correct: math.Abs(matched.ModelScore-matched.ReferenceScore) < epsilon,
		SevereError: severeError(*matched), ScoreBand: matched.ScoreBand, OCRQuality: matched.OCRQuality, ObservedAt: s.now().UTC(),
	}
	return s.store.AddEvidence(ctx, tenantID, calibrationID, evidence)
}

func (s *Service) Complete(ctx context.Context, tenantID, calibrationID string) (Calibration, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(calibrationID) == "" {
		return Calibration{}, ErrInvalidInput
	}
	calibration, err := s.store.Get(ctx, tenantID, calibrationID)
	if err != nil {
		return Calibration{}, err
	}
	if calibration.Status != StatusDraft {
		return Calibration{}, ErrStateConflict
	}
	evidence, err := s.store.ListEvidence(ctx, tenantID, calibrationID)
	if err != nil {
		return Calibration{}, err
	}
	if len(evidence) == 0 {
		return Calibration{}, ErrEvaluationRequired
	}
	// 先根据冻结的证据生成不可变 artifact，再计算哈希；审批引用的是这份完整产物。
	artifact := buildArtifact(calibration.Method, evidence)
	raw, err := json.Marshal(artifact)
	if err != nil {
		return Calibration{}, err
	}
	digest := sha256.Sum256(raw)
	return s.store.Complete(ctx, tenantID, calibrationID, len(evidence), artifact,
		"database://model-calibration/"+calibrationID, hex.EncodeToString(digest[:]), s.now().UTC())
}

// Approval is explicit. Completing a curve does not change model eligibility;
// an authorized operator must review and approve the immutable artifact.
func (s *Service) Approve(ctx context.Context, tenantID, calibrationID, actorID string) (Calibration, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(calibrationID) == "" || strings.TrimSpace(actorID) == "" {
		return Calibration{}, ErrInvalidInput
	}
	return s.store.Approve(ctx, tenantID, calibrationID, strings.TrimSpace(actorID), s.now().UTC())
}

func (s *Service) Invalidate(ctx context.Context, tenantID, calibrationID, actorID, reason string) (Calibration, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(calibrationID) == "" || strings.TrimSpace(actorID) == "" || !safeText(reason, 512) {
		return Calibration{}, ErrInvalidInput
	}
	return s.store.Invalidate(ctx, tenantID, calibrationID, strings.TrimSpace(actorID), strings.TrimSpace(reason), s.now().UTC())
}

func (s *Service) Get(ctx context.Context, tenantID, calibrationID string) (Calibration, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(calibrationID) == "" {
		return Calibration{}, ErrInvalidInput
	}
	return s.store.Get(ctx, tenantID, calibrationID)
}

func (s *Service) List(ctx context.Context, tenantID string, axis Axis, limit int) ([]Calibration, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || !validAxis(axis) {
		return nil, ErrInvalidInput
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.store.List(ctx, tenantID, normalizeAxis(axis), limit)
}

func (s *Service) ListEvidence(ctx context.Context, tenantID, calibrationID string) ([]CalibrationEvidence, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(calibrationID) == "" {
		return nil, ErrInvalidInput
	}
	return s.store.ListEvidence(ctx, tenantID, calibrationID)
}

// Approved returns the narrow, opaque evidence projection consumed by A14.
// It deliberately ignores drafts, completed-but-unapproved artifacts, and an
// artifact whose exact model/prompt/rubric axis does not match the request.
func (s *Service) Approved(ctx context.Context, tenantID string, axis Axis) (ApprovedEvidence, error) {
	if s.store == nil || s.evaluations == nil || strings.TrimSpace(tenantID) == "" || !validAxis(axis) {
		return ApprovedEvidence{}, ErrInvalidInput
	}
	calibration, err := s.store.FindApproved(ctx, tenantID, normalizeAxis(axis))
	if err == ErrNotFound {
		return ApprovedEvidence{Available: false}, nil
	}
	if err != nil {
		return ApprovedEvidence{}, err
	}
	// An A16 run may later be invalidated. Approval never lets a calibration
	// outlive its source evidence, and this check makes the gate fail closed
	// until an operator produces a replacement artifact.
	run, err := s.evaluations.GetRun(ctx, tenantID, calibration.EvaluationRunID)
	if err != nil {
		return ApprovedEvidence{Available: false}, nil
	}
	if run.Status != "completed" || !sameProvenance(axis, run) {
		return ApprovedEvidence{Available: false}, nil
	}
	severeRate := 0.0
	for _, point := range calibration.Artifact.RiskCoverageCurve {
		if math.Abs(point.Threshold) < epsilon {
			severeRate = point.SevereErrorRate
			break
		}
	}
	return ApprovedEvidence{Available: true, CalibrationID: calibration.ID, CalibrationRef: calibration.ArtifactURI,
		CalibrationN: calibration.CalibrationN, EvaluationRunID: calibration.EvaluationRunID, SevereErrorRate: severeRate}, nil
}

// RecordCandidate leaves an immutable trace of raw confidence, calibrated
// confidence and any abstention. It never handles a score, so it cannot be
// mistaken for a grading result.
func (s *Service) RecordCandidate(ctx context.Context, tenantID string, input RecordCandidateInput) (Candidate, error) {
	if s.store == nil || s.evaluations == nil || strings.TrimSpace(tenantID) == "" || !validCandidate(input) {
		return Candidate{}, ErrInvalidInput
	}
	input = normalizeCandidate(input)
	candidate := Candidate{CandidateKey: input.CandidateKey, Axis: input.Axis, RawConfidence: input.RawConfidence, TargetRisk: input.TargetRisk, CreatedAt: s.now().UTC()}
	// 找不到或来源评估已失效时记录 abstain；候选仍可落库供审计，但不会伪造可用置信度。
	calibration, err := s.store.FindApproved(ctx, tenantID, input.Axis)
	if err == ErrNotFound {
		candidate.AbstainReason = "approved_calibration_not_found"
		return s.store.CreateOrGetCandidate(ctx, tenantID, candidate)
	}
	if err != nil {
		return Candidate{}, err
	}
	run, runErr := s.evaluations.GetRun(ctx, tenantID, calibration.EvaluationRunID)
	if runErr != nil || run.Status != "completed" || !sameProvenance(input.Axis, run) {
		candidate.AbstainReason = "approved_calibration_source_invalid"
		return s.store.CreateOrGetCandidate(ctx, tenantID, candidate)
	}
	calibrated := mapConfidence(calibration.Artifact.Bins, input.RawConfidence)
	candidate.CalibratedConfidence, candidate.CalibrationID = &calibrated, calibration.ID
	if input.TargetRisk != nil {
		threshold, found := thresholdForRisk(calibration.Artifact.RiskCoverageCurve, *input.TargetRisk)
		if !found {
			candidate.AbstainReason = "target_risk_not_met_by_calibration"
		} else if calibrated+epsilon < threshold {
			candidate.AbstainReason = "calibrated_confidence_below_target_risk_threshold"
		}
	}
	return s.store.CreateOrGetCandidate(ctx, tenantID, candidate)
}

// auto 会在同一批证据上比较三种确定性方法，选择 Brier 分数更低者；平分时按方法名稳定决策。
func buildArtifact(method Method, evidence []CalibrationEvidence) Artifact {
	methods := []Method{method}
	if method == MethodAuto {
		methods = []Method{MethodIsotonic, MethodLogistic, MethodConformal}
	}
	var best Artifact
	for _, candidate := range methods {
		artifact := artifactFor(candidate, evidence)
		if best.Method == "" || artifact.Metrics.BrierScore < best.Metrics.BrierScore-epsilon ||
			(math.Abs(artifact.Metrics.BrierScore-best.Metrics.BrierScore) < epsilon && string(artifact.Method) < string(best.Method)) {
			best = artifact
		}
	}
	return best
}

func artifactFor(method Method, evidence []CalibrationEvidence) Artifact {
	var bins []Bin
	switch method {
	case MethodLogistic:
		bins = logisticBins(evidence)
	case MethodConformal:
		bins = conformalBins(evidence)
	default:
		bins = isotonicBins(evidence)
	}
	artifact := Artifact{SchemaVersion: 1, Method: method, Bins: bins}
	artifact.Metrics = metricsFor(bins, evidence)
	artifact.RiskCoverageCurve = riskCoverage(bins, evidence)
	return artifact
}

func isotonicBins(items []CalibrationEvidence) []Bin {
	type group struct {
		min, max   float64
		n, correct int
	}
	ordered := append([]CalibrationEvidence(nil), items...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].RawConfidence < ordered[j].RawConfidence })
	groups := make([]group, 0, len(ordered))
	for _, item := range ordered {
		if len(groups) == 0 || math.Abs(groups[len(groups)-1].max-item.RawConfidence) > epsilon {
			groups = append(groups, group{min: item.RawConfidence, max: item.RawConfidence})
		}
		index := len(groups) - 1
		groups[index].n++
		if item.Correct {
			groups[index].correct++
		}
	}
	// Pool-adjacent-violators yields a non-decreasing empirical mapping.
	for index := 0; index+1 < len(groups); {
		left, right := groups[index], groups[index+1]
		if float64(left.correct)/float64(left.n) <= float64(right.correct)/float64(right.n)+epsilon {
			index++
			continue
		}
		groups[index] = group{min: left.min, max: right.max, n: left.n + right.n, correct: left.correct + right.correct}
		groups = append(groups[:index+1], groups[index+2:]...)
		if index > 0 {
			index--
		}
	}
	result := make([]Bin, 0, len(groups))
	for _, group := range groups {
		result = append(result, Bin{MinRawConfidence: rounded(group.min), MaxRawConfidence: rounded(group.max), CalibratedConfidence: rounded(float64(group.correct) / float64(group.n)), SampleCount: group.n, CorrectCount: group.correct})
	}
	return result
}

func logisticBins(items []CalibrationEvidence) []Bin {
	// A small deterministic logistic fit avoids a dependency on a statistical
	// runtime. Inputs are clamped only for logit calculation, never persisted.
	mean := 0.0
	for _, item := range items {
		if item.Correct {
			mean++
		}
	}
	mean /= float64(len(items))
	intercept := math.Log(clamp(mean, .01, .99) / (1 - clamp(mean, .01, .99)))
	slope := 0.0
	for step := 0; step < 800; step++ {
		var gi, gs float64
		for _, item := range items {
			x := logit(item.RawConfidence)
			y := 0.0
			if item.Correct {
				y = 1
			}
			delta := sigmoid(intercept+slope*x) - y
			gi += delta
			gs += delta * x
		}
		rate := .08 / math.Sqrt(float64(step)+1)
		intercept -= rate * gi / float64(len(items))
		slope -= rate * gs / float64(len(items))
	}
	// Preserve exact raw-confidence groups for audit and give each the fitted
	// probability. This makes a later mapping exact and reproducible.
	groups := map[float64]Bin{}
	for _, item := range items {
		bin := groups[item.RawConfidence]
		bin.MinRawConfidence, bin.MaxRawConfidence = item.RawConfidence, item.RawConfidence
		bin.SampleCount++
		if item.Correct {
			bin.CorrectCount++
		}
		groups[item.RawConfidence] = bin
	}
	keys := sortedConfidenceKeys(groups)
	result := make([]Bin, 0, len(keys))
	for _, key := range keys {
		bin := groups[key]
		bin.CalibratedConfidence = rounded(sigmoid(intercept + slope*logit(key)))
		result = append(result, bin)
	}
	return result
}

func conformalBins(items []CalibrationEvidence) []Bin {
	// The Wilson lower bound is a conservative selective-prediction confidence
	// estimate. It is intentionally less optimistic than raw empirical rates.
	type tally struct{ n, correct int }
	groups := [10]tally{}
	for _, item := range items {
		bucket := int(math.Floor(item.RawConfidence * 10))
		if bucket > 9 {
			bucket = 9
		}
		groups[bucket].n++
		if item.Correct {
			groups[bucket].correct++
		}
	}
	result := make([]Bin, 0, 10)
	for bucket, group := range groups {
		if group.n == 0 {
			continue
		}
		min := float64(bucket) / 10
		max := min + .1
		if bucket == 9 {
			max = 1
		}
		result = append(result, Bin{MinRawConfidence: min, MaxRawConfidence: max, CalibratedConfidence: rounded(wilsonLower(group.correct, group.n)), SampleCount: group.n, CorrectCount: group.correct})
	}
	return result
}

func metricsFor(bins []Bin, items []CalibrationEvidence) Metrics {
	metrics := Metrics{SampleCount: len(items)}
	var brier, middleBrier float64
	for _, item := range items {
		confidence := mapConfidence(bins, item.RawConfidence)
		outcome := 0.0
		if item.Correct {
			outcome = 1
		}
		brier += (confidence - outcome) * (confidence - outcome)
		if item.ScoreBand == "partial" {
			metrics.MiddleScoreSampleCount++
			middleBrier += (confidence - outcome) * (confidence - outcome)
		}
	}
	metrics.BrierScore = rounded(brier / float64(len(items)))
	if metrics.MiddleScoreSampleCount > 0 {
		value := rounded(middleBrier / float64(metrics.MiddleScoreSampleCount))
		metrics.MiddleScoreBrierScore = &value
	}
	for _, bin := range bins {
		observed := float64(bin.CorrectCount) / float64(bin.SampleCount)
		metrics.ExpectedCalibrationError += math.Abs(bin.CalibratedConfidence-observed) * float64(bin.SampleCount) / float64(len(items))
	}
	metrics.ExpectedCalibrationError = rounded(metrics.ExpectedCalibrationError)
	return metrics
}

// 风险覆盖曲线只统计达到置信度阈值的样本；没有样本的阈值保持零覆盖，不能被当成低风险证明。
func riskCoverage(bins []Bin, items []CalibrationEvidence) []RiskCoveragePoint {
	points := make([]RiskCoveragePoint, 0, 21)
	for step := 0; step <= 20; step++ {
		threshold := float64(step) / 20
		selected, wrong, severe := 0, 0, 0
		for _, item := range items {
			if mapConfidence(bins, item.RawConfidence)+epsilon < threshold {
				continue
			}
			selected++
			if !item.Correct {
				wrong++
			}
			if item.SevereError {
				severe++
			}
		}
		point := RiskCoveragePoint{Threshold: rounded(threshold), SampleCount: selected, Coverage: rounded(float64(selected) / float64(len(items)))}
		if selected > 0 {
			point.EmpiricalRisk = rounded(float64(wrong) / float64(selected))
			point.SevereErrorRate = rounded(float64(severe) / float64(selected))
		}
		points = append(points, point)
	}
	return points
}

func thresholdForRisk(points []RiskCoveragePoint, target float64) (float64, bool) {
	bestThreshold, bestCoverage, found := 0.0, -1.0, false
	for _, point := range points {
		if point.SampleCount == 0 || point.SevereErrorRate > target+epsilon {
			continue
		}
		if point.Coverage > bestCoverage+epsilon || (math.Abs(point.Coverage-bestCoverage) < epsilon && point.Threshold < bestThreshold) {
			bestThreshold, bestCoverage, found = point.Threshold, point.Coverage, true
		}
	}
	return bestThreshold, found
}

func mapConfidence(bins []Bin, raw float64) float64 {
	if len(bins) == 0 {
		return 0
	}
	for _, bin := range bins {
		if raw+epsilon >= bin.MinRawConfidence && raw-epsilon <= bin.MaxRawConfidence {
			return bin.CalibratedConfidence
		}
	}
	// A value absent from the held-out curve maps to the nearest measured bin.
	// This does not extrapolate an unjustified higher confidence.
	best := bins[0]
	bestDistance := math.Abs(raw - midpoint(best))
	for _, bin := range bins[1:] {
		distance := math.Abs(raw - midpoint(bin))
		if distance < bestDistance-epsilon || (math.Abs(distance-bestDistance) < epsilon && bin.CalibratedConfidence < best.CalibratedConfidence) {
			best, bestDistance = bin, distance
		}
	}
	return best.CalibratedConfidence
}

func validCreate(input CreateInput) bool {
	return validKey(input.Key) && validKey(input.EvaluationRunID) && validAxis(input.Axis) && input.Method.Valid()
}
func validEvidenceInput(input AddEvidenceInput) bool {
	return validKey(input.ResponseKey) && validRate(input.RawConfidence)
}
func validCandidate(input RecordCandidateInput) bool {
	return validKey(input.CandidateKey) && validAxis(input.Axis) && validRate(input.RawConfidence) && (input.TargetRisk == nil || validRate(*input.TargetRisk))
}
func validAxis(axis Axis) bool {
	return safeText(axis.ModelReference, 256) && safeText(axis.PromptVersion, 128) && safeText(axis.RubricVersion, 128) &&
		validKey(axis.Subject) && validKey(axis.Archetype) && validSliceKey(axis.SliceKey)
}
func validSliceKey(value string) bool {
	value = strings.TrimSpace(value)
	if value == "all" {
		return true
	}
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return false
	}
	if parts[0] == "score_band" {
		return parts[1] == "zero" || parts[1] == "partial" || parts[1] == "full"
	}
	if parts[0] == "ocr_quality" {
		return parts[1] == "high" || parts[1] == "medium" || parts[1] == "low" || parts[1] == "unknown"
	}
	return false
}
func normalizeCreate(input CreateInput) CreateInput {
	input.Key = strings.TrimSpace(input.Key)
	input.EvaluationRunID = strings.TrimSpace(input.EvaluationRunID)
	input.Axis = normalizeAxis(input.Axis)
	return input
}
func normalizeCandidate(input RecordCandidateInput) RecordCandidateInput {
	input.CandidateKey = strings.TrimSpace(input.CandidateKey)
	input.Axis = normalizeAxis(input.Axis)
	return input
}
func normalizeAxis(axis Axis) Axis {
	axis.ModelReference = strings.TrimSpace(axis.ModelReference)
	axis.PromptVersion = strings.TrimSpace(axis.PromptVersion)
	axis.RubricVersion = strings.TrimSpace(axis.RubricVersion)
	axis.Subject = strings.TrimSpace(axis.Subject)
	axis.Archetype = strings.TrimSpace(axis.Archetype)
	axis.SliceKey = strings.TrimSpace(axis.SliceKey)
	return axis
}
func sameProvenance(axis Axis, run EvaluationRun) bool {
	return axis.ModelReference == run.ModelReference && axis.PromptVersion == run.PromptVersion && axis.RubricVersion == run.RubricVersion
}
func matchesAxis(axis Axis, observation EvaluationObservation) bool {
	if axis.Subject != observation.Subject || axis.Archetype != observation.Archetype {
		return false
	}
	if axis.SliceKey == "all" {
		return true
	}
	parts := strings.Split(axis.SliceKey, ":")
	return (parts[0] == "score_band" && parts[1] == observation.ScoreBand) || (parts[0] == "ocr_quality" && parts[1] == observation.OCRQuality)
}
func severeError(item EvaluationObservation) bool {
	return math.Abs(item.ModelScore-item.ReferenceScore) >= math.Max(1, item.MaxScore*.4)-epsilon
}
func validKey(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	if first := value[0]; !((first >= 'a' && first <= 'z') || (first >= '0' && first <= '9')) {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func safeText(value string, max int) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= max
}
func validRate(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}
func rounded(value float64) float64 { return math.Round(value*1e6) / 1e6 }
func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
func logit(value float64) float64 {
	value = clamp(value, 1e-5, 1-1e-5)
	return math.Log(value / (1 - value))
}
func sigmoid(value float64) float64 {
	if value >= 0 {
		return 1 / (1 + math.Exp(-value))
	}
	exp := math.Exp(value)
	return exp / (1 + exp)
}
func wilsonLower(correct, n int) float64 {
	if n == 0 {
		return 0
	}
	z := 1.96
	p := float64(correct) / float64(n)
	den := 1 + z*z/float64(n)
	center := p + z*z/(2*float64(n))
	margin := z * math.Sqrt((p*(1-p)+z*z/(4*float64(n)))/float64(n))
	return clamp((center-margin)/den, 0, 1)
}
func midpoint(bin Bin) float64 { return (bin.MinRawConfidence + bin.MaxRawConfidence) / 2 }
func sortedConfidenceKeys(groups map[float64]Bin) []float64 {
	keys := make([]float64, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Float64s(keys)
	return keys
}
func mapEvaluationError(err error) error {
	if err == nil {
		return nil
	}
	return ErrEvaluationRequired
}
