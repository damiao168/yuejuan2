package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestManagedTeacherRequiresConfirmedInitialPassword(t *testing.T) {
	for _, test := range []struct {
		name, password, confirmation string
		want                         int
		roles                        []string
	}{
		{"missing", "", "", http.StatusBadRequest, nil},
		{"confirmation missing", "InitialPass123!", "", http.StatusBadRequest, nil},
		{"mismatch", "InitialPass123!", "OtherInitial123!", http.StatusBadRequest, nil},
		{"too short", "1234567", "1234567", http.StatusBadRequest, nil},
		{"valid passphrase", " 初始密码保留空格和中文123 ", " 初始密码保留空格和中文123 ", http.StatusCreated, nil},
		{"arbitrator missing", "", "", http.StatusBadRequest, []string{"arbitrator"}},
		{"arbitrator valid", "InitialPass123!", "InitialPass123!", http.StatusCreated, []string{"arbitrator"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, actor, scope := managedAccessFixture(t)
			roles := test.roles
			if roles == nil {
				roles = []string{"teacher", "grader", "arbitrator"}
			}
			body, err := json.Marshal(map[string]any{"username": "new-teacher", "display_name": "教师", "role_codes": roles, "school_id": "school-1", "teaching_subjects": []string{"math"}, "password": test.password, "password_confirm": test.confirmation})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(string(body)))
			req = req.WithContext(WithAccessScope(WithUser(req.Context(), actor), scope))
			rec := httptest.NewRecorder()
			NewHandler(store, time.Hour).CreateManagedUser(rec, req)
			if rec.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, test.want, rec.Body.String())
			}
			stored, err := store.FindUserByLogin(context.Background(), actor.TenantCode, "new-teacher")
			if test.want != http.StatusCreated {
				if err == nil {
					t.Fatal("rejected creation left an account")
				}
				return
			}
			if err != nil || !stored.MustChangePassword || !CheckPassword(stored.PasswordHash, test.password) || stored.Status != "active" {
				t.Fatalf("initial password account state invalid: err=%v", err)
			}
			access, err := store.ResolveAccessScope(context.Background(), stored.User)
			if err != nil || !access.AssignedOnly || access.TenantWide || access.IsSchoolWide() || len(access.ClassIDs) != 0 {
				t.Fatal("initial password must not expand assigned-task authorization")
			}
			var response struct {
				User       ManagedUser     `json:"user"`
				Activation json.RawMessage `json:"activation"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if !response.User.MustChangePassword || len(response.Activation) != 0 || strings.Contains(rec.Body.String(), test.password) {
				t.Fatal("creation must return the first-change marker without a password or invitation")
			}
		})
	}
}

func TestManagedSchoolAdministratorStillUsesIndependentInvitation(t *testing.T) {
	store, actor, scope := managedAccessFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{"username":"school-owner","display_name":"学校管理员","role_codes":["school_admin"],"school_id":"school-1"}`))
	req = req.WithContext(WithAccessScope(WithUser(req.Context(), actor), scope))
	rec := httptest.NewRecorder()
	NewHandler(store, time.Hour).CreateManagedUser(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"status":"invited"`) || !strings.Contains(rec.Body.String(), `"activation"`) {
		t.Fatalf("administrator invitation changed: %d %s", rec.Code, rec.Body.String())
	}
}
