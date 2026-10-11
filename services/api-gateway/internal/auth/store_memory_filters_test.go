package auth

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestMemoryAuditFiltersKeepInclusiveTimeBoundsAndStableCursor(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"c", "a", "b"} {
		store.audits = append(store.audits, AuditRecord{
			ID: id, TenantID: "tenant", ActorID: "actor", Action: "action",
			TargetType: "exam", TargetID: "exam", IPAddress: "127.0.0.1", CreatedAt: now,
		})
	}
	store.audits = append(store.audits, AuditRecord{ID: "foreign", TenantID: "other", CreatedAt: now})
	filter := AuditFilter{
		Action: "action", ActorID: "actor", TargetType: "exam", TargetID: "exam", ExamID: "exam",
		IPAddress: "127.0.0.1", CreatedFrom: now, CreatedTo: now, CursorCreatedAt: now, CursorID: "c", Limit: 1,
	}
	items, err := store.ListAudits(context.Background(), "tenant", filter)
	if err != nil || len(items) != 1 || items[0].ID != "b" {
		t.Fatalf("same-time cursor and inclusive bounds: items=%#v err=%v", items, err)
	}
	filter.ExamID = "different"
	items, err = store.ListAudits(context.Background(), "tenant", filter)
	if err != nil || len(items) != 0 {
		t.Fatalf("exam filter must intersect target filter: items=%#v err=%v", items, err)
	}
	filter.ScopeMode = "school"
	items, err = store.ListAudits(context.Background(), "tenant", filter)
	if err != nil || len(items) != 0 {
		t.Fatalf("unsupported audit scope must remain empty: items=%#v err=%v", items, err)
	}
}

func TestMemoryManagedUserFiltersIntersectSchoolRoleAndAliases(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"c", "a", "b"} {
		store.AddUser(UserWithPassword{
			User:     User{ID: id, TenantID: "tenant", TenantCode: "tenant", Username: id, Roles: []string{"teacher"}},
			SchoolID: "school", EmployeeNo: "EMP-42", CreatedAt: now,
		})
	}
	store.AddUser(UserWithPassword{
		User:     User{ID: "foreign", TenantID: "tenant", TenantCode: "tenant", Username: "foreign", Roles: []string{"teacher"}},
		SchoolID: "other", EmployeeNo: "EMP-42", CreatedAt: now,
	})
	filter := ManagedUserFilter{Query: "emp-42", Role: "teacher", RestrictSchools: true, SchoolIDs: []string{"school"}, CursorID: "c", CursorCreatedAt: now}
	items, err := store.ListManagedUsers(context.Background(), "tenant", filter)
	var ids []string
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	if err != nil || !reflect.DeepEqual(ids, []string{"b", "a"}) {
		t.Fatalf("combined managed-user filters: ids=%v err=%v", ids, err)
	}
	filter.Role = "grader"
	items, err = store.ListManagedUsers(context.Background(), "tenant", filter)
	if err != nil || len(items) != 0 {
		t.Fatalf("role filter must not broaden school matches: items=%#v err=%v", items, err)
	}
}

func TestMemoryLoginRiskContextIgnoresForeignExpiredAndUnusedFacts(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	store.riskEvents = []RiskEvent{
		{TenantID: "tenant", UserID: "user", Purpose: RiskPurposeLogin, OccurredAt: now, UserAgentHash: "agent", IPPrefix: "network"},
		{TenantID: "other", UserID: "user", Purpose: RiskPurposeLogin, OccurredAt: now},
		{TenantID: "tenant", UserID: "user", Purpose: RiskPurposeLogin, OccurredAt: now.Add(-RiskHistoryRetention - time.Second)},
	}
	store.devices["device"] = memoryDeviceBinding{TenantID: "tenant", UserID: "user", AssuranceLevel: 2, ExpiresAt: now}
	store.audits = []AuditRecord{
		{TenantID: "tenant", ActorID: "user", Action: "auth.login_failed", CreatedAt: now.Add(-RiskFailureWindow)},
		{TenantID: "tenant", ActorID: "user", Action: "auth.login_rate_limited", CreatedAt: now},
		{TenantID: "other", ActorID: "user", Action: "auth.login_failed", CreatedAt: now},
	}
	store.recoveries["unused"] = memoryRecovery{TenantID: "tenant", UserID: "user"}
	store.recoveries["foreign"] = memoryRecovery{TenantID: "other", UserID: "user", UsedAt: now}
	request := LoginRiskContextRequest{TenantID: "tenant", UserID: "user", DeviceTokenHash: "device", UserAgentHash: "agent", IPPrefix: "network", Now: now}
	result, err := store.LoadLoginRiskContext(context.Background(), request)
	want := LoginRiskContext{PriorSuccessfulLogins: 1, KnownUserAgent: true, KnownNetwork: true, RecentFailures: 2}
	if err != nil || result != want {
		t.Fatalf("isolated risk context=%#v want=%#v err=%v", result, want, err)
	}
	store.devices["device"] = memoryDeviceBinding{TenantID: "tenant", UserID: "user", AssuranceLevel: 2, ExpiresAt: now.Add(time.Hour)}
	store.recoveries["used"] = memoryRecovery{TenantID: "tenant", UserID: "user", UsedAt: now.Add(-RiskRecoveryWindow)}
	result, err = store.LoadLoginRiskContext(context.Background(), request)
	if err != nil || !result.KnownDevice || !result.TrustedDevice || !result.RecentRecovery {
		t.Fatalf("valid device and inclusive recovery cutoff: result=%#v err=%v", result, err)
	}
}
