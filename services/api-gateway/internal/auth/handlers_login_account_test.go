package auth_test

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginMeLogout(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)

	token := login(t, router, "demo", "teacher", "ChangeMe123!")

	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+token)
	meRec := httptest.NewRecorder()
	router.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("me expected 200, got %d: %s", meRec.Code, meRec.Body.String())
	}
	if !strings.Contains(meRec.Body.String(), `"organization_scope"`) || !strings.Contains(meRec.Body.String(), `"tenant_wide":false`) || !strings.Contains(meRec.Body.String(), `"class_ids":[]`) {
		t.Fatalf("me should expose resolved organization scope: %s", meRec.Body.String())
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+token)
	logoutRec := httptest.NewRecorder()
	router.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout expected 200, got %d: %s", logoutRec.Code, logoutRec.Body.String())
	}

	afterLogout := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	afterLogout.Header.Set("Authorization", "Bearer "+token)
	afterLogoutRec := httptest.NewRecorder()
	router.ServeHTTP(afterLogoutRec, afterLogout)
	if afterLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout expected 401, got %d", afterLogoutRec.Code)
	}
}

func TestSecurityEventsExposeOnlySanitizedCurrentUserActivity(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	token := login(t, router, "demo", "teacher", "ChangeMe123!")
	if err := store.Audit(context.Background(), auth.AuditEvent{
		TenantID: "t-1", ActorID: "u-1", Action: "auth.password_changed", TargetType: "user", TargetID: "u-1",
		IPAddress: "203.0.113.45", UserAgent: "SecretBrowser/1.0 (Windows NT 10.0)", RequestID: "private-request-id",
		BeforeValue: map[string]any{"secret": "before"}, AfterValue: map[string]any{"secret": "after", "risk_level": "high"},
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/security-events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("security events expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"203.0.113.45", "SecretBrowser", "private-request-id", "before", "after"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("security events leaked %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"event_type":"auth.password_changed"`) || !strings.Contains(body, `"risk_level":"high"`) || !strings.Contains(body, `"device_summary":"Windows 设备"`) {
		t.Fatalf("security event projection missing expected fields: %s", body)
	}
}

func TestLoginRejectsOversizedCredentialFields(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	tests := []map[string]string{
		{"tenant_code": strings.Repeat("t", 129), "username": "teacher", "password": "ChangeMe123!"},
		{"tenant_code": "demo", "username": strings.Repeat("u", 257), "password": "ChangeMe123!"},
		{"tenant_code": "demo", "username": "teacher", "password": strings.Repeat("P", 1025)},
	}
	for _, payload := range tests {
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal login payload: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) {
			t.Fatalf("oversized login field expected 400 invalid_request, got %d: %s", rec.Code, rec.Body.String())
		}
	}
}

func TestLoginRejectsOversizedRequestBodyBeforeCredentialValidation(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	body, err := json.Marshal(map[string]any{
		"tenant_code": "demo",
		"username":    "teacher",
		"password":    strings.Repeat("P", 5*1024),
	})
	if err != nil {
		t.Fatalf("marshal login payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), `"code":"request_body_too_large"`) {
		t.Fatalf("oversized login request expected 413 request_body_too_large, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLoginRejectsTrailingJSONValue(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	body := `{"tenant_code":"demo","username":"teacher","password":"ChangeMe123!"}{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("trailing login JSON expected 400 invalid_request, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInactiveTenantBlocksLoginAndExistingSession(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	token := login(t, router, "demo", "teacher", "ChangeMe123!")

	store.SetTenantStatus("t-1", "inactive")

	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+token)
	meRec := httptest.NewRecorder()
	router.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusUnauthorized {
		t.Fatalf("inactive tenant session expected 401, got %d: %s", meRec.Code, meRec.Body.String())
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{
		"tenant_code":"demo","username":"teacher","password":"ChangeMe123!"
	}`))
	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusUnauthorized || !strings.Contains(loginRec.Body.String(), `"code":"invalid_credentials"`) {
		t.Fatalf("inactive tenant login expected generic 401, got %d: %s", loginRec.Code, loginRec.Body.String())
	}
}

func TestOrganizationUserCreationRejectsOversizedFields(t *testing.T) {
	store := newOrganizationAdminStore(t)
	router := newTestRouter(store)
	token := login(t, router, "demo", "admin", "AdminStart123!")
	body, err := json.Marshal(map[string]any{
		"username":          strings.Repeat("u", 257),
		"display_name":      "Teacher",
		"password":          "TeacherStart123!",
		"password_confirm":  "TeacherStart123!",
		"role_code":         "teacher",
		"teaching_subjects": []string{"mathematics"},
		"school_id":         "school-1",
	})
	if err != nil {
		t.Fatalf("marshal user payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("oversized user field expected 400 invalid_request, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOrganizationUserCreationRejectsOversizedRequestBody(t *testing.T) {
	store := newOrganizationAdminStore(t)
	router := newTestRouter(store)
	token := login(t, router, "demo", "admin", "AdminStart123!")
	body, err := json.Marshal(map[string]any{
		"username":          "teacher",
		"display_name":      strings.Repeat("T", 5*1024),
		"password":          "TeacherStart123!",
		"password_confirm":  "TeacherStart123!",
		"role_code":         "teacher",
		"teaching_subjects": []string{"mathematics"},
		"school_id":         "school-1",
	})
	if err != nil {
		t.Fatalf("marshal user payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), `"code":"request_body_too_large"`) {
		t.Fatalf("oversized user request expected 413 request_body_too_large, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOrganizationUserCreationRejectsTrailingJSONValue(t *testing.T) {
	store := newOrganizationAdminStore(t)
	router := newTestRouter(store)
	token := login(t, router, "demo", "admin", "AdminStart123!")
	body := `{"username":"teacher","display_name":"Teacher","password":"TeacherStart123!","teaching_subjects":["mathematics"],"role_code":"teacher","school_id":"school-1"}{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("trailing user JSON expected 400 invalid_request, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOrganizationAdminCreatesAndListsTenantUser(t *testing.T) {
	store := newOrganizationAdminStore(t)
	router := newTestRouter(store)
	token := login(t, router, "demo", "admin", "AdminStart123!")

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{
		"username":"teacher01","display_name":"张老师","password":"TeacherStart123!","password_confirm":"TeacherStart123!","teaching_subjects":["mathematics"],"role_code":"teacher","school_id":"school-1"
	}`))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create user expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	if strings.Contains(createRec.Body.String(), `"password":`) || strings.Contains(createRec.Body.String(), "TeacherStart123!") || strings.Contains(createRec.Body.String(), "hash") {
		t.Fatalf("create user response leaked password material: %s", createRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), "teacher01") {
		t.Fatalf("list users expected created user, got %d: %s", listRec.Code, listRec.Body.String())
	}

	rolesReq := httptest.NewRequest(http.MethodGet, "/api/v1/roles", nil)
	rolesReq.Header.Set("Authorization", "Bearer "+token)
	rolesRec := httptest.NewRecorder()
	router.ServeHTTP(rolesRec, rolesReq)
	if rolesRec.Code != http.StatusOK || !strings.Contains(rolesRec.Body.String(), `"code":"teacher"`) || !strings.Contains(rolesRec.Body.String(), `"code":"grader"`) || strings.Contains(rolesRec.Body.String(), `"code":"tenant_admin"`) || strings.Contains(rolesRec.Body.String(), `"code":"page_processing_worker"`) {
		t.Fatalf("roles must stay in current tenant, got %d: %s", rolesRec.Code, rolesRec.Body.String())
	}

	foundAudit := false
	for _, event := range store.Audits() {
		if event.Action == "auth.user_created" && event.TargetType == "user" {
			foundAudit = true
			if strings.Contains(event.Reason, "TeacherStart123!") {
				t.Fatal("user creation audit leaked password")
			}
		}
	}
	if !foundAudit {
		t.Fatal("expected auth.user_created audit")
	}
}

func TestOrganizationAdminCannotDisableLastActiveSchoolAdmin(t *testing.T) {
	store := newOrganizationAdminStore(t)
	router := newTestRouter(store)
	token := login(t, router, "demo", "admin", "AdminStart123!")
	createAdmin := func(username string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{
			"username":"`+username+`","display_name":"学校管理员","password":"SchoolAdmin123!","role_code":"school_admin","school_id":"school-1"
		}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create school admin expected 201, got %d: %s", rec.Code, rec.Body.String())
		}
		var response struct {
			User auth.ManagedUser `json:"user"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatalf("decode school admin: %v", err)
		}
		return response.User.ID
	}
	updateStatus := func(userID, status string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/"+userID+"/status", strings.NewReader(`{"status":"`+status+`"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	firstID := createAdmin("school_admin_1")
	secondID := createAdmin("school_admin_2")
	if rec := updateStatus(firstID, "disabled"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"disabled"`) {
		t.Fatalf("disable school admin expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := updateStatus(secondID, "disabled"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"last_school_admin"`) {
		t.Fatalf("last school admin expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := updateStatus(firstID, "active"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"active"`) {
		t.Fatalf("restore school admin expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	foundAudit := false
	for _, event := range store.Audits() {
		if event.Action == "auth.user_status_updated" && event.TargetID == firstID {
			foundAudit = true
		}
	}
	if !foundAudit {
		t.Fatal("expected auth.user_status_updated audit")
	}
}

func TestDisableAndReEnableUserDoesNotRestoreExistingSession(t *testing.T) {
	store := newOrganizationAdminStore(t)
	router := newTestRouter(store)
	adminToken := login(t, router, "demo", "admin", "AdminStart123!")
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{
		"username":"session_teacher","display_name":"Session Teacher","password":"TeacherSession123!","password_confirm":"TeacherSession123!","teaching_subjects":["mathematics"],"role_code":"teacher","school_id":"school-1"
	}`))
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		User auth.ManagedUser `json:"user"`
	}
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	oldToken := login(t, router, "demo", "session_teacher", "TeacherSession123!")
	for _, status := range []string{"disabled", "active"} {
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/"+created.User.ID+"/status", strings.NewReader(`{"status":"`+status+`"}`))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("set status %s: %d %s", status, rec.Code, rec.Body.String())
		}
	}
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+oldToken)
	meRec := httptest.NewRecorder()
	router.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusUnauthorized {
		t.Fatalf("old session must remain revoked after re-enable, got %d %s", meRec.Code, meRec.Body.String())
	}
	_ = login(t, router, "demo", "session_teacher", "TeacherSession123!")
}
