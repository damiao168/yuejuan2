package grading

import (
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestRouteScoringSegmentSendsManualOnlyTypesToHumanReview(t *testing.T) {
	for _, questionType := range paper.ManualOnlyQuestionTypes() {
		// A manual-only question can be fully configured: published rule,
		// recorded answer, option regions. None of that gives it a scoring path.
		route := routeScoringSegment(questionType, true, true, 4)
		if route.Kind != scoringRouteManualReview {
			t.Fatalf("%s must be routed to a human, got %#v", questionType, route)
		}
		if route.Reason != reviewReasonManualOnlyQuestionType {
			t.Fatalf("%s must carry an actionable reason, got %q", questionType, route.Reason)
		}
		if route.Source != reviewSourceHumanGraded {
			t.Fatalf("%s is human graded by design, got source %q", questionType, route.Source)
		}
	}
}

func TestRouteScoringSegmentKeepsAutomatedRoutes(t *testing.T) {
	tests := []struct {
		name       string
		kind       string
		rule       bool
		answer     bool
		options    int
		wantKind   string
		wantReason string
	}{
		{name: "omr queued", kind: "single_choice", rule: true, options: 4, wantKind: scoringRouteOMR},
		{name: "rule input queued", kind: "fill_blank", rule: true, answer: true, wantKind: scoringRouteRuleInput},
		{name: "omr without rule", kind: "multiple_choice", options: 4, wantKind: scoringRouteManualReview, wantReason: "scoring_rule_missing"},
		{name: "omr without option regions", kind: "true_false", rule: true, options: 1, wantKind: scoringRouteManualReview, wantReason: "omr_option_regions_missing"},
		{name: "rule input without answer", kind: "numeric", rule: true, wantKind: scoringRouteManualReview, wantReason: "rule_review_required"},
		{name: "ai assisted subjective", kind: "essay", rule: true, answer: true, wantKind: scoringRouteManualReview, wantReason: "rule_review_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := routeScoringSegment(tt.kind, tt.rule, tt.answer, tt.options)
			if route.Kind != tt.wantKind || route.Reason != tt.wantReason {
				t.Fatalf("got %#v want kind %q reason %q", route, tt.wantKind, tt.wantReason)
			}
			if tt.wantKind == scoringRouteManualReview && route.Source != reviewSourceRuleReview {
				t.Fatalf("automated types keep the rule review queue, got %q", route.Source)
			}
		})
	}
}

func TestFrozenScoringModeOverridesAvailableRule(t *testing.T) {
	for _, mode := range []string{"HUMAN_PRIMARY", "DUAL_HUMAN", "MANUAL_ONLY"} {
		route := routeScoringSegmentForMode("fill_blank", mode, true, true, 0)
		if route.Kind != scoringRouteManualReview || route.Source != reviewSourceHumanGraded {
			t.Fatalf("mode %s must not auto-confirm via a published rule: %#v", mode, route)
		}
	}
	if route := routeScoringSegmentForMode("essay", "AI_ASSIST", true, true, 0); route.Kind != scoringRouteManualReview || route.Reason != "ai_assist_teacher_confirmation" {
		t.Fatalf("AI suggestion must remain teacher-review-only: %#v", route)
	}
	if route := routeScoringSegmentForMode("fill_blank", "RULE_AUTO", true, true, 0); route.Kind != scoringRouteRuleInput {
		t.Fatalf("frozen rule mode must retain rule path: %#v", route)
	}
}

// TestScoringRunWithFormulaQuestionCompletes covers the exam-day failure this
// routing exists for: a paper whose formula question passed the readiness gate
// must produce a started run with human work, not a failed run.
func TestScoringRunWithFormulaQuestionCompletes(t *testing.T) {
	segments := []struct {
		kind    string
		rule    bool
		answer  bool
		options int
	}{
		{kind: "single_choice", rule: true, options: 4},
		{kind: "formula", rule: true, answer: true},
		{kind: "formula", rule: true, answer: true},
	}
	queued, review := 0, 0
	reasons := []string{}
	for _, segment := range segments {
		switch route := routeScoringSegment(segment.kind, segment.rule, segment.answer, segment.options); route.Kind {
		case scoringRouteOMR:
			queued++
		case scoringRouteRuleInput:
		default:
			review++
			reasons = append(reasons, route.Reason)
		}
	}
	if queued != 1 || review != 2 {
		t.Fatalf("expected one queued segment and two human tasks, got queued=%d review=%d", queued, review)
	}
	for _, reason := range reasons {
		if reason != reviewReasonManualOnlyQuestionType {
			t.Fatalf("formula segments must state why they are human graded, got %q", reason)
		}
	}
	if status := scoringRunStatusForCounts(len(segments), queued, review); status != "processing" {
		t.Fatalf("a run with queued automated work stays processing, got %q", status)
	}
	if status := scoringRunStatusForCounts(2, 0, 2); status != "needs_review" {
		t.Fatalf("a formula-only run must wait for humans instead of failing, got %q", status)
	}
	if status := scoringRunStatusForCounts(0, 0, 0); status != "failed" {
		t.Fatalf("a run with nothing to score is still a failure, got %q", status)
	}
}

func TestManualReviewRouteForGradeErrorReplacesRunFailure(t *testing.T) {
	engine := NewEngine()
	segment := Context{
		SegmentID: "segment-1",
		Question:  paper.Question{ID: "question-1", TenantID: "tenant-1", ExamID: "exam-1", QuestionNo: "Q7", QuestionType: "formula", Score: 12},
		AnswerKey: paper.AnswerKey{ID: "key-1", QuestionID: "question-1", AnswerVersion: "v1", StandardAnswer: "F=ma"},
		Answer:    SegmentAnswer{TenantID: "tenant-1", AnswerSegmentID: "segment-1", AnswerText: "F=ma", Source: "manual_entry"},
	}
	_, err := engine.Grade(segment)
	if !errors.Is(err, ErrUnsupportedQuestionType) {
		t.Fatalf("formula still has no rule grading path, got %v", err)
	}
	route, routable := manualReviewRouteForGradeError(err, segment.Question.QuestionType)
	if !routable {
		t.Fatal("an unsupported question type must not abort the whole scoring run")
	}
	if route.Kind != scoringRouteManualReview || route.Reason != reviewReasonManualOnlyQuestionType || route.Source != reviewSourceHumanGraded {
		t.Fatalf("unexpected route for a formula candidate: %#v", route)
	}

	route, routable = manualReviewRouteForGradeError(ErrUnsupportedQuestionType, "essay")
	if !routable || route.Reason != reviewReasonRuleGradingUnsupported {
		t.Fatalf("a subjective type reaching rule grading needs its own reason, got %#v routable=%v", route, routable)
	}

	if _, routable = manualReviewRouteForGradeError(ErrAnswerKeyMissing, "formula"); routable {
		t.Fatal("only unsupported question types may be downgraded to a review task")
	}
}

// TestRuleGradedQuestionTypesMatchEngine guards the readiness advisory against
// drift: paper.HasAutomatedScoringPath decides what the exam owner is told, and
// this engine decides what actually happens.
func TestRuleGradedQuestionTypesMatchEngine(t *testing.T) {
	engine := NewEngine()
	grade := func(questionType string) error {
		_, err := engine.Grade(Context{
			SegmentID: "segment-1",
			Question:  paper.Question{ID: "question-1", QuestionNo: "Q1", QuestionType: questionType, Score: 5},
			AnswerKey: paper.AnswerKey{ID: "key-1", QuestionID: "question-1", StandardAnswer: "A"},
			Answer:    SegmentAnswer{AnswerSegmentID: "segment-1", AnswerText: "A", Source: "manual_entry"},
		})
		return err
	}
	for _, questionType := range paper.RuleGradedQuestionTypes() {
		if err := grade(questionType); err != nil {
			t.Fatalf("%q is advertised as rule graded but the engine refused it: %v", questionType, err)
		}
	}
	for _, questionType := range paper.ManualOnlyQuestionTypes() {
		if err := grade(questionType); !errors.Is(err, ErrUnsupportedQuestionType) {
			t.Fatalf("%q is advertised as human graded but the engine scored it: %v", questionType, err)
		}
	}
}
