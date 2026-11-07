package auth

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"
)

func authBatch50Input() CreateManagedUserInput {
	return CreateManagedUserInput{TeachingSubjects: []string{"mathematics"}, Username: "login", DisplayName: "name", Phone: "13800138000", EmployeeNo: "employee", RoleCodes: []string{"teacher", "grader"}, SchoolID: " school ", ActivationTokenHash: "activation-hash", ActivationExpiresAt: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}
}

func authBatch50CreateSteps(input CreateManagedUserInput) []authBatch49Step {
	created := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	return []authBatch49Step{
		{kind: "query", fragment: "scope_type FROM role", args: []any{"tenant", "grader"}, columns: 2, rows: [][]driver.Value{{"grader-id", "exam_task"}}},
		{kind: "query", fragment: "scope_type FROM role", args: []any{"tenant", "teacher"}, columns: 2, rows: [][]driver.Value{{"teacher-id", "exam_task"}}},
		{kind: "query", fragment: "FROM school WHERE", args: []any{"tenant", "school"}, columns: 1, rows: [][]driver.Value{{true}}},
		{kind: "query", fragment: "$8, $9, $9, string_to_array($10", args: []any{"tenant", "school", "login", "name", "password-hash", "13800138000", "employee", "invited", nil, "mathematics", input.MustChangePassword}, columns: 8, rows: [][]driver.Value{{"user-id", "login", "name", "invited", "school", nil, created, input.MustChangePassword}}},
		{kind: "exec", fragment: "INSERT INTO user_role", args: []any{"tenant", "user-id", "grader-id", `{"scope":"exam_task"}`}},
		{kind: "exec", fragment: "INSERT INTO user_role", args: []any{"tenant", "user-id", "teacher-id", `{"scope":"exam_task"}`}},
		{kind: "exec", fragment: "INSERT INTO auth_activation", args: []any{"tenant", "user-id", "activation-hash", input.ActivationExpiresAt}},
	}
}

func TestBatch50ManagedCreationKeepsAllBindingsInOneTransaction(t *testing.T) {
	for _, lateFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[lateFailure], func(t *testing.T) {
			assertManagedUserBindingsTransaction(t, lateFailure)
		})
	}
}

func TestBatch50ManagedCreationRejectsInvalidBindingsBeforeAccountInsert(t *testing.T) {
	for _, tc := range []struct {
		name  string
		index int
		row   []driver.Value
		want  error
	}{
		{"missing role", 0, nil, ErrRoleNotFound},
		{"wrong canonical scope", 0, []driver.Value{"grader-id", "tenant"}, ErrRoleAssignment},
		{"missing school", 2, []driver.Value{false}, ErrInvalidRoleBinding},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := authBatch50Input()
			steps := authBatch50CreateSteps(input)[:tc.index+1]
			steps[tc.index].rows = nil
			if tc.row != nil {
				steps[tc.index].rows = [][]driver.Value{tc.row}
			}
			store, script := openAuthBatch49Store(t, steps)
			_, err := store.CreateManagedUser(t.Context(), User{TenantID: "tenant", Roles: []string{"tenant_admin"}}, AccessScope{TenantWide: true}, input, "password-hash")
			if !errors.Is(err, tc.want) {
				t.Fatalf("binding error=%v, want %v", err, tc.want)
			}
			assertAuthBatch49Script(t, script, "rollback")
		})
	}
}

func TestBatch50ManagedCreationPreservesUniqueErrorPriority(t *testing.T) {
	for _, tc := range []struct {
		message string
		want    error
	}{
		{"duplicate uq_app_user_tenant_phone", ErrIdentityExists},
		{"unique uq_app_user_tenant_employee_no", ErrIdentityExists},
		{"duplicate username", ErrUsernameExists},
	} {
		input := authBatch50Input()
		steps := authBatch50CreateSteps(input)[:4]
		steps[3].err = errors.New(tc.message)
		store, script := openAuthBatch49Store(t, steps)
		_, err := store.CreateManagedUser(t.Context(), User{TenantID: "tenant", Roles: []string{"tenant_admin"}}, AccessScope{TenantWide: true}, input, "password-hash")
		if !errors.Is(err, tc.want) {
			t.Fatalf("error=%v, want %v", err, tc.want)
		}
		assertAuthBatch49Script(t, script, "rollback")
	}
}

func TestBatch50ManagedCreationSchoolDefaultAndPermissionPrecedeDatabase(t *testing.T) {
	actor := User{Roles: []string{"school_admin"}}
	input := CreateManagedUserInput{TeachingSubjects: []string{"mathematics"}, RoleCode: "teacher"}
	scope := AccessScope{SchoolIDs: []string{"school", "teaching-school"}, schoolWide: true, schoolManagementIDs: []string{"school"}}
	got, err := managedUserCreationSchool(actor, scope, input, []string{"teacher"})
	if err != nil || got != "school" {
		t.Fatalf("single school default=%q error=%v", got, err)
	}
	store := NewPostgresStore(nil)
	if _, err := store.CreateManagedUser(t.Context(), actor, AccessScope{SchoolIDs: []string{"school", "other"}, schoolWide: true, schoolManagementIDs: []string{"school", "other"}}, input, "hash"); !errors.Is(err, ErrInvalidRoleBinding) {
		t.Fatalf("ambiguous school error=%v", err)
	}
	input.SchoolID = "outside"
	if _, err := store.CreateManagedUser(t.Context(), actor, scope, input, "hash"); !errors.Is(err, ErrOrganizationScope) {
		t.Fatalf("outside school error=%v", err)
	}
}

func assertManagedUserBindingsTransaction(t *testing.T, lateFailure bool) {
	t.Helper()
	input := authBatch50Input()
	steps := authBatch50CreateSteps(input)
	failure := errors.New("activation write failed")
	if lateFailure {
		steps[len(steps)-1].err = failure
	}
	store, script := openAuthBatch49Store(t, steps)
	got, err := store.CreateManagedUser(t.Context(), User{TenantID: "tenant", Roles: []string{"tenant_admin"}}, AccessScope{TenantWide: true}, input, "password-hash")
	if lateFailure {
		if !errors.Is(err, failure) || got.ID != "" {
			t.Fatalf("late failure returned user=%+v error=%v", got, err)
		}
		assertAuthBatch49Script(t, script, "rollback")
		return
	}
	if err != nil || got.ID != "user-id" || got.Status != "invited" || got.ActivatedAt != nil || got.PhoneMasked == input.Phone || got.EmployeeNo != input.EmployeeNo || !reflect.DeepEqual(got.Roles, []string{"grader", "teacher"}) || !reflect.DeepEqual(got.TeachingSubjects, input.TeachingSubjects) {
		t.Fatalf("created user=%+v error=%v", got, err)
	}
	input.TeachingSubjects[0] = "changed-after-return"
	if got.TeachingSubjects[0] != "mathematics" {
		t.Fatal("returned subject slice aliases caller input")
	}
	assertAuthBatch49Script(t, script, "commit")
}
