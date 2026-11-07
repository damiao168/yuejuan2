package mathunderstanding

import (
	"strings"
)

func applySymbolicVerifications(contract CreateArtifactInput, checks []MathVerification, correctionRevision int64) (CreateArtifactInput, map[string]any, error) {
	// 先限制已有记录，避免在拒绝过量结果前深拷贝整个历史契约。
	if len(checks) == 0 || len(checks) > 2048 || len(contract.Verifications) > maxArtifactVerificationCount {
		return CreateArtifactInput{}, nil, ErrInvalidInput
	}
	contract = cloneInput(contract)
	retained, knownIDs, criticalConfidence, requiresReview, err := retainSyntaxVerifications(contract, len(checks))
	if err != nil {
		return CreateArtifactInput{}, nil, err
	}
	statusCounts := map[string]int{"verified": 0, "contradicted": 0, "uncertain": 0, "not_applicable": 0}
	activeFormulas := activeSolutionFormulas(contract.SolutionGraph)
	checkedFormulas := map[string]bool{}
	for _, check := range checks {
		if check.Engine != "sympy" || !set("equivalence", "constraint")[check.Kind] || knownIDs[check.ID] || !validSymbolicBinding(contract, check) {
			return CreateArtifactInput{}, nil, ErrInvalidInput
		}
		knownIDs[check.ID] = true
		recordCheckedFormula(check, activeFormulas, checkedFormulas)
		statusCounts[check.Status]++
		criticalConfidence, requiresReview = updateSymbolicConfidence(check, criticalConfidence, requiresReview)
		retained = append(retained, check)
	}
	contract.Verifications = retained
	contract.SolutionGraph.RequiresHumanReview = contract.SolutionGraph.RequiresHumanReview || requiresReview
	if err := ValidateCreateArtifact(contract); err != nil {
		return CreateArtifactInput{}, nil, err
	}
	quality := map[string]any{
		"verification_count": len(checks), "verified_count": statusCounts["verified"],
		"contradicted_count": statusCounts["contradicted"], "uncertain_count": statusCounts["uncertain"],
		"not_applicable_count": statusCounts["not_applicable"], "critical_confidence": criticalConfidence,
		"engine": "sympy", "correction_revision": correctionRevision,
		"formula_count": len(contract.Formulas), "active_formula_count": len(activeFormulas), "checked_formula_count": len(checkedFormulas),
		"unchecked_formula_count": len(activeFormulas) - len(checkedFormulas),
	}
	return contract, quality, nil
}

func retainSyntaxVerifications(contract CreateArtifactInput, incomingCount int) ([]MathVerification, map[string]bool, float64, bool, error) {
	if len(contract.Verifications) > maxArtifactVerificationCount || incomingCount < 0 || incomingCount > 2048 {
		return nil, nil, 0, false, ErrInvalidInput
	}
	syntaxCount := 0
	for _, check := range contract.Verifications {
		if check.Kind == "syntax" {
			syntaxCount++
		}
	}
	// 旧符号结果会被替换；只为保留的语法记录和本轮结果计预算，减法检查在容量相加前执行。
	if incomingCount > maxArtifactVerificationCount-syntaxCount {
		return nil, nil, 0, false, ErrInvalidInput
	}
	retained, knownIDs, criticalConfidence, requiresReview := collectRetainedSyntaxEvidence(contract, syntaxCount+incomingCount)
	return retained, knownIDs, criticalConfidence, requiresReview, nil
}

func activeSolutionFormulas(graph SolutionGraph) map[string]bool {
	active := map[string]bool{}
	for _, step := range graph.Steps {
		for _, id := range step.FormulaIDs {
			active[id] = true
		}
	}
	return active
}

func recordCheckedFormula(check MathVerification, active, checked map[string]bool) {
	if active[check.FormulaID] {
		checked[check.FormulaID] = true
	}
	if fromID, ok := check.Details["from_formula_id"].(string); ok {
		if active[fromID] {
			checked[fromID] = true
		}
	}
}

func updateSymbolicConfidence(check MathVerification, confidence float64, requiresReview bool) (float64, bool) {
	if check.Status == "contradicted" {
		confidence = 0
		requiresReview = true
	} else if check.Status == "uncertain" {
		if check.Confidence < confidence {
			confidence = check.Confidence
		}
		requiresReview = true
	}
	return confidence, requiresReview
}

func validSymbolicBinding(contract CreateArtifactInput, check MathVerification) bool {
	steps := map[string]SolutionStep{}
	for _, step := range contract.SolutionGraph.Steps {
		steps[step.ID] = step
	}
	contains := func(ids []string, id string) bool {
		for _, candidate := range ids {
			if candidate == id {
				return true
			}
		}
		return false
	}
	if check.StepID != "" && !contains(steps[check.StepID].FormulaIDs, check.FormulaID) {
		return false
	}
	if check.Kind != "equivalence" {
		return true
	}
	fromStep, _ := check.Details["from_step_id"].(string)
	fromFormula, _ := check.Details["from_formula_id"].(string)
	if fromStep == "" || fromFormula == "" || check.StepID == "" || check.FormulaID == "" || !contains(steps[fromStep].FormulaIDs, fromFormula) {
		return false
	}
	for _, edge := range contract.SolutionGraph.Edges {
		if edge.Kind == "derives" && edge.FromStepID == fromStep && edge.ToStepID == check.StepID {
			return true
		}
	}
	return false
}

func prepareRuntimeArtifact(input CreateArtifactInput) (CreateArtifactInput, error) {
	relations := input.Relations
	if len(relations) == 0 {
		relations = BuildSpatialRelations(input.Blocks, SpatialGraphOptions{})
	}

	formulaModelVersion := "text-only"
	if len(input.Formulas) > 0 {
		formulaModelVersion = strings.TrimSpace(input.SolutionGraph.FormulaModelVersion)
		for index := 0; formulaModelVersion == "" && index < len(input.Formulas); index++ {
			formulaModelVersion = strings.TrimSpace(input.Formulas[index].RecognitionVersion)
		}
	}

	graph, err := BuildSolutionGraph(SolutionBuildInput{
		AnswerSegmentID:     input.AnswerSegmentID,
		Blocks:              input.Blocks,
		Formulas:            input.Formulas,
		Relations:           relations,
		Verifications:       input.Verifications,
		BuilderVersion:      runtimeSolutionBuilderVersion,
		FormulaModelVersion: formulaModelVersion,
	})
	if err != nil {
		return CreateArtifactInput{}, err
	}
	graph.RequiresHumanReview = graph.RequiresHumanReview || input.SolutionGraph.RequiresHumanReview

	input.Relations = relations
	input.SolutionGraph = graph
	return input, nil
}

// 容量由调用方在分配前检查；收集保留的语法证据同时累积识别疑点，不能由后续符号验证抹掉。
func collectRetainedSyntaxEvidence(contract CreateArtifactInput, capacity int) ([]MathVerification, map[string]bool, float64, bool) {
	retained := make([]MathVerification, 0, capacity)
	knownIDs := map[string]bool{}
	criticalConfidence := contract.SolutionGraph.OverallConfidence
	requiresReview := false
	// 保留识别阶段的语法检查，再替换符号验证结果；后续计算成功不能抹掉原先的识别疑点。
	for _, check := range contract.Verifications {
		if check.Kind == "syntax" {
			retained = append(retained, check)
			knownIDs[check.ID] = true
			if check.Status == "uncertain" || check.Status == "contradicted" {
				criticalConfidence = min(criticalConfidence, check.Confidence)
				if check.Status == "contradicted" {
					criticalConfidence = 0
				}
				requiresReview = true
			}
		}
	}
	return retained, knownIDs, criticalConfidence, requiresReview
}
