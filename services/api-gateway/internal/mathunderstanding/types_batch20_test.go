package mathunderstanding

import (
	"errors"
	"testing"
)

func TestArtifactValidationBatch20PreservesFailureOrder(t *testing.T) {
	// 每次同时破坏前后两个阶段，保护对外返回的首个错误，而不是仅检查是否拒绝。
	for _, test := range []struct {
		name   string
		change func(*CreateArtifactInput)
		reason string
	}{
		{"block before formula", func(input *CreateArtifactInput) {
			input.Blocks[0].RecognitionEngine = ""
			input.Formulas[0].ParserVersion = ""
		}, "invalid answer block"},
		{"formula identity before AST", func(input *CreateArtifactInput) {
			input.Formulas[0].BlockID = "missing"
			input.Formulas[0].AST.Kind = "execute"
		}, "invalid formula artifact"},
		{"AST before selection", func(input *CreateArtifactInput) {
			input.Formulas[0].AST.Kind = "execute"
			input.Formulas[0].SelectedCandidate = 1
		}, "invalid formula AST"},
		{"selection before candidate", func(input *CreateArtifactInput) {
			input.Formulas[0].SelectedCandidate = -1
			input.Formulas[0].Candidates[0].Engine = ""
		}, "invalid formula candidate selection"},
		{"candidate before symbol", func(input *CreateArtifactInput) {
			input.Formulas[0].Candidates[0].Engine = ""
			input.Formulas[0].Symbols = []FormulaSymbol{{ID: "bad"}}
		}, "invalid formula candidate"},
		{"symbol before relation", func(input *CreateArtifactInput) {
			input.Formulas[0].Symbols = []FormulaSymbol{{ID: "bad"}}
			input.Formulas[0].Relations = []FormulaRelation{{FromSymbolID: "bad", ToSymbolID: "missing"}}
		}, "invalid formula symbol"},
		{"spatial relation before graph", func(input *CreateArtifactInput) {
			input.Relations = []SpatialRelation{{ID: "bad"}}
			input.SolutionGraph.AnswerSegmentID = "other"
		}, "invalid spatial relation"},
		{"graph segment before steps", func(input *CreateArtifactInput) {
			input.SolutionGraph.AnswerSegmentID = "other"
			input.SolutionGraph.Steps[0].ID = ""
		}, "graph segment mismatch"},
		{"step references before edges", func(input *CreateArtifactInput) {
			input.SolutionGraph.Steps[0].BlockIDs = []string{"missing"}
			input.SolutionGraph.Edges = []SolutionEdge{{FromStepID: "missing"}}
		}, "unknown block"},
		{"verification before evidence", func(input *CreateArtifactInput) {
			input.Verifications[0].FormulaID = "missing"
			input.RubricEvidence[0].RubricCriterionKey = ""
		}, "invalid verification"},
		{"evidence fields before references", func(input *CreateArtifactInput) {
			input.RubricEvidence[0].RubricCriterionKey = ""
			input.RubricEvidence[0].VerificationIDs = []string{"missing"}
		}, "invalid rubric evidence"},
		{"evidence verification before source", func(input *CreateArtifactInput) {
			input.RubricEvidence[0].VerificationIDs = []string{"missing"}
			input.RubricEvidence[0].SourceArtifactIDs = []string{"missing"}
		}, "unknown evidence verification"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := validInput()
			test.change(&input)
			err := ValidateCreateArtifact(input)
			if !errors.Is(err, ErrInvalidInput) || err.Error() != ErrInvalidInput.Error()+": "+test.reason {
				t.Fatalf("error=%v want=%q", err, test.reason)
			}
		})
	}
}

func TestArtifactValidationBatch20KeepsFormulaLocalSymbols(t *testing.T) {
	input := validInput()
	box := input.Formulas[0].BoundingBox
	first := input.Formulas[0]
	first.Symbols = []FormulaSymbol{{ID: "a", Value: "a", BoundingBox: box, Confidence: 1}}
	second := input.Formulas[0]
	second.ID = "formula-2"
	second.Symbols = []FormulaSymbol{{ID: "b", Value: "b", BoundingBox: box, Confidence: 1}}
	second.Relations = []FormulaRelation{{FromSymbolID: "a", ToSymbolID: "b", Kind: "right_of", Confidence: 1}}
	input.Formulas = []FormulaArtifact{first, second}
	err := ValidateCreateArtifact(input)
	if !errors.Is(err, ErrInvalidInput) || err.Error() != ErrInvalidInput.Error()+": invalid formula relation" {
		t.Fatalf("cross-formula symbol reference accepted: %v", err)
	}
}

func TestArtifactValidationBatch20KeepsOptionalEvidenceAndCandidates(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*CreateArtifactInput)
		valid  bool
	}{
		{"empty candidates ignore selected index", func(input *CreateArtifactInput) {
			input.Formulas[0].Candidates = nil
			input.Formulas[0].SelectedCandidate = -1
		}, true},
		{"uncertain evidence may lack sources", func(input *CreateArtifactInput) {
			input.RubricEvidence[0].Status = "uncertain"
			input.RubricEvidence[0].SourceArtifactIDs = nil
		}, true},
		{"supported evidence requires sources", func(input *CreateArtifactInput) {
			input.RubricEvidence[0].SourceArtifactIDs = nil
		}, false},
		{"optional verification references", func(input *CreateArtifactInput) {
			input.Verifications[0].StepID = ""
			input.Verifications[0].FormulaID = ""
		}, true},
		{"duplicate block", func(input *CreateArtifactInput) {
			input.Blocks = append(input.Blocks, input.Blocks[0])
		}, false},
		{"duplicate formula", func(input *CreateArtifactInput) {
			input.Formulas = append(input.Formulas, input.Formulas[0])
		}, false},
		{"duplicate verification", func(input *CreateArtifactInput) {
			input.Verifications = append(input.Verifications, input.Verifications[0])
		}, false},
		{"duplicate evidence", func(input *CreateArtifactInput) {
			input.RubricEvidence = append(input.RubricEvidence, input.RubricEvidence[0])
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := validInput()
			test.change(&input)
			if err := ValidateCreateArtifact(input); (err == nil) != test.valid {
				t.Fatalf("error=%v valid=%t", err, test.valid)
			}
		})
	}
}

func TestArtifactValidationBatch20KeepsSharedAndUnreachableGraphChecks(t *testing.T) {
	input := validInput()
	for _, id := range []string{"step-2", "step-3", "step-4"} {
		input.SolutionGraph.Steps = append(input.SolutionGraph.Steps, SolutionStep{ID: id, BlockIDs: []string{"block-1"}, Confidence: 1})
	}
	input.SolutionGraph.Edges = []SolutionEdge{
		{FromStepID: "step-1", ToStepID: "step-3", Kind: "supports"},
		{FromStepID: "step-2", ToStepID: "step-3", Kind: "supports"},
	}
	if err := ValidateCreateArtifact(input); err != nil {
		t.Fatalf("shared downstream step rejected: %v", err)
	}
	// 首个步骤无法到达的分支也要查环，不能只从首个步骤开始检查。
	input.SolutionGraph.Edges = append(input.SolutionGraph.Edges,
		SolutionEdge{FromStepID: "step-2", ToStepID: "step-4", Kind: "next"},
		SolutionEdge{FromStepID: "step-4", ToStepID: "step-2", Kind: "next"})
	err := ValidateCreateArtifact(input)
	if !errors.Is(err, ErrInvalidInput) || err.Error() != ErrInvalidInput.Error()+": solution graph must be acyclic" {
		t.Fatalf("unreachable cycle accepted: %v", err)
	}
}
