package auth

import "testing"

func TestSeedBoundaryRequiresAssigneeEvenWithBroadScope(t *testing.T) {
	boundary := ResourceBoundary{ResourceType: "seed_task", ResourceID: "seed", TenantID: "tenant", AssignedTo: "grader"}
	for _, tc := range []struct {
		name  string
		scope AccessScope
		want  bool
	}{
		{"assigned", AccessScope{TenantID: "tenant", ActorID: "grader", AssignedOnly: true}, true},
		{"other grader", AccessScope{TenantID: "tenant", ActorID: "other", AssignedOnly: true}, false},
		{"other tenant", AccessScope{TenantID: "other", ActorID: "grader", TenantWide: true}, false},
		{"manager is not assignee", AccessScope{TenantID: "tenant", ActorID: "manager", TenantWide: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AllowsResourceBoundary(tc.scope, boundary); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
