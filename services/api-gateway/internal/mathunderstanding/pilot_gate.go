package mathunderstanding

type PilotMetrics struct {
	FormulaExactRate        float64 `json:"formula_exact_rate"`
	ASTExactRate            float64 `json:"ast_exact_rate"`
	SpatialRelationF1       float64 `json:"spatial_relation_f1"`
	SolutionGraphEdgeF1     float64 `json:"solution_graph_edge_f1"`
	EquivalencePrecision    float64 `json:"equivalence_precision"`
	RubricEvidencePrecision float64 `json:"rubric_evidence_precision"`
	UnsafeSuggestionRate    float64 `json:"unsafe_suggestion_rate"`
	RiskyCaseRecall         float64 `json:"risky_case_recall"`
	SampleCount             int     `json:"sample_count"`
}

type PilotPolicy struct {
	MinimumSamples              int     `json:"minimum_samples"`
	MinimumFormulaExact         float64 `json:"minimum_formula_exact"`
	MinimumASTExact             float64 `json:"minimum_ast_exact"`
	MinimumSpatialF1            float64 `json:"minimum_spatial_f1"`
	MinimumGraphF1              float64 `json:"minimum_graph_f1"`
	MinimumEquivalencePrecision float64 `json:"minimum_equivalence_precision"`
	MinimumRubricPrecision      float64 `json:"minimum_rubric_precision"`
	MaximumUnsafeSuggestionRate float64 `json:"maximum_unsafe_suggestion_rate"`
	MinimumRiskyCaseRecall      float64 `json:"minimum_risky_case_recall"`
}

type PilotGateDecision struct {
	Passed   bool     `json:"passed"`
	Blockers []string `json:"blockers"`
	Scope    string   `json:"scope"`
}

// EvaluatePilotGate 将提交的指标逐项与阈值比较，返回教师建议范围的评估结果。
// 此函数不启用模型，也不授权写入正式成绩。
func EvaluatePilotGate(subject string, metrics PilotMetrics, policy PilotPolicy) PilotGateDecision {
	decision := PilotGateDecision{Scope: "teacher_suggestion_only", Blockers: []string{}}
	if !set("mathematics", "physics", "chemistry")[subject] {
		decision.Blockers = append(decision.Blockers, "subject_not_enabled")
	}
	if metrics.SampleCount < policy.MinimumSamples {
		decision.Blockers = append(decision.Blockers, "insufficient_samples")
	}
	checks := []struct {
		name            string
		actual, minimum float64
	}{{"formula_exact", metrics.FormulaExactRate, policy.MinimumFormulaExact}, {"ast_exact", metrics.ASTExactRate, policy.MinimumASTExact}, {"spatial_relation", metrics.SpatialRelationF1, policy.MinimumSpatialF1}, {"solution_graph", metrics.SolutionGraphEdgeF1, policy.MinimumGraphF1}, {"equivalence_precision", metrics.EquivalencePrecision, policy.MinimumEquivalencePrecision}, {"rubric_precision", metrics.RubricEvidencePrecision, policy.MinimumRubricPrecision}, {"risky_case_recall", metrics.RiskyCaseRecall, policy.MinimumRiskyCaseRecall}}
	for _, check := range checks {
		if check.actual < check.minimum {
			decision.Blockers = append(decision.Blockers, check.name+"_below_threshold")
		}
	}
	if metrics.UnsafeSuggestionRate > policy.MaximumUnsafeSuggestionRate {
		decision.Blockers = append(decision.Blockers, "unsafe_suggestion_rate_above_threshold")
	}
	decision.Passed = len(decision.Blockers) == 0
	return decision
}
