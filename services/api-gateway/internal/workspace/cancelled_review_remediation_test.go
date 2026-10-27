package workspace

import (
	"context"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/review"
)

func TestCancelledReviewExcludedFromPendingAndProgressTotal(t *testing.T) {
	for _, tc := range []struct {
		name           string
		tasks          []review.ReviewTask
		total, pending int
	}{
		{"cancelled only", []review.ReviewTask{{Status: "cancelled"}}, 0, 0},
		{"new run completed", []review.ReviewTask{{Status: "cancelled"}, {Status: "submitted"}, {Status: "completed"}}, 2, 0},
		{"actionable work remains", []review.ReviewTask{{Status: "cancelled"}, {Status: "submitted"}, {Status: "pending"}, {Status: "returned"}, {Status: "assigned"}, {Status: "in_progress"}}, 5, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewService(Dependencies{Reviews: fakeReviewReader{tasks: tc.tasks}})
			var result Projection
			total := s.applyReviewCounts(context.Background(), exam.Exam{ID: "exam", TenantID: "tenant"}, &result)
			if total != tc.total || result.Counts.PendingReviewCount != tc.pending {
				t.Fatalf("total=%d pending=%d, want %d/%d", total, result.Counts.PendingReviewCount, tc.total, tc.pending)
			}
		})
	}
}
