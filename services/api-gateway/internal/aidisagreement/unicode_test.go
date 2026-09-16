package aidisagreement

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestClassificationNoteLimitCountsRunes(t *testing.T) {
	for _, char := range []string{"a", "你", "🙂"} {
		store := NewMemoryStore()
		store.AddActualComparison("tenant-1", comparison())
		service := NewService(store)
		item, _, err := service.Capture(context.Background(), "tenant-1", CaptureInput{AICandidateID: "ai-1", HumanGradeID: "human-1"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.Classify(context.Background(), "tenant-1", item.ID, "grader-1", ClassifyInput{Taxonomy: TaxonomyOCRError, Notes: strings.Repeat(char, 2001), ExpectedRevision: item.Revision})
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("long note char=%s err=%v", char, err)
		}
		_, err = service.Classify(context.Background(), "tenant-1", item.ID, "grader-1", ClassifyInput{Taxonomy: TaxonomyOCRError, Notes: "  " + strings.Repeat(char, 2000) + "  ", ExpectedRevision: item.Revision})
		if err != nil {
			t.Fatalf("valid note char=%s err=%v", char, err)
		}
	}
}
