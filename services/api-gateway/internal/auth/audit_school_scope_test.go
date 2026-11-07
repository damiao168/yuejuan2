package auth

import (
	"slices"
	"testing"
)

func TestMemorySchoolAuditUsesTargetSnapshotInsteadOfActorOrFreeText(t *testing.T) {
	store := NewMemoryStore()
	store.AddUser(UserWithPassword{User: User{ID: "actor", TenantID: "tenant", TenantCode: "tenant", Username: "actor"}, SchoolID: "school-a"})
	store.AddUser(UserWithPassword{User: User{ID: "foreign", TenantID: "tenant", TenantCode: "tenant", Username: "foreign"}, SchoolID: "school-b"})
	store.AddResourceBoundary("exam", "exam-a", ResourceBoundary{TenantID: "tenant", SchoolID: "school-a", ExamID: "exam-a"})
	store.AddResourceBoundary("exam", "exam-b", ResourceBoundary{TenantID: "tenant", SchoolID: "school-b", ExamID: "exam-b"})
	for _, event := range []AuditEvent{
		{TenantID: "tenant", ActorID: "actor", Action: "own.login", TargetType: "user", TargetID: "actor"},
		{TenantID: "tenant", ActorID: "actor", Action: "local.exam", TargetType: "exam", TargetID: "exam-a"},
		{TenantID: "tenant", ActorID: "actor", Action: "foreign.exam", TargetType: "exam", TargetID: "exam-b"},
		{TenantID: "tenant", ActorID: "actor", Action: "foreign.user", TargetType: "user", TargetID: "foreign"},
		{TenantID: "tenant", ActorID: "actor", Action: "unknown", TargetType: "unknown", AfterValue: map[string]any{"school_id": "school-a"}},
	} {
		if err := store.Audit(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	store.AddResourceBoundary("exam", "exam-b", ResourceBoundary{TenantID: "tenant", SchoolID: "school-a", ExamID: "exam-b"})
	items, err := store.ListAudits(t.Context(), "tenant", AuditFilter{ScopeMode: "school", SchoolIDs: []string{"school-a"}})
	actions := []string{}
	for _, record := range items {
		actions = append(actions, record.Action)
	}
	slices.Sort(actions)
	if err != nil || !slices.Equal(actions, []string{"local.exam", "own.login"}) {
		t.Fatalf("scoped audit actions=%v error=%v", actions, err)
	}
}
