package auth

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestAccountProfileRequiresAuthenticatedIdentity(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(NewMemoryStore(), 0).AccountProfile(response, httptest.NewRequest(http.MethodGet, "/api/v1/auth/profile", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated profile status = %d", response.Code)
	}
}

func TestPostgresAccountProfileIncludesSchoolAndSubjectsWithoutLoginProjection(t *testing.T) {
	store, script := openAuthBatch49Store(t, []authBatch49Step{{
		kind: "query", fragment: "COALESCE(u.school_id::text,''), u.teaching_subjects", args: []any{"tenant-self", "teacher-self"}, columns: 7,
		rows: [][]driver.Value{{"teacher-self", "grader-self", "本人教师", "13800138000", "T-001", "school-self", "{mathematics,physics}"}},
	}})
	profile, err := store.ReadAccountProfile(context.Background(), "tenant-self", "teacher-self")
	if err != nil || profile.SchoolID != "school-self" || profile.PhoneMasked != "138****8000" || !slices.Equal(profile.TeachingSubjects, []string{"mathematics", "physics"}) {
		t.Fatalf("profile=%+v error=%v", profile, err)
	}
	assertAuthBatch49Script(t, script, "")
}

func TestAccountProfileReadsOnlySessionOwnerAndSanitizesProjection(t *testing.T) {
	store := NewMemoryStore()
	actor := User{ID: "teacher-self", TenantID: "tenant-self", TenantCode: "self", Username: "self", DisplayName: "本人教师", Status: "active", Roles: []string{"grader"}}
	store.AddUser(UserWithPassword{User: actor, PasswordHash: "must-not-leak-password-hash", SchoolID: "school-self", PhoneNormalized: "13800138000", EmployeeNo: "T-001", SecurityEpoch: 197, TeachingSubjects: []string{"mathematics"}})
	store.AddUser(UserWithPassword{User: User{ID: "other", TenantID: "other-tenant", TenantCode: "other", Username: "other", DisplayName: "不得返回的教师", Status: "active"}, TeachingSubjects: []string{"english"}})
	// 客户端即使伪造目标账号和租户，资料读取仍只能使用本次会话的身份。
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/profile?user_id=other&tenant_id=other-tenant", nil)
	request = request.WithContext(WithUser(context.Background(), actor))
	response := httptest.NewRecorder()
	NewHandler(store, 0).AccountProfile(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("profile status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("personal profile must not be cached")
	}
	var body struct {
		Profile map[string]any `json:"profile"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Profile["id"] != actor.ID || body.Profile["phone_masked"] != "138****8000" || body.Profile["employee_no"] != "T-001" {
		t.Fatalf("unexpected own profile: %#v", body.Profile)
	}
	for _, secret := range []string{"13800138000", "must-not-leak-password-hash", "SecurityEpoch", "不得返回的教师", "other-tenant"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("profile leaked %q", secret)
		}
	}
	if len(body.Profile) != 8 {
		t.Fatalf("unexpected profile fields: %#v", body.Profile)
	}
}
