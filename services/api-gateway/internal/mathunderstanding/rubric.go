package mathunderstanding

import (
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"fmt"
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
	// Legacy unanchored labels are not authoritative evidence.
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
		// Multiple requirements on one point are conjunctive, not separate awards.
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
	// The scorer adds a conjunction wrapper to the existing depth-8 DSL.
	if depth > 9 {
		return false
	}
	if set("all_of", "any_of", "at_least")[r.Type] {
		// Empty all_of represents a legacy point without an evidence DSL.
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
		if len(r.Children) == 0 {
			return unresolved("evidence_requirements_missing")
		}
		if len(r.Children) == 1 && (r.Type != "at_least" || r.Minimum == 1) {
			return matchRequirement(r.Children[0], input)
		}
		needed := len(r.Children)
		if r.Type == "any_of" {
			needed = 1
		}
		if r.Type == "at_least" {
			needed = r.Minimum
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
		status := "contradicted"
		if supported >= needed {
			status = "supported"
		} else if supported+unknown >= needed {
			status = "uncertain"
		}
		out := evidenceMatch{status: status, reason: "requirements_" + status, source: "rule"}
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
	if r.Type == "concept" || r.Type == "unit" || r.Type == "domain" {
		return unresolved("semantic_evidence_requires_review")
	}
	if strings.TrimSpace(r.Target) == "" {
		return unresolved("rubric_target_unbound")
	}
	matches := []evidenceMatch{}
	for _, step := range input.Graph.Steps {
		for _, formulaID := range step.FormulaIDs {
			for _, formula := range input.Formulas {
				if formula.ID != formulaID || strings.TrimSpace(formula.CanonicalLatex) != strings.TrimSpace(r.Target) {
					continue
				}
				if !reliableFormula(step, formula, input) {
					m := unresolved("recognition_or_structure_uncertain")
					m.sources = []string{step.ID, formula.ID}
					matches = append(matches, m)
					continue
				}
				if r.Type == "final_result" {
					// Exact frozen-target equality is a narrow server rule. Solving the
					// student's equation alone never proves the final answer.
					if step.Kind == "conclusion" {
						matches = append(matches, evidenceMatch{status: "supported", sources: []string{step.ID, formula.ID}, reason: "frozen_result_exact_match", source: "rule"})
					}
					continue
				}
				for _, check := range input.Verifications {
					if check.Kind != "equivalence" || check.Engine != "sympy" || check.StepID != step.ID || check.FormulaID != formula.ID || !boundDerivation(check, input.Graph) {
						continue
					}
					from, _ := check.Details["from_step_id"].(string)
					fromFormula, _ := check.Details["from_formula_id"].(string)
					if !reliableSource(from, fromFormula, input) {
						m := unresolved("source_step_uncertain")
						m.sources = []string{from, fromFormula, step.ID, formula.ID}
						m.verifications = []string{check.ID}
						matches = append(matches, m)
						continue
					}
					if dependentStepRequiresReview(from, input) {
						m := unresolved("dependent_step_requires_review")
						m.sources = []string{from, fromFormula, step.ID, formula.ID}
						m.verifications = []string{check.ID}
						matches = append(matches, m)
						continue
					}
					m := evidenceMatch{status: "uncertain", sources: []string{from, fromFormula, step.ID, formula.ID}, verifications: []string{check.ID}, reason: check.ReasonCode, source: "symbolic"}
					if check.Domain != "real" || len(check.Constraints) > 0 {
						m.reason = "verification_scope_requires_review"
					} else if check.Confidence >= .8 && check.Status == "verified" {
						m.status = "supported"
					} else if check.Confidence >= .8 && check.Status == "contradicted" {
						m.status = "contradicted"
					}
					matches = append(matches, m)
				}
			}
		}
	}
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
	active := false
	for _, block := range input.Blocks {
		if block.ID == formula.BlockID {
			active = (block.Status == "active" || block.Status == "inserted") && block.RecognitionConfidence >= .8 && block.StructureConfidence >= .8
		}
	}
	if !active {
		return false
	}
	syntaxVerified := false
	for _, check := range input.Verifications {
		if check.Kind == "syntax" && check.FormulaID == formula.ID {
			if check.Status != "verified" || check.Confidence < .8 {
				return false
			}
			syntaxVerified = true
		}
	}
	return syntaxVerified
}
func reliableSource(stepID, formulaID string, input EvidenceMatchInput) bool {
	for _, step := range input.Graph.Steps {
		if step.ID != stepID {
			continue
		}
		for _, id := range step.FormulaIDs {
			if id == formulaID {
				for _, formula := range input.Formulas {
					if formula.ID == id {
						return reliableFormula(step, formula, input)
					}
				}
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
	visited := map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if visited[id] {
			return false
		}
		visited[id] = true
		for _, edge := range input.Graph.Edges {
			if edge.ToStepID != id {
				continue
			}
			if edge.Kind == "corrects" {
				return true
			}
			if edge.Kind != "derives" {
				continue
			}
			verified := false
			for _, check := range input.Verifications {
				from, _ := check.Details["from_step_id"].(string)
				if check.Kind != "equivalence" || check.StepID != id || from != edge.FromStepID {
					continue
				}
				if check.Engine != "sympy" || check.Status != "verified" || check.Confidence < .8 || check.Domain != "real" || len(check.Constraints) > 0 || !boundDerivation(check, input.Graph) {
					return true
				}
				verified = true
			}
			if !verified || visit(edge.FromStepID) {
				return true
			}
		}
		return false
	}
	return visit(stepID)
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

// Global critical confidence may be zero precisely because another check
// confidently contradicted a different step. Local evidence confidence must
// describe this proof's recognition/provenance, not erase that distinction.
func matchConfidence(m evidenceMatch, input EvidenceMatchInput) float64 {
	if m.status == "uncertain" {
		return min(input.Graph.OverallConfidence, .49)
	}
	confidence := 1.0
	refs := map[string]bool{}
	for _, id := range m.sources {
		refs[id] = true
	}
	for _, step := range input.Graph.Steps {
		if refs[step.ID] {
			confidence = min(confidence, step.Confidence)
		}
	}
	for _, formula := range input.Formulas {
		if !refs[formula.ID] {
			continue
		}
		confidence = min(confidence, formula.Confidence)
		for _, block := range input.Blocks {
			if block.ID == formula.BlockID {
				confidence = min(confidence, min(block.RecognitionConfidence, block.StructureConfidence))
			}
		}
	}
	for _, id := range m.verifications {
		for _, check := range input.Verifications {
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
