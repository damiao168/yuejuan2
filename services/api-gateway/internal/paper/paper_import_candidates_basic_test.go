package paper

import (
	"context"
	"testing"
	"time"
)

func TestAddPaperImportSourcesClearsPreviousFailure(t *testing.T) {
	store, job := candidateImport(t)
	if _, err := store.FailPaperImport(context.Background(), "tenant", job.ID, "ai_parse_failed", []string{"stale failure"}); err != nil {
		t.Fatal(err)
	}

	updated, err := store.AddPaperImportSources(context.Background(), "tenant", job.ID, "user", AddPaperImportSourcesInput{ExpectedGeneration: job.Generation, Sources: []CreatePaperImportSourceInput{{FileAssetID: "asset-2", DocumentIndex: 1, RoleHint: "auto"}}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "processing" || updated.ErrorCode != "" || len(updated.Issues) != 0 {
		t.Fatalf("new source must clear stale failure while processing: %#v", updated)
	}
}

func TestRestartClearsPreviousStructuredIssues(t *testing.T) {
	for _, action := range []string{"add", "replace"} {
		t.Run(action, func(t *testing.T) {
			store, job := candidateImport(t)
			_, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{Issues: []PaperImportIssue{{Code: "NO_EXAM_CONTENT_DETECTED", Severity: "error", Message: "old result"}}})
			if err != nil {
				t.Fatal(err)
			}
			var restarted PaperImportJob
			if action == "add" {
				restarted, err = store.AddPaperImportSources(context.Background(), "tenant", job.ID, "user", AddPaperImportSourcesInput{ExpectedGeneration: job.Generation, Sources: []CreatePaperImportSourceInput{{FileAssetID: "new", DocumentIndex: 1, RoleHint: "auto"}}})
			} else {
				restarted, err = store.ReplacePaperImportSources(context.Background(), "tenant", job.ID, "user", ReplacePaperImportSourcesInput{ExpectedGeneration: job.Generation, Sources: []ReplacePaperImportSourceInput{{ID: job.Sources[0].ID, DocumentIndex: 0, RoleHint: "auto"}}})
			}
			if err != nil {
				t.Fatal(err)
			}
			if restarted.Status != "processing" || len(restarted.StructuredIssues) > 0 || len(restarted.Issues) > 0 {
				t.Fatal("restart retained stale recognition warnings")
			}
		})
	}
}

func confirmRequiredDraftFields(draft *PaperImportDraftQuestion) {
	draft.HumanConfirmedFields = requiredHumanConfirmedFields(*draft)
}

func TestAnswerOnlyImportRemainsReviewableWithoutInventingQuestions(t *testing.T) {
	store, job := candidateImport(t)
	out, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{AnswerCandidates: []AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "A", Confidence: .95}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "review_required" || len(out.Questions) != 0 || len(out.AnswerCandidates) != 1 {
		t.Fatalf("unexpected answer-only result: %#v", out)
	}
	if !issueCodes(out)["UNMATCHED_ANSWER"] || !paperImportHasBlockingIssues(out) {
		t.Fatalf("answer-only result must remain reviewable but not applicable: %#v", out.StructuredIssues)
	}
	if !issueCodes(out)["UNMATCHED_ANSWER"] || !issueCodes(out)["POSSIBLE_MISSING_QUESTION"] {
		t.Fatalf("missing answer-only issues: %#v", out.StructuredIssues)
	}
}

func TestQuestionOnlyImportReportsMissingAnswerAndMissingScore(t *testing.T) {
	store, job := candidateImport(t)
	out, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{QuestionCandidates: []QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "第1题", QuestionType: "short_answer", Stem: "题干", Confidence: .9}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "review_required" || len(out.QuestionCandidates) != 1 {
		t.Fatalf("unexpected question-only result: %#v", out)
	}
	codes := issueCodes(out)
	if !codes["MISSING_ANSWER"] || !codes["MISSING_SCORE"] {
		t.Fatalf("missing completeness issues: %#v", out.StructuredIssues)
	}
}

func TestCandidateReconciliationDetectsGapAndConflictingAnswers(t *testing.T) {
	store, job := candidateImport(t)
	score := 3.0
	questions := []QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "01", QuestionType: "single_choice", Score: &score, Confidence: .9}, {CandidateID: "q3", QuestionNoRaw: "第3题", QuestionType: "single_choice", Score: &score, Confidence: .9}}
	answers := []AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "A", Confidence: .9}, {CandidateID: "a2", QuestionNoHint: "1.", StandardAnswer: "C", Confidence: .9}}
	out, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{QuestionCandidates: questions, AnswerCandidates: answers})
	if err != nil {
		t.Fatal(err)
	}
	codes := issueCodes(out)
	if !codes["CONFLICTING_ANSWERS"] || !codes["POSSIBLE_MISSING_QUESTION"] {
		t.Fatalf("expected conflict and gap: %#v", out.StructuredIssues)
	}
}

func TestCandidateReconciliationKeepsQuestionAnswerAndSolutionIndependent(t *testing.T) {
	score := 5.0
	question := QuestionCandidate{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "single_choice", Score: &score, Stem: "题干", Confidence: .9}
	answer := AnswerCandidate{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "B", Confidence: .9}
	solution := SolutionCandidate{CandidateID: "s1", QuestionNoHint: "1", RawText: "排除 A、C、D", Steps: []SolutionStep{{StepNo: 1, Content: "比较选项"}}, Confidence: .9}
	tests := []paperCandidateReconciliationCase{
		{"question and answer", []QuestionCandidate{question}, []AnswerCandidate{answer}, nil, 1, true, false, false, false},
		{"question and solution", []QuestionCandidate{question}, nil, []SolutionCandidate{solution}, 1, false, true, false, false},
		{"answer and solution", nil, []AnswerCandidate{answer}, []SolutionCandidate{solution}, 0, false, false, true, true},
		{"fully mixed", []QuestionCandidate{question}, []AnswerCandidate{answer}, []SolutionCandidate{solution}, 1, true, true, false, false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			assertPaperCandidatesStayIndependent(t, testCase)
		})
	}
}

func TestQuestionNumberNormalizerPreservesParentChildStructure(t *testing.T) {
	if got := normalizePaperImportQuestionNumber("18（1）"); got != "18(1)" {
		t.Fatalf("got %q", got)
	}
	if got := normalizePaperImportQuestionNumber("01."); got != "1" {
		t.Fatalf("got %q", got)
	}
	if got := normalizePaperImportQuestionNumber("1）"); got != "1" {
		t.Fatalf("got %q", got)
	}
	if got := normalizePaperImportQuestionNumber("二十三"); got != "23" {
		t.Fatalf("got %q", got)
	}
	if got := normalizePaperImportQuestionNumber("1))"); got == "1" {
		t.Fatalf("malformed number was silently normalized: %q", got)
	}
}

func TestSolutionOnlyImportRemainsReviewableWithoutInventingQuestion(t *testing.T) {
	store, job := candidateImport(t)
	out, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{SolutionCandidates: []SolutionCandidate{{CandidateID: "s1", QuestionNoHint: "18(1)", RawText: "因为，所以", Confidence: .9}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "review_required" || len(out.Questions) != 0 || len(out.SolutionCandidates) != 1 || !issueCodes(out)["UNMATCHED_SOLUTION"] {
		t.Fatalf("unexpected solution-only result: %#v", out)
	}
	if !paperImportHasBlockingIssues(out) {
		t.Fatal("solution-only result was incorrectly applicable")
	}
}

func TestObjectiveCandidateGetsDeterministicRubricButEssayDoesNotInventAnswer(t *testing.T) {
	score := 3.0
	drafts, issues := reconcilePaperImportCandidates(
		[]QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "single_choice", Score: &score, Stem: "选择", Confidence: .9}},
		[]AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "A", Confidence: .9}}, nil, nil, nil,
	)
	if len(drafts) != 1 || drafts[0].Rubric == nil || !scoreEqual(drafts[0].Rubric.MaxScore, score) {
		t.Fatalf("objective rubric was not generated: %#v", drafts)
	}
	if issueSet := func() map[string]bool {
		out := map[string]bool{}
		for _, issue := range issues {
			out[issue.Code] = true
		}
		return out
	}(); issueSet["MISSING_ANSWER"] {
		t.Fatalf("unexpected missing answer: %#v", issues)
	}

	essayScore := 20.0
	_, essayIssues := reconcilePaperImportCandidates([]QuestionCandidate{{CandidateID: "qe", QuestionNoRaw: "2", QuestionType: "essay", Score: &essayScore, Stem: "作文", Confidence: .9}}, nil, nil, nil, nil)
	codes := map[string]bool{}
	for _, issue := range essayIssues {
		codes[issue.Code] = true
	}
	if codes["MISSING_ANSWER"] || !codes["MISSING_RUBRIC"] {
		t.Fatalf("essay requirements were wrong: %#v", essayIssues)
	}
}

func TestFillBlankCandidateUsesAnswerKeyWithoutRubricConfiguration(t *testing.T) {
	store, job := candidateImport(t)
	score := 2.0
	legacyRubricScore := 1.0
	out, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{
		QuestionCandidates: []QuestionCandidate{{CandidateID: "q8", QuestionNoRaw: "8", QuestionType: "fill_blank", Score: &score, Stem: "函数的零点是 ____。", Confidence: .95}},
		AnswerCandidates:   []AnswerCandidate{{CandidateID: "a8", QuestionNoHint: "8", StandardAnswer: "1", Confidence: .95}},
		RubricCandidates:   []RubricCandidate{{CandidateID: "legacy-r8", QuestionNoHint: "8", MaxScore: &legacyRubricScore, Points: []RubricCandidatePoint{{ID: "legacy", Description: "旧评分点", Score: &legacyRubricScore}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Questions) != 1 || out.Questions[0].AnswerKey == nil {
		t.Fatalf("fill blank answer was not matched: %#v", out.Questions)
	}
	if out.Questions[0].Rubric != nil || out.Questions[0].RubricCandidateID != "" {
		t.Fatalf("fill blank retained redundant rubric configuration: %#v", out.Questions[0])
	}
	for _, code := range []string{"CONFLICTING_RUBRICS", "RUBRIC_SCORE_MISMATCH", "RUBRIC_POINTS_SCORE_MISMATCH", "MISSING_RUBRIC"} {
		if issueCodes(out)[code] {
			t.Fatalf("fill blank produced rubric issue %s: %#v", code, out.StructuredIssues)
		}
	}

	draft := out.Questions[0]
	draft.RubricCandidateID = "legacy-r8"
	draft.Rubric = &RubricInput{Status: "draft", MaxScore: 1, Points: []RubricPoint{{ID: "legacy", Description: "旧评分点", Score: 1}}}
	draft.HumanConfirmedFields = append(requiredHumanConfirmedFields(draft), "rubric")
	saved, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "user", ReviewPaperImportInput{ExpectedGeneration: out.Generation, ExpectedUpdatedAt: out.UpdatedAt.Format(time.RFC3339Nano), Questions: []PaperImportDraftQuestion{draft}})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Questions[0].Rubric != nil || saved.Questions[0].RubricCandidateID != "" || stringSet(saved.Questions[0].HumanConfirmedFields)["rubric"] {
		t.Fatalf("review kept legacy fill blank rubric: %#v", saved.Questions[0])
	}
}
