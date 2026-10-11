package paper

import (
	"context"
	"testing"
)

func TestAnswerSheetSourceDoesNotSatisfyPaperReadiness(t *testing.T) {
	result := buildReadiness(100, 1, 1, []Paper{{ID: "sheet", Purpose: PaperPurposeAnswerSheet}}, nil, nil)
	for _, check := range result.Checks {
		if check.Code == "paper_file" {
			if check.Passed || check.Section != "paper" {
				t.Fatalf("answer sheet must not count as test paper: %+v", check)
			}
			return
		}
	}
	t.Fatal("paper_file check missing")
}

func TestLegacyQuestionPaperStillSatisfiesPaperReadiness(t *testing.T) {
	result := buildReadiness(100, 1, 1, []Paper{{ID: "old-paper"}}, nil, nil)
	for _, check := range result.Checks {
		if check.Code == "paper_file" && check.Passed {
			return
		}
	}
	t.Fatal("legacy paper must keep its original readiness behavior")
}

func TestAnswerSheetRegistrationCanRetryWithoutCreatingAnotherSource(t *testing.T) {
	s := NewMemoryStore()
	input := CreatePaperInput{FileAssetID: "uploaded-sheet", Purpose: PaperPurposeAnswerSheet}
	first, err := s.CreatePaper(context.Background(), "tenant", "exam", "user", input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreatePaper(context.Background(), "tenant", "exam", "user", input)
	if err != nil || first.ID != second.ID {
		t.Fatalf("retry duplicated source: %+v %v", second, err)
	}
	input.Purpose = PaperPurposeQuestion
	if _, err := s.CreatePaper(context.Background(), "tenant", "exam", "user", input); err != ErrInvalidInput {
		t.Fatalf("source purpose must not change on retry: %v", err)
	}
}

func TestAnswerSheetSourceRespectsFrozenPreparation(t *testing.T) {
	s := NewMemoryStore()
	s.SetReadinessContext("exam", 100, 1, 1, "collecting")
	if _, err := s.CreatePaper(context.Background(), "tenant", "exam", "user", CreatePaperInput{FileAssetID: "sheet", Purpose: PaperPurposeAnswerSheet}); err != ErrExamFrozen {
		t.Fatalf("frozen exam accepted new source: %v", err)
	}
}
