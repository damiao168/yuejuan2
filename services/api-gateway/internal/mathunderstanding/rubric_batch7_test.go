package mathunderstanding

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func batch7MatchInput() EvidenceMatchInput {
	_, effective := scoringFixture()
	input := effective.EffectiveContract
	return EvidenceMatchInput{Graph: input.SolutionGraph, Blocks: input.Blocks, Formulas: input.Formulas, Verifications: input.Verifications}
}

func reportBatch7Proof(t *testing.T, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("MATH_RUBRIC_BATCH7_PROOF %s", raw)
}

func assertBatch7Match(t *testing.T, actual, expected evidenceMatch) {
	t.Helper()
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("match changed: got %#v, want %#v", actual, expected)
	}
	reportBatch7Proof(t, map[string]any{"status": actual.status, "sources": actual.sources, "verifications": actual.verifications, "reason": actual.reason, "source": actual.source})
}

func TestRubricBatch7CompositeFilteringAndLeafIdentity(t *testing.T) {
	input := batch7MatchInput()
	input.Verifications[2].Status = "contradicted"
	result := EvidenceRequirement{Type: "final_result", Target: "2*x=4"}
	transform := EvidenceRequirement{Type: "valid_transformation", Target: "2*x=4"}
	semantic := EvidenceRequirement{Type: "concept", Target: "factorization"}
	children := []EvidenceRequirement{result, transform, semantic}
	for _, tc := range []struct {
		name     string
		required EvidenceRequirement
		expected evidenceMatch
	}{
		{"empty", EvidenceRequirement{Type: "all_of"}, unresolved("evidence_requirements_missing")},
		{"single_all", EvidenceRequirement{Type: "all_of", Children: []EvidenceRequirement{transform}}, evidenceMatch{status: "contradicted", sources: []string{"step-1", "formula-1", "step-2", "formula-2"}, verifications: []string{"eq-2"}, reason: "equivalent_transform", source: "symbolic"}},
		{"single_at_least", EvidenceRequirement{Type: "at_least", Minimum: 1, Children: []EvidenceRequirement{semantic}}, unresolved("semantic_evidence_requires_review")},
		{"any_supported", EvidenceRequirement{Type: "any_of", Children: children}, evidenceMatch{status: "supported", sources: []string{"step-2", "formula-2"}, reason: "requirements_supported", source: "rule"}},
		{"all_contradicted", EvidenceRequirement{Type: "all_of", Children: children}, evidenceMatch{status: "contradicted", sources: []string{"step-1", "formula-1", "step-2", "formula-2"}, verifications: []string{"eq-2"}, reason: "requirements_contradicted", source: "rule"}},
		{"at_least_uncertain", EvidenceRequirement{Type: "at_least", Minimum: 2, Children: children}, evidenceMatch{status: "uncertain", sources: []string{"step-2", "formula-2", "step-1", "formula-1", "step-2", "formula-2"}, verifications: []string{"eq-2"}, reason: "requirements_uncertain", source: "rule"}},
	} {
		t.Run(tc.name, func(t *testing.T) { assertBatch7Match(t, matchRequirement(tc.required, input), tc.expected) })
	}
}

func TestRubricBatch7TargetTraversalAndConflictProvenance(t *testing.T) {
	input := batch7MatchInput()
	input.Graph.Steps = append(input.Graph.Steps, SolutionStep{ID: "step-3", Kind: "conclusion", FormulaIDs: []string{"formula-2", "formula-2"}, Confidence: .9})
	assertBatch7Match(t, matchRequirement(EvidenceRequirement{Type: "final_result", Target: " 2*x=4 "}, input), evidenceMatch{status: "supported", sources: []string{"step-2", "formula-2", "step-3", "formula-2", "step-3", "formula-2"}, reason: "frozen_result_exact_match", source: "rule"})

	input = batch7MatchInput()
	check := input.Verifications[2]
	check.ID, check.Status, check.ReasonCode = "conflict", "contradicted", "second_reason"
	input.Verifications = append(input.Verifications, check)
	assertBatch7Match(t, matchRequirement(EvidenceRequirement{Type: "valid_transformation", Target: "2*x=4"}, input), evidenceMatch{status: "uncertain", sources: []string{"step-1", "formula-1", "step-2", "formula-2", "step-1", "formula-1", "step-2", "formula-2"}, verifications: []string{"eq-2", "conflict"}, reason: "ambiguous_target_evidence", source: "symbolic"})

	input = batch7MatchInput()
	duplicate := input.Formulas[1]
	duplicate.Confidence = .3
	input.Formulas = append(input.Formulas, duplicate)
	assertBatch7Match(t, matchRequirement(EvidenceRequirement{Type: "final_result", Target: "2*x=4"}, input), evidenceMatch{status: "uncertain", sources: []string{"step-2", "formula-2", "step-2", "formula-2"}, reason: "ambiguous_target_evidence", source: "rule"})
	assertBatch7Match(t, matchRequirement(EvidenceRequirement{Type: "final_result", Target: "missing"}, input), unresolved("target_evidence_missing"))
}

func TestRubricBatch7SourceAndScopeReviewPriority(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*EvidenceMatchInput)
		reason string
	}{
		{"source", func(input *EvidenceMatchInput) { input.Blocks[0].RecognitionConfidence = .3 }, "source_step_uncertain"},
		{"correction", func(input *EvidenceMatchInput) {
			input.Graph.Edges = append(input.Graph.Edges, SolutionEdge{FromStepID: "earlier", ToStepID: "step-1", Kind: "corrects"})
		}, "dependent_step_requires_review"},
		{"domain", func(input *EvidenceMatchInput) { input.Verifications[2].Domain = "complex" }, "verification_scope_requires_review"},
		{"constraint", func(input *EvidenceMatchInput) { input.Verifications[2].Constraints = []string{"x>0"} }, "verification_scope_requires_review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := batch7MatchInput()
			tc.mutate(&input)
			source := "symbolic"
			if tc.name == "source" || tc.name == "correction" {
				source = "rule"
			}
			assertBatch7Match(t, matchRequirement(EvidenceRequirement{Type: "valid_transformation", Target: "2*x=4"}, input), evidenceMatch{status: "uncertain", sources: []string{"step-1", "formula-1", "step-2", "formula-2"}, verifications: []string{"eq-2"}, reason: tc.reason, source: source})
		})
	}
}

func TestRubricBatch7ReliabilityPreservesDuplicateAndSyntaxOrder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*EvidenceMatchInput)
		want   bool
	}{
		{"active", func(*EvidenceMatchInput) {}, true},
		{"last_block_inactive", func(input *EvidenceMatchInput) {
			block := input.Blocks[1]
			block.Status = "crossed_out"
			input.Blocks = append(input.Blocks, block)
		}, false},
		{"last_block_active", func(input *EvidenceMatchInput) {
			block := input.Blocks[1]
			input.Blocks[1].Status = "crossed_out"
			input.Blocks = append(input.Blocks, block)
		}, true},
		{"bad_syntax_last", func(input *EvidenceMatchInput) {
			check := input.Verifications[1]
			check.Status = "uncertain"
			input.Verifications = append(input.Verifications, check)
		}, false},
		{"bad_syntax_first", func(input *EvidenceMatchInput) {
			check := input.Verifications[1]
			input.Verifications[1].Status = "uncertain"
			input.Verifications = append(input.Verifications, check)
		}, false},
		{"missing_syntax", func(input *EvidenceMatchInput) {
			input.Verifications = append(input.Verifications[:1], input.Verifications[2:]...)
		}, false},
		{"nan_syntax_confidence", func(input *EvidenceMatchInput) { input.Verifications[1].Confidence = math.NaN() }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := batch7MatchInput()
			tc.mutate(&input)
			actual := reliableFormula(input.Graph.Steps[1], input.Formulas[1], input)
			if actual != tc.want {
				t.Fatalf("formula reliability: got %v, want %v", actual, tc.want)
			}
			reportBatch7Proof(t, actual)
		})
	}
	input := batch7MatchInput()
	duplicate := input.Formulas[0]
	input.Formulas[0].Confidence = .3
	input.Formulas = append(input.Formulas, duplicate)
	if reliableSource("step-1", "formula-1", input) {
		t.Fatal("later duplicate formula bypassed the first matching source")
	}
	input = batch7MatchInput()
	step := input.Graph.Steps[0]
	input.Graph.Steps[0].FormulaIDs = []string{"missing"}
	input.Graph.Steps = append(input.Graph.Steps, step)
	if !reliableSource("step-1", "formula-1", input) {
		t.Fatal("source search stopped before a step containing the requested formula")
	}
}

func batch7DerivationCheck(from, to string) MathVerification {
	return MathVerification{ID: from + "-" + to, StepID: to, Kind: "equivalence", Engine: "sympy", Status: "verified", Confidence: .8, Domain: "real", Details: map[string]any{"from_step_id": from, "from_formula_id": "formula-" + from}}
}

func TestRubricBatch7DependencyKindsCyclesAndAnyBadCheck(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edges  []SolutionEdge
		checks []MathVerification
		want   bool
	}{
		{"next_is_not_dependency", []SolutionEdge{{FromStepID: "A", ToStepID: "B", Kind: "next"}}, nil, false},
		{"corrects_requires_review", []SolutionEdge{{FromStepID: "A", ToStepID: "B", Kind: "corrects"}}, nil, true},
		{"derives_missing", []SolutionEdge{{FromStepID: "A", ToStepID: "B", Kind: "derives"}}, nil, true},
		{"derives_verified", []SolutionEdge{{FromStepID: "A", ToStepID: "B", Kind: "derives"}}, []MathVerification{batch7DerivationCheck("A", "B")}, false},
		// 私有回查可收到环；同一visited集合必须截断重复访问，不能无限递归。
		{"verified_cycle", []SolutionEdge{{FromStepID: "A", ToStepID: "B", Kind: "derives"}, {FromStepID: "B", ToStepID: "A", Kind: "derives"}}, []MathVerification{batch7DerivationCheck("A", "B"), batch7DerivationCheck("B", "A")}, false},
		{"earlier_missing", []SolutionEdge{{FromStepID: "A", ToStepID: "B", Kind: "derives"}, {FromStepID: "X", ToStepID: "A", Kind: "derives"}}, []MathVerification{batch7DerivationCheck("A", "B")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual := dependentStepRequiresReview("B", EvidenceMatchInput{Graph: SolutionGraph{Edges: tc.edges}, Verifications: tc.checks})
			if actual != tc.want {
				t.Fatalf("dependency review: got %v, want %v", actual, tc.want)
			}
			reportBatch7Proof(t, actual)
		})
	}
	input := EvidenceMatchInput{Graph: SolutionGraph{Edges: []SolutionEdge{{FromStepID: "A", ToStepID: "B", Kind: "derives"}}}, Verifications: []MathVerification{batch7DerivationCheck("A", "B")}}
	bad := batch7DerivationCheck("A", "B")
	bad.Engine = "other"
	input.Verifications = append(input.Verifications, bad)
	if !dependentStepRequiresReview("B", input) {
		t.Fatal("one valid check hid an invalid matching predecessor check")
	}
	input.Verifications = input.Verifications[:1]
	input.Verifications[0].Confidence = math.NaN()
	if dependentStepRequiresReview("B", input) {
		t.Fatal("refactor changed the original less-than confidence comparison")
	}
}

func TestRubricBatch7ConfidenceKeepsAccumulatorOrderAndFloatBits(t *testing.T) {
	nan := math.Float64frombits(0x7ff8000000000042)
	for _, tc := range []struct {
		name   string
		mutate func(*EvidenceMatchInput, *evidenceMatch)
		want   float64
	}{
		{"local_not_global", func(input *EvidenceMatchInput, _ *evidenceMatch) { input.Graph.OverallConfidence = 0 }, .8},
		{"uncertain_cap", func(_ *EvidenceMatchInput, m *evidenceMatch) { m.status = "uncertain" }, .49},
		{"uncertain_nan", func(input *EvidenceMatchInput, m *evidenceMatch) {
			input.Graph.OverallConfidence = nan
			m.status = "uncertain"
		}, .49},
		{"block_nan_then_low", func(input *EvidenceMatchInput, _ *evidenceMatch) {
			input.Blocks[0].StructureConfidence = nan
			block := input.Blocks[0]
			block.StructureConfidence = .6
			input.Blocks = append(input.Blocks, block)
		}, .6},
		{"block_low_then_nan", func(input *EvidenceMatchInput, _ *evidenceMatch) {
			block := input.Blocks[0]
			block.StructureConfidence = nan
			input.Blocks[0].StructureConfidence = .6
			input.Blocks = append(input.Blocks, block)
		}, nan},
		{"check_nan_then_low", func(input *EvidenceMatchInput, m *evidenceMatch) {
			input.Verifications = []MathVerification{{ID: "v", Confidence: nan}, {ID: "v", Confidence: .7}}
			m.verifications = []string{"v", "v"}
		}, .7},
		{"check_low_then_nan", func(input *EvidenceMatchInput, m *evidenceMatch) {
			input.Verifications = []MathVerification{{ID: "v", Confidence: .7}, {ID: "v", Confidence: nan}}
			m.verifications = []string{"v", "v"}
		}, nan},
		{"negative_zero", func(input *EvidenceMatchInput, _ *evidenceMatch) {
			input.Verifications = []MathVerification{{ID: "v", Confidence: math.Copysign(0, -1)}}
		}, math.Copysign(0, -1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := batch7MatchInput()
			m := evidenceMatch{status: "supported", sources: []string{"step-1", "formula-1"}, verifications: []string{"v"}}
			tc.mutate(&input, &m)
			actual := matchConfidence(m, input)
			if math.Float64bits(actual) != math.Float64bits(tc.want) {
				t.Fatalf("confidence bits changed: got %016x, want %016x", math.Float64bits(actual), math.Float64bits(tc.want))
			}
			reportBatch7Proof(t, math.Float64bits(actual))
		})
	}
}

func TestRubricBatch7PublicNilArraysAndExactScoreContract(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input EvidenceMatchInput
		want  []RubricEvidence
	}{
		{"no_requirements", EvidenceMatchInput{}, []RubricEvidence{}},
		{"empty_legacy", EvidenceMatchInput{Requirements: []EvidenceRequirement{{Type: "all_of", Criterion: "P"}}}, []RubricEvidence{{ID: "rubric-evidence-1", RubricCriterionKey: "P", EvidenceType: "all_of", SourceArtifactIDs: []string{}, VerificationIDs: []string{}, Status: "uncertain", Explanation: "evidence_requirements_missing", Confidence: 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := MatchRubricEvidence(tc.input)
			if err != nil || !reflect.DeepEqual(actual, tc.want) {
				t.Fatalf("public nil/empty contract changed: got %#v %v, want %#v", actual, err, tc.want)
			}
			reportBatch7Proof(t, actual)
		})
	}
	frozen, effective := scoringFixture()
	score, err := ScoreFrozenRubric(frozen, effective, nil)
	if err != nil {
		t.Fatal(err)
	}
	if score.VerifiedScore != 4 || score.UnresolvedScore != 2 || score.SuggestedScore != nil || !score.RequiresHumanReview || score.ScoreRange != (ScoreRange{Min: 4, Max: 6}) {
		t.Fatalf("frozen scoring authority changed: %#v", score)
	}
	reportBatch7Proof(t, score)
}
