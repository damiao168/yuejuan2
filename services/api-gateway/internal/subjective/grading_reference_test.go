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

func TestAnswerPartsPreserveMathAndDoNotInventMissingParts(t *testing.T) {
	for _, test := range []struct {
		text  string
		count int
	}{
		{`(1)$x=2$。(2)$(-\infty,-e^{-2}]$。`, 2},
		{`（1）赛制一：$\frac{1}{6}$；赛制二：$\frac{1}{2}$。`, 1},
		{`$(0,1)$`, 0}, {`x=2，代入式(1)即可`, 0}, {`(1)x=2；(3)y=4`, 0},
	} {
		parts := gradingAnswerParts(test.text)
		if len(parts) != test.count {
			t.Fatalf("%q: unexpected parts %#v", test.text, parts)
		}
	}
	value, err := referenceContextFromSnapshot([]byte(`{"answer":{"standard_answer":"(1)x=2。(2)y=4。"},"solution":{"raw_text":"完整教师解析","steps":[]}}`), strings.Repeat("b", 64))
	if err != nil || len(value.AnswerParts) != 2 || value.StandardAnswer != "(1)x=2。(2)y=4。" || value.SolutionText != "完整教师解析" {
		t.Fatalf("reference lost raw facts: %#v, %v", value, err)
	}
}
