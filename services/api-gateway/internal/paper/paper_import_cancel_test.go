package paper

import (
	"context"
	"errors"
	"testing"
)

func TestCancelPaperImportCreatesDistinctTerminalState(t *testing.T) {
	store, job := candidateImport(t)

	cancelled, err := store.CancelPaperImport(context.Background(), "tenant", job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" || cancelled.ErrorCode != "paper_import_cancelled" {
		t.Fatalf("unexpected cancelled import: %#v", cancelled)
	}
	if len(cancelled.Issues) != 1 || cancelled.Issues[0] != "识别任务已手动停止" {
		t.Fatalf("expected an explicit manual-stop message, got %#v", cancelled.Issues)
	}
	if cancelled.Sources[0].ProcessingStatus != "failed" {
		t.Fatalf("active source must no longer appear processing: %#v", cancelled.Sources)
	}
}

func TestCancelledPaperImportRejectsLateResults(t *testing.T) {
	store, job := candidateImport(t)
	if _, err := store.CancelPaperImport(context.Background(), "tenant", job.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{QuestionCandidates: []QuestionCandidate{{CandidateID: "late"}}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("late success must not overwrite cancellation, got %v", err)
	}
	if _, err := store.FailPaperImport(context.Background(), "tenant", job.ID, "ai_parse_failed", []string{"late failure"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("late failure must not overwrite cancellation, got %v", err)
	}
}

func TestOnlyProcessingPaperImportCanBeCancelled(t *testing.T) {
	store, job := candidateImport(t)
	if _, err := store.CompletePaperImportCandidates(context.Background(), "tenant", job.ID, PaperImportParseResult{QuestionCandidates: []QuestionCandidate{{CandidateID: "q1"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelPaperImport(context.Background(), "tenant", job.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("review result must not be cancelled, got %v", err)
	}
}
