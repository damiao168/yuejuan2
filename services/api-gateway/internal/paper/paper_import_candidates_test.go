package paper

import (
	"context"
	"errors"
	"testing"
)

func candidateImport(t *testing.T) (*MemoryStore, PaperImportJob) {
	t.Helper()
	store := NewMemoryStore()
	job, err := store.CreatePaperImport(context.Background(), "tenant", "exam", "user", CreatePaperImportInput{Subject: "mathematics", Sources: []CreatePaperImportSourceInput{{FileAssetID: "asset-1", DocumentIndex: 0, RoleHint: "auto"}}})
	if err != nil {
		t.Fatal(err)
	}
	return store, job
}

func issueCodes(job PaperImportJob) map[string]bool {
	out := map[string]bool{}
	for _, issue := range job.StructuredIssues {
		out[issue.Code] = true
	}
	return out
}

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
			_, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil, nil, nil, nil, nil, []PaperImportIssue{{Code: "NO_EXAM_CONTENT_DETECTED", Severity: "error", Message: "old result"}})
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
	out, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil, nil, []AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "A", Confidence: .95}}, nil, nil, nil)
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
	out, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil, []QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "第1题", QuestionType: "short_answer", Stem: "题干", Confidence: .9}}, nil, nil, nil, nil)
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
	out, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil, questions, answers, nil, nil, nil)
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
	tests := []struct {
		name                  string
		questions             []QuestionCandidate
		answers               []AnswerCandidate
		solutions             []SolutionCandidate
		wantDrafts            int
		wantAnswer, wantSoln  bool
		wantUnmatchedAnswer   bool
		wantUnmatchedSolution bool
	}{
		{"question and answer", []QuestionCandidate{question}, []AnswerCandidate{answer}, nil, 1, true, false, false, false},
		{"question and solution", []QuestionCandidate{question}, nil, []SolutionCandidate{solution}, 1, false, true, false, false},
		{"answer and solution", nil, []AnswerCandidate{answer}, []SolutionCandidate{solution}, 0, false, false, true, true},
		{"fully mixed", []QuestionCandidate{question}, []AnswerCandidate{answer}, []SolutionCandidate{solution}, 1, true, true, false, false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			drafts, issues := reconcilePaperImportCandidates(testCase.questions, testCase.answers, testCase.solutions, nil, nil)
			if len(drafts) != testCase.wantDrafts {
				t.Fatalf("draft count = %d, want %d", len(drafts), testCase.wantDrafts)
			}
			if len(drafts) > 0 && ((drafts[0].AnswerKey != nil) != testCase.wantAnswer || (drafts[0].Solution != nil) != testCase.wantSoln) {
				t.Fatalf("independent fields were conflated: %#v", drafts[0])
			}
			codes := map[string]bool{}
			for _, issue := range issues {
				codes[issue.Code] = true
			}
			if codes["UNMATCHED_ANSWER"] != testCase.wantUnmatchedAnswer || codes["UNMATCHED_SOLUTION"] != testCase.wantUnmatchedSolution {
				t.Fatalf("unmatched issues = %#v", issues)
			}
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
	out, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil, nil, nil, []SolutionCandidate{{CandidateID: "s1", QuestionNoHint: "18(1)", RawText: "因为，所以", Confidence: .9}}, nil, nil)
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
	out, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil,
		[]QuestionCandidate{{CandidateID: "q8", QuestionNoRaw: "8", QuestionType: "fill_blank", Score: &score, Stem: "函数的零点是 ____。", Confidence: .95}},
		[]AnswerCandidate{{CandidateID: "a8", QuestionNoHint: "8", StandardAnswer: "1", Confidence: .95}}, nil,
		[]RubricCandidate{{CandidateID: "legacy-r8", QuestionNoHint: "8", MaxScore: &legacyRubricScore, Points: []RubricCandidatePoint{{ID: "legacy", Description: "旧评分点", Score: &legacyRubricScore}}}}, nil)
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
	saved, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "user", ReviewPaperImportInput{ExpectedGeneration: out.Generation, Questions: []PaperImportDraftQuestion{draft}})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Questions[0].Rubric != nil || saved.Questions[0].RubricCandidateID != "" || stringSet(saved.Questions[0].HumanConfirmedFields)["rubric"] {
		t.Fatalf("review kept legacy fill blank rubric: %#v", saved.Questions[0])
	}
}

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
	out, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil,
		[]QuestionCandidate{{CandidateID: "q18", QuestionNoRaw: "18.1", QuestionType: "short_answer", Score: &score, Stem: "解答", Confidence: .95, SourceRefs: []PaperImportSourceRef{questionRef}}},
		[]AnswerCandidate{{CandidateID: "a18", QuestionNoHint: "18(1)", StandardAnswer: "42", Confidence: .95, SourceRefs: []PaperImportSourceRef{answerRef}}}, nil,
		[]RubricCandidate{rubric}, nil)
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
	out, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil, nil, nil, nil,
		[]RubricCandidate{{CandidateID: "r1", QuestionNoHint: "1", MaxScore: &score, Points: []RubricCandidatePoint{{ID: "p1", Description: "正确", Score: &pointScore}}, Confidence: .9, SourceRefs: []PaperImportSourceRef{{SourceID: "rubric-source"}}}}, nil)
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
	processed, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil,
		[]QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "short_answer", Score: &score, Stem: "解答", Confidence: .95, SourceRefs: []PaperImportSourceRef{{SourceID: "question-source"}}}},
		[]AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "42", Confidence: .95, SourceRefs: []PaperImportSourceRef{{SourceID: "answer-source"}}}}, nil,
		[]RubricCandidate{{CandidateID: "r1", QuestionNoHint: "1", MaxScore: &score, Points: []RubricCandidatePoint{
			{ID: "p1", Description: "过程", Score: &point2}, {ID: "p2", Description: "结果", Score: &point3},
		}, Confidence: .95, SourceRefs: []PaperImportSourceRef{{SourceID: "rubric-source"}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !issueCodes(processed)["RUBRIC_POINTS_SCORE_MISMATCH"] {
		t.Fatalf("initial mismatch was not reported: %#v", processed.StructuredIssues)
	}
	processed.Questions[0].Rubric.Points[1].Score = 4
	processed.Questions[0].Rubric.Status = "locked"
	confirmRequiredDraftFields(&processed.Questions[0])
	reviewed, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "user", ReviewPaperImportInput{ExpectedGeneration: processed.Generation, Questions: processed.Questions})
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
	processed, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil,
		[]QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "short_answer", Score: &score, Stem: "解答", Confidence: .95}},
		[]AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "42", Confidence: .95}}, nil,
		[]RubricCandidate{{CandidateID: "r1", QuestionNoHint: "1", MaxScore: &score, Points: []RubricCandidatePoint{{ID: "p1", Description: "正确", Score: &score}}, Confidence: .95}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	processed.Questions[0].Rubric.Status = "locked"
	processed.Questions[0].HumanConfirmedFields = []string{"question_no", "question_type", "score", "stem", "answer"}
	reviewed, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "user", ReviewPaperImportInput{ExpectedGeneration: processed.Generation, Questions: processed.Questions})
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

func TestCandidateImportRequiresExplicitHumanReviewBeforeApply(t *testing.T) {
	store, job := candidateImport(t)
	score := 3.0
	processed, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil,
		[]QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "single_choice", Score: &score, Stem: "选择正确答案", Confidence: .95}},
		[]AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "A", Confidence: .95}}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !issueCodes(processed)["HUMAN_REVIEW_REQUIRED"] {
		t.Fatalf("candidate result did not expose review gate: %#v", processed.StructuredIssues)
	}
	if _, err = store.ApplyPaperImport(context.Background(), "tenant", job.ID, "user"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unreviewed candidate apply error = %v, want ErrInvalidInput", err)
	}
	confirmRequiredDraftFields(&processed.Questions[0])
	reviewed, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "user", ReviewPaperImportInput{ExpectedGeneration: processed.Generation, Questions: processed.Questions})
	if err != nil {
		t.Fatal(err)
	}
	if issueCodes(reviewed)["HUMAN_REVIEW_REQUIRED"] {
		t.Fatalf("explicit review did not clear gate: %#v", reviewed.StructuredIssues)
	}
	if reviewed.Questions[0].CompletenessStatus != "complete" {
		t.Fatalf("reviewed draft completeness = %q", reviewed.Questions[0].CompletenessStatus)
	}
	if _, err = store.ApplyPaperImport(context.Background(), "tenant", job.ID, "user"); err != nil {
		t.Fatalf("reviewed candidate did not apply: %v", err)
	}
	questions, err := store.ListQuestions(context.Background(), "tenant", "exam")
	if err != nil || len(questions) != 1 {
		t.Fatalf("list applied questions: count=%d err=%v", len(questions), err)
	}
	if questions[0].PaperImportID != job.ID || questions[0].PaperImportCandidateID != "q1" || questions[0].AnswerKey == nil || questions[0].AnswerKey.PaperImportCandidateID != "a1" {
		t.Fatalf("applied provenance was not retained: %#v", questions[0])
	}
}

func TestHumanReviewCannotClearARequiredFieldAndBypassApplyGate(t *testing.T) {
	store, job := candidateImport(t)
	score := 3.0
	processed, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil,
		[]QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "single_choice", Score: &score, Stem: "选择", Confidence: .9}},
		[]AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "A", Confidence: .9}}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	processed.Questions[0].AnswerKey.StandardAnswer = ""
	confirmRequiredDraftFields(&processed.Questions[0])
	reviewed, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "user", ReviewPaperImportInput{ExpectedGeneration: processed.Generation, Questions: processed.Questions})
	if err != nil {
		t.Fatal(err)
	}
	if !issueCodes(reviewed)["MISSING_ANSWER"] {
		t.Fatalf("review did not recompute missing answer: %#v", reviewed.StructuredIssues)
	}
	if _, err = store.ApplyPaperImport(context.Background(), "tenant", job.ID, "user"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid reviewed draft apply error = %v, want ErrInvalidInput", err)
	}
}

func TestReplacePaperImportSourcesReordersRemovesAndChangesRole(t *testing.T) {
	store := NewMemoryStore()
	job, err := store.CreatePaperImport(context.Background(), "tenant", "exam", "user", CreatePaperImportInput{Subject: "math", Sources: []CreatePaperImportSourceInput{{FileAssetID: "a", DocumentIndex: 0, RoleHint: "auto"}, {FileAssetID: "b", DocumentIndex: 1, RoleHint: "auto"}, {FileAssetID: "c", DocumentIndex: 2, RoleHint: "auto"}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err = store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil, nil, []AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "A"}}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := store.ReplacePaperImportSources(context.Background(), "tenant", job.ID, "user", ReplacePaperImportSourcesInput{ExpectedGeneration: job.Generation, Sources: []ReplacePaperImportSourceInput{{ID: job.Sources[2].ID, DocumentIndex: 0, RoleHint: "solution"}, {ID: job.Sources[0].ID, DocumentIndex: 1, RoleHint: "auto"}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "processing" || len(out.Sources) != 2 || out.Sources[0].FileAssetID != "c" || out.Sources[0].RoleHint != "solution" || out.Sources[1].DocumentIndex != 1 {
		t.Fatalf("unexpected replacement: %#v", out.Sources)
	}
	out, err = store.ReplacePaperImportSources(context.Background(), "tenant", job.ID, "user", ReplacePaperImportSourcesInput{ExpectedGeneration: out.Generation, Sources: []ReplacePaperImportSourceInput{}})
	if err != nil {
		t.Fatalf("deleting all sources while processing should be allowed: %v", err)
	}
	if out.Status != "failed" || out.ErrorCode != "paper_import_no_sources" || len(out.Sources) != 0 {
		t.Fatalf("unexpected empty-source state: %#v", out)
	}
}

func TestLegacyTwoFileImportCreatesOrderedTypedSources(t *testing.T) {
	store := NewMemoryStore()
	job, err := store.CreatePaperImport(context.Background(), "tenant", "exam", "user", CreatePaperImportInput{Subject: "math", PaperFileAssetID: "paper", AnswerFileAssetID: "answer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(job.Sources) != 2 || job.Sources[0].FileAssetID != "paper" || job.Sources[0].RoleHint != "question" || job.Sources[1].FileAssetID != "answer" || job.Sources[1].RoleHint != "answer" {
		t.Fatalf("legacy source compatibility failed: %#v", job.Sources)
	}
	if _, err := store.GetPaperImport(context.Background(), "other-tenant", job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant import read error = %v", err)
	}
}

func TestCandidateRerunIsIdempotent(t *testing.T) {
	store, job := candidateImport(t)
	score := 2.0
	questions := []QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "single_choice", Score: &score, Stem: "题干", Confidence: .9}}
	answers := []AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "A", Confidence: .9}}
	first, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil, questions, answers, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil, questions, answers, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Questions) != 1 || len(second.StructuredIssues) != len(first.StructuredIssues) || second.Questions[0].CandidateID != "q1" {
		t.Fatalf("rerun changed deterministic result: first=%#v second=%#v", first, second)
	}
}

func TestPaperImportSourceConfigurationHashTracksOrderAndRole(t *testing.T) {
	sources := []PaperImportSource{{ID: "a", FileAssetID: "fa", DocumentIndex: 0, RoleHint: "auto"}, {ID: "b", FileAssetID: "fb", DocumentIndex: 1, RoleHint: "answer"}}
	baseline := paperImportSourceConfigurationHash(sources)
	if baseline != paperImportSourceConfigurationHash([]PaperImportSource{sources[1], sources[0]}) {
		t.Fatal("hash depended on database row order instead of document_index")
	}
	changedRole := append([]PaperImportSource{}, sources...)
	changedRole[0].RoleHint = "question"
	changedOrder := append([]PaperImportSource{}, sources...)
	changedOrder[0].DocumentIndex, changedOrder[1].DocumentIndex = 1, 0
	if baseline == paperImportSourceConfigurationHash(changedRole) || baseline == paperImportSourceConfigurationHash(changedOrder) {
		t.Fatal("hash did not track a semantic source configuration change")
	}
}

func TestIncrementalRerunPreservesHumanConfirmedAnswer(t *testing.T) {
	store, job := candidateImport(t)
	score := 3.0
	q := []QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "single_choice", Score: &score, Stem: "题干", Confidence: .9}}
	a := []AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "A", Confidence: .9}}
	solutions := []SolutionCandidate{{CandidateID: "s1", QuestionNoHint: "1", RawText: "机器解析一", Confidence: .9}}
	first, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil, q, a, solutions, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	first.Questions[0].AnswerKey.StandardAnswer = "C"
	first.Questions[0].Solution.RawText = "人工确认解析"
	confirmRequiredDraftFields(&first.Questions[0])
	reviewed, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "user", ReviewPaperImportInput{ExpectedGeneration: first.Generation, Questions: first.Questions})
	if err != nil {
		t.Fatal(err)
	}
	if reviewed.Questions[0].AnswerKey.StandardAnswer != "C" {
		t.Fatal("review was not saved")
	}
	q[0].CandidateID = "q1-rerun"
	solutions[0].RawText = "机器解析二"
	rerun, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, nil, q, a, solutions, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rerun.Questions[0].AnswerKey.StandardAnswer != "C" {
		t.Fatalf("human answer overwritten: %#v", rerun.Questions[0].AnswerKey)
	}
	if rerun.Questions[0].Solution == nil || rerun.Questions[0].Solution.RawText != "人工确认解析" {
		t.Fatalf("human solution overwritten: %#v", rerun.Questions[0].Solution)
	}
	if !issueCodes(rerun)["CONFLICTING_ANSWERS"] {
		t.Fatalf("new machine conflict was hidden: %#v", rerun.StructuredIssues)
	}
	if !issueCodes(rerun)["HUMAN_CONFIRMED_CONFLICT"] {
		t.Fatalf("new machine solution conflict was hidden: %#v", rerun.StructuredIssues)
	}
}
