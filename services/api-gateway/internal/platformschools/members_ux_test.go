package platformschools

import (
	"testing"
	"time"
)

func TestMembersSeparatesInvitedDisabledAndUnknownWithoutChangingWholeSchoolSummary(t *testing.T) {
	store := NewMemoryStore()
	store.PutSchool(PlatformSchoolSummary{TenantID: "school", Status: "active"})
	expires := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	store.PutMembers("school", []Member{{ID: "a", Status: "active"}, {ID: "b", Status: "invited", ActivationExpiresAt: &expires}, {ID: "c", Status: "disabled"}, {ID: "d", Status: "future-status"}})
	got, err := NewService(store).Members(t.Context(), "school", "", "", "invited")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Members) != 1 || got.Members[0].ID != "b" || got.Members[0].ActivationExpiresAt == nil || !got.Members[0].ActivationExpiresAt.Equal(expires) {
		t.Fatalf("invitation fact missing: %+v", got)
	}
	if got.Summary.Total != 4 || got.Summary.Active != 1 || got.Summary.Invited != 1 || got.Summary.Disabled != 1 || got.Summary.Unknown != 1 {
		t.Fatalf("incorrect status accounting: %+v", got.Summary)
	}
}
