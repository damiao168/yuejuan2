package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

// 使用超过近期验证时限的真实登录会话，确认取消密码弹窗后仍执行学校、角色和旧值检查。
func TestManagedProfileSaveWithoutRepeatedPassword(t *testing.T) {
	for _, tc := range []struct {
		name, targetID, schoolID, tenantID, role, expectedName string
		unauthenticated, denyPermission                        bool
		want                                                   int
	}{
		{name: "save within managed school", want: http.StatusOK},
		{name: "login required", unauthenticated: true, want: http.StatusUnauthorized},
		{name: "permission required", denyPermission: true, want: http.StatusForbidden},
		{name: "foreign school rejected", schoolID: "school-b", want: http.StatusForbidden},
		{name: "foreign tenant rejected", tenantID: "tenant-b", want: http.StatusNotFound},
		{name: "higher role rejected", role: "tenant_admin", want: http.StatusForbidden},
		{name: "own account rejected", targetID: "user-1", expectedName: "Teacher", want: http.StatusForbidden},
		{name: "stale profile rejected", expectedName: "Outdated", want: http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &recentAuthTestStore{
				MemoryStore: testAuthStoreWithPermissions(t, []string{"org:manage"}),
				stale:       true, denyPermissions: tc.denyPermission,
			}
			actor, err := store.FindUserByLogin(t.Context(), "demo", "teacher")
			if err != nil {
				t.Fatal(err)
			}
			actor.Roles = []string{"school_admin"}
			actor.SchoolID = "school-a"
			actor.DataScope = map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}
			store.AddUser(actor)
			schoolID, tenantID, role := tc.schoolID, tc.tenantID, tc.role
			if schoolID == "" {
				schoolID = "school-a"
			}
			if tenantID == "" {
				tenantID = actor.TenantID
			}
			if role == "" {
				role = "grader"
			}
			store.AddUser(auth.UserWithPassword{User: auth.User{
				ID: "target", TenantID: tenantID, TenantCode: "demo", Username: "target",
				DisplayName: "Original", Status: "active", Roles: []string{role},
			}, SchoolID: schoolID, TeachingSubjects: []string{"mathematics"}})
			router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, store, org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
			token := serverLogin(t, router)
			targetID, expectedName := tc.targetID, tc.expectedName
			if targetID == "" {
				targetID = "target"
			}
			if expectedName == "" {
				expectedName = "Original"
			}
			payload, err := json.Marshal(map[string]string{
				"display_name": "Updated", "employee_no": "", "expected_display_name": expectedName,
				"expected_employee_no": "", "expected_phone_masked": "",
			})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/"+targetID+"/profile", strings.NewReader(string(payload)))
			req.Header.Set("Content-Type", "application/json")
			if !tc.unauthenticated {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("profile save: got %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			target, err := store.FindUserByLogin(t.Context(), "demo", "target")
			if err != nil {
				t.Fatal(err)
			}
			wantName := "Original"
			if tc.want == http.StatusOK {
				wantName = "Updated"
			}
			if target.DisplayName != wantName {
				t.Fatalf("stored profile=%q, want %q", target.DisplayName, wantName)
			}
		})
	}
}
