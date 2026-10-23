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

func TestPasswordMinimumAcceptsCommonPasswordsWithinRoleLength(t *testing.T) {
	for _, test := range []struct {
		roles    []string
		password string
		minimum  int
		valid    bool
	}{
		{[]string{"grader"}, "1234567", 8, false},
		{[]string{"grader"}, "12345678", 8, true},
		{[]string{"teacher"}, "😀😀😀😀😀😀😀😀", 8, true},
		{[]string{"school_admin"}, "12345678901", 12, false},
		{[]string{"grader", "school_admin"}, "123456789012", 12, true},
		{[]string{"tenant_admin"}, "123456789012345", 12, true},
		{[]string{"platform_admin"}, "passwordpassword", 12, true},
		{[]string{"grader"}, "        ", 8, false},
		{[]string{"grader"}, strings.Repeat("a", maxPasswordBytes+1), 8, false},
	} {
		if minimum := PasswordMinimum(test.roles); minimum != test.minimum || PasswordMeetsMinimum(test.password, minimum) != test.valid {
			t.Errorf("roles=%v minimum=%d: unexpected password policy result", test.roles, minimum)
		}
	}
}

func TestActivationAndRecoveryUseTargetRolePasswordMinimum(t *testing.T) {
	// 两条一次性链接路径必须使用目标账号的真实角色；长度失败后令牌仍应可用于再次提交。
	for _, flow := range []string{"activation", "recovery"} {
		for _, role := range []string{"grader", "school_admin"} {
			t.Run(flow+"/"+role, func(t *testing.T) {
				store := NewMemoryStore()
				status := "active"
				if flow == "activation" {
					status = "invited"
				}
				user := User{ID: "target", TenantID: "tenant", TenantCode: "demo", Username: "target", DisplayName: "目标人员", Status: status, Roles: []string{role}}
				store.AddUser(UserWithPassword{User: user, PasswordHash: "!activation-required"})
				expires := time.Now().UTC().Add(30 * time.Minute)
				token := "one-time-test-token"
				if flow == "activation" {
					store.activations[HashToken(token)] = memoryActivation{TenantID: user.TenantID, UserID: user.ID, ExpiresAt: expires}
				} else {
					store.recoveries[HashToken(token)] = memoryRecovery{TenantID: user.TenantID, UserID: user.ID, ExpiresAt: expires, SecurityEpoch: 1}
				}
				handler := NewHandler(store, time.Hour)
				minimum := PasswordMinimum(user.Roles)
				verify := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(`{"token":"`+token+`"}`))
				if flow == "activation" {
					handler.VerifyActivation(verify, request)
				} else {
					handler.VerifyRecovery(verify, request)
				}
				var preview map[string]struct {
					PasswordMinimum int `json:"password_min_length"`
				}
				if err := json.Unmarshal(verify.Body.Bytes(), &preview); err != nil || verify.Code != http.StatusOK || preview[flow].PasswordMinimum != minimum {
					t.Fatalf("preview must expose target minimum=%d: status=%d body=%s", minimum, verify.Code, verify.Body.String())
				}
				for index, want := range []int{http.StatusBadRequest, http.StatusOK, http.StatusBadRequest} {
					password := strings.Repeat("1", minimum)
					if index == 0 {
						password = password[:minimum-1]
					}
					request := httptest.NewRequest(http.MethodPost, "/complete", strings.NewReader(`{"token":"`+token+`","password":"`+password+`"}`))
					recorder := httptest.NewRecorder()
					if flow == "activation" {
						handler.CompleteActivation(recorder, request)
					} else {
						handler.CompleteRecovery(recorder, request)
					}
					if recorder.Code != want {
						t.Fatalf("attempt %d expected %d got %d: %s", index, want, recorder.Code, recorder.Body.String())
					}
				}
				stored, err := store.FindUserByLogin(context.Background(), "demo", "target")
				if err != nil || !CheckPassword(stored.PasswordHash, strings.Repeat("1", minimum)) {
					t.Fatal("accepted numeric password must be usable on the same account")
				}
			})
		}
	}
}
