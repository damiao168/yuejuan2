package onboarding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type stubReadinessService struct{ result OnboardingReadiness }

func (s stubReadinessService) Readiness(context.Context, Actor) (OnboardingReadiness, error) {
	return s.result, nil
}

func TestReadinessHandlerRejectsUnauthenticatedAndNonAdminRoles(t *testing.T) {
	handler := NewHandler(stubReadinessService{})
	for _, testCase := range []struct {
		name string
		user *auth.User
		want int
	}{
		{name: "unauthenticated", want: http.StatusUnauthorized},
		{name: "teacher", user: &auth.User{Roles: []string{"teacher"}}, want: http.StatusForbidden},
		{name: "grader", user: &auth.User{Roles: []string{"grader"}}, want: http.StatusForbidden},
		{name: "student", user: &auth.User{Roles: []string{"student"}}, want: http.StatusForbidden},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/onboarding/readiness", nil)
			if testCase.user != nil {
				request = request.WithContext(auth.WithUser(request.Context(), *testCase.user))
			}
			recorder := httptest.NewRecorder()
			handler.Readiness(recorder, request)
			if recorder.Code != testCase.want {
				t.Fatalf("status = %d, want %d", recorder.Code, testCase.want)
			}
		})
	}
}

func TestReadinessHandlerReturnsScopedResponseWithoutSecrets(t *testing.T) {
	handler := NewHandler(stubReadinessService{result: OnboardingReadiness{
		Scope: "platform", ReadyForUse: false,
		Checks: []ReadinessCheck{{Key: "first_school", State: CheckAction, Severity: SeverityBlocking, Title: "创建第一所学校"}},
	}})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/onboarding/readiness", nil)
	ctx := auth.WithUser(request.Context(), auth.User{ID: "admin", TenantID: auth.PlatformTenantID, Roles: []string{"platform_admin"}})
	ctx = auth.WithAccessScope(ctx, auth.AccessScope{TenantID: auth.PlatformTenantID, IsPlatform: true})
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()
	handler.Readiness(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var decoded OnboardingReadiness
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Scope != "platform" {
		t.Fatalf("scope = %q", decoded.Scope)
	}
	lower := strings.ToLower(recorder.Body.String())
	for _, forbidden := range []string{"password", "secret", "dsn", "token", "api_key", "credential"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("response contains forbidden field %q", forbidden)
		}
	}
}
