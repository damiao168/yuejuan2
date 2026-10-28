package backmark

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
		out[id] = QueueSummary{ExamName: "合成考试", Subject: "math", QuestionNo: "2", AnonymousCode: "匿名回标"}
	}
	return out, nil
}

func TestAssignedQueueSummaryUsesOnlyCurrentPageAndDoesNotExposeOriginalGrade(t *testing.T) {
	ctx := context.Background()
	memory := NewMemoryStore()
	store := &summaryQueueStore{MemoryStore: memory}
	memory.SeedSource(SourceTask{ReviewTaskID: "task", OriginalGradeID: "old-grade-secret", OriginalReviewer: "original-reviewer", OriginalScore: 9, MaxScore: 10})
	service := NewService(store).WithContextSource(backmarkContextSource{})
	if _, err := service.Create(ctx, "tenant", "exam", "question", "manager", CreateInput{SourceIncidentID: "incident", ReassignedTo: "reviewer", Policy: Policy{Disposition: DispositionConfirm}}); err != nil {
		t.Fatal(err)
	}
	items, err := service.ListAssigned(ctx, "tenant", "reviewer")
	if err != nil || len(items) != 1 {
		t.Fatalf("queue=%+v error=%v", items, err)
	}
	if store.tenant != "tenant" || store.actor != "reviewer" || len(store.ids) != 1 || store.ids[0] != items[0].ID || items[0].QuestionNo != "2" {
		t.Fatalf("incorrect summary scope: %+v %+v", store, items)
	}
	raw, _ := json.Marshal(items)
	for _, forbidden := range []string{"old-grade-secret", "original-reviewer", "original_score", "student_id"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("blind queue leaked %s: %s", forbidden, raw)
		}
	}
}
