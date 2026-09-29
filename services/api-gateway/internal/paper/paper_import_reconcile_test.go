package paper

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func newReconcileFixture(t *testing.T, questions []CreateQuestionInput) (*MemoryStore, PaperImportJob) {
	t.Helper()
	store := NewMemoryStore()
	ctx := context.Background()
	p, err := store.CreatePaper(ctx, "tenant", "exam", "user", CreatePaperInput{FileAssetID: "paper-file"})
	if err != nil {
		t.Fatal(err)
	}
	var total float64
	for _, input := range questions {
		input.ExamPaperID = p.ID
		total += input.Score
		if _, err := store.CreateQuestion(ctx, "tenant", "exam", "user", input); err != nil {
			t.Fatal(err)
		}
	}
	if len(questions) > 0 {
		store.SetExamTotal("exam", total)
	}
	job, err := store.CreatePaperImport(ctx, "tenant", "exam", "user", CreatePaperImportInput{ExamPaperID: p.ID, PaperFileAssetID: p.FileAssetID, AnswerFileAssetID: "answer-file", Subject: "mathematics"})
	if err != nil {
		t.Fatal(err)
	}
	return store, job
}

func importedDraft(number, kind string, score float64) PaperImportDraftQuestion {
	return PaperImportDraftQuestion{
		QuestionNo: number, QuestionType: kind, Score: score, Stem: "正式题干 " + number,
		KnowledgePoints: []string{"知识点"}, Confidence: .91, Issues: []string{},
		AnswerKey: &AnswerKeyInput{StandardAnswer: "42", EquivalentAnswers: []any{"42"}, Tolerance: map[string]any{}},
		Rubric:    &RubricInput{Status: "draft", MaxScore: score, Points: []RubricPoint{{ID: "p1", Description: "正确", Score: score, Required: true}}},
	}
}

func joinedImportIssues(job PaperImportJob) string {
	parts := append([]string{}, job.Issues...)
	for _, question := range job.Questions {
		parts = append(parts, question.Issues...)
	}
	return strings.Join(parts, " ")
}

func TestNormalizePaperImportQuestionNumber(t *testing.T) {
	for input, expected := range map[string]string{
		"1": "1", "1.": "1", "1、": "1", "（1）": "1", "1(1)": "1(1)", "一": "1", "一、1": "一、1",
	} {
		if actual := normalizePaperImportQuestionNumber(input); actual != expected {
			t.Errorf("normalize %q: got %q want %q", input, actual, expected)
		}
	}
}

func TestPaperImportReconciliationDetectsQuestionSetConflicts(t *testing.T) {
	blueprint := []CreateQuestionInput{
		{QuestionNo: "1", QuestionType: "short_answer", Score: 10, SortOrder: 1},
		{QuestionNo: "2", QuestionType: "short_answer", Score: 10, SortOrder: 2},
	}
	tests := []struct {
		name   string
		drafts []PaperImportDraftQuestion
		code   string
	}{
		{"duplicate normalized number", []PaperImportDraftQuestion{importedDraft("1.", "short_answer", 10), importedDraft("1、", "short_answer", 10)}, "paper_import.duplicate_question"},
		{"missing question", []PaperImportDraftQuestion{importedDraft("1", "short_answer", 10)}, "paper_import.missing_question"},
		{"unexpected question", []PaperImportDraftQuestion{importedDraft("1", "short_answer", 10), importedDraft("2", "short_answer", 10), importedDraft("3", "short_answer", 10)}, "paper_import.unexpected_question"},
		{"answer for nonexistent question", []PaperImportDraftQuestion{importedDraft("1", "short_answer", 10), importedDraft("3", "short_answer", 10)}, "paper_import.unexpected_question"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store, job := newReconcileFixture(t, blueprint)
			preview, err := store.CompletePaperImport(context.Background(), "tenant", job.ID, testCase.drafts, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(joinedImportIssues(preview), testCase.code) {
				t.Fatalf("missing issue %s: %#v", testCase.code, preview)
			}
		})
	}
}

func TestPaperImportReconciliationDetectsQuestionAndRubricConflicts(t *testing.T) {
	blueprint := []CreateQuestionInput{{QuestionNo: "1", QuestionType: "short_answer", Score: 10, SortOrder: 1}}
	tests := []struct {
		name   string
		mutate func(*PaperImportDraftQuestion)
		code   string
	}{
		{"question type", func(draft *PaperImportDraftQuestion) { draft.QuestionType = "essay" }, "paper_import.question_type_mismatch"},
		{"question score", func(draft *PaperImportDraftQuestion) {
			draft.Score, draft.Rubric.MaxScore, draft.Rubric.Points[0].Score = 8, 8, 8
		}, "paper_import.question_score_mismatch"},
		{"rubric max score", func(draft *PaperImportDraftQuestion) { draft.Rubric.MaxScore = 8 }, "paper_import.rubric_max_score_mismatch"},
		{"rubric point total", func(draft *PaperImportDraftQuestion) { draft.Rubric.Points[0].Score = 8 }, "paper_import.rubric_points_score_mismatch"},
		{"missing answer", func(draft *PaperImportDraftQuestion) { draft.AnswerKey = nil }, "paper_import.missing_answer"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store, job := newReconcileFixture(t, blueprint)
			draft := importedDraft("1", "short_answer", 10)
			testCase.mutate(&draft)
			preview, err := store.CompletePaperImport(context.Background(), "tenant", job.ID, []PaperImportDraftQuestion{draft}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(joinedImportIssues(preview), testCase.code) {
				t.Fatalf("missing issue %s: %#v", testCase.code, preview)
			}
		})
	}
}

func TestPaperImportReconciliationChecksExamTotalWithoutExistingQuestions(t *testing.T) {
	store, job := newReconcileFixture(t, nil)
	store.SetExamTotal("exam", 20)
	preview, err := store.CompletePaperImport(context.Background(), "tenant", job.ID, []PaperImportDraftQuestion{importedDraft("1", "short_answer", 10)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(joinedImportIssues(preview), "paper_import.total_score_mismatch") {
		t.Fatalf("exam total mismatch was not reported: %#v", preview)
	}
}

func TestPaperImportValidCompletePaperApplies(t *testing.T) {
	store, job := newReconcileFixture(t, []CreateQuestionInput{
		{QuestionNo: "1", QuestionType: "short_answer", Score: 10, SortOrder: 1},
		{QuestionNo: "2", QuestionType: "short_answer", Score: 10, SortOrder: 2},
	})
	preview, err := store.CompletePaperImport(context.Background(), "tenant", job.ID, []PaperImportDraftQuestion{
		importedDraft("1.", "short_answer", 10), importedDraft("2、", "short_answer", 10),
	}, nil)
	if err != nil || len(preview.Issues) != 0 {
		t.Fatalf("valid paper rejected: %#v err=%v", preview, err)
	}
	if _, err = store.ApplyPaperImport(context.Background(), "tenant", job.ID, "user"); err != nil {
		t.Fatal(err)
	}
	questions, _ := store.ListQuestions(context.Background(), "tenant", "exam")
	if len(questions) != 2 || questions[0].AnswerKey == nil || questions[1].AnswerKey == nil {
		t.Fatalf("valid import was not applied: %#v", questions)
	}
}

func TestPaperImportReconciliationIssuesBlockApply(t *testing.T) {
	store, job := newReconcileFixture(t, []CreateQuestionInput{{QuestionNo: "1", QuestionType: "short_answer", Score: 10, SortOrder: 1}})
	invalid := importedDraft("1", "essay", 10)
	if _, err := store.CompletePaperImport(context.Background(), "tenant", job.ID, []PaperImportDraftQuestion{invalid}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyPaperImport(context.Background(), "tenant", job.ID, "user"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("reconciliation issue bypassed apply gate: %v", err)
	}
	questions, _ := store.ListQuestions(context.Background(), "tenant", "exam")
	if questions[0].Stem != "" || questions[0].AnswerKey != nil {
		t.Fatalf("blocked import mutated canonical question: %#v", questions[0])
	}
}

func TestPaperImportCanApplyAfterCandidateCorrection(t *testing.T) {
	store, job := newReconcileFixture(t, []CreateQuestionInput{{QuestionNo: "1", QuestionType: "short_answer", Score: 10, SortOrder: 1}})
	if _, err := store.CompletePaperImport(context.Background(), "tenant", job.ID, []PaperImportDraftQuestion{importedDraft("2", "short_answer", 10)}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyPaperImport(context.Background(), "tenant", job.ID, "user"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid candidate unexpectedly applied: %v", err)
	}
	corrected, err := store.CompletePaperImport(context.Background(), "tenant", job.ID, []PaperImportDraftQuestion{importedDraft("1", "short_answer", 10)}, nil)
	if err != nil || len(corrected.Issues) != 0 {
		t.Fatalf("corrected candidate did not reconcile: %#v err=%v", corrected, err)
	}
	if _, err := store.ApplyPaperImport(context.Background(), "tenant", job.ID, "user"); err != nil {
		t.Fatalf("corrected candidate could not apply: %v", err)
	}
}

func TestPaperImportBlueprintScoreFillsMissingScoreBeforeRubricValidation(t *testing.T) {
	store, job := newReconcileFixture(t, []CreateQuestionInput{{QuestionNo: "1", QuestionType: "calculation", Score: 11, SortOrder: 1}})
	draft := importedDraft("1", "calculation", 0)
	draft.ScoreSource = "missing"
	draft.Rubric.MaxScore = 11
	draft.Rubric.Points[0].Score = 11
	preview, err := store.CompletePaperImport(context.Background(), "tenant", job.ID, []PaperImportDraftQuestion{draft}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Issues) != 0 || preview.Questions[0].Score != 11 || preview.Questions[0].ScoreSource != "blueprint" {
		t.Fatalf("blueprint score did not clear stale validation issues: %#v", preview)
	}
	if _, err := store.ApplyPaperImport(context.Background(), "tenant", job.ID, "user"); err != nil {
		t.Fatalf("completed import could not apply: %v", err)
	}
}

func TestPaperImportScoreConflictRequiresExplicitResolution(t *testing.T) {
	store, job := newReconcileFixture(t, []CreateQuestionInput{{QuestionNo: "1", QuestionType: "single_choice", Score: 5, SortOrder: 1}})
	draft := importedDraft("1", "single_choice", 4)
	draft.ScoreSource = "material"
	draft.Rubric = deterministicObjectiveRubric("single_choice", 4)
	if _, err := store.CompletePaperImport(context.Background(), "tenant", job.ID, []PaperImportDraftQuestion{draft}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyPaperImport(context.Background(), "tenant", job.ID, "user"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unresolved score conflict should block apply: %v", err)
	}
	draft.ScoreResolution = "use_blueprint"
	preview, err := store.CompletePaperImport(context.Background(), "tenant", job.ID, []PaperImportDraftQuestion{draft}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Issues) != 0 || preview.Questions[0].Score != 5 || preview.Questions[0].ScoreSource != "blueprint" {
		t.Fatalf("explicit blueprint resolution failed: %#v", preview)
	}
	if _, err := store.ApplyPaperImport(context.Background(), "tenant", job.ID, "user"); err != nil {
		t.Fatalf("resolved score conflict could not apply: %v", err)
	}
}

func TestPaperImportReviewResolvesTypeBeforeCreatingObjectivePoints(t *testing.T) {
	store, job := newReconcileFixture(t, []CreateQuestionInput{{QuestionNo: "1", QuestionType: "single_choice", Score: 5, SortOrder: 1}})
	draft := importedDraft("1", "fill_blank", 0)
	draft.ScoreSource = "missing"
	draft.Rubric = nil
	preview, err := store.CompletePaperImport(context.Background(), "tenant", job.ID, []PaperImportDraftQuestion{draft}, nil)
	if err != nil {
		t.Fatal(err)
	}
	preview.Questions[0].QuestionTypeResolution = "use_blueprint"
	reviewed, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "user", ReviewPaperImportInput{ExpectedGeneration: preview.Generation, Questions: preview.Questions})
	if err != nil {
		t.Fatal(err)
	}
	question := reviewed.Questions[0]
	if question.QuestionType != "single_choice" || question.Score != 5 || question.ScoreSource != "blueprint" || question.Rubric == nil || question.Rubric.MaxScore != 5 || SumRubricPoints(question.Rubric.Points) != 5 {
		t.Fatalf("review did not produce fixed points from the resolved type and score: %#v", question)
	}
	if len(reviewed.Issues) != 0 {
		t.Fatalf("resolved blueprint review retained stale issues: %#v", reviewed.Issues)
	}
}

func TestPaperImportLegacyMissingScoreUsesBlueprint(t *testing.T) {
	draft := importedDraft("1", "single_choice", 0)
	draft.Rubric = nil
	questions, issues := reconcilePaperImportDrafts([]PaperImportDraftQuestion{draft}, []paperImportExistingQuestion{{id: "question-1", number: "1", kind: "single_choice", score: 5}}, nil, nil)
	if len(issues) != 0 || questions[0].Score != 5 || questions[0].ScoreSource != "blueprint" || questions[0].Rubric == nil || SumRubricPoints(questions[0].Rubric.Points) != 5 {
		t.Fatalf("legacy missing score was not completed from matching blueprint: %#v, %#v", questions, issues)
	}
}

func TestDerivedObjectiveRubricRequiresConfirmedAnswerAndScore(t *testing.T) {
	draft := importedDraft("1", "single_choice", 5)
	draft.Rubric = deterministicObjectiveRubric("single_choice", 5)
	draft.HumanConfirmedFields = []string{"question_no", "question_type", "score", "stem", "answer"}
	if !humanReviewComplete(draft) {
		t.Fatal("derived fixed points require a redundant separate rubric confirmation")
	}
	draft.HumanConfirmedFields = []string{"question_no", "question_type", "stem", "answer"}
	if humanReviewComplete(draft) {
		t.Fatal("derived fixed points bypassed score confirmation")
	}
	draft.HumanConfirmedFields = []string{"question_no", "question_type", "score", "stem", "answer"}
	draft.RubricCandidateID = "material-rubric"
	if humanReviewComplete(draft) {
		t.Fatal("material rubric bypassed separate review")
	}
	draft.RubricCandidateID = ""
	draft.Rubric.Points[0].Description = "教师单独编写的评分依据"
	if humanReviewComplete(draft) {
		t.Fatal("authored rubric bypassed separate review")
	}
}

func TestMaterialTypeResolutionDoesNotRetainBlueprintArchetype(t *testing.T) {
	draft := importedDraft("1", "calculation", 5)
	draft.Rubric = nil
	draft.AssessmentArchetype = "exact_text"
	draft.QuestionTypeResolution = "use_material"
	draft.HumanConfirmedFields = []string{"question_type"}
	questions, issues := reconcilePaperImportDrafts([]PaperImportDraftQuestion{draft}, []paperImportExistingQuestion{{id: "question-1", number: "1", kind: "fill_blank", score: 5}}, nil, nil)
	if questions[0].AssessmentArchetype != "structured_steps" || !strings.Contains(strings.Join(issues, " "), "missing_rubric") {
		t.Fatalf("material calculation used the old blueprint's answer-only archetype: %#v, %#v", questions, issues)
	}
}
