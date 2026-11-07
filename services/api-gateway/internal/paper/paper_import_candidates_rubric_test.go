package paper

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExplicitRubricCandidateMatchesAndPersistsWithProvenance(t *testing.T) {
	store, job := candidateImport(t)
	score := 6.0
	pointScore := 2.0
	required := true
	questionRef := PaperImportSourceRef{SourceID: "source-question", FileAssetID: "asset-question", DocumentIndex: 0, PageNo: 1}
	answerRef := PaperImportSourceRef{SourceID: "source-answer", FileAssetID: "asset-answer", DocumentIndex: 1, PageNo: 1}
	rubricRef := PaperImportSourceRef{SourceID: "source-rubric", FileAssetID: "asset-rubric", DocumentIndex: 2, PageNo: 3}
	rubric := RubricCandidate{
		CandidateID: "r18", QuestionNoHint: "第18（1）题", MaxScore: &score, Confidence: .96,
		Points: []RubricCandidatePoint{
			{ID: "p1", Description: "列出正确关系式", Score: &pointScore, Required: &required},
			{ID: "p2", Description: "计算过程正确", Score: &pointScore, Required: &required},
			{ID: "p3", Description: "结果正确", Score: &pointScore, Required: &required},
		},
		Deductions: []any{}, Examples: []any{}, SourceRefs: []PaperImportSourceRef{rubricRef}, Issues: []string{},
	}
	out, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{
		QuestionCandidates: []QuestionCandidate{{CandidateID: "q18", QuestionNoRaw: "18.1", QuestionType: "short_answer", Score: &score, Stem: "解答", Confidence: .95, SourceRefs: []PaperImportSourceRef{questionRef}}},
		AnswerCandidates:   []AnswerCandidate{{CandidateID: "a18", QuestionNoHint: "18(1)", StandardAnswer: "42", Confidence: .95, SourceRefs: []PaperImportSourceRef{answerRef}}},
		RubricCandidates:   []RubricCandidate{rubric},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.RubricCandidates) != 1 || len(out.Questions) != 1 {
		t.Fatalf("rubric candidate was not persisted: %#v", out)
	}
	draft := out.Questions[0]
	if draft.RubricCandidateID != "r18" || draft.Rubric == nil || len(draft.Rubric.Points) != 3 || !scoreEqual(SumRubricPoints(draft.Rubric.Points), score) {
		t.Fatalf("explicit rubric was not matched faithfully: %#v", draft)
	}
	if issueCodes(out)["MISSING_RUBRIC"] || issueCodes(out)["RUBRIC_SCORE_MISMATCH"] || issueCodes(out)["RUBRIC_POINTS_SCORE_MISMATCH"] {
		t.Fatalf("valid explicit rubric produced a false issue: %#v", out.StructuredIssues)
	}
	foundRubricRef := false
	for _, ref := range draft.SourceRefs {
		foundRubricRef = foundRubricRef || ref.SourceID == rubricRef.SourceID
	}
	if !foundRubricRef {
		t.Fatalf("rubric provenance was not merged into the review draft: %#v", draft.SourceRefs)
	}
}

func TestRubricOnlyImportReportsUnmatchedRubric(t *testing.T) {
	store, job := candidateImport(t)
	score := 6.0
	pointScore := 6.0
	out, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{
		RubricCandidates: []RubricCandidate{{CandidateID: "r1", QuestionNoHint: "1", MaxScore: &score, Points: []RubricCandidatePoint{{ID: "p1", Description: "正确", Score: &pointScore}}, Confidence: .9, SourceRefs: []PaperImportSourceRef{{SourceID: "rubric-source"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Questions) != 0 || len(out.RubricCandidates) != 1 || !issueCodes(out)["UNMATCHED_RUBRIC"] {
		t.Fatalf("unexpected rubric-only result: %#v", out)
	}
	if issueCodes(out)["UNKNOWN_DOCUMENT_ROLE"] || !paperImportHasBlockingIssues(out) {
		t.Fatalf("rubric-only import must stay reviewable and blocked without being treated as empty: %#v", out.StructuredIssues)
	}
}

func TestSolutionDoesNotBecomeSubjectiveRubric(t *testing.T) {
	score := 6.0
	drafts, issues := reconcilePaperImportCandidates(
		[]QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "short_answer", Score: &score, Stem: "计算", Confidence: .9}},
		[]AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "42", Confidence: .9}},
		[]SolutionCandidate{{CandidateID: "s1", QuestionNoHint: "1", RawText: "第一步列式，第二步计算", Confidence: .9}}, nil, nil,
	)
	if len(drafts) != 1 || drafts[0].Solution == nil || drafts[0].Rubric != nil {
		t.Fatalf("solution was conflated with a formal rubric: %#v", drafts)
	}
	codes := map[string]bool{}
	for _, issue := range issues {
		codes[issue.Code] = true
	}
	if !codes["MISSING_RUBRIC"] {
		t.Fatalf("subjective question without explicit rubric must remain incomplete: %#v", issues)
	}
}

func TestRubricCandidateValidationIssues(t *testing.T) {
	questionScore := 6.0
	point2, point3, point4 := 2.0, 3.0, 4.0
	baseQuestion := []QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "short_answer", Score: &questionScore, Stem: "解答", Confidence: .9}}
	tests := []struct {
		name    string
		rubrics []RubricCandidate
		code    string
	}{
		{
			name: "conflicting rubric candidates",
			rubrics: []RubricCandidate{
				{CandidateID: "r1", QuestionNoHint: "1", MaxScore: &questionScore, Points: []RubricCandidatePoint{{ID: "p", Description: "过程", Score: &point2}}},
				{CandidateID: "r2", QuestionNoHint: "1", MaxScore: &questionScore, Points: []RubricCandidatePoint{{ID: "p", Description: "结果", Score: &point4}}},
			},
			code: "CONFLICTING_RUBRICS",
		},
		{
			name: "rubric max score mismatch",
			rubrics: []RubricCandidate{{CandidateID: "r1", QuestionNoHint: "1", MaxScore: &point4, Points: []RubricCandidatePoint{
				{ID: "p1", Description: "过程", Score: &point2}, {ID: "p2", Description: "结果", Score: &point4},
			}}},
			code: "RUBRIC_SCORE_MISMATCH",
		},
		{
			name: "rubric point total mismatch",
			rubrics: []RubricCandidate{{CandidateID: "r1", QuestionNoHint: "1", MaxScore: &questionScore, Points: []RubricCandidatePoint{
				{ID: "p1", Description: "过程", Score: &point2}, {ID: "p2", Description: "结果", Score: &point3},
			}}},
			code: "RUBRIC_POINTS_SCORE_MISMATCH",
		},
		{
			name: "rubric point score missing",
			rubrics: []RubricCandidate{{CandidateID: "r1", QuestionNoHint: "1", MaxScore: &questionScore, Points: []RubricCandidatePoint{
				{ID: "p1", Description: "过程"}, {ID: "p2", Description: "结果", Score: &questionScore},
			}}},
			code: "RUBRIC_POINT_SCORE_MISSING",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, issues := reconcilePaperImportCandidates(baseQuestion, nil, nil, testCase.rubrics, nil)
			codes := map[string]bool{}
			for _, issue := range issues {
				codes[issue.Code] = true
			}
			if !codes[testCase.code] {
				t.Fatalf("missing %s: %#v", testCase.code, issues)
			}
		})
	}
}

func TestHumanCorrectedLockedRubricAppliesWithAnswerAndRubricProvenance(t *testing.T) {
	store, job := candidateImport(t)
	score, point2, point3 := 6.0, 2.0, 3.0
	processed, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{
		QuestionCandidates: []QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "short_answer", Score: &score, Stem: "解答", Confidence: .95, SourceRefs: []PaperImportSourceRef{{SourceID: "question-source"}}}},
		AnswerCandidates:   []AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "42", Confidence: .95, SourceRefs: []PaperImportSourceRef{{SourceID: "answer-source"}}}},
		RubricCandidates: []RubricCandidate{{CandidateID: "r1", QuestionNoHint: "1", MaxScore: &score, Points: []RubricCandidatePoint{
			{ID: "p1", Description: "过程", Score: &point2}, {ID: "p2", Description: "结果", Score: &point3},
		}, Confidence: .95, SourceRefs: []PaperImportSourceRef{{SourceID: "rubric-source"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !issueCodes(processed)["RUBRIC_POINTS_SCORE_MISMATCH"] {
		t.Fatalf("initial mismatch was not reported: %#v", processed.StructuredIssues)
	}
	processed.Questions[0].Rubric.Points[1].Score = 4
	processed.Questions[0].Rubric.Status = "locked"
	confirmRequiredDraftFields(&processed.Questions[0])
	reviewed, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "user", ReviewPaperImportInput{ExpectedGeneration: processed.Generation, ExpectedUpdatedAt: processed.UpdatedAt.Format(time.RFC3339Nano), Questions: processed.Questions})
	if err != nil {
		t.Fatal(err)
	}
	if issueCodes(reviewed)["RUBRIC_POINTS_SCORE_MISMATCH"] || reviewed.Questions[0].CompletenessStatus != "complete" {
		t.Fatalf("human correction did not clear rubric mismatch: %#v", reviewed)
	}
	if _, err = store.ApplyPaperImport(context.Background(), "tenant", job.ID, "user"); err != nil {
		t.Fatalf("corrected locked rubric did not apply: %v", err)
	}
	questions, err := store.ListQuestions(context.Background(), "tenant", "exam")
	if err != nil || len(questions) != 1 {
		t.Fatalf("list applied question: count=%d err=%v", len(questions), err)
	}
	question := questions[0]
	if question.AnswerKey == nil || question.AnswerKey.StandardAnswer != "42" || question.AnswerKey.PaperImportID != job.ID || question.AnswerKey.PaperImportCandidateID != "a1" || len(question.AnswerKey.PaperImportSourceRefs) == 0 {
		t.Fatalf("answer apply path lost data or provenance: %#v", question.AnswerKey)
	}
	if question.Rubric == nil || question.Rubric.Status != "locked" || question.Rubric.PaperImportID != job.ID || question.Rubric.PaperImportCandidateID != "r1" || len(question.Rubric.PaperImportSourceRefs) == 0 {
		t.Fatalf("rubric apply path lost status or provenance: %#v", question.Rubric)
	}
}

func TestLockedRubricCannotApplyWithoutExplicitRubricConfirmation(t *testing.T) {
	store, job := candidateImport(t)
	score := 6.0
	processed, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{
		QuestionCandidates: []QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "short_answer", Score: &score, Stem: "解答", Confidence: .95}},
		AnswerCandidates:   []AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "42", Confidence: .95}},
		RubricCandidates:   []RubricCandidate{{CandidateID: "r1", QuestionNoHint: "1", MaxScore: &score, Points: []RubricCandidatePoint{{ID: "p1", Description: "正确", Score: &score}}, Confidence: .95}},
	})
	if err != nil {
		t.Fatal(err)
	}
	processed.Questions[0].Rubric.Status = "locked"
	processed.Questions[0].HumanConfirmedFields = []string{"question_no", "question_type", "score", "stem", "answer"}
	reviewed, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "user", ReviewPaperImportInput{ExpectedGeneration: processed.Generation, ExpectedUpdatedAt: processed.UpdatedAt.Format(time.RFC3339Nano), Questions: processed.Questions})
	if err != nil {
		t.Fatal(err)
	}
	if !issueCodes(reviewed)["HUMAN_REVIEW_REQUIRED"] || reviewed.Questions[0].CompletenessStatus != "needs_review" {
		t.Fatalf("missing rubric confirmation was treated as an explicit review: %#v", reviewed)
	}
	if _, err = store.ApplyPaperImport(context.Background(), "tenant", job.ID, "user"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unconfirmed locked rubric apply error = %v, want ErrInvalidInput", err)
	}
}

func TestReviewedDraftRequirementsFollowAssessmentArchetype(t *testing.T) {
	draft := PaperImportDraftQuestion{
		QuestionNo: "1", QuestionType: "short_answer", AssessmentArchetype: "extended_response", Score: 10, Stem: "开放写作",
		Rubric: &RubricInput{Status: "draft", MaxScore: 10, Points: []RubricPoint{{ID: "p1", Description: "内容", Score: 10}}},
	}
	issues := appendReviewedDraftIssues(nil, []PaperImportDraftQuestion{draft})
	for _, issue := range issues {
		if issue.Code == "MISSING_ANSWER" {
			t.Fatalf("extended_response archetype incorrectly required a unique answer: %#v", issues)
		}
	}
}
