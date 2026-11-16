package auth

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestExamCollectionAccessKeepsAssignmentOutOfManagementScope(t *testing.T) {
	const tenant = "tenant"
	scope := AccessScope{
		TenantID: tenant, ActorID: "reviewer",
		ClassIDs: []string{"assigned-class"}, ExamIDs: []string{"assigned-exam"},
	}
	boundaries := map[string]ResourceBoundary{
		"assigned-submission": {TenantID: tenant, SchoolID: "assigned-school", ExamID: "assigned-exam"},
		"other-submission":    {TenantID: tenant, SchoolID: "other-school", ExamID: "other-exam"},
		"foreign-submission":  {TenantID: "foreign-tenant", SchoolID: "assigned-school", ExamID: "assigned-exam"},
	}
	resourceResolver := collectionResolverFunc(func(_ context.Context, _ AccessScope, resourceType, resourceID string) (ResourceBoundary, error) {
		boundary, found := boundaries[resourceID]
		if !found {
			return ResourceBoundary{}, ErrResourceBoundaryNotFound
		}
		boundary.ResourceType = resourceType
		boundary.ResourceID = resourceID
		return boundary, nil
	})
	read := ExamCollectionAccess{Scope: &scope, Resolver: resourceResolver}
	manage := ExamCollectionAccess{Scope: &scope, Resolver: resourceResolver, Management: true}
	for _, tc := range []struct {
		name, resourceID string
		access           ExamCollectionAccess
		want             bool
	}{
		{"read assigned", "assigned-submission", read, true},
		{"read unrelated", "other-submission", read, false},
		{"read unresolved", "deleted-submission", read, false},
		{"read foreign tenant", "foreign-submission", read, false},
		{"manage assigned", "assigned-submission", manage, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 显示的考试 ID 由列表请求提供，集合权限以服务端解析出的考试为准。
			got, err := tc.access.Allows(t.Context(), tenant, "submission", tc.resourceID, "assigned-exam")
			if err != nil || got != tc.want {
				t.Fatalf("Allows returned (%t, %v), want %t", got, err, tc.want)
			}
		})
	}
	readArgs, err := read.SQLArgs(tenant)
	if want := []any{false, []string{}, []string{"assigned-class"}, []string{"assigned-exam"}}; err != nil || !reflect.DeepEqual(readArgs, want) {
		t.Fatalf("SQL read scope values = %v, err=%v; want %v", readArgs, err, want)
	}
	managementArgs, err := manage.SQLArgs(tenant)
	if want := []any{false, []string{}, []string{}, []string{}}; err != nil || !reflect.DeepEqual(managementArgs, want) {
		t.Fatalf("SQL management scope values = %v, err=%v; want %v", managementArgs, err, want)
	}
}

func TestExamCollectionAccessReportsResolverFailureAndInvalidScope(t *testing.T) {
	const tenant = "tenant"
	unavailable := errors.New("resource boundary store unavailable")
	calls := 0
	scope := AccessScope{TenantID: tenant, ActorID: "reviewer", TenantWide: true}
	access := ExamCollectionAccess{Scope: &scope, Resolver: collectionResolverFunc(func(context.Context, AccessScope, string, string) (ResourceBoundary, error) {
		calls++
		return ResourceBoundary{}, unavailable
	})}
	if allowed, err := access.Allows(t.Context(), tenant, "exam", "exam", "exam"); allowed || !errors.Is(err, unavailable) {
		t.Fatalf("unavailable resolver returned (%t, %v) instead of a failed query", allowed, err)
	}
	if allowed, err := access.Allows(t.Context(), "other-tenant", "exam", "exam", "exam"); allowed || !errors.Is(err, ErrForbidden) || calls != 1 {
		t.Fatalf("invalid tenant scope returned (%t, %v) with %d resolver calls", allowed, err, calls)
	}
	if args, err := access.SQLArgs("other-tenant"); args != nil || !errors.Is(err, ErrForbidden) {
		t.Fatalf("invalid tenant scope returned SQL args %v, err=%v", args, err)
	}
}

type collectionResolverFunc func(context.Context, AccessScope, string, string) (ResourceBoundary, error)

func (resolve collectionResolverFunc) ResolveResourceBoundary(ctx context.Context, scope AccessScope, resourceType, resourceID string) (ResourceBoundary, error) {
	return resolve(ctx, scope, resourceType, resourceID)
}
