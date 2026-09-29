package subjective

import (
	"context"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
)

func TestReferenceContextFromSnapshot(t *testing.T) {
	value, err := referenceContextFromSnapshot([]byte(`{"answer":{"standard_answer":"2","equivalent_answers":["x=2"]},"solution":{"raw_text":"Solve x+1=3.","steps":[{"step_no":1,"content":"Subtract 1"}]}}`), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	if value == nil || value.StandardAnswer != "2" || value.SolutionText != "Solve x+1=3." || len(value.SolutionSteps) != 1 || !validGradingReferenceContext(value) {
		t.Fatalf("unexpected confirmed reference: %#v", value)
	}
	legacy, err := referenceContextFromSnapshot(nil, "")
	if err != nil || legacy != nil {
		t.Fatalf("legacy snapshot should be absent: %#v, %v", legacy, err)
	}
}

func TestBuildAdapterInputPreservesConfirmedReference(t *testing.T) {
	reference := &GradingReferenceContext{Source: "confirmed_exam_import_snapshot", StandardAnswer: "x=2"}
	input, _, _, err := (&Handler{}).buildAdapterInput(context.Background(), "tenant", "request-001", Context{ReferenceContext: reference}, ModelPolicy{}, PromptGuard{}, aieligibility.OutputConstraint{})
	if err != nil || input.ReferenceContext != reference {
		t.Fatalf("single-adapter path lost reference context: %#v, %v", input.ReferenceContext, err)
	}
}
