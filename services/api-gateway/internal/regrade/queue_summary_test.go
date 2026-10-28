package regrade

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type summaryQueueStore struct {
	*MemoryStore
	tenant string
	actor  string
	ids    []string
}

func (s *summaryQueueStore) AssignedQueueSummaries(_ context.Context, tenant, actor string, ids []string) (map[string]QueueSummary, error) {
	s.tenant, s.actor, s.ids = tenant, actor, ids
	out := map[string]QueueSummary{}
	for _, id := range ids {
		out[id] = QueueSummary{ExamName: "合成考试", Subject: "math", QuestionNo: "3", AnonymousCode: "复评-匿名码"}
	}
	return out, nil
}
func TestRegradeQueueSummaryKeepsIndependentViewAndAssignmentScope(t *testing.T) {
	ctx := context.Background()
	memory, _, claimed := claimIndependentRegradeFixture(t, ctx)
	store := &summaryQueueStore{MemoryStore: memory}
	items, err := NewService(store).ListAssigned(ctx, "tenant-a", "reviewer-a")
	if err != nil || len(items) != 1 {
		t.Fatalf("queue=%+v error=%v", items, err)
	}
	if store.tenant != "tenant-a" || store.actor != "reviewer-a" || len(store.ids) != 1 || store.ids[0] != claimed.ID || items[0].QuestionNo != "3" {
		t.Fatalf("incorrect summary scope: %+v %+v", store, items)
	}
	raw, _ := json.Marshal(items)
	for _, forbidden := range []string{"old-grade-secret", "old_score", "submission_id", "student_id", "original_reviewer"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("independent queue leaked %s: %s", forbidden, raw)
		}
	}
}
