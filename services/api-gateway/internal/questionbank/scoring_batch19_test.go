package questionbank

import (
	"math"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func scoringFixture() Scoring {
	return Scoring{
		Answer:   &paper.AnswerKeyInput{StandardAnswer: "A"},
		Solution: &paper.SolutionInput{RawText: "solution"},
		Rubric:   &paper.RubricInput{Status: "draft", MaxScore: 2.5, Points: []paper.RubricPoint{{ID: "p1", Description: "point", Score: 2.5}}},
	}
}

func TestNormalizeScoringPreservesCopyAndCollectionDefaults(t *testing.T) {
	input := scoringFixture()
	got, err := normalizeScoring(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.UsePolicy != "practice_only" || got.Assets == nil || got.Answer.EquivalentAnswers == nil || got.Answer.Tolerance == nil ||
		got.Solution.Steps == nil || got.Solution.SourceRefs == nil || got.Rubric.Status != "approved" || got.Rubric.Deductions == nil || got.Rubric.Examples == nil {
		t.Fatalf("normalization defaults missing: %+v", got)
	}
	got.Answer.StandardAnswer = "B"
	got.Solution.RawText = "changed"
	got.Rubric.Points[0].Score = 1
	if input.Answer.StandardAnswer != "A" || input.Answer.EquivalentAnswers != nil || input.Answer.Tolerance != nil || input.Solution.RawText != "solution" ||
		input.Solution.Steps != nil || input.Rubric.Status != "draft" || input.Rubric.Points[0].Score != 2.5 || input.Rubric.Deductions != nil {
		t.Fatal("normalization changed caller scoring")
	}
}

func TestNormalizeScoringStopsAtFailingSection(t *testing.T) {
	asset := Asset{FileAssetID: "00000000-0000-0000-0000-000000000123", SHA256: strings.Repeat("a", 64), Name: "image.png", ContentType: "image/png"}
	cases := []scoringNormalizationFailureCase{
		{"duplicate attachment", func(s *Scoring) { s.Assets = []Asset{asset, asset} }, "assets"},
		{"invalid attachment digest", func(s *Scoring) { a := asset; a.SHA256 = strings.Repeat("z", 64); s.Assets = []Asset{a} }, "assets"},
		{"unknown tolerance key", func(s *Scoring) { s.Answer.Tolerance = map[string]any{"other": 0} }, "answer"},
		{"negative tolerance", func(s *Scoring) { s.Answer.Tolerance = map[string]any{"absolute": -.1} }, "answer"},
		{"non numeric tolerance", func(s *Scoring) { s.Answer.Tolerance = map[string]any{"relative": "0.1"} }, "answer"},
		{"unordered solution steps", func(s *Scoring) { s.Solution.Steps = []paper.SolutionStep{{StepNo: 2, Content: "second"}} }, "solution"},
		{"duplicate rubric point", func(s *Scoring) { s.Rubric.Points = append(s.Rubric.Points, s.Rubric.Points[0]) }, "rubric"},
		{"fractional score precision", func(s *Scoring) { s.Rubric.Points[0].Score = .001 }, "rubric"},
		{"unknown rubric evidence", func(s *Scoring) {
			s.Rubric.Points[0].EvidenceRequirements = []paper.EvidenceRequirement{{Type: "unknown"}}
		}, "rubric"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertScoringNormalizationStopsAtSection(t, tc)
		})
	}
}

func publishableFixture() Version {
	content := fixtureContent()
	content.Metadata.Copyright = "owned"
	return Version{Content: content, Scoring: Scoring{UsePolicy: "exam_allowed", Answer: &paper.AnswerKeyInput{StandardAnswer: "A"}}}
}

func TestPublishableValidatesCurrentTypeAndEveryEquivalentAnswer(t *testing.T) {
	cases := []struct {
		name       string
		archetype  string
		kind       string
		answer     any
		equivalent []any
		allowed    bool
	}{
		{"single option", "selected_response", "single_choice", "A", []any{"B"}, true},
		{"option outside current list", "selected_response", "single_choice", "C", nil, false},
		{"equivalent option outside list", "selected_response", "single_choice", "A", []any{"C"}, false},
		{"non string equivalent option", "selected_response", "single_choice", "A", []any{float64(1)}, false},
		{"distinct multiple options", "selected_response", "multiple_choice", []any{"A", "B"}, nil, true},
		{"duplicate multiple options", "selected_response", "multiple_choice", []any{"A", "A"}, nil, false},
		{"empty multiple options", "selected_response", "multiple_choice", []any{}, nil, false},
		{"boolean answer", "selected_response", "true_false", false, []any{true}, true},
		{"boolean text rejected", "selected_response", "true_false", "false", nil, false},
		{"exact text", "exact_text", "fill_blank", "answer", nil, true},
		{"empty equivalent text", "exact_text", "fill_blank", "answer", []any{" "}, false},
		{"numeric value", "numeric_expression", "numeric", float64(2.5), []any{"5/2"}, true},
		{"numeric expression text", "numeric_expression", "formula", "x+1", nil, true},
		{"non finite numeric value", "numeric_expression", "numeric", math.NaN(), nil, false},
		{"infinite equivalent numeric value", "numeric_expression", "numeric", float64(2.5), []any{math.Inf(1)}, false},
		{"empty numeric text", "numeric_expression", "numeric", " ", nil, false},
		{"unsupported numeric answer", "numeric_expression", "numeric", true, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := publishableFixture()
			v.AssessmentArchetype, v.QuestionType = tc.archetype, tc.kind
			v.Scoring.Answer.StandardAnswer, v.Scoring.Answer.EquivalentAnswers = tc.answer, tc.equivalent
			err := publishable(v, "question")
			if (err == nil) != tc.allowed || (!tc.allowed && err != ErrInvalidInput) {
				t.Fatalf("publication error=%v, allowed=%v", err, tc.allowed)
			}
		})
	}
}

func TestPublishablePreservesPolicyToleranceAndRubricGates(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		mutate  func(*Version)
		allowed bool
	}{
		{"unmapped grade", "question", func(v *Version) { v.Metadata.GradeScope = "unmapped" }, false},
		{"unknown copyright", "question", func(v *Version) { v.Metadata.Copyright = "unknown" }, false},
		{"practice only", "question", func(v *Version) { v.Scoring.UsePolicy = "practice_only" }, false},
		{"missing objective answer", "question", func(v *Version) { v.Scoring.Answer = nil }, false},
		{"non numeric tolerance", "question", func(v *Version) { v.Scoring.Answer.Tolerance = map[string]any{"absolute": .1} }, false},
		{"numeric tolerance", "question", func(v *Version) {
			v.AssessmentArchetype = "numeric_expression"
			v.Scoring.Answer.StandardAnswer = float64(2.5)
			v.Scoring.Answer.Tolerance = map[string]any{"relative": .1}
		}, true},
		{"missing constructed rubric", "question", func(v *Version) { v.AssessmentArchetype = "short_constructed" }, false},
		{"aligned constructed rubric", "question", func(v *Version) {
			v.AssessmentArchetype = "short_constructed"
			v.Scoring.Rubric = scoringFixture().Rubric
		}, true},
		{"rubric total mismatch", "question", func(v *Version) { v.Scoring.Rubric = scoringFixture().Rubric; v.Scoring.Rubric.Points[0].Score = 2.4 }, false},
		{"rubric max mismatch", "question", func(v *Version) { v.Scoring.Rubric = scoringFixture().Rubric; v.Scoring.Rubric.MaxScore = 3 }, false},
		{"missing template rubric", "rubric_template", func(v *Version) {}, false},
		{"template uses rubric facts", "rubric_template", func(v *Version) {
			v.Scoring.Answer.StandardAnswer = "outside options"
			v.Scoring.Rubric = scoringFixture().Rubric
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := publishableFixture()
			tc.mutate(&v)
			err := publishable(v, tc.kind)
			if (err == nil) != tc.allowed || (!tc.allowed && err != ErrInvalidInput) {
				t.Fatalf("publication error=%v, allowed=%v", err, tc.allowed)
			}
		})
	}
}

func TestScoringPublicationPreservesTransitionFailureOrder(t *testing.T) {
	v := publishableFixture()
	v.Revision, v.BundleHash, v.AuthorID, v.WorkflowStatus = 3, "current-bundle", "author", "draft"
	in := ReviewInput{ExpectedRevision: 3, BundleHash: "current-bundle"}
	if _, err := nextStatus(v, "question", "reviewer", "publish", in); err != ErrLocked {
		t.Fatalf("draft publication error=%v, want locked", err)
	}
	if _, err := nextStatus(v, "question", "author", "approve", in); err != ErrInvalidInput {
		t.Fatalf("self approval error=%v, want invalid input before state check", err)
	}
	v.WorkflowStatus = "approved"
	v.Scoring.Answer.StandardAnswer = "C"
	if _, err := nextStatus(v, "question", "reviewer", "publish", in); err != ErrInvalidInput {
		t.Fatalf("invalid approved answer error=%v, want invalid input", err)
	}
	in.ExpectedRevision = 2
	if _, err := nextStatus(v, "question", "author", "approve", in); err != ErrConflict {
		t.Fatalf("stale self approval error=%v, want conflict before author check", err)
	}
}

type scoringNormalizationFailureCase struct {
	name   string
	mutate func(*Scoring)
	stage  string
}

func assertScoringNormalizationStopsAtSection(t *testing.T, tc scoringNormalizationFailureCase) {
	t.Helper()
	input := scoringFixture()
	tc.mutate(&input)
	got, err := normalizeScoring(input)
	if err != ErrInvalidInput {
		t.Fatalf("error=%v, want invalid input", err)
	}
	// 失败前的集合默认值保留；后续评分部分不执行，且调用方原始对象不受影响。
	if tc.stage == "assets" && got.Answer.EquivalentAnswers != nil {
		t.Fatal("attachment failure normalized answers")
	}
	if tc.stage != "assets" && got.Answer.EquivalentAnswers == nil {
		t.Fatal("earlier answer defaults were lost")
	}
	if tc.stage == "answer" && got.Solution.Steps != nil {
		t.Fatal("answer failure normalized solution")
	}
	if tc.stage != "rubric" && got.Rubric.Status != "draft" {
		t.Fatal("earlier failure normalized rubric")
	}
	if tc.stage == "rubric" && (got.Rubric.Status != "approved" || got.Rubric.Deductions == nil || got.Rubric.Examples == nil) {
		t.Fatal("rubric failure lost its earlier defaults")
	}
	if input.Answer.EquivalentAnswers != nil || input.Rubric.Status != "draft" {
		t.Fatal("failed normalization changed caller scoring")
	}
}
