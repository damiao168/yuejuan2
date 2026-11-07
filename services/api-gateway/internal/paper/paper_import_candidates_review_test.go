package paper

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCandidateImportRequiresExplicitHumanReviewBeforeApply(t *testing.T) {
	store, job := candidateImport(t)
	score := 3.0
	processed, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{
		QuestionCandidates: []QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "single_choice", Score: &score, Stem: "选择正确答案", Confidence: .95}},
		AnswerCandidates:   []AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "A", Confidence: .95}},
	})
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
	reviewed, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "user", ReviewPaperImportInput{ExpectedGeneration: processed.Generation, ExpectedUpdatedAt: processed.UpdatedAt.Format(time.RFC3339Nano), Questions: processed.Questions})
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
	processed, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{
		QuestionCandidates: []QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "single_choice", Score: &score, Stem: "选择", Confidence: .9}},
		AnswerCandidates:   []AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "A", Confidence: .9}},
	})
	if err != nil {
		t.Fatal(err)
	}
	processed.Questions[0].AnswerKey.StandardAnswer = ""
	confirmRequiredDraftFields(&processed.Questions[0])
	reviewed, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "user", ReviewPaperImportInput{ExpectedGeneration: processed.Generation, ExpectedUpdatedAt: processed.UpdatedAt.Format(time.RFC3339Nano), Questions: processed.Questions})
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
	job, err = store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{AnswerCandidates: []AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "A"}}})
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
	first, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{QuestionCandidates: questions, AnswerCandidates: answers})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{QuestionCandidates: questions, AnswerCandidates: answers})
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
	first, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{QuestionCandidates: q, AnswerCandidates: a, SolutionCandidates: solutions})
	if err != nil {
		t.Fatal(err)
	}
	first.Questions[0].AnswerKey.StandardAnswer = "C"
	first.Questions[0].Solution.RawText = "人工确认解析"
	confirmRequiredDraftFields(&first.Questions[0])
	reviewed, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "user", ReviewPaperImportInput{ExpectedGeneration: first.Generation, ExpectedUpdatedAt: first.UpdatedAt.Format(time.RFC3339Nano), Questions: first.Questions})
	if err != nil {
		t.Fatal(err)
	}
	if reviewed.Questions[0].AnswerKey.StandardAnswer != "C" {
		t.Fatal("review was not saved")
	}
	q[0].CandidateID = "q1-rerun"
	solutions[0].RawText = "机器解析二"
	rerun, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{QuestionCandidates: q, AnswerCandidates: a, SolutionCandidates: solutions})
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
