package exam

import (
	"context"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestMemorySessionExamsHonorsTenantAndExamScope(t *testing.T) {
	store := NewMemoryStore()
	store.items = map[string]Exam{
		"math":          {ID: "math", TenantID: "tenant-1", SchoolID: "school-1", SessionID: "session-1", Subject: "mathematics"},
		"physics":       {ID: "physics", TenantID: "tenant-1", SchoolID: "school-2", SessionID: "session-1", Subject: "physics"},
		"foreign":       {ID: "foreign", TenantID: "tenant-2", SchoolID: "school-1", SessionID: "session-1", Subject: "history"},
		"other-session": {ID: "other-session", TenantID: "tenant-1", SchoolID: "school-1", SessionID: "session-2", Subject: "chinese"},
	}
	items, err := store.ListSessionExams(context.Background(), auth.AccessScope{TenantID: "tenant-1", ExamIDs: []string{"math"}}, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "math" {
		t.Fatalf("scope leaked sibling exams: %#v", items)
	}
}
