package paper

import (
	"reflect"
	"strings"
	"testing"
)

func TestChineseQuestionNumberKeepsAcceptedForms(t *testing.T) {
	tests := []struct {
		input, number string
		ok            bool
	}{
		{"十", "10", true}, {"十一", "11", true}, {"九十", "90", true},
		{"九十九", "99", true}, {"一十零", "10", true}, {"一十〇", "10", true},
		{"九", "9", true}, {"零", "", false}, {"〇", "", false},
		{"十零", "", false}, {"零十", "", false}, {"零十一", "", false},
		{"一二", "", false}, {"一百", "", false}, {"", "", false},
	}
	for _, test := range tests {
		number, ok := normalizeChineseQuestionNumber(test.input)
		if number != test.number || ok != test.ok {
			t.Errorf("normalize %q = %q, %v; want %q, %v", test.input, number, ok, test.number, test.ok)
		}
	}
}

func TestCandidateReconciliationKeepsDiagnosticOrderAndInputNormalization(t *testing.T) {
	score := 10.0
	questions := []QuestionCandidate{{QuestionNoRaw: "第03题", QuestionType: "calculation", Score: &score, Stem: "stem"}, {}}
	answers := []AnswerCandidate{{}, {QuestionNoHint: "02.", StandardAnswer: "A"}, {QuestionNoHint: "二", StandardAnswer: "B"}}
	solutions := []SolutionCandidate{{}}
	rubrics := []RubricCandidate{{}}
	drafts, issues := reconcilePaperImportCandidates(questions, answers, solutions, rubrics, []PaperImportIssue{{Code: "BASE", Message: "base"}})
	want := []string{"BASE:", "AMBIGUOUS_MATCH:", "AMBIGUOUS_MATCH:", "UNMATCHED_SOLUTION:", "AMBIGUOUS_RUBRIC_MATCH:", "CONFLICTING_ANSWERS:2", "UNMATCHED_ANSWER:2", "POSSIBLE_MISSING_QUESTION:2"}
	got := make([]string, len(want))
	for i := range got {
		got[i] = issues[i].Code + ":" + issues[i].QuestionNo
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnostic prefix = %#v, want %#v", got, want)
	}
	if questions[0].QuestionNoNormalized != "3" || answers[1].QuestionNoNormalized != "2" || answers[2].QuestionNoNormalized != "2" {
		t.Fatalf("normalized candidate input was not retained: %#v, %#v", questions, answers)
	}
	if len(drafts) != 2 || drafts[0].QuestionNo != "3" || drafts[1].QuestionNo != "" {
		t.Fatalf("drafts lost original question order: %#v", drafts)
	}
}

func TestEquivalentRubricsKeepProvenanceAndMissingPointScoreBoundary(t *testing.T) {
	score, maxScore, pointScore, optional := 10.0, 8.0, 3.0, false
	questions := []QuestionCandidate{{QuestionNoRaw: "1", QuestionType: "calculation", Score: &score, Stem: "stem", SourceRefs: []PaperImportSourceRef{{SourceID: "question"}}}}
	answers := []AnswerCandidate{{QuestionNoHint: "1", StandardAnswer: "42", SourceRefs: []PaperImportSourceRef{{SourceID: "answer"}}}}
	points := []RubricCandidatePoint{{ID: "missing"}, {ID: "known", Score: &pointScore, Required: &optional}}
	rubrics := []RubricCandidate{
		{CandidateID: "first", QuestionNoHint: "1", MaxScore: &maxScore, Points: points, SourceRefs: []PaperImportSourceRef{{SourceID: "rubric-1"}}},
		{CandidateID: "second", QuestionNoHint: "一", MaxScore: &maxScore, Points: points, SourceRefs: []PaperImportSourceRef{{SourceID: "rubric-2"}}},
	}
	drafts, issues := reconcilePaperImportCandidates(questions, answers, nil, rubrics, nil)
	draft := drafts[0]
	if draft.RubricCandidateID != "first" || draft.Rubric == nil || len(draft.Rubric.Points) != 1 || draft.Rubric.Points[0].Required {
		t.Fatalf("equivalent rubric materialization changed: %#v", draft)
	}
	wantRefs := []PaperImportSourceRef{{SourceID: "question"}, {SourceID: "answer"}, {SourceID: "rubric-1"}, {SourceID: "rubric-2"}}
	if !reflect.DeepEqual(draft.SourceRefs, wantRefs) {
		t.Fatalf("merged provenance = %#v, want %#v", draft.SourceRefs, wantRefs)
	}
	wantCodes := []string{"MISSING_SOLUTION", "RUBRIC_POINT_SCORE_MISSING", "RUBRIC_SCORE_MISMATCH", "HUMAN_REVIEW_REQUIRED"}
	gotCodes := make([]string, 0, len(issues))
	for _, issue := range issues {
		gotCodes = append(gotCodes, issue.Code)
	}
	if !reflect.DeepEqual(gotCodes, wantCodes) {
		t.Fatalf("missing point must not also create an inferred total conflict: %#v", issues)
	}
	if !reflect.DeepEqual(issues[1].SourceRefs, wantRefs[2:]) {
		t.Fatalf("missing point lost merged rubric evidence: %#v", issues[1])
	}
}

func TestHumanConfirmationKeepsNumberPriorityAndRecordsBeforePreservation(t *testing.T) {
	old := PaperImportDraftQuestion{QuestionNo: "1", CandidateID: "old", Stem: "first", HumanConfirmedFields: []string{"stem", "options", "answer"}}
	byCandidate := old
	byCandidate.QuestionNo, byCandidate.CandidateID, byCandidate.Stem = "2", "same-id", "candidate"
	latest := old
	latest.QuestionNo, latest.CandidateID, latest.Stem = "01.", "latest", "number"
	latest.Options = []string{"old option"}
	latest.AnswerKey = &AnswerKeyInput{StandardAnswer: "old answer"}
	latest.ScoreResolution, latest.QuestionTypeResolution = "use_blueprint", "use_material"
	fresh := []PaperImportDraftQuestion{{QuestionNo: "一", CandidateID: "same-id", Stem: "fresh", Options: []string{"new option"}, AnswerKey: &AnswerKeyInput{StandardAnswer: "new answer"}}}
	existing := []PaperImportDraftQuestion{old, byCandidate, latest}
	issues := appendHumanConfirmationConflicts(nil, fresh, existing)
	wantMessages := []string{"新识别答案与人工确认答案冲突", "新识别题干与人工确认值冲突", "新识别选项与人工确认值冲突"}
	if !reflect.DeepEqual(issueMessages(issues), wantMessages) {
		t.Fatalf("confirmation conflicts changed order or selected old value: %#v", issues)
	}
	got := preserveHumanConfirmedDrafts(fresh, existing)[0]
	if got.Stem != "number" || got.AnswerKey != latest.AnswerKey || got.ScoreResolution != latest.ScoreResolution || got.QuestionTypeResolution != latest.QuestionTypeResolution {
		t.Fatalf("number priority or confirmation metadata lost: %#v", got)
	}
	got.Options[0] = "changed"
	got.HumanConfirmedFields[0] = "changed"
	if latest.Options[0] != "old option" || latest.HumanConfirmedFields[0] != "stem" {
		t.Fatal("preserved confirmation slices alias the old draft")
	}
}

func TestHumanReviewResolvesRubricBySourceWithoutNumberMatch(t *testing.T) {
	draft := importedDraft("1", "calculation", 10)
	draft.RubricCandidateID = "selected"
	draft.HumanConfirmedFields = []string{"rubric"}
	draft.SourceRefs = []PaperImportSourceRef{{SourceID: "source", FileAssetID: "asset"}}
	tests := []struct {
		name, source, asset string
		resolve             bool
		remaining           int
	}{
		{"same source and asset", "source", "asset", true, 0},
		{"different asset", "source", "other", true, 1},
		{"different source", "other", "asset", true, 1},
		{"missing source", "", "asset", true, 1},
		{"conflicts not resolved", "source", "asset", false, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issue := PaperImportIssue{Code: "AMBIGUOUS_RUBRIC_MATCH", QuestionNo: "unknown", SourceRefs: []PaperImportSourceRef{{SourceID: test.source, FileAssetID: test.asset}}}
			got := issuesAfterHumanReview([]PaperImportIssue{issue}, []PaperImportDraftQuestion{draft}, test.resolve)
			if len(got) != test.remaining {
				t.Fatalf("source-based review retained %d issues, want %d: %#v", len(got), test.remaining, got)
			}
		})
	}
}

func TestReviewedDraftValidationReplacesStaleIssuesInOrder(t *testing.T) {
	drafts := []PaperImportDraftQuestion{
		{QuestionNo: "01", QuestionType: "fill_blank", Score: 1, Stem: "stem", AnswerKey: &AnswerKeyInput{StandardAnswer: "answer"}},
		{QuestionNo: "一", QuestionType: "calculation", Rubric: &RubricInput{MaxScore: 2, Points: []RubricPoint{{Score: 3}}}},
	}
	issues := []PaperImportIssue{{Code: "MISSING_SCORE", Message: "stale"}, {Code: "UNKNOWN_DOCUMENT_ROLE", Message: "keep"}}
	got := appendReviewedDraftIssues(issues, drafts)
	wantCodes := []string{"UNKNOWN_DOCUMENT_ROLE", "MISSING_QUESTION", "DUPLICATE_QUESTION_NO", "MISSING_SCORE", "MISSING_ANSWER", "RUBRIC_SCORE_MISMATCH", "RUBRIC_POINTS_SCORE_MISMATCH"}
	gotCodes := make([]string, 0, len(got))
	for _, issue := range got {
		gotCodes = append(gotCodes, issue.Code)
	}
	if !reflect.DeepEqual(gotCodes, wantCodes) {
		t.Fatalf("review validation order = %#v, want %#v", gotCodes, wantCodes)
	}
}

func TestBlueprintReconciliationKeepsSortMutationAndTotalIssueOrder(t *testing.T) {
	existing := []paperImportExistingQuestion{
		{id: "third", number: "3", kind: "fill_blank", score: 4, sortOrder: 3},
		{id: "first", number: "1", kind: "fill_blank", score: 2, sortOrder: 1},
		{id: "second", number: "2", kind: "fill_blank", score: 3, sortOrder: 2},
	}
	draft := importedDraft("1", "fill_blank", 2)
	expected := 10.0
	drafts, issues := reconcilePaperImportDrafts([]PaperImportDraftQuestion{draft}, existing, &expected, []string{"keep", "paper_import.old: stale"})
	if existing[0].id != "first" || existing[1].id != "second" || existing[2].id != "third" || drafts[0].MatchedQuestionID != "first" {
		t.Fatalf("blueprint sort or match changed: %#v, %#v", existing, drafts)
	}
	want := []string{"keep", "paper_import.missing_question: AI 结果缺少蓝图题目 2", "paper_import.missing_question: AI 结果缺少蓝图题目 3", "paper_import.total_score_mismatch: AI 识别总分 2.00 与蓝图总分 9.00 不一致", "paper_import.total_score_mismatch: AI 识别总分 2.00 与考试总分 10.00 不一致", "paper_import.blueprint_total_score_mismatch: 蓝图总分 9.00 与考试总分 10.00 不一致"}
	if !reflect.DeepEqual(issues, want) {
		t.Fatalf("blueprint issue order = %#v, want %#v", issues, want)
	}
}

func TestImportBlockingKeepsLegacyIssueCompatibility(t *testing.T) {
	draft := importedDraft("1", "single_choice", 5)
	draft.Rubric = deterministicObjectiveRubric(draft.QuestionType, draft.Score)
	draft.HumanConfirmedFields = []string{"question_no", "question_type", "score", "stem", "answer"}
	tests := []struct {
		name       string
		issues     []string
		structured []PaperImportIssue
		blocked    bool
	}{
		{"legacy issue without structured issues", []string{"legacy warning"}, nil, true},
		{"structured warning replaces legacy gate", []string{"legacy warning"}, []PaperImportIssue{{Severity: "warning"}}, false},
		{"reconciliation issue always blocks", []string{"  paper_import.conflict"}, []PaperImportIssue{{Severity: "warning"}}, true},
		{"structured error blocks", nil, []PaperImportIssue{{Severity: "error"}}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job := PaperImportJob{Questions: []PaperImportDraftQuestion{draft}, QuestionCandidates: []QuestionCandidate{{}}, Issues: test.issues, StructuredIssues: test.structured}
			if got := paperImportHasBlockingIssues(job); got != test.blocked {
				t.Fatalf("blocking = %v, want %v; issues=%s", got, test.blocked, strings.Join(test.issues, ","))
			}
		})
	}
}
