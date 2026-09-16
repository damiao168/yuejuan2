package regrade

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRegradeTextLimitsCountRunes(t *testing.T) {
	for _, char := range []string{"a", "你", "🙂"} {
		for _, size := range []int{2000, 2001} {
			text := "  " + strings.Repeat(char, size) + "  "
			input := CreateInput{SourceReleaseID: "release-1", ReasonCode: ReasonOther, ReasonText: text, Strategy: StrategyHumanRecheck, IdempotencyKey: "unicode-test-key"}
			if validCreate(input) != (size == 2000) {
				t.Fatalf("reason char=%s size=%d", char, size)
			}
			if validReview(ReviewInput{Decision: ReviewAccept, Note: text}) != (size == 2000) {
				t.Fatalf("note char=%s size=%d", char, size)
			}
			_, err := NewService(NewMemoryStore()).RecordCandidate(context.Background(), "tenant-1", "missing", "grader-1", CandidateInput{Score: 1, Comment: text, ExpectedRevision: 1})
			if errors.Is(err, ErrInvalidInput) != (size == 2001) {
				t.Fatalf("comment char=%s size=%d err=%v", char, size, err)
			}
		}
	}
}
