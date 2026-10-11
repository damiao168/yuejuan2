package org_test

import (
	"context"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/org"
)

func TestBatch24MemoryTenantListingPreservesScopeOrderAndCursor(t *testing.T) {
	store := org.NewMemoryStore()
	ctx := context.Background()
	inputs := []org.TenantProvision{
		{Name: "Alpha", Code: "DUP"},
		{Name: "Beta", Code: "DUP"},
		{Name: "Delta", Code: "Z", Status: "disabled"},
		{Name: "Other", Code: "A"},
	}
	tenants := make([]org.Tenant, 0, len(inputs))
	for _, input := range inputs {
		item, err := store.CreateTenant(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		tenants = append(tenants, item)
	}
	all := []string{tenants[3].ID, tenants[0].ID, tenants[1].ID, tenants[2].ID}
	for _, scenario := range []struct {
		name   string
		all    bool
		filter org.TenantListFilter
		want   []string
	}{
		{"ordered including disabled", true, org.TenantListFilter{}, all},
		{"own tenant only", false, org.TenantListFilter{}, []string{tenants[1].ID}},
		{"name query ignores case", true, org.TenantListFilter{Query: "beTA"}, []string{tenants[1].ID}},
		{"code query ignores case", true, org.TenantListFilter{Query: "dup"}, []string{tenants[0].ID, tenants[1].ID}},
		{"store preserves query spaces", true, org.TenantListFilter{Query: " DUP "}, []string{}},
		{"scope still limits query", false, org.TenantListFilter{Query: "Alpha"}, []string{}},
		{"exclusive tuple cursor", true, org.TenantListFilter{CursorCode: "DUP", CursorID: tenants[0].ID}, []string{tenants[1].ID, tenants[2].ID}},
		{"cursor requires code", true, org.TenantListFilter{CursorID: tenants[0].ID}, all},
		{"cursor requires id", true, org.TenantListFilter{CursorCode: "DUP"}, all},
		{"cursor beyond last", true, org.TenantListFilter{CursorCode: "ZZ", CursorID: tenants[2].ID}, []string{}},
		{"limit follows cursor", true, org.TenantListFilter{CursorCode: "DUP", CursorID: tenants[0].ID, Limit: 1}, []string{tenants[1].ID}},
		{"negative limit is unbounded", true, org.TenantListFilter{Limit: -1}, all},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			items, err := store.ListTenants(ctx, tenants[1].ID, scenario.all, scenario.filter)
			if err != nil {
				t.Fatal(err)
			}
			if items == nil || !reflect.DeepEqual(batch24TenantIDs(items), scenario.want) {
				t.Fatalf("tenants=%#v want IDs=%v", items, scenario.want)
			}
		})
	}
	items, err := store.ListTenants(ctx, tenants[1].ID, false, org.TenantListFilter{})
	if err != nil || len(items) != 1 {
		t.Fatalf("own tenant=%#v err=%v", items, err)
	}
	items[0].Name = "changed outside store"
	again, err := store.ListTenants(ctx, tenants[1].ID, false, org.TenantListFilter{})
	if err != nil || len(again) != 1 || again[0].Name != "Beta" {
		t.Fatalf("list result mutated store: %#v err=%v", again, err)
	}
}

func TestBatch24MemoryStudentListingPreservesScopeFiltersAndCursor(t *testing.T) {
	store := org.NewMemoryStore()
	ctx := context.Background()
	for _, input := range []org.Student{
		{ID: "student-a", TenantID: "tenant-a", SchoolID: "school-a", ClassID: "class-a", StudentNo: "DUP", Name: "Alice", Status: "disabled"},
		{ID: "student-b", TenantID: "tenant-a", SchoolID: "school-b", ClassID: "class-b", StudentNo: "DUP", Name: "Bob"},
		{ID: "student-c", TenantID: "tenant-a", SchoolID: "school-c", ClassID: "class-b", StudentNo: "Z", Name: "Carol"},
		{ID: "student-empty", TenantID: "tenant-a", SchoolID: "school-d", ClassID: "class-a", Name: "Empty"},
		{ID: "student-other", TenantID: "tenant-b", SchoolID: "school-e", ClassID: "class-a", StudentNo: "DUP", Name: "Else"},
	} {
		if _, err := store.CreateStudent(ctx, input.TenantID, input); err != nil {
			t.Fatal(err)
		}
	}
	all := []string{"student-empty", "student-a", "student-b", "student-c"}
	// 本人例外与显式班级、IDs、查询条件相交；它只放宽班级访问限制。
	for _, scenario := range []struct {
		name   string
		filter org.StudentListFilter
		want   []string
	}{
		{"ordered including disabled", org.StudentListFilter{}, all},
		{"allowed class", org.StudentListFilter{RestrictClasses: true, ClassIDs: []string{"class-a"}}, []string{"student-empty", "student-a"}},
		{"empty restricted scope denies all", org.StudentListFilter{RestrictClasses: true}, []string{}},
		{"self outside allowed classes", org.StudentListFilter{RestrictClasses: true, ClassIDs: []string{"class-a"}, StudentID: "student-b"}, []string{"student-empty", "student-a", "student-b"}},
		{"self alone", org.StudentListFilter{RestrictClasses: true, StudentID: "student-b"}, []string{"student-b"}},
		{"self does not bypass tenant", org.StudentListFilter{RestrictClasses: true, StudentID: "student-other"}, []string{}},
		{"self does not bypass explicit class", org.StudentListFilter{ClassID: "class-a", RestrictClasses: true, ClassIDs: []string{"class-a"}, StudentID: "student-b"}, []string{"student-empty", "student-a"}},
		{"self does not bypass ids", org.StudentListFilter{RestrictClasses: true, StudentID: "student-b", StudentIDs: []string{"student-a"}}, []string{}},
		{"self does not bypass query", org.StudentListFilter{RestrictClasses: true, StudentID: "student-b", Query: "Alice"}, []string{}},
		{"self query ignores case", org.StudentListFilter{RestrictClasses: true, StudentID: "student-b", Query: "BoB"}, []string{"student-b"}},
		{"duplicate ids do not duplicate rows", org.StudentListFilter{StudentIDs: []string{"student-b", "student-b"}}, []string{"student-b"}},
		{"ids do not bypass tenant", org.StudentListFilter{StudentIDs: []string{"student-other"}}, []string{}},
		{"student number query ignores case", org.StudentListFilter{Query: "dup"}, []string{"student-a", "student-b"}},
		{"store preserves query spaces", org.StudentListFilter{Query: " dup "}, []string{}},
		{"exclusive tuple cursor", org.StudentListFilter{CursorStudentNo: "DUP", CursorID: "student-a"}, []string{"student-b", "student-c"}},
		{"cursor allows empty student number", org.StudentListFilter{CursorID: "student-empty"}, []string{"student-a", "student-b", "student-c"}},
		{"cursor requires only id", org.StudentListFilter{CursorStudentNo: "DUP"}, all},
		{"cursor beyond last", org.StudentListFilter{CursorStudentNo: "ZZ", CursorID: "student-c"}, []string{}},
		{"limit follows cursor", org.StudentListFilter{CursorStudentNo: "DUP", CursorID: "student-a", Limit: 1}, []string{"student-b"}},
		{"negative limit is unbounded", org.StudentListFilter{Limit: -1}, all},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			items, err := store.ListStudents(ctx, "tenant-a", scenario.filter)
			if err != nil {
				t.Fatal(err)
			}
			if items == nil || !reflect.DeepEqual(batch24StudentIDs(items), scenario.want) {
				t.Fatalf("students=%#v want IDs=%v", items, scenario.want)
			}
		})
	}
	items, err := store.ListStudents(ctx, "tenant-a", org.StudentListFilter{StudentIDs: []string{"student-b"}})
	if err != nil || len(items) != 1 {
		t.Fatalf("student-b=%#v err=%v", items, err)
	}
	items[0].ClassID, items[0].Name = "outside-class", "changed outside store"
	again, err := store.ListStudents(ctx, "tenant-a", org.StudentListFilter{StudentIDs: []string{"student-b"}})
	if err != nil || len(again) != 1 || again[0].ClassID != "class-b" || again[0].Name != "Bob" {
		t.Fatalf("list result mutated store: %#v err=%v", again, err)
	}
}

func batch24TenantIDs(items []org.Tenant) []string {
	ids := []string{}
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func batch24StudentIDs(items []org.Student) []string {
	ids := []string{}
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}
