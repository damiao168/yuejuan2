package paper

import (
	"strings"
	"testing"
)

func advisoryQuestion(id string, no string, kind string, score float64, rubric bool) Question {
	question := Question{
		ID:           id,
		TenantID:     "tenant-1",
		ExamID:       "exam-1",
		QuestionNo:   no,
		QuestionType: kind,
		Score:        score,
		AnswerKey:    &AnswerKey{ID: id + "-key", QuestionID: id, AnswerVersion: "v1", StandardAnswer: "A"},
	}
	if rubric {
		question.Rubric = &Rubric{ID: id + "-rubric", QuestionID: id, Version: "v1", Status: "locked", MaxScore: score, Points: []RubricPoint{{ID: "p1", Description: "解题步骤", Score: score}}}
	}
	return question
}

func advisoryReadiness(t *testing.T, questions []Question) ReadinessResult {
	t.Helper()
	total := 0.0
	regions := []LayoutRegion{}
	for index, question := range questions {
		total += question.Score
		regions = append(regions, LayoutRegion{ID: question.ID + "-region", QuestionID: question.ID, X: 0.1, Y: 0.05 + float64(index)*0.1, Width: 0.8, Height: 0.08})
	}
	papers := []Paper{{ID: "paper-1", TenantID: "tenant-1", ExamID: "exam-1", FileAssetID: "file-1", VersionNo: 1, Status: "ready"}}
	templates := []AnswerSheetTemplate{{ID: "template-1", TenantID: "tenant-1", ExamID: "exam-1", VersionNo: 1, Status: "locked", PageCount: 1, Layout: TemplateLayout{Pages: []TemplatePage{{PageNo: 1, Width: 1000, Height: 1400, QuestionRegions: regions}}}}}
	return buildReadiness(total, 1, 30, papers, questions, templates)
}

func TestReadinessAdvisesManualOnlyQuestionTypesWithoutBlocking(t *testing.T) {
	result := advisoryReadiness(t, []Question{
		advisoryQuestion("question-1", "Q1", "single_choice", 5, false),
		advisoryQuestion("question-2", "Q2", "essay", 15, true),
		advisoryQuestion("question-3", "Q3", "formula", 12, true),
	})

	if !result.Ready {
		t.Fatalf("a paper with a formula question must still pass the blocking checks: %#v", result.Checks)
	}
	if len(result.Advisories) != 1 {
		t.Fatalf("expected one advisory, got %#v", result.Advisories)
	}
	advisory := result.Advisories[0]
	if advisory.Code != "manual_only_question_types" || advisory.Severity != "warning" || advisory.Section != "questions" {
		t.Fatalf("unexpected advisory envelope: %#v", advisory)
	}
	if len(advisory.Questions) != 1 || advisory.Questions[0].QuestionID != "question-3" || advisory.Questions[0].QuestionType != "formula" {
		t.Fatalf("advisory must name the formula question only: %#v", advisory.Questions)
	}
	if advisory.Score != 12 {
		t.Fatalf("advisory must report the human-graded score, got %v", advisory.Score)
	}
	if !strings.Contains(advisory.Message, "Q3") || !strings.Contains(advisory.Message, "12.00") {
		t.Fatalf("advisory message must be actionable, got %q", advisory.Message)
	}
	for _, check := range result.Checks {
		if check.Code == advisory.Code {
			t.Fatal("the advisory must not be published as a blocking readiness check")
		}
	}
}

func TestReadinessHasNoAdvisoryWhenEveryTypeIsScored(t *testing.T) {
	result := advisoryReadiness(t, []Question{
		advisoryQuestion("question-1", "Q1", "single_choice", 5, false),
		advisoryQuestion("question-2", "Q2", "numeric", 5, false),
		advisoryQuestion("question-3", "Q3", "short_answer", 10, true),
	})

	if !result.Ready {
		t.Fatalf("expected a ready configuration: %#v", result.Checks)
	}
	if len(result.Advisories) != 0 {
		t.Fatalf("expected no advisory, got %#v", result.Advisories)
	}
}

func TestReadinessAdvisoryDoesNotChangeConfigurationHash(t *testing.T) {
	questions := []Question{advisoryQuestion("question-1", "Q1", "formula", 10, true)}
	first := advisoryReadiness(t, questions)
	second := advisoryReadiness(t, questions)
	if first.ConfigurationHash != second.ConfigurationHash {
		t.Fatal("configuration hash must stay stable so an advisory never invalidates a confirmation")
	}
}

func TestManualOnlyQuestionTypesCoverEveryConfigurableType(t *testing.T) {
	manual := map[string]bool{}
	for _, questionType := range ManualOnlyQuestionTypes() {
		manual[questionType] = true
	}
	if !manual["formula"] || !manual["coding"] {
		t.Fatalf("formula and coding have no scoring path and must be reported as manual only, got %v", ManualOnlyQuestionTypes())
	}
	for questionType := range validQuestionTypes {
		if manual[questionType] == HasAutomatedScoringPath(questionType) {
			t.Fatalf("%q is classified as both manual only and automated", questionType)
		}
	}
	for _, questionType := range append(RuleGradedQuestionTypes(), AIAssistedQuestionTypes()...) {
		if !validQuestionTypes[questionType] {
			t.Fatalf("%q claims an automated scoring path but is not a configurable question type", questionType)
		}
	}
}
