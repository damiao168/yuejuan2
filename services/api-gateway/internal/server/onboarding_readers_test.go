package server

import (
	"context"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/onboarding"
)

func TestOnboardingBusinessUserIgnoresPlatformAndServiceAccounts(t *testing.T) {
	store := auth.NewMemoryStore()
	for _, user := range []auth.UserWithPassword{
		{User: auth.User{ID: "platform", TenantID: "tenant-1", TenantCode: "tenant", Username: "platform", Status: "active", Roles: []string{"platform_admin"}}},
		{User: auth.User{ID: "worker", TenantID: "tenant-1", TenantCode: "tenant", Username: "worker", Status: "active", Roles: []string{"page_processing_worker"}}},
	} {
		store.AddUser(user)
	}

	reader := onboardingUserReader{store: store}
	ready, err := reader.HasBusinessUser(context.Background(), "tenant-1", onboarding.ResourceScope{TenantWide: true})
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("platform and service accounts must not satisfy school staff readiness")
	}

	store.AddUser(auth.UserWithPassword{User: auth.User{
		ID: "teacher", TenantID: "tenant-1", TenantCode: "tenant", Username: "teacher", Status: "active", Roles: []string{"teacher"},
	}})
	ready, err = reader.HasBusinessUser(context.Background(), "tenant-1", onboarding.ResourceScope{TenantWide: true})
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("an active teaching user must satisfy school staff readiness")
	}
}
