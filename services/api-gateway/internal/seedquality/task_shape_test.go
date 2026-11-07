package seedquality

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestSeedAnonymousCodeMatchesOrdinaryMissingCandidateFallback(t *testing.T) {
	id := uuid.NewString()
	legacy := Task{ID: id, AnonymousCode: "ANON-old", Source: "manual", Status: "in_progress"}
	store := NewMemoryStore()
	store.tasks[taskKey("tenant", id)] = memoryTask{task: legacy}
	read, err := store.GetTask(context.Background(), "tenant", id)
	if err != nil {
		t.Fatal(err)
	}
	public := PublicTask(read, "tenant")
	if public.GradeRound != "single" || public.Status != "assigned" {
		t.Fatalf("seed task exposes a distinct round or claim state: %+v", public)
	}
	if public.AnonymousCode != public.AnswerSegmentID || public.AnonymousCode == public.ID || public.Source != "manual_sample" {
		t.Fatalf("distinguishable task fallback: %+v", public)
	}
	for _, value := range []string{public.AnonymousCode, public.AnswerSegmentID, public.SubmissionID, public.CreatedBy} {
		parsed, err := uuid.Parse(value)
		if err != nil || parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 {
			t.Fatalf("identifier is not an ordinary v4 UUID: %q %v", value, err)
		}
	}
	if PublicTask(legacy, "tenant").AnonymousCode != public.AnonymousCode {
		t.Fatal("legacy projection retained its old anonymous format")
	}
	if PublicTask(read, "tenant").AnswerSegmentID != public.AnswerSegmentID {
		t.Fatal("opaque identifiers changed between reads")
	}
}

func TestSeedPublicStatusMatchesOrdinarySubmittedTask(t *testing.T) {
	if got := PublicTask(Task{ID: uuid.NewString(), Status: "completed"}, "tenant").Status; got != "submitted" {
		t.Fatalf("submitted seed has distinct state: %q", got)
	}
}
