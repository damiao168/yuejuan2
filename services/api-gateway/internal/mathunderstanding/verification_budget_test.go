package mathunderstanding

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func verificationBudgetInput(count int, kind string) CreateArtifactInput {
	input := validInput()
	check := input.Verifications[0]
	check.Kind = kind
	if kind != "syntax" {
		check.Engine = "sympy"
	}
	input.Verifications = make([]MathVerification, count)
	for i := range input.Verifications {
		check.ID = fmt.Sprintf("existing-%d", i)
		input.Verifications[i] = check
	}
	return input
}

func TestArtifactVerificationBudgetAppliesWithoutHTTPBodyLimit(t *testing.T) {
	if err := ValidateCreateArtifact(verificationBudgetInput(maxArtifactVerificationCount, "syntax")); err != nil {
		t.Fatalf("record budget boundary rejected: %v", err)
	}
	if err := ValidateCreateArtifact(verificationBudgetInput(maxArtifactVerificationCount+1, "syntax")); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("over-budget artifact accepted: %v", err)
	}
}

func TestSymbolicVerificationBudgetsRejectOversizedExistingIncomingAndMergedRecords(t *testing.T) {
	check := MathVerification{ID: "new-symbolic", FormulaID: "formula-1", Kind: "constraint", Status: "verified",
		Domain: "real", Engine: "sympy", EngineVersion: "v1", RulesetVersion: "v1", Confidence: 1}
	for _, tc := range []struct {
		name          string
		existingCount int
		incomingCount int
	}{
		{"existing records", maxArtifactVerificationCount + 1, 1},
		{"incoming records", 1, 2049},
		{"merged records", maxArtifactVerificationCount, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := verificationBudgetInput(tc.existingCount, "syntax")
			before := cloneInput(input)
			checks := make([]MathVerification, tc.incomingCount)
			for i := range checks {
				checks[i] = check
				checks[i].ID = fmt.Sprintf("new-%d", i)
			}
			derived, quality, err := applySymbolicVerifications(input, checks, 0)
			if !errors.Is(err, ErrInvalidInput) || !reflect.DeepEqual(derived, CreateArtifactInput{}) || quality != nil || !reflect.DeepEqual(input, before) {
				t.Fatalf("over-budget result mutated or returned contract: quality=%v err=%v", quality, err)
			}
		})
	}
}

func TestSymbolicVerificationBudgetPreservesSyntaxAndReplacesPriorRevision(t *testing.T) {
	input := verificationBudgetInput(maxArtifactVerificationCount-2048, "syntax")
	input.Verifications[0].Status, input.Verifications[0].Confidence = "uncertain", .2
	checks := make([]MathVerification, 2048)
	for i := range checks {
		checks[i] = MathVerification{ID: fmt.Sprintf("new-%d", i), FormulaID: "formula-1", Kind: "constraint", Status: "verified",
			Domain: "real", Engine: "sympy", EngineVersion: "v1", RulesetVersion: "v1", Confidence: 1}
	}
	derived, quality, err := applySymbolicVerifications(input, checks, 1)
	if err != nil || len(derived.Verifications) != maxArtifactVerificationCount || quality["critical_confidence"] != .2 || !derived.SolutionGraph.RequiresHumanReview {
		t.Fatalf("merged boundary failed to retain syntax doubt: count=%d quality=%v err=%v", len(derived.Verifications), quality, err)
	}
	// 重复修订替换整批旧符号结果，不能按历史记录加新增记录误算容量。
	checks[0].ID = "replacement"
	revised, quality, err := applySymbolicVerifications(derived, checks, 2)
	if err != nil || len(revised.Verifications) != maxArtifactVerificationCount || revised.Verifications[0].Kind != "syntax" ||
		revised.Verifications[maxArtifactVerificationCount-2048].ID != "replacement" || quality["correction_revision"] != int64(2) {
		t.Fatalf("replacement revision accumulated old records: count=%d quality=%v err=%v", len(revised.Verifications), quality, err)
	}
}
