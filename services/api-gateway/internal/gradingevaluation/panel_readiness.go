package gradingevaluation

import (
	"math"
	"sort"
	"strings"
)

var seniorPanelSubjects = []string{
	"biology", "chemistry", "chinese", "english", "ethics_politics",
	"geography", "history", "mathematics", "physics",
}

// PanelReadinessPolicy is supplied by an approved, versioned shadow policy.
// This package intentionally has no permissive defaults: missing empirical
// thresholds make readiness invalid rather than silently enabling a panel.
type PanelReadinessPolicy struct {
	PolicyVersion               string  `json:"policy_version"`
	MinSamplesPerSlice          int     `json:"min_samples_per_slice"`
	MinArbitrationsPerSlice     int     `json:"min_arbitrations_per_slice"`
	MaxFalseConsensusRate       float64 `json:"max_false_consensus_rate"`
	MaxArbitrationErrorRate     float64 `json:"max_arbitration_error_rate"`
	MaxResolvedMAE              float64 `json:"max_resolved_mae"`
	MinResolvedQWK              float64 `json:"min_resolved_qwk"`
	MinArbiterMAEImprovement    float64 `json:"min_arbiter_mae_improvement"`
	MinResolvedCoverageRate     float64 `json:"min_resolved_coverage_rate"`
	MaxHumanEscalationRate      float64 `json:"max_human_escalation_rate"`
	MaxArbitrationTriggerRate   float64 `json:"max_arbitration_trigger_rate"`
	MaxCostPer1000AnswersMicros int64   `json:"max_cost_per_1000_answers_micros"`
}

type PanelSliceReadiness struct {
	PolicyVersion  string       `json:"policy_version"`
	EducationStage string       `json:"education_stage"`
	Subject        string       `json:"subject"`
	Archetype      string       `json:"archetype"`
	Ready          bool         `json:"ready"`
	Reasons        []string     `json:"reasons"`
	Metrics        PanelMetrics `json:"metrics"`
}

type SeniorPanelReadinessReport struct {
	PolicyVersion   string                `json:"policy_version"`
	Ready           bool                  `json:"ready"`
	MissingSubjects []string              `json:"missing_subjects"`
	Slices          []PanelSliceReadiness `json:"slices"`
}

// Senior 面板必须覆盖九个学科，并同时满足版本化策略的样本、准确率、人工升级和成本阈值。
func AssessSeniorNineSubjectShadowReadiness(items []PanelObservation, policy PanelReadinessPolicy) (SeniorPanelReadinessReport, error) {
	if ValidatePanelReadinessPolicy(policy) != nil {
		return SeniorPanelReadinessReport{}, ErrInvalidInput
	}
	for _, item := range items {
		if item.EducationStage != "senior" {
			return SeniorPanelReadinessReport{}, ErrInvalidInput
		}
	}
	slices, err := CalculatePanelMetricsBySlice(items)
	if err != nil {
		return SeniorPanelReadinessReport{}, err
	}
	report := SeniorPanelReadinessReport{PolicyVersion: strings.TrimSpace(policy.PolicyVersion), Ready: true}
	covered := map[string]bool{}
	for _, slice := range slices {
		covered[slice.Subject] = true
		reasons := panelSliceReadinessReasons(slice.Metrics, policy)
		entry := PanelSliceReadiness{PolicyVersion: policy.PolicyVersion, EducationStage: "senior", Subject: slice.Subject, Archetype: slice.Archetype, Ready: len(reasons) == 0, Reasons: reasons, Metrics: slice.Metrics}
		report.Slices = append(report.Slices, entry)
		if !entry.Ready {
			report.Ready = false
		}
	}
	for _, subject := range seniorPanelSubjects {
		if !covered[subject] {
			report.MissingSubjects = append(report.MissingSubjects, subject)
		}
	}
	if len(report.MissingSubjects) > 0 || len(report.Slices) == 0 {
		report.Ready = false
	}
	return report, nil
}

func AssessPanelSliceShadowReadiness(educationStage, subject, archetype string, items []PanelObservation, policy PanelReadinessPolicy) (PanelSliceReadiness, error) {
	educationStage, subject, archetype = strings.TrimSpace(educationStage), strings.TrimSpace(subject), strings.TrimSpace(archetype)
	if ValidatePanelReadinessPolicy(policy) != nil || (educationStage != "junior" && educationStage != "senior") || !panelSubjectCodes[subject] || archetype == "" || len(items) == 0 {
		return PanelSliceReadiness{}, ErrInvalidInput
	}
	for _, item := range items {
		if item.EducationStage != educationStage || item.Subject != subject || item.Archetype != archetype {
			return PanelSliceReadiness{}, ErrInvalidInput
		}
	}
	metrics, err := CalculatePanelMetrics(items)
	if err != nil {
		return PanelSliceReadiness{}, err
	}
	reasons := panelSliceReadinessReasons(metrics, policy)
	return PanelSliceReadiness{PolicyVersion: policy.PolicyVersion, EducationStage: educationStage, Subject: subject, Archetype: archetype, Ready: len(reasons) == 0, Reasons: reasons, Metrics: metrics}, nil
}

// 每个失败阈值都保留原因码；缺数据或 QWK 不可用时保持未就绪，不能用默认值放行。
func panelSliceReadinessReasons(metrics PanelMetrics, policy PanelReadinessPolicy) []string {
	reasons := []string{}
	if metrics.SampleCount < policy.MinSamplesPerSlice {
		reasons = append(reasons, "insufficient_samples")
	}
	if metrics.Arbiter.SampleCount < policy.MinArbitrationsPerSlice {
		reasons = append(reasons, "insufficient_arbitrations")
	}
	if metrics.FalseConsensusRate > policy.MaxFalseConsensusRate {
		reasons = append(reasons, "false_consensus_rate_exceeded")
	}
	if metrics.ArbitrationErrorRate > policy.MaxArbitrationErrorRate {
		reasons = append(reasons, "arbitration_error_rate_exceeded")
	}
	if metrics.Resolved.MAE > policy.MaxResolvedMAE {
		reasons = append(reasons, "resolved_mae_exceeded")
	}
	if !metrics.Resolved.QWKAvailable || metrics.Resolved.QWK == nil {
		reasons = append(reasons, "resolved_qwk_unavailable")
	} else if *metrics.Resolved.QWK < policy.MinResolvedQWK {
		reasons = append(reasons, "resolved_qwk_below_threshold")
	}
	if metrics.Arbiter.SampleCount >= policy.MinArbitrationsPerSlice {
		bestPrimaryMAE := math.Min(metrics.ArbitratedPrimaryA.MAE, metrics.ArbitratedPrimaryB.MAE)
		if bestPrimaryMAE-metrics.Arbiter.MAE < policy.MinArbiterMAEImprovement {
			reasons = append(reasons, "arbiter_mae_improvement_below_threshold")
		}
	}
	resolvedCoverage := 0.0
	if metrics.SampleCount > 0 {
		resolvedCoverage = float64(metrics.Resolved.SampleCount) / float64(metrics.SampleCount)
	}
	if resolvedCoverage < policy.MinResolvedCoverageRate {
		reasons = append(reasons, "resolved_coverage_below_threshold")
	}
	if metrics.HumanEscalationRate > policy.MaxHumanEscalationRate {
		reasons = append(reasons, "human_escalation_rate_exceeded")
	}
	if metrics.ArbitrationTriggerRate > policy.MaxArbitrationTriggerRate {
		reasons = append(reasons, "arbitration_trigger_rate_exceeded")
	}
	if metrics.CostPer1000AnswersMicros > policy.MaxCostPer1000AnswersMicros {
		reasons = append(reasons, "cost_per_1000_exceeded")
	}
	sort.Strings(reasons)
	return reasons
}

func ValidatePanelReadinessPolicy(policy PanelReadinessPolicy) error {
	rate := func(value float64) bool {
		return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
	}
	valid := strings.TrimSpace(policy.PolicyVersion) != "" && policy.MinSamplesPerSlice > 0 && policy.MinArbitrationsPerSlice > 0 &&
		rate(policy.MaxFalseConsensusRate) && rate(policy.MaxArbitrationErrorRate) &&
		!math.IsNaN(policy.MaxResolvedMAE) && !math.IsInf(policy.MaxResolvedMAE, 0) && policy.MaxResolvedMAE >= 0 &&
		!math.IsNaN(policy.MinResolvedQWK) && !math.IsInf(policy.MinResolvedQWK, 0) && policy.MinResolvedQWK >= -1 && policy.MinResolvedQWK <= 1 &&
		!math.IsNaN(policy.MinArbiterMAEImprovement) && !math.IsInf(policy.MinArbiterMAEImprovement, 0) && policy.MinArbiterMAEImprovement >= 0 &&
		rate(policy.MinResolvedCoverageRate) && rate(policy.MaxHumanEscalationRate) && rate(policy.MaxArbitrationTriggerRate) &&
		policy.MaxCostPer1000AnswersMicros >= 0
	if !valid {
		return ErrInvalidInput
	}
	return nil
}
