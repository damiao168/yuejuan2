package grading_test

import (
	"math"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestEngineGradesSupportedQuestionTypes(t *testing.T) {
	tests := []struct {
		name          string
		kind          string
		standard      any
		equiv         []any
		tolerance     any
		answerText    string
		answerPayload map[string]any
		wantScore     float64
		wantReview    bool
		wantRisk      string
	}{
		{
			name:       "single choice exact",
			kind:       "single_choice",
			standard:   "B",
			answerText: "b",
			wantScore:  5,
		},
		{
			name:       "true false exact",
			kind:       "true_false",
			standard:   true,
			answerText: "正确",
			wantScore:  5,
		},
		{
			name:       "multiple choice wrong option zero",
			kind:       "multiple_choice",
			standard:   []any{"A", "C", "D"},
			answerText: "A,B",
			wantScore:  0,
		},
		{
			name:       "multiple choice partial proportional",
			kind:       "multiple_choice",
			standard:   []any{"A", "C", "D"},
			tolerance:  map[string]any{"allow_partial": true},
			answerText: "A,C",
			wantScore:  10.0 / 3.0 * 2.0,
		},
		{name: "recognized compact multiple choice", kind: "multiple_choice", standard: "BC", answerText: "C,B", wantScore: 10},
		{name: "compact student multiple choice", kind: "multiple_choice", standard: []any{"B", "C"}, answerText: "BC", wantScore: 10},
		{name: "compact choice does not allow missing options", kind: "multiple_choice", standard: "BC", answerText: "B", wantScore: 0},
		{name: "recognized latex fraction", kind: "fill_blank", standard: `$\frac{10}{11}$`, answerText: "10/11", wantScore: 5},
		{name: "recognized latex interval", kind: "fill_blank", standard: `$(0,1)$`, answerText: "(0,1)", wantScore: 5},
		{name: "interval endpoints remain different", kind: "fill_blank", standard: `$(0,1)$`, answerText: "[0,1]", wantScore: 0},
		{name: "decimal equivalence requires configuration", kind: "fill_blank", standard: `$\frac{1}{2}$`, answerText: "0.5", wantScore: 0},
		{
			name:       "fill blank equivalent normalized",
			kind:       "fill_blank",
			standard:   "F=ma",
			equiv:      []any{"force equals mass times acceleration"},
			tolerance:  map[string]any{"ignore_case": true, "ignore_spaces": true},
			answerText: "Force Equals Mass Times Acceleration",
			wantScore:  5,
		},
		{
			name:       "numeric tolerance with unit",
			kind:       "numeric",
			standard:   map[string]any{"value": 9.8, "unit": "m/s2"},
			tolerance:  map[string]any{"absolute": 0.05, "unit_required": true},
			answerText: "9.81m/s2",
			wantScore:  5,
		},
		{
			name:       "numeric parse failed requires review",
			kind:       "numeric",
			standard:   9.8,
			answerText: "about ten",
			wantScore:  0,
			wantReview: true,
			wantRisk:   "numeric_parse_failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := gradingContext(tt.kind, tt.standard, tt.equiv, tt.tolerance, tt.answerText, tt.answerPayload)
			grade, err := grading.NewEngine().Grade(ctx)
			if err != nil {
				t.Fatalf("grade: %v", err)
			}
			assertSupportedRuleGrade(t, grade, tt.wantScore, tt.wantReview, tt.wantRisk)
		})
	}
}

func TestEngineRejectsUnsupportedQuestionType(t *testing.T) {
	ctx := gradingContext("essay", "expected", nil, nil, "answer", nil)
	if _, err := grading.NewEngine().Grade(ctx); err != grading.ErrUnsupportedQuestionType {
		t.Fatalf("expected unsupported type error, got %v", err)
	}
}

func TestEngineRoutesLowConfidenceMachineAnswerToReview(t *testing.T) {
	ctx := gradingContext("single_choice", "B", nil, nil, "B", nil)
	confidence := 0.72
	ctx.Answer.Source = "ocr_text"
	ctx.Answer.Confidence = &confidence
	grade, err := grading.NewEngine().Grade(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !grade.NeedsHumanReview || grade.AutoPass || grade.Confidence != confidence || !contains(grade.RiskFlags, "answer_low_confidence") {
		t.Fatalf("low confidence machine input must not auto pass: %#v", grade)
	}
}

func TestEngineNormalizesExplicitFillRules(t *testing.T) {
	ctx := gradingContext("fill_blank", "Ａ，B", nil, map[string]any{
		"ignore_case": true, "ignore_spaces": true, "ignore_punctuation": true,
	}, "a b", nil)
	grade, err := grading.NewEngine().Grade(ctx)
	if err != nil || grade.SuggestedScore != grade.MaxScore {
		t.Fatalf("explicit NFKC and punctuation rules should match: grade=%#v err=%v", grade, err)
	}
}

func TestEngineNumericFractionPercentAndRelativeTolerance(t *testing.T) {
	tests := []struct {
		expected  any
		answer    string
		tolerance any
	}{
		{expected: 0.5, answer: "1/2", tolerance: map[string]any{"absolute": 0.0001}},
		{expected: 0.5, answer: "50%", tolerance: map[string]any{"absolute": 0.0001}},
		{expected: 100.0, answer: "104", tolerance: map[string]any{"relative": 0.05}},
	}
	for _, tt := range tests {
		ctx := gradingContext("numeric", tt.expected, nil, tt.tolerance, tt.answer, nil)
		grade, err := grading.NewEngine().Grade(ctx)
		if err != nil || grade.SuggestedScore != grade.MaxScore {
			t.Fatalf("numeric format should match: %#v grade=%#v err=%v", tt, grade, err)
		}
	}
}

func gradingContext(kind string, standard any, equiv []any, tolerance any, answerText string, payload map[string]any) grading.Context {
	if payload == nil {
		payload = map[string]any{}
	}
	score := 5.0
	if kind == "multiple_choice" {
		score = 10
	}
	return grading.Context{
		SegmentID: "segment-1",
		Question: paper.Question{
			ID:           "question-1",
			TenantID:     tenantID,
			ExamID:       "exam-1",
			QuestionNo:   "Q1",
			QuestionType: kind,
			Score:        score,
		},
		AnswerKey: paper.AnswerKey{
			ID:                "answer-key-1",
			QuestionID:        "question-1",
			AnswerVersion:     "v1",
			StandardAnswer:    standard,
			EquivalentAnswers: equiv,
			Tolerance:         tolerance,
		},
		Answer: grading.SegmentAnswer{
			ID:              "answer-1",
			TenantID:        tenantID,
			AnswerSegmentID: "segment-1",
			AnswerText:      answerText,
			AnswerPayload:   payload,
			Source:          "manual_entry",
		},
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertSupportedRuleGrade(t *testing.T, grade grading.Grade, wantScore float64, wantReview bool, wantRisk string) {
	t.Helper()
	if math.Abs(grade.SuggestedScore-wantScore) > 0.0001 {
		t.Fatalf("score got %.4f want %.4f in %#v", grade.SuggestedScore, wantScore, grade)
	}
	if grade.SuggestedScore > grade.MaxScore {
		t.Fatalf("score must not exceed max: %#v", grade)
	}
	if grade.NeedsHumanReview != wantReview {
		t.Fatalf("review got %v want %v in %#v", grade.NeedsHumanReview, wantReview, grade)
	}
	if wantRisk != "" && !contains(grade.RiskFlags, wantRisk) {
		t.Fatalf("missing risk %s in %#v", wantRisk, grade.RiskFlags)
	}
	if grade.GraderType != grading.GraderType || grade.RuleVersion != grading.RuleVersion || grade.Mock {
		t.Fatalf("grade source must be truthful rule-based non-mock: %#v", grade)
	}
}
