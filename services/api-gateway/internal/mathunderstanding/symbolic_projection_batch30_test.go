package mathunderstanding

import (
	"errors"
	"reflect"
	"testing"
)

func TestBatch30SymbolicProjectionKeepsSyntaxConfidenceAndInput(t *testing.T) {
	for _, tc := range []struct {
		name, syntax, symbolic string
		confidence, want       float64
		review                 bool
	}{
		{"verified keeps recognition floor", "verified", "verified", 1, .8, false},
		{"uncertain syntax survives success", "uncertain", "verified", .3, .3, true},
		{"contradicted syntax survives success", "contradicted", "verified", .7, 0, true},
		{"symbolic uncertainty lowers floor", "verified", "uncertain", .2, .2, true},
		{"symbolic contradiction forces zero", "verified", "contradicted", .7, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := validInput()
			input.Verifications[0].Status = tc.syntax
			if tc.syntax != "verified" {
				input.Verifications[0].Confidence = tc.confidence
			}
			before := cloneInput(input)
			check := MathVerification{ID: "symbolic", FormulaID: "formula-1", Kind: "constraint", Status: tc.symbolic,
				Domain: "real", Engine: "sympy", EngineVersion: "v1", RulesetVersion: "v1", Confidence: tc.confidence}
			derived, quality, err := applySymbolicVerifications(input, []MathVerification{check}, 7)
			if err != nil || quality["critical_confidence"] != tc.want || derived.SolutionGraph.RequiresHumanReview != tc.review {
				t.Fatalf("projection=%+v quality=%v err=%v", derived, quality, err)
			}
			if !reflect.DeepEqual(input, before) || len(derived.Verifications) != 2 || derived.Verifications[0].ID != "check-1" ||
				quality["checked_formula_count"] != 1 || quality["unchecked_formula_count"] != 0 || quality["correction_revision"] != int64(7) {
				t.Fatal("input, retained syntax order, or formula accounting changed")
			}
		})
	}
}

func TestBatch30SymbolicProjectionRejectsDuplicateBeforeReturningContract(t *testing.T) {
	check := MathVerification{ID: "check-1", FormulaID: "formula-1", Kind: "constraint", Status: "verified",
		Domain: "real", Engine: "sympy", EngineVersion: "v1", RulesetVersion: "v1", Confidence: 1}
	contract, quality, err := applySymbolicVerifications(validInput(), []MathVerification{check}, 0)
	if !errors.Is(err, ErrInvalidInput) || !reflect.DeepEqual(contract, CreateArtifactInput{}) || quality != nil {
		t.Fatalf("duplicate syntax ID accepted: %+v, %v, %v", contract, quality, err)
	}
}
