package paper

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func questionRubricSegment(spans ...QuestionRubricSolutionSpan) QuestionRubricSolutionSegment {
	return QuestionRubricSolutionSegment{Spans: spans}
}

func TestQuestionRubricSegmentsRebuildStoredUnicodeAndMergedRanges(t *testing.T) {
	steps := []SolutionStep{{Content: " "}, {StepNo: 9, Content: "  设🧮x=1\n再化简  "}, {StepNo: 12, Content: " 得 x=1 "}}
	firstLength := len(utf16.Encode([]rune(strings.TrimSpace(steps[1].Content))))
	segments := []QuestionRubricSolutionSegment{
		questionRubricSegment(QuestionRubricSolutionSpan{SourceID: "step-2", Start: 0, End: 3}),
		questionRubricSegment(QuestionRubricSolutionSpan{SourceID: "step-2", Start: 3, End: firstLength}, QuestionRubricSolutionSpan{SourceID: "step-3", Start: 0, End: 5}),
	}
	got, fullText, err := questionRubricSegmentSteps(steps, segments)
	want := []SolutionStep{{StepNo: 1, Content: "设🧮"}, {StepNo: 2, Content: "x=1\n再化简\n\n得 x=1"}}
	if err != nil || !reflect.DeepEqual(got, want) || fullText != "设🧮x=1\n再化简\n\n得 x=1" {
		t.Fatalf("UTF-16 ranges lost stored text: %#v context=%q error=%v", got, fullText, err)
	}
}

func TestQuestionRubricSegmentOffsetsUseBrowserTrimWhitespace(t *testing.T) {
	for _, text := range []string{"\ufeff甲\ufeff", "\u00a0甲\u3000", "\u0085甲\u0085"} {
		want := strings.Trim(text, "\ufeff\u00a0\u3000")
		segments := []QuestionRubricSolutionSegment{questionRubricSegment(QuestionRubricSolutionSpan{SourceID: "step-1", Start: 0, End: len(utf16.Encode([]rune(want)))})}
		got, _, err := questionRubricSegmentSteps([]SolutionStep{{Content: text}}, segments)
		if err != nil || len(got) != 1 || got[0].Content != want {
			t.Fatalf("browser trim differs for %q: %#v %v", text, got, err)
		}
	}
}

func TestQuestionRubricSegmentsRejectGapsReorderAndInvalidUTF16BeforeModel(t *testing.T) {
	span := func(id string, start, end int) QuestionRubricSolutionSpan {
		return QuestionRubricSolutionSpan{SourceID: id, Start: start, End: end}
	}
	for _, item := range []struct {
		name     string
		segments []QuestionRubricSolutionSegment
	}{
		{"empty", []QuestionRubricSolutionSegment{}},
		{"empty spans", []QuestionRubricSolutionSegment{{}}},
		{"unknown source", []QuestionRubricSolutionSegment{questionRubricSegment(span("step-9", 0, 5))}},
		{"out of order", []QuestionRubricSolutionSegment{questionRubricSegment(span("step-3", 0, 1), span("step-2", 0, 5))}},
		{"initial gap", []QuestionRubricSolutionSegment{questionRubricSegment(span("step-2", 1, 5), span("step-3", 0, 1))}},
		{"middle gap", []QuestionRubricSolutionSegment{questionRubricSegment(span("step-2", 0, 1), span("step-2", 3, 5), span("step-3", 0, 1))}},
		{"overlap", []QuestionRubricSolutionSegment{questionRubricSegment(span("step-2", 0, 3), span("step-2", 1, 5), span("step-3", 0, 1))}},
		{"repeated source", []QuestionRubricSolutionSegment{questionRubricSegment(span("step-2", 0, 5), span("step-2", 0, 5), span("step-3", 0, 1))}},
		{"missing tail", []QuestionRubricSolutionSegment{questionRubricSegment(span("step-2", 0, 4), span("step-3", 0, 1))}},
		{"missing source", []QuestionRubricSolutionSegment{questionRubricSegment(span("step-2", 0, 5))}},
		{"negative offset", []QuestionRubricSolutionSegment{questionRubricSegment(span("step-2", -1, 5), span("step-3", 0, 1))}},
		{"empty range", []QuestionRubricSolutionSegment{questionRubricSegment(span("step-2", 0, 0), span("step-2", 0, 5), span("step-3", 0, 1))}},
		{"beyond text", []QuestionRubricSolutionSegment{questionRubricSegment(span("step-2", 0, 6), span("step-3", 0, 1))}},
		{"inside emoji", []QuestionRubricSolutionSegment{questionRubricSegment(span("step-2", 0, 2), span("step-2", 2, 5), span("step-3", 0, 1))}},
	} {
		t.Run(item.name, func(t *testing.T) {
			store, question := questionDraftFixture(t)
			question.Solution.Steps = []SolutionStep{{Content: " "}, {Content: "甲🧮乙丙"}, {Content: "丁"}}
			store.questions[question.ID] = question
			service := &DocumentImportService{parser: NewHTTPDocumentParser("http://must-not-call.invalid", strings.Repeat("t", 32), time.Second)}
			service.WithModelResolver(func(context.Context, string) (*DocumentModelConfig, error) {
				t.Error("invalid partition reached school model resolution")
				return nil, nil
			})
			_, err := suggestQuestionMathRubricDraft(context.Background(), store, service, "tenant", "exam", question.ID, QuestionMathRubricDraftInput{ExpectedQuestion: &question, SolutionSegments: item.segments})
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("invalid partition was not rejected before inference: %v", err)
			}
		})
	}
}

func TestQuestionRubricSegmentsRejectEmptyContentAndResourceLimits(t *testing.T) {
	blank := []QuestionRubricSolutionSegment{
		questionRubricSegment(QuestionRubricSolutionSpan{SourceID: "step-1", Start: 0, End: 1}),
		questionRubricSegment(QuestionRubricSolutionSpan{SourceID: "step-1", Start: 1, End: 2}),
		questionRubricSegment(QuestionRubricSolutionSpan{SourceID: "step-1", Start: 2, End: 3}),
	}
	if _, _, err := questionRubricSegmentSteps([]SolutionStep{{Content: "甲\n乙"}}, blank); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("whitespace-only model step accepted: %v", err)
	}
	if _, _, err := questionRubricSegmentSteps([]SolutionStep{{Content: "甲"}}, make([]QuestionRubricSolutionSegment, maxQuestionRubricSegments+1)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("too many segments accepted: %v", err)
	}
	if _, _, err := questionRubricSegmentSteps([]SolutionStep{{Content: "甲"}}, []QuestionRubricSolutionSegment{{Spans: make([]QuestionRubricSolutionSpan, maxQuestionRubricSpans+1)}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("too many spans accepted: %v", err)
	}
}

func TestQuestionRubricDraftUsesTeacherPartitionWithoutChangingSavedSolution(t *testing.T) {
	store, question := questionDraftFixture(t)
	question.Solution.Steps[1].Content = "  移项\n得出结果  "
	question.Solution.RawText = "过期的识别原文"
	store.questions[question.ID] = question
	segments := []QuestionRubricSolutionSegment{
		questionRubricSegment(QuestionRubricSolutionSpan{SourceID: "step-2", Start: 0, End: 3}),
		questionRubricSegment(QuestionRubricSolutionSpan{SourceID: "step-2", Start: 3, End: 7}),
	}
	server := questionDraftServer(t, func(_ QuestionCandidate, solution SolutionCandidate, _ *DocumentModelConfig) {
		if !reflect.DeepEqual(solution.Steps, []SolutionStep{{StepNo: 1, Content: "移项\n"}, {StepNo: 2, Content: "得出结果"}}) || solution.RawText != "移项\n得出结果" {
			t.Errorf("teacher partition was not rebuilt from saved solution: %#v", solution)
		}
	})
	defer server.Close()
	service := &DocumentImportService{parser: NewHTTPDocumentParser(server.URL, strings.Repeat("t", 32), time.Second)}
	result, err := suggestQuestionMathRubricDraft(context.Background(), store, service, "tenant", "exam", question.ID, QuestionMathRubricDraftInput{ExpectedQuestion: &question, SolutionSegments: segments})
	if err != nil || result.SuggestedRubricCandidates[0].Points[0].EvidenceStepIDs[0] != "step-1" {
		t.Fatalf("suggestion did not use newly numbered partition: %#v %v", result, err)
	}
	stored, _ := store.ListQuestions(context.Background(), "tenant", "exam")
	if !reflect.DeepEqual(stored[0].Solution, question.Solution) || stored[0].Rubric != nil {
		t.Fatal("read-only partition inference changed the saved solution or rubric")
	}
}

func TestQuestionRubricDraftPartitionsRawFallbackFormulaWithStableSourceIDs(t *testing.T) {
	store, question := questionDraftFixture(t)
	formula := "$$\n\\begin{aligned}\nx&=1\\\\\ny&=2\n\\end{aligned}\n$$"
	question.Solution.Steps = []SolutionStep{{Content: " "}, {Content: "\n"}}
	question.Solution.RawText = "\n说明🧮\n" + formula + "\n结论\n"
	store.questions[question.ID] = question
	cut := len(utf16.Encode([]rune(formula[:strings.Index(formula, "y&=2")])))
	formulaLength := len(utf16.Encode([]rune(formula)))
	segments := []QuestionRubricSolutionSegment{
		questionRubricSegment(QuestionRubricSolutionSpan{SourceID: "step-1", Start: 0, End: 4}),
		questionRubricSegment(QuestionRubricSolutionSpan{SourceID: "step-2", Start: 0, End: cut}),
		questionRubricSegment(QuestionRubricSolutionSpan{SourceID: "step-2", Start: cut, End: formulaLength}, QuestionRubricSolutionSpan{SourceID: "step-3", Start: 0, End: 2}),
	}
	server := questionDraftServer(t, func(_ QuestionCandidate, solution SolutionCandidate, _ *DocumentModelConfig) {
		if len(solution.Steps) != 3 || solution.Steps[0].Content != "说明🧮" || solution.Steps[1].Content+strings.Split(solution.Steps[2].Content, "\n\n结论")[0] != formula || solution.RawText != "说明🧮\n\n"+formula+"\n\n结论" {
			t.Errorf("raw fallback source IDs or formula context changed: %#v", solution)
		}
	})
	defer server.Close()
	service := &DocumentImportService{parser: NewHTTPDocumentParser(server.URL, strings.Repeat("t", 32), time.Second)}
	if _, err := suggestQuestionMathRubricDraft(context.Background(), store, service, "tenant", "exam", question.ID, QuestionMathRubricDraftInput{ExpectedQuestion: &question, SolutionSegments: segments}); err != nil {
		t.Fatalf("raw fallback formula partition failed: %v", err)
	}
}
