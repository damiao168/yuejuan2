package org

import (
	"context"
	"testing"
)

func TestBatch27OnboardingReadinessKeepsTenantSchoolAndActiveScope(t *testing.T) {
	store := NewMemoryStore()
	store.schools["a"] = School{ID: "a", TenantID: "tenant", Status: "active"}
	store.schools["b"] = School{ID: "b", TenantID: "tenant", Status: "active"}
	store.schools["inactive"] = School{ID: "inactive", TenantID: "tenant", Status: "inactive"}
	store.schools["foreign"] = School{ID: "foreign", TenantID: "other", Status: "active"}
	store.grades["a"] = Grade{TenantID: "tenant", SchoolID: "a", Status: "active"}
	store.grades["b"] = Grade{TenantID: "tenant", SchoolID: "b", Status: "inactive"}
	store.classes["b"] = Class{TenantID: "tenant", SchoolID: "b", Status: "active"}
	store.classes["foreign"] = Class{TenantID: "other", SchoolID: "a", Status: "active"}
	store.students["b"] = Student{TenantID: "tenant", SchoolID: "b", Status: "active"}
	store.students["a"] = Student{TenantID: "tenant", SchoolID: "a", Status: "inactive"}
	for _, tc := range []struct {
		name    string
		schools []string
		wide    bool
		want    OnboardingReadinessSummary
	}{
		{name: "empty allowed scope"},
		{name: "active grade without class", schools: []string{"a"}, want: OnboardingReadinessSummary{HasSchool: true}},
		{name: "active class without grade", schools: []string{"b"}, want: OnboardingReadinessSummary{HasSchool: true, HasStudents: true}},
		{name: "all selected schools", schools: []string{"a", "b"}, want: OnboardingReadinessSummary{true, true, true}},
		{name: "tenant wide", wide: true, want: OnboardingReadinessSummary{true, true, true}},
		{name: "foreign tenant not included", schools: []string{"foreign"}},
		{name: "inactive school has no records", schools: []string{"inactive"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.OnboardingReadiness(context.Background(), "tenant", tc.schools, tc.wide)
			if err != nil || got != tc.want {
				t.Fatalf("summary=%#v want=%#v err=%v", got, tc.want, err)
			}
		})
	}
}
