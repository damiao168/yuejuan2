package answergroup

import (
	"errors"
	"fmt"
	"testing"
)

func readyMemorySourceGroup(t *testing.T) (*MemoryStore, Group, []SourceAnswer, int) {
	t.Helper()
	source := make([]SourceAnswer, 6)
	for i := range source {
		source[i] = SourceAnswer{SubmissionID: fmt.Sprintf("submission-%d", i), SegmentID: fmt.Sprintf("segment-%d", i), SnapshotID: "snapshot", ArchetypeCode: ArchetypeExactText, AnswerText: "ATP", Source: "manual_entry"}
	}
	store := NewMemoryStore(nil, DefaultPolicy())
	store.SetSourceAnswers("tenant", "exam", "question", source)
	groups, err := store.Build(t.Context(), "tenant", "exam", "question", "teacher", BuildInput{})
	if err != nil || len(groups) != 1 {
		t.Fatalf("build: groups=%d err=%v", len(groups), err)
	}
	group, err := store.PutDecision(t.Context(), "tenant", groups[0].ID, "teacher", DecisionInput{ScoreCandidate: map[string]any{"score": 2}, RubricSelection: map[string]any{"P1": "matched"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range append([]Member{}, group.Members...)[:group.MinimumSample] {
		group, err = store.ReviewSample(t.Context(), "tenant", group.ID, member.SegmentID, "teacher", SampleReviewInput{Outcome: SampleAccepted})
		if err != nil {
			t.Fatal(err)
		}
	}
	if !group.CanConfirm || group.MinimumSample >= len(group.Members) {
		t.Fatalf("fixture needs completed sampling and an unsampled member: %#v", group)
	}
	for _, member := range group.Members {
		if member.SampleStatus == "" {
			for i := range source {
				if source[i].SegmentID == member.SegmentID {
					return store, group, source, i
				}
			}
		}
	}
	t.Fatal("no unsampled member")
	return nil, Group{}, nil, 0
}

func TestConfirmationRevalidatesEveryMemberSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]SourceAnswer, int) []SourceAnswer
		allow  bool
	}{
		{"unchanged", func(s []SourceAnswer, _ int) []SourceAnswer { return s }, true},
		{"same normalized representation", func(s []SourceAnswer, i int) []SourceAnswer { s[i].AnswerText = "  ＡＴＰ。  "; return s }, true},
		{"unsampled answer changed", func(s []SourceAnswer, i int) []SourceAnswer { s[i].AnswerText = "ADP"; return s }, false},
		{"sampled answer changed", func(s []SourceAnswer, _ int) []SourceAnswer { s[0].AnswerText = "ADP"; return s }, false},
		{"unsampled answer removed", func(s []SourceAnswer, i int) []SourceAnswer { return append(s[:i], s[i+1:]...) }, false},
		{"blank answer", func(s []SourceAnswer, i int) []SourceAnswer { s[i].AnswerText = " "; return s }, false},
		{"low confidence OCR", func(s []SourceAnswer, i int) []SourceAnswer {
			c := 0.3
			s[i].Source, s[i].Confidence = "ocr_text", &c
			return s
		}, false},
		{"new snapshot", func(s []SourceAnswer, i int) []SourceAnswer { s[i].SnapshotID = "new-snapshot"; return s }, false},
		{"unsupported archetype", func(s []SourceAnswer, i int) []SourceAnswer { s[i].ArchetypeCode = "extended_response"; return s }, false},
		{"different submission", func(s []SourceAnswer, i int) []SourceAnswer { s[i].SubmissionID = "other"; return s }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, group, sources, unsampled := readyMemorySourceGroup(t)
			store.SetSourceAnswers("tenant", "exam", "question", tc.change(sources, unsampled))
			confirmed, candidates, err := store.Confirm(t.Context(), "tenant", group.ID, "teacher", ConfirmInput{ExpectedRevision: group.Decision.Revision})
			if tc.allow {
				if err != nil || confirmed.Status != StatusConfirmed || len(candidates) != group.MemberCount {
					t.Fatalf("valid group rejected: status=%s candidates=%d err=%v", confirmed.Status, len(candidates), err)
				}
				assertGroupingCandidatesAreProposals(t, candidates, confirmed.Decision.RollbackReference)
				return
			}
			if !errors.Is(err, ErrRevisionConflict) || len(candidates) != 0 {
				t.Fatalf("stale source confirmed: status=%s candidates=%d err=%v", confirmed.Status, len(candidates), err)
			}
			stored, err := store.Get(t.Context(), "tenant", group.ID)
			if err != nil || stored.Status == StatusConfirmed || stored.Decision.ConfirmedAt != nil || len(store.candidates[itemKey("tenant", group.ID)]) != 0 {
				t.Fatalf("rejected confirmation left partial state: %#v err=%v", stored, err)
			}
		})
	}
}
