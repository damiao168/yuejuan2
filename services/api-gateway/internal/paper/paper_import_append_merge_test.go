package paper

import (
	"context"
	"testing"
)

func TestIncrementalParseInputOnlyIncludesNewSources(t *testing.T) {
	binding := PaperImportRunBinding{
		CommandType:  "add_sources",
		NewSourceIDs: []string{"new"},
		Input: PaperImportParseRequest{
			Documents: []PaperImportParseDocument{{SourceID: "old"}, {SourceID: "new"}},
			Pages:     []PaperImportDecodedPage{{SourceID: "old", PageNo: 1}, {SourceID: "new", PageNo: 2}},
			ExtraIssues: []PaperImportIssue{
				{Code: "OLD", SourceRefs: []PaperImportSourceRef{{SourceID: "old"}}},
				{Code: "NEW", SourceRefs: []PaperImportSourceRef{{SourceID: "new"}}},
			},
		},
	}
	documents, pages, issues := incrementalPaperImportParseInput(binding)
	if len(documents) != 1 || documents[0].SourceID != "new" || len(pages) != 1 || pages[0].SourceID != "new" || len(issues) != 1 || issues[0].Code != "NEW" {
		t.Fatalf("expected only new-source input, got documents=%#v pages=%#v issues=%#v", documents, pages, issues)
	}
}

func TestAppendResultPreservesHistoryAndReplacesSameQuestionNumber(t *testing.T) {
	previous := PaperImportJob{
		QuestionCandidates: []QuestionCandidate{
			{CandidateID: "old-1", QuestionNoNormalized: "1", Stem: "旧题一"},
			{CandidateID: "old-2", QuestionNoNormalized: "2", Stem: "旧题二"},
		},
		AnswerCandidates: []AnswerCandidate{{CandidateID: "old-a1", QuestionNoNormalized: "1", StandardAnswer: "A"}},
	}
	fresh := PaperImportParseResult{
		QuestionCandidates: []QuestionCandidate{
			{CandidateID: "new-2", QuestionNoRaw: "2.", Stem: "更新题二"},
			{CandidateID: "new-3", QuestionNoNormalized: "3", Stem: "新增题三"},
		},
		AnswerCandidates: []AnswerCandidate{{CandidateID: "new-a2", QuestionNoHint: "2", StandardAnswer: "B"}},
	}

	merged := mergePaperImportAppendResult(previous, fresh)
	if len(merged.QuestionCandidates) != 3 || merged.QuestionCandidates[0].Stem != "旧题一" || merged.QuestionCandidates[1].Stem != "更新题二" || merged.QuestionCandidates[2].Stem != "新增题三" {
		t.Fatalf("unexpected merged questions: %#v", merged.QuestionCandidates)
	}
	if len(merged.AnswerCandidates) != 2 || merged.AnswerCandidates[0].StandardAnswer != "A" || merged.AnswerCandidates[1].StandardAnswer != "B" {
		t.Fatalf("unexpected merged answers: %#v", merged.AnswerCandidates)
	}
}

func TestAppendPreservesConflictingFreshAnswersWhileReplacingHistory(t *testing.T) {
	score := 5.0
	previous := PaperImportJob{AnswerCandidates: []AnswerCandidate{
		{CandidateID: "old-1a", QuestionNoNormalized: "1", StandardAnswer: "old"},
		{CandidateID: "old-1b", QuestionNoNormalized: "1", StandardAnswer: "old conflict"},
		{CandidateID: "old-2", QuestionNoNormalized: "2", StandardAnswer: "keep"},
	}}
	fresh := PaperImportParseResult{
		QuestionCandidates: []QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "single_choice", Score: &score, Confidence: .9}},
		AnswerCandidates: []AnswerCandidate{
			{CandidateID: "new-a", QuestionNoHint: "1.", StandardAnswer: "A", Confidence: .9, SourceRefs: []PaperImportSourceRef{{SourceID: "new-a"}}},
			{CandidateID: "new-b", QuestionNoNormalized: "1", StandardAnswer: "B", Confidence: .9, SourceRefs: []PaperImportSourceRef{{SourceID: "new-b"}}},
		},
	}
	merged := mergePaperImportAppendResult(previous, fresh)
	if len(merged.AnswerCandidates) != 3 || merged.AnswerCandidates[0].CandidateID != "old-2" || merged.AnswerCandidates[1].SourceRefs[0].SourceID != "new-a" || merged.AnswerCandidates[2].SourceRefs[0].SourceID != "new-b" {
		t.Fatalf("sources overwritten: %#v", merged.AnswerCandidates)
	}
	store, job := candidateImport(t)
	result, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, merged)
	if err != nil {
		t.Fatal(err)
	}
	if !issueCodes(result)["CONFLICTING_ANSWERS"] {
		t.Fatalf("fresh conflict disappeared: %#v", result.StructuredIssues)
	}
	control := mergeAnswerCandidates(previous.AnswerCandidates, fresh.AnswerCandidates[:1])
	if len(control) != 2 || control[1].StandardAnswer != "A" {
		t.Fatalf("historical replacement changed: %#v", control)
	}
}
