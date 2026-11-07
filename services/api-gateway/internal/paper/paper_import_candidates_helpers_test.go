package paper

import (
	"testing"
)

type paperCandidateReconciliationCase struct {
	name                  string
	questions             []QuestionCandidate
	answers               []AnswerCandidate
	solutions             []SolutionCandidate
	wantDrafts            int
	wantAnswer, wantSoln  bool
	wantUnmatchedAnswer   bool
	wantUnmatchedSolution bool
}

func assertPaperCandidatesStayIndependent(t *testing.T, testCase paperCandidateReconciliationCase) {
	t.Helper()
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
}
