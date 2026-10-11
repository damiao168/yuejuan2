package mathunderstanding

import (
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"fmt"
	"slices"
	"strings"
)

type EvidenceRequirement struct {
	Type      string                `json:"type"`
	Criterion string                `json:"criterion,omitempty"`
	Target    string                `json:"target,omitempty"`
	Minimum   int                   `json:"minimum,omitempty"`
	Children  []EvidenceRequirement `json:"children,omitempty"`
}

type EvidenceMatchInput struct {
	Requirements  []EvidenceRequirement
	Graph         SolutionGraph
	Blocks        []MathAnswerBlock
	Formulas      []FormulaArtifact
	Verifications []MathVerification
	// 旧版未锚定标签不是权威证据。
	Concepts []string
	Units    []string
	Domains  []string
}

func RequirementsFromRubric(points []paper.RubricPoint) []EvidenceRequirement {
	out := []EvidenceRequirement{}
	for _, point := range points {
		children := []EvidenceRequirement{}
		for _, r := range point.EvidenceRequirements {
			children = append(children, evidenceRequirementFromPaper(point.ID, r))
		}
		// 同一得分点的多个要求是合取关系，不是分别给分。
		out = append(out, EvidenceRequirement{Type: "all_of", Criterion: point.ID, Children: children})
	}
	return out
}
func evidenceRequirementFromPaper(criterion string, r paper.EvidenceRequirement) EvidenceRequirement {
	out := EvidenceRequirement{Type: r.Type, Criterion: criterion, Target: r.Target, Minimum: r.Minimum}
	for _, child := range r.Children {
		out.Children = append(out.Children, evidenceRequirementFromPaper(criterion, child))
	}
	return out
}

type evidenceMatch struct {
	status        string
	sources       []string
	verifications []string
	reason        string
	source        string
}

func unresolved(reason string) evidenceMatch {
	return evidenceMatch{status: "uncertain", reason: reason, source: "rule"}
}

// MatchRubricEvidence 根据量规查找可追溯到具体步骤、公式的证据。
// 找不到证据或只有未核实的语义标签时返回 uncertain，不能把“未确认”直接记成零分。
func MatchRubricEvidence(input EvidenceMatchInput) ([]RubricEvidence, error) {
	out := make([]RubricEvidence, 0, len(input.Requirements))
	for index, r := range input.Requirements {
		if r.Criterion == "" || !validMatchRequirement(r, 0) {
			return nil, ErrInvalidInput
		}
		m := matchRequirement(r, input)
		confidence := matchConfidence(m, input)
		out = append(out, RubricEvidence{ID: fmt.Sprintf("rubric-evidence-%d", index+1), RubricCriterionKey: r.Criterion, EvidenceType: r.Type,
			SourceArtifactIDs: uniqueIDs(m.sources), VerificationIDs: uniqueIDs(m.verifications), Status: m.status, Explanation: m.reason, Confidence: confidence})
	}
	return out, nil
}
func validMatchRequirement(r EvidenceRequirement, depth int) bool {
	// 评分器在现有深度为 8 的 DSL 外增加合取包装。
	if depth > 9 {
		return false
	}
	if set("all_of", "any_of", "at_least")[r.Type] {
		// 空 all_of 表示没有证据 DSL 的旧版得分点。
		if len(r.Children) > 32 || (len(r.Children) == 0 && r.Type != "all_of") || (r.Type == "at_least" && (r.Minimum < 1 || r.Minimum > len(r.Children))) {
			return false
		}
		for _, child := range r.Children {
			if !validMatchRequirement(child, depth+1) {
				return false
			}
		}
		return true
	}
	return set("valid_transformation", "final_result", "concept", "unit", "domain")[r.Type] && len(r.Children) == 0
}
func matchRequirement(r EvidenceRequirement, input EvidenceMatchInput) evidenceMatch {
	if set("all_of", "any_of", "at_least")[r.Type] {
		return matchCompositeRequirement(r, input)
	}
	if r.Type == "concept" || r.Type == "unit" || r.Type == "domain" {
		return unresolved("semantic_evidence_requires_review")
	}
	if strings.TrimSpace(r.Target) == "" {
		return unresolved("rubric_target_unbound")
	}
	matches := []evidenceMatch{}
	for _, step := range input.Graph.Steps {
		matches = append(matches, matchStepRequirement(r, step, input)...)
	}
	return mergeTargetEvidence(matches)
}

func matchCompositeRequirement(r EvidenceRequirement, input EvidenceMatchInput) evidenceMatch {
	if len(r.Children) == 0 {
		return unresolved("evidence_requirements_missing")
	}
	// 单子要求直接沿用叶子结果，保留它的解释和证据来源。
	if len(r.Children) == 1 && (r.Type != "at_least" || r.Minimum == 1) {
		return matchRequirement(r.Children[0], input)
	}
	matches := []evidenceMatch{}
	supported, unknown := 0, 0
	for _, child := range r.Children {
		m := matchRequirement(child, input)
		matches = append(matches, m)
		if m.status == "supported" {
			supported++
		} else if m.status != "contradicted" {
			unknown++
		}
	}
	needed := requiredEvidenceCount(r)
	status := "contradicted"
	if supported >= needed {
		status = "supported"
	} else if supported+unknown >= needed {
		status = "uncertain"
	}
	return combineRequirementEvidence(status, matches)
}

func requiredEvidenceCount(r EvidenceRequirement) int {
	switch r.Type {
	case "any_of":
		return 1
	case "at_least":
		return r.Minimum
	default:
		return len(r.Children)
	}
}

func combineRequirementEvidence(status string, matches []evidenceMatch) evidenceMatch {
	out := evidenceMatch{status: status, reason: "requirements_" + status, source: "rule"}
	// 确定支持或反驳时只保留同类证据；未确定时保留全部子要求供人工复核。
	for _, m := range matches {
		if status == "supported" && m.status != "supported" {
			continue
		}
		if status == "contradicted" && m.status != "contradicted" {
			continue
		}
		out.sources = append(out.sources, m.sources...)
		out.verifications = append(out.verifications, m.verifications...)
	}
	return out
}

func matchStepRequirement(r EvidenceRequirement, step SolutionStep, input EvidenceMatchInput) []evidenceMatch {
	matches := []evidenceMatch{}
	// 按步骤内公式引用、再按原公式数组查找，重复引用和同ID公式仍分别保留。
	for _, formulaID := range step.FormulaIDs {
		for _, formula := range input.Formulas {
			if formula.ID != formulaID || strings.TrimSpace(formula.CanonicalLatex) != strings.TrimSpace(r.Target) {
				continue
			}
			matches = append(matches, matchFormulaRequirement(r, step, formula, input)...)
		}
	}
	return matches
}

func matchFormulaRequirement(r EvidenceRequirement, step SolutionStep, formula FormulaArtifact, input EvidenceMatchInput) []evidenceMatch {
	if !reliableFormula(step, formula, input) {
		m := unresolved("recognition_or_structure_uncertain")
		m.sources = []string{step.ID, formula.ID}
		return []evidenceMatch{m}
	}
	if r.Type == "final_result" {
		// 这里只接受结论与冻结目标完全相等；解出学生自己的方程不能证明最终答案。
		if step.Kind == "conclusion" {
			return []evidenceMatch{{status: "supported", sources: []string{step.ID, formula.ID}, reason: "frozen_result_exact_match", source: "rule"}}
		}
		return nil
	}
	matches := []evidenceMatch{}
	for _, check := range input.Verifications {
		if check.Kind != "equivalence" || check.Engine != "sympy" || check.StepID != step.ID || check.FormulaID != formula.ID || !boundDerivation(check, input.Graph) {
			continue
		}
		matches = append(matches, matchEquivalenceEvidence(step, formula, check, input))
	}
	return matches
}

func matchEquivalenceEvidence(step SolutionStep, formula FormulaArtifact, check MathVerification, input EvidenceMatchInput) evidenceMatch {
	from, _ := check.Details["from_step_id"].(string)
	fromFormula, _ := check.Details["from_formula_id"].(string)
	m := evidenceMatch{status: "uncertain", sources: []string{from, fromFormula, step.ID, formula.ID}, verifications: []string{check.ID}, reason: check.ReasonCode, source: "symbolic"}
	if !reliableSource(from, fromFormula, input) {
		m.reason, m.source = "source_step_uncertain", "rule"
		return m
	}
	if dependentStepRequiresReview(from, input) {
		m.reason, m.source = "dependent_step_requires_review", "rule"
		return m
	}
	if check.Domain != "real" || len(check.Constraints) > 0 {
		m.reason = "verification_scope_requires_review"
	} else if check.Confidence >= .8 && check.Status == "verified" {
		m.status = "supported"
	} else if check.Confidence >= .8 && check.Status == "contradicted" {
		m.status = "contradicted"
	}
	return m
}

func mergeTargetEvidence(matches []evidenceMatch) evidenceMatch {
	if len(matches) == 0 {
		return unresolved("target_evidence_missing")
	}
	out := matches[0]
	for _, m := range matches[1:] {
		if m.status != out.status {
			out.status = "uncertain"
			out.reason = "ambiguous_target_evidence"
		}
		out.sources = append(out.sources, m.sources...)
		out.verifications = append(out.verifications, m.verifications...)
	}
	return out
}
func reliableFormula(step SolutionStep, formula FormulaArtifact, input EvidenceMatchInput) bool {
	if step.Confidence < .8 || formula.Confidence < .8 || formula.ParseStatus != "parsed" || formula.AST == nil {
		return false
	}
	if !activeFormulaBlock(formula.BlockID, input.Blocks) {
		return false
	}
	return verifiedFormulaSyntax(formula.ID, input.Verifications)
}

func activeFormulaBlock(blockID string, blocks []MathAnswerBlock) bool {
	active := false
	// 同ID块按原数组顺序覆盖，最后一次匹配决定当前状态。
	for _, block := range blocks {
		if block.ID == blockID {
			active = (block.Status == "active" || block.Status == "inserted") && block.RecognitionConfidence >= .8 && block.StructureConfidence >= .8
		}
	}
	return active
}

func verifiedFormulaSyntax(formulaID string, checks []MathVerification) bool {
	syntaxVerified := false
	for _, check := range checks {
		if check.Kind != "syntax" || check.FormulaID != formulaID {
			continue
		}
		// 任一语法核验不可靠就阻断，后面的成功核验不能覆盖它。
		if check.Status != "verified" || check.Confidence < .8 {
			return false
		}
		syntaxVerified = true
	}
	return syntaxVerified
}
func reliableSource(stepID, formulaID string, input EvidenceMatchInput) bool {
	for _, step := range input.Graph.Steps {
		if step.ID != stepID || !slices.Contains(step.FormulaIDs, formulaID) {
			continue
		}
		for _, formula := range input.Formulas {
			if formula.ID == formulaID {
				return reliableFormula(step, formula, input)
			}
		}
	}
	return false
}
func boundDerivation(check MathVerification, graph SolutionGraph) bool {
	from, _ := check.Details["from_step_id"].(string)
	formula, _ := check.Details["from_formula_id"].(string)
	if from == "" || formula == "" {
		return false
	}
	for _, edge := range graph.Edges {
		if edge.Kind == "derives" && edge.FromStepID == from && edge.ToStepID == check.StepID {
			return true
		}
	}
	return false
}

// dependentStepRequiresReview 沿 derives 回查前置推导，next 阅读顺序不算数学依赖。
// 前面算错后是否给后续步骤分，需要量规和教师判断；这里只要前置推导存疑就转人工。
func dependentStepRequiresReview(stepID string, input EvidenceMatchInput) bool {
	return reviewDependencyStep(stepID, input, map[string]bool{})
}

func reviewDependencyStep(stepID string, input EvidenceMatchInput, visited map[string]bool) bool {
	if visited[stepID] {
		return false
	}
	// 整条回查共享访问集合，环或多条路径重复到达同一步时不再递归。
	visited[stepID] = true
	for _, edge := range input.Graph.Edges {
		if edge.ToStepID != stepID {
			continue
		}
		switch edge.Kind {
		case "corrects":
			return true
		case "derives":
			if derivationEdgeRequiresReview(edge, input) || reviewDependencyStep(edge.FromStepID, input, visited) {
				return true
			}
		}
	}
	return false
}

func derivationEdgeRequiresReview(edge SolutionEdge, input EvidenceMatchInput) bool {
	verified := false
	for _, check := range input.Verifications {
		from, _ := check.Details["from_step_id"].(string)
		if check.Kind != "equivalence" || check.StepID != edge.ToStepID || from != edge.FromStepID {
			continue
		}
		if check.Engine != "sympy" || check.Status != "verified" || check.Confidence < .8 || check.Domain != "real" || len(check.Constraints) > 0 || !boundDerivation(check, input.Graph) {
			return true
		}
		verified = true
	}
	return !verified
}
func uniqueIDs(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, id := range values {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// 全局置信度可能因其他步骤被明确反驳而归零；这里仅描述当前证据的识别和来源可靠性。
func matchConfidence(m evidenceMatch, input EvidenceMatchInput) float64 {
	if m.status == "uncertain" {
		return min(input.Graph.OverallConfidence, .49)
	}
	refs := map[string]bool{}
	for _, id := range m.sources {
		refs[id] = true
	}
	// 保留步骤、公式及其块、核验的累计顺序；自定义min对NaN和有符号零依赖参数顺序。
	confidence := stepEvidenceConfidence(refs, input.Graph.Steps, 1.0)
	confidence = formulaEvidenceConfidence(refs, input, confidence)
	return verificationEvidenceConfidence(m.verifications, input.Verifications, confidence)
}

func stepEvidenceConfidence(refs map[string]bool, steps []SolutionStep, confidence float64) float64 {
	for _, step := range steps {
		if refs[step.ID] {
			confidence = min(confidence, step.Confidence)
		}
	}
	return confidence
}

func formulaEvidenceConfidence(refs map[string]bool, input EvidenceMatchInput, confidence float64) float64 {
	for _, formula := range input.Formulas {
		if !refs[formula.ID] {
			continue
		}
		confidence = min(confidence, formula.Confidence)
		confidence = blockEvidenceConfidence(formula.BlockID, input.Blocks, confidence)
	}
	return confidence
}

func blockEvidenceConfidence(blockID string, blocks []MathAnswerBlock, confidence float64) float64 {
	for _, block := range blocks {
		if block.ID == blockID {
			confidence = min(confidence, min(block.RecognitionConfidence, block.StructureConfidence))
		}
	}
	return confidence
}

func verificationEvidenceConfidence(ids []string, checks []MathVerification, confidence float64) float64 {
	for _, id := range ids {
		for _, check := range checks {
			if check.ID == id {
				confidence = min(confidence, check.Confidence)
			}
		}
	}
	return confidence
}
func min(left, right float64) float64 {
	if left < right {
		return left
	}
	return right
}
