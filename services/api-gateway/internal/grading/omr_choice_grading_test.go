package grading

import (
	"math"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func confirmedOMRChoiceContext(kind string, confidence float64, selected []string) Context {
	return Context{SegmentID: "segment-1", Question: paper.Question{ID: "q1", QuestionType: kind, Score: 6},
		AnswerKey: paper.AnswerKey{StandardAnswer: []any{"A", "C"}, Tolerance: map[string]any{"allow_partial": true, "score_per_correct_option": 2.0}},
		Answer:    SegmentAnswer{Source: "omr", Confidence: &confidence, AnswerPayload: map[string]any{"answers": selected, "presence": "present"}}}
}

func TestConfirmedOMRChoiceUsesFrozenThresholdAndKeepsActualConfidenceAndScoring(t *testing.T) {
	for _, test := range []struct {
		kind       string
		confidence float64
		selected   []string
	}{
		{"single_choice", .8155, []string{"A"}},
		{"multiple_choice", .8155, []string{"A", "C"}},
		{"multiple_choice", .855, []string{"A"}},
		{"multiple_choice", .855, []string{"A", "B"}},
	} {
		t.Run(test.kind+stringify(test.selected), func(t *testing.T) {
			ctx := confirmedOMRChoiceContext(test.kind, test.confidence, test.selected)
			if test.kind == "single_choice" {
				ctx.AnswerKey.StandardAnswer = "A"
			}
			engine := NewEngine()
			grade, err := engine.gradeConfirmedOMRChoice(ctx, paper.DefaultOMRAutoConfirmMinimumConfidence)
			if err != nil || !grade.AutoPass || grade.NeedsHumanReview || grade.Confidence != test.confidence {
				t.Fatalf("accepted OMR result was blocked or its strength changed: %#v err=%v", grade, err)
			}
			original := engine.gradeMultipleChoice(ctx)
			if test.kind == "single_choice" {
				original = engine.gradeSingleChoice(ctx)
			}
			if grade.SuggestedScore != original.SuggestedScore || !reflect.DeepEqual(grade.MatchedPoints, original.MatchedPoints) || !reflect.DeepEqual(grade.MissingPoints, original.MissingPoints) || !reflect.DeepEqual(grade.RiskFlags, original.RiskFlags) {
				t.Fatalf("OMR acceptance changed answer-key scoring: original=%#v got=%#v", original, grade)
			}
		})
	}
}

func TestConfirmedOMRChoiceDoesNotChangeOtherInputsOrHideRuleRisks(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Context)
		min    float64
	}{
		{"below default threshold", func(ctx *Context) { *ctx.Answer.Confidence = .79 }, .8},
		{"higher frozen threshold", func(*Context) {}, .98},
		{"ordinary OCR", func(ctx *Context) { ctx.Answer.Source = "ocr_text" }, .8},
		{"fill answer", func(ctx *Context) {
			ctx.Question.QuestionType = "fill_blank"
			ctx.AnswerKey.StandardAnswer = "A"
			ctx.Answer.AnswerText = "A"
		}, .8},
		{"missing confidence", func(ctx *Context) { ctx.Answer.Confidence = nil }, .8},
		{"unparseable multiple choice", func(ctx *Context) { ctx.Answer.AnswerPayload["answers"] = []string{} }, .8},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := confirmedOMRChoiceContext("multiple_choice", .855, []string{"A"})
			test.change(&ctx)
			grade, err := NewEngine().gradeConfirmedOMRChoice(ctx, test.min)
			if err != nil || grade.AutoPass || !grade.NeedsHumanReview {
				t.Fatalf("unaccepted input bypassed its original boundary: %#v err=%v", grade, err)
			}
		})
	}
	for _, threshold := range []float64{math.NaN(), math.Inf(1), -.1, 1.1} {
		if _, err := NewEngine().gradeConfirmedOMRChoice(confirmedOMRChoiceContext("multiple_choice", .855, []string{"A"}), threshold); err != ErrInvalidInput {
			t.Fatalf("invalid frozen threshold accepted: %v err=%v", threshold, err)
		}
	}
}

func TestDefaultOMRChoiceQualityKeepsAnnotationOverlapInReviewAndAcceptsClearSetsAndBlank(t *testing.T) {
	for _, test := range []struct {
		name           string
		confidence     float64
		classification string
		selected       []string
		workerReview   bool
		wantAccepted   bool
	}{
		{"question 9 annotation overlap", .3033, "annotation_overlap", []string{"B", "C"}, true, false},
		{"question 10 clear", .8155, "clear", []string{"A", "C"}, false, true},
		{"question 11 clear", .855, "clear", []string{"A"}, false, true},
		{"low strength clear", .79, "clear", []string{"A"}, false, false},
		{"clear blank", .855, "blank", []string{}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			risks := []string{}
			if test.workerReview {
				risks = append(risks, test.classification)
			}
			quality := map[string]any{"version": "omr-set-quality-v1", "question_type": "multiple_choice", "classification": test.classification,
				"option_count": 4, "selected_count": len(test.selected), "set_confidence": test.confidence, "risk_flags": risks}
			input := OMRResultInput{Decision: "selected", Selected: test.selected, Confidence: test.confidence, NeedsHumanReview: test.workerReview, Thresholds: map[string]any{"quality": quality}}
			ctx := confirmedOMRChoiceContext("multiple_choice", test.confidence, test.selected)
			if test.classification == "blank" {
				input.Decision = "blank"
				ctx.Answer.AnswerPayload["presence"] = "blank"
			}
			accepted := omrResultCanAutoConfirmForQuestion(input, ctx.Question.QuestionType, true, paper.DefaultOMRAutoConfirmMinimumConfidence)
			if accepted != test.wantAccepted {
				t.Fatalf("quality acceptance=%v want=%v", accepted, test.wantAccepted)
			}
			if accepted {
				grade, err := NewEngine().gradeConfirmedOMRChoice(ctx, paper.DefaultOMRAutoConfirmMinimumConfidence)
				if err != nil || !grade.AutoPass || grade.NeedsHumanReview || grade.Confidence != test.confidence {
					t.Fatalf("quality-accepted result reblocked: %#v err=%v", grade, err)
				}
				if input.Decision == "blank" && grade.SuggestedScore != 0 {
					t.Fatalf("explicit blank did not receive zero: %#v", grade)
				}
			}
		})
	}
}
