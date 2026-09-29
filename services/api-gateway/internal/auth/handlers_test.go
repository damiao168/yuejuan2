package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/server"
	"golang.org/x/crypto/bcrypt"
)

func newTestStore(t *testing.T) *auth.MemoryStore {
	// 这里显式设置权限以隔离 HTTP 行为；真实角色授权矩阵由 PostgreSQL 契约测试验证。
	t.Helper()
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	store := auth.NewMemoryStore()
	store.AddUser(auth.UserWithPassword{
		User: auth.User{
			ID:          "u-1",
			TenantID:    "t-1",
			TenantCode:  "demo",
			Username:    "teacher",
			DisplayName: "Teacher",
			Status:      "active",
			Roles:       []string{"teacher"},
			Permissions: []string{"system:read", "grading:review", "exam:manage"},
			DataScope:   map[string]any{"scope": "class", "class_ids": []any{"class-1"}},
		},
		PasswordHash: hash,
	})
	return store
}

func newTestRouter(store *auth.MemoryStore) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{
			Name:             "test",
			Environment:      "test",
			ReadinessTimeout: time.Millisecond,
		},
		Auth: config.AuthConfig{SessionTTL: time.Hour},
	}
	return server.NewRouter(cfg, logger.New(io.Discard, "error"), nil, store, org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
}

func newTestRouterWithConfig(store *auth.MemoryStore, cfg config.Config) http.Handler {
	return server.NewRouter(cfg, logger.New(io.Discard, "error"), nil, store, org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
}

func newOrganizationAdminStore(t *testing.T) *auth.MemoryStore {
	t.Helper()
	hash, err := auth.HashPassword("AdminStart123!")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	store := auth.NewMemoryStore()
	store.AddRole("t-1", auth.AssignableRole{Code: "teacher", Name: "教师", ScopeType: "class"})
	store.AddRole("t-1", auth.AssignableRole{Code: "grader", Name: "阅卷员", ScopeType: "exam_task"})
	store.AddRole("t-1", auth.AssignableRole{Code: "arbitrator", Name: "仲裁员", ScopeType: "exam_task"})
	store.AddRole("t-1", auth.AssignableRole{Code: "school_admin", Name: "学校管理员", ScopeType: "school"})
	store.AddRole("t-1", auth.AssignableRole{Code: "student", Name: "学生", ScopeType: "self"})
	store.AddRole("t-1", auth.AssignableRole{Code: "page_processing_worker", Name: "Worker", ScopeType: "service"})
	store.AddRole("t-1", auth.AssignableRole{Code: "tenant_admin", Name: "租户管理员", ScopeType: "tenant"})
	store.AddRole("t-2", auth.AssignableRole{Code: "grader", Name: "阅卷员", ScopeType: "exam_task"})
	store.AddUser(auth.UserWithPassword{
		User: auth.User{
			ID: "admin-1", TenantID: "t-1", TenantCode: "demo", Username: "admin",
			DisplayName: "Organization Admin", Status: "active", Roles: []string{"tenant_admin"},
			Permissions: []string{"org:manage"}, DataScope: map[string]any{"scope": "tenant"},
		},
		PasswordHash: hash,
	})
	return store
}

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
	if !strings.Contains(meRec.Body.String(), `"organization_scope"`) || !strings.Contains(meRec.Body.String(), `"tenant_wide":false`) || !strings.Contains(meRec.Body.String(), `"class_ids":["class-1"]`) {
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
	body, err := json.Marshal(map[string]string{
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
	body, err := json.Marshal(map[string]string{
		"username":     strings.Repeat("u", 257),
		"display_name": "Teacher",
		"password":     "TeacherStart123!",
		"role_code":    "teacher",
		"school_id":    "school-1",
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
	body, err := json.Marshal(map[string]string{
		"username":     "teacher",
		"display_name": strings.Repeat("T", 5*1024),
		"password":     "TeacherStart123!",
		"role_code":    "teacher",
		"school_id":    "school-1",
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
	body := `{"username":"teacher","display_name":"Teacher","password":"TeacherStart123!","role_code":"teacher","school_id":"school-1"}{}`
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
		"username":"teacher01","display_name":"张老师","password":"TeacherStart123!","role_code":"teacher","school_id":"school-1"
	}`))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create user expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	if strings.Contains(createRec.Body.String(), "password") || strings.Contains(createRec.Body.String(), "hash") {
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
		"username":"session_teacher","display_name":"Session Teacher","password":"TeacherSession123!","role_code":"teacher","school_id":"school-1"
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

func TestInvitedTeacherActivatesAndLogsInWithPhoneIdentifier(t *testing.T) {
	store := newOrganizationAdminStore(t)
	router := newTestRouter(store)
	adminToken := login(t, router, "demo", "admin", "AdminStart123!")
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{
		"display_name":"张老师","phone":"138 0013 8000","employee_no":"T-1001","role_code":"teacher","school_id":"school-1"
	}`))
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create invitation: %d %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		User       auth.ManagedUser `json:"user"`
		Activation struct {
			Token string `json:"token"`
		} `json:"activation"`
	}
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.User.Status != "invited" || created.Activation.Token == "" || created.User.PhoneMasked != "138****8000" {
		t.Fatalf("unexpected invitation response: %#v", created)
	}
	verifyReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/activation/verify", strings.NewReader(`{"token":"`+created.Activation.Token+`"}`))
	verifyRec := httptest.NewRecorder()
	router.ServeHTTP(verifyRec, verifyReq)
	if verifyRec.Code != http.StatusOK || !strings.Contains(verifyRec.Body.String(), "张老师") {
		t.Fatalf("verify activation: %d %s", verifyRec.Code, verifyRec.Body.String())
	}
	password := "今天认真完成所有班级的阅卷工作流程"
	completeReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/activation/complete", strings.NewReader(`{"token":"`+created.Activation.Token+`","password":"`+password+`"}`))
	completeRec := httptest.NewRecorder()
	router.ServeHTTP(completeRec, completeReq)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("complete activation: %d %s", completeRec.Code, completeRec.Body.String())
	}
	reuseReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/activation/verify", strings.NewReader(`{"token":"`+created.Activation.Token+`"}`))
	reuseRec := httptest.NewRecorder()
	router.ServeHTTP(reuseRec, reuseReq)
	if reuseRec.Code != http.StatusBadRequest {
		t.Fatalf("activation token must be single-use, got %d %s", reuseRec.Code, reuseRec.Body.String())
	}
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", strings.NewReader(`{"tenant_hint":"demo","identifier":"13800138000","password":"`+password+`","client_type":"desktop"}`))
	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("phone identifier login: %d %s", loginRec.Code, loginRec.Body.String())
	}
}

func TestAdministratorCanReissueTeacherActivation(t *testing.T) {
	store := newOrganizationAdminStore(t)
	router := newTestRouter(store)
	adminToken := login(t, router, "demo", "admin", "AdminStart123!")
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{
		"phone":"13900139000","employee_no":"T-1002","display_name":"Pending Teacher","role_code":"teacher","school_id":"school-1"
	}`))
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create invitation: %d %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		User       auth.ManagedUser `json:"user"`
		Activation struct {
			Token string `json:"token"`
		} `json:"activation"`
	}
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	reissueReq := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+created.User.ID+"/activation", nil)
	reissueReq.Header.Set("Authorization", "Bearer "+adminToken)
	reissueRec := httptest.NewRecorder()
	router.ServeHTTP(reissueRec, reissueReq)
	if reissueRec.Code != http.StatusCreated {
		t.Fatalf("reissue activation: %d %s", reissueRec.Code, reissueRec.Body.String())
	}
	if got := reissueRec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("activation delivery must not be cached, got %q", got)
	}
	var reissued struct {
		Activation struct {
			Token string `json:"token"`
		} `json:"activation"`
	}
	if err := json.NewDecoder(reissueRec.Body).Decode(&reissued); err != nil {
		t.Fatal(err)
	}
	if reissued.Activation.Token == "" || reissued.Activation.Token == created.Activation.Token {
		t.Fatal("reissued activation must return a fresh one-time token")
	}
	for token, expected := range map[string]int{created.Activation.Token: http.StatusBadRequest, reissued.Activation.Token: http.StatusOK} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/activation/verify", strings.NewReader(`{"token":"`+token+`"}`))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != expected {
			t.Fatalf("activation verification expected %d, got %d: %s", expected, rec.Code, rec.Body.String())
		}
	}
}

func TestLegacyBcryptLoginRehashesToArgon2id(t *testing.T) {
	store := newTestStore(t)
	legacyPassword := "LegacyTeacher123!"
	legacyHash, err := bcrypt.GenerateFromPassword([]byte(legacyPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.FindUserByLogin(context.Background(), "demo", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	user.PasswordHash = string(legacyHash)
	store.AddUser(user)
	router := newTestRouter(store)
	_ = login(t, router, "demo", "teacher", legacyPassword)
	updated, err := store.FindUserByLogin(context.Background(), "demo", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(updated.PasswordHash, "$argon2id$") || !auth.CheckPassword(updated.PasswordHash, legacyPassword) || updated.LastLoginAt.IsZero() {
		t.Fatal("legacy login must transparently upgrade the hash and record last_login_at")
	}
}

func TestAdministratorRecoveryRevokesSessionsAndDoesNotAutoLogin(t *testing.T) {
	store := newOrganizationAdminStore(t)
	router := newTestRouter(store)
	adminToken := login(t, router, "demo", "admin", "AdminStart123!")
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{
		"username":"recover_teacher","display_name":"Recover Teacher","password":"OriginalTeacher123!","role_code":"teacher","school_id":"school-1"
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
	oldToken := login(t, router, "demo", "recover_teacher", "OriginalTeacher123!")
	recoveryReq := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+created.User.ID+"/credential-reset", nil)
	recoveryReq.Header.Set("Authorization", "Bearer "+adminToken)
	recoveryRec := httptest.NewRecorder()
	router.ServeHTTP(recoveryRec, recoveryReq)
	if recoveryRec.Code != http.StatusCreated {
		t.Fatalf("create recovery: %d %s", recoveryRec.Code, recoveryRec.Body.String())
	}
	var recovery struct {
		Recovery struct {
			Token string `json:"token"`
		} `json:"recovery"`
	}
	if err := json.NewDecoder(recoveryRec.Body).Decode(&recovery); err != nil {
		t.Fatal(err)
	}
	newPassword := "这是管理员协助恢复后的教师账号安全密码"
	completeReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/recovery/complete", strings.NewReader(`{"token":"`+recovery.Recovery.Token+`","password":"`+newPassword+`"}`))
	completeRec := httptest.NewRecorder()
	router.ServeHTTP(completeRec, completeReq)
	if completeRec.Code != http.StatusOK || len(completeRec.Result().Cookies()) != 0 {
		t.Fatalf("complete recovery must not auto-login: %d %s cookies=%v", completeRec.Code, completeRec.Body.String(), completeRec.Result().Cookies())
	}
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+oldToken)
	meRec := httptest.NewRecorder()
	router.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusUnauthorized {
		t.Fatalf("recovery must revoke old sessions, got %d %s", meRec.Code, meRec.Body.String())
	}
	if oldLogin := loginResponseRaw(router, "demo", "recover_teacher", "OriginalTeacher123!"); oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("old password must fail after recovery, got %d %s", oldLogin.Code, oldLogin.Body.String())
	}
	_ = login(t, router, "demo", "recover_teacher", newPassword)
	reuseReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/recovery/verify", strings.NewReader(`{"token":"`+recovery.Recovery.Token+`"}`))
	reuseRec := httptest.NewRecorder()
	router.ServeHTTP(reuseRec, reuseReq)
	if reuseRec.Code != http.StatusBadRequest {
		t.Fatalf("recovery token must be single-use, got %d %s", reuseRec.Code, reuseRec.Body.String())
	}
}

func TestOrganizationUserCreationRejectsDuplicateCrossTenantRoleAndTenantField(t *testing.T) {
	store := newOrganizationAdminStore(t)
	router := newTestRouter(store)
	token := login(t, router, "demo", "admin", "AdminStart123!")
	request := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	first := request(`{"username":"teacher01","display_name":"Teacher","password":"TeacherStart123!","role_code":"teacher","school_id":"school-1"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create expected 201, got %d: %s", first.Code, first.Body.String())
	}
	duplicate := request(`{"username":"teacher01","display_name":"Teacher 2","password":"TeacherStart123!","role_code":"teacher","school_id":"school-1"}`)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate expected 409, got %d: %s", duplicate.Code, duplicate.Body.String())
	}
	crossTenantRole := request(`{"username":"grader01","display_name":"Grader","password":"GraderStart123!","role_code":"grader"}`)
	if crossTenantRole.Code != http.StatusBadRequest {
		t.Fatalf("cross tenant role expected 400, got %d: %s", crossTenantRole.Code, crossTenantRole.Body.String())
	}
	privilegedRole := request(`{"username":"admin02","display_name":"Admin","password":"AdminStart123!!","role_code":"tenant_admin"}`)
	if privilegedRole.Code != http.StatusForbidden {
		t.Fatalf("privileged role expected 403, got %d: %s", privilegedRole.Code, privilegedRole.Body.String())
	}
	forgedTenant := request(`{"tenant_id":"t-2","username":"teacher02","display_name":"Teacher","password":"TeacherStart123!","role_code":"teacher"}`)
	if forgedTenant.Code != http.StatusBadRequest {
		t.Fatalf("tenant_id field expected 400, got %d: %s", forgedTenant.Code, forgedTenant.Body.String())
	}
}

func TestOrganizationUserManagementRequiresPermission(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	token := login(t, router, "demo", "teacher", "ChangeMe123!")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("user list without org:manage expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLoginSetsHttpOnlySessionCookieAndMeAcceptsCookie(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)

	loginRec := loginResponse(t, router, "demo", "teacher", "ChangeMe123!")
	cookie := sessionCookie(t, loginRec)
	if !cookie.HttpOnly {
		t.Fatalf("session cookie must be HttpOnly: %#v", cookie)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie must use SameSite=Lax, got %v", cookie.SameSite)
	}
	if cookie.Value == "" {
		t.Fatal("session cookie must contain token value")
	}
	if cookie.Path != "/api/v1" {
		t.Fatalf("session cookie must use the minimum API path, got %q", cookie.Path)
	}
	var browserResponse map[string]any
	if err := json.Unmarshal(loginRec.Body.Bytes(), &browserResponse); err != nil {
		t.Fatal(err)
	}
	if _, leaked := browserResponse["access_token"]; leaked {
		t.Fatalf("browser login response exposed an access token: %s", loginRec.Body.String())
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.AddCookie(cookie)
	meRec := httptest.NewRecorder()
	router.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("me with cookie expected 200, got %d: %s", meRec.Code, meRec.Body.String())
	}
}

func TestPublicComputerLoginUsesShortNonPersistentSession(t *testing.T) {
	store := newTestStore(t)
	publicTTL := 45 * time.Minute
	handler := auth.NewHandler(store, 8*time.Hour, auth.HandlerOptions{PublicSessionTTL: publicTTL})
	before := time.Now().UTC()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(
		`{"tenant_code":"demo","identifier":"teacher","password":"ChangeMe123!","public_device":true}`,
	))
	rec := httptest.NewRecorder()
	handler.Login(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("public computer login expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cookie := sessionCookie(t, rec)
	if cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Fatalf("public computer login must use a non-persistent cookie, got %#v", cookie)
	}
	var response struct {
		ExpiresAt time.Time `json:"expires_at"`
		User      auth.User `json:"user"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.User.CurrentSessionType != auth.SessionTypePublicDevice {
		t.Fatalf("current session type=%q want %q", response.User.CurrentSessionType, auth.SessionTypePublicDevice)
	}
	if response.ExpiresAt.Before(before.Add(publicTTL-time.Second)) || response.ExpiresAt.After(time.Now().UTC().Add(publicTTL+time.Second)) {
		t.Fatalf("public session expiry %s does not use configured lifetime %s", response.ExpiresAt, publicTTL)
	}
	user, err := store.FindUserBySession(context.Background(), auth.HashToken(cookie.Value), time.Now().UTC())
	if err != nil || user.CurrentSessionType != auth.SessionTypePublicDevice {
		t.Fatalf("stored public session not recoverable with its type: user=%#v err=%v", user, err)
	}
	sessions, err := store.ListSessions(context.Background(), "t-1", "u-1", auth.HashToken(cookie.Value), time.Now().UTC())
	if err != nil || len(sessions) != 1 || sessions[0].SessionType != auth.SessionTypePublicDevice {
		t.Fatalf("public session not projected by session management: sessions=%#v err=%v", sessions, err)
	}
}

func TestPublicComputerModeRejectsRememberedAndTokenSessions(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "remembered browser",
			path: "/api/v1/auth/login",
			body: `{"tenant_code":"demo","identifier":"teacher","password":"ChangeMe123!","remember_device":true,"public_device":true}`,
		},
		{
			name: "bearer token",
			path: "/api/v1/auth/token",
			body: `{"tenant_code":"demo","identifier":"teacher","password":"ChangeMe123!","client_type":"desktop","public_device":true}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			handler := auth.NewHandler(store, time.Hour)
			req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			rec := httptest.NewRecorder()
			if test.path == "/api/v1/auth/token" {
				handler.TokenLogin(rec, req)
			} else {
				handler.Login(rec, req)
			}
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_device_mode"`) {
				t.Fatalf("expected 400 invalid_device_mode, got %d: %s", rec.Code, rec.Body.String())
			}
			sessions, err := store.ListSessions(context.Background(), "t-1", "u-1", "", time.Now().UTC())
			if err != nil || len(sessions) != 0 {
				t.Fatalf("invalid public mode must not create a session: sessions=%#v err=%v", sessions, err)
			}
		})
	}
}

func TestPublicComputerSessionCanReauthenticateWithoutLosingWorkspace(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(
		`{"tenant_code":"demo","identifier":"teacher","password":"ChangeMe123!","public_device":true}`,
	))
	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("public computer login expected 200, got %d: %s", loginRec.Code, loginRec.Body.String())
	}
	cookie := sessionCookie(t, loginRec)
	lockReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/lock", nil)
	lockReq.AddCookie(cookie)
	lockReq.Header.Set("X-EduGrade-CSRF", "1")
	lockRec := httptest.NewRecorder()
	router.ServeHTTP(lockRec, lockReq)
	if lockRec.Code != http.StatusOK || !strings.Contains(lockRec.Body.String(), `"status":"locked"`) {
		t.Fatalf("public session lock expected 200, got %d: %s", lockRec.Code, lockRec.Body.String())
	}
	lockedMeReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	lockedMeReq.AddCookie(cookie)
	lockedMeRec := httptest.NewRecorder()
	router.ServeHTTP(lockedMeRec, lockedMeReq)
	if lockedMeRec.Code != http.StatusUnauthorized {
		t.Fatalf("locked session must fail normal authentication, got %d: %s", lockedMeRec.Code, lockedMeRec.Body.String())
	}
	reauthenticate := func(password string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reauthenticate", strings.NewReader(`{"password":"`+password+`"}`))
		req.AddCookie(cookie)
		req.Header.Set("X-EduGrade-CSRF", "1")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	wrong := reauthenticate("incorrect password")
	if wrong.Code != http.StatusUnauthorized || !strings.Contains(wrong.Body.String(), `"code":"reauthentication_failed"`) {
		t.Fatalf("wrong reauthentication expected generic 401, got %d: %s", wrong.Code, wrong.Body.String())
	}
	success := reauthenticate("ChangeMe123!")
	if success.Code != http.StatusOK || !strings.Contains(success.Body.String(), `"status":"reauthenticated"`) {
		t.Fatalf("correct reauthentication expected 200, got %d: %s", success.Code, success.Body.String())
	}
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.AddCookie(cookie)
	meRec := httptest.NewRecorder()
	router.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK || !strings.Contains(meRec.Body.String(), `"current_session_type":"public_device"`) {
		t.Fatalf("reauthentication must preserve the public session: %d %s", meRec.Code, meRec.Body.String())
	}
	audits := store.Audits()
	foundLocked, foundFailure, foundSuccess := false, false, false
	for _, event := range audits {
		foundLocked = foundLocked || event.Action == "auth.session_locked"
		foundFailure = foundFailure || event.Action == "auth.reauthentication_failed"
		foundSuccess = foundSuccess || event.Action == "auth.session_reauthenticated"
	}
	if !foundLocked || !foundFailure || !foundSuccess {
		t.Fatalf("reauthentication attempts must be audited: %#v", audits)
	}
}

func TestStandardBrowserSessionCannotUsePublicComputerLock(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	cookie := sessionCookie(t, loginResponse(t, router, "demo", "teacher", "ChangeMe123!"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/lock", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-EduGrade-CSRF", "1")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"session_lock_forbidden"`) {
		t.Fatalf("standard session lock expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestServiceAccountCannotCreateBrowserSession(t *testing.T) {
	store := newTestStore(t)
	hash, err := auth.HashPassword("WorkerStart123!")
	if err != nil {
		t.Fatal(err)
	}
	store.AddUser(auth.UserWithPassword{
		User: auth.User{
			ID: "worker-1", TenantID: "t-1", TenantCode: "demo", Username: "page_worker",
			DisplayName: "Page Worker", Status: "active", Roles: []string{"page_processing_worker"},
			Permissions: []string{"ocr:manage"}, DataScope: map[string]any{"scope": "tenant"},
		},
		PasswordHash: hash,
	})
	router := newTestRouter(store)

	browserReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(
		`{"tenant_code":"demo","username":"page_worker","password":"WorkerStart123!"}`,
	))
	browserRec := httptest.NewRecorder()
	router.ServeHTTP(browserRec, browserReq)
	if browserRec.Code != http.StatusForbidden || !strings.Contains(browserRec.Body.String(), `"code":"client_type_forbidden"`) {
		t.Fatalf("service browser login expected 403 client_type_forbidden, got %d: %s", browserRec.Code, browserRec.Body.String())
	}

	serviceReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", strings.NewReader(
		`{"tenant_code":"demo","username":"page_worker","password":"WorkerStart123!","client_type":"service"}`,
	))
	serviceRec := httptest.NewRecorder()
	router.ServeHTTP(serviceRec, serviceReq)
	if serviceRec.Code != http.StatusOK || !strings.Contains(serviceRec.Body.String(), `"access_token"`) {
		t.Fatalf("service token login expected 200 with token, got %d: %s", serviceRec.Code, serviceRec.Body.String())
	}
}

func TestLogoutClearsSessionCookieAndRevokesCookieSession(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)

	loginRec := loginResponse(t, router, "demo", "teacher", "ChangeMe123!")
	cookie := sessionCookie(t, loginRec)

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutReq.AddCookie(cookie)
	deniedRec := httptest.NewRecorder()
	router.ServeHTTP(deniedRec, logoutReq)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("cookie logout without CSRF protection expected 403, got %d", deniedRec.Code)
	}
	logoutReq.Header.Set("X-EduGrade-CSRF", "1")
	logoutRec := httptest.NewRecorder()
	router.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout expected 200, got %d: %s", logoutRec.Code, logoutRec.Body.String())
	}
	cleared := sessionCookie(t, logoutRec)
	if cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Fatalf("logout must clear session cookie, got %#v", cleared)
	}

	afterLogout := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	afterLogout.AddCookie(cookie)
	afterLogoutRec := httptest.NewRecorder()
	router.ServeHTTP(afterLogoutRec, afterLogout)
	if afterLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("me after cookie logout expected 401, got %d", afterLogoutRec.Code)
	}
}

func TestLoginFailureAudited(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)

	body := bytes.NewBufferString(`{"tenant_code":"demo","username":"teacher","password":"bad"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	audits := store.Audits()
	if len(audits) != 1 || audits[0].Action != "auth.login_failed" {
		t.Fatalf("expected login failure audit, got %#v", audits)
	}
	if audits[0].IPAddress == "203.0.113.99" {
		t.Fatalf("login audit must not trust unverified X-Forwarded-For: %#v", audits[0])
	}
}

func TestLoginFailureRateLimited(t *testing.T) {
	store := newTestStore(t)
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth: config.AuthConfig{
			SessionTTL:         time.Hour,
			LoginFailureLimit:  2,
			LoginFailureWindow: time.Hour,
		},
	}
	router := newTestRouterWithConfig(store, cfg)

	for i, expected := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusTooManyRequests} {
		body := bytes.NewBufferString(`{"tenant_code":"demo","username":"teacher","password":"bad"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != expected {
			t.Fatalf("attempt %d expected %d, got %d %s", i+1, expected, rec.Code, rec.Body.String())
		}
		if expected == http.StatusTooManyRequests && rec.Header().Get("Retry-After") == "" {
			t.Fatalf("rate limited response must include Retry-After")
		}
	}
	audits := store.Audits()
	found := false
	for _, audit := range audits {
		if audit.Action == "auth.login_rate_limited" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected auth.login_rate_limited audit, got %#v", audits)
	}
}

func TestLoginStoreFailureReturnsUnavailableWithoutRateLimiting(t *testing.T) {
	base := newTestStore(t)
	store := &loginErrorStore{Store: base, err: errors.New("postgres is starting up")}
	handler := auth.NewHandler(store, time.Hour, auth.HandlerOptions{
		LoginFailureLimit:  1,
		LoginFailureWindow: time.Hour,
	})

	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"tenant_code":"demo","username":"teacher","password":"ChangeMe123!"}`))
		rec := httptest.NewRecorder()
		handler.Login(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("attempt %d expected 503, got %d: %s", attempt+1, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") != "" {
			t.Fatalf("dependency failure must not set login limiter Retry-After: %s", rec.Header().Get("Retry-After"))
		}
	}
	if audits := base.Audits(); len(audits) != 0 {
		t.Fatalf("dependency failure must not create invalid-login audits: %#v", audits)
	}

	store.err = nil
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"tenant_code":"demo","username":"teacher","password":"ChangeMe123!"}`))
	rec := httptest.NewRecorder()
	handler.Login(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid login after dependency recovery expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLoginFailsClosedWhenDistributedLimiterIsDegraded(t *testing.T) {
	handler := auth.NewHandler(newTestStore(t), time.Hour, auth.HandlerOptions{
		LoginGuard:             degradedLoginGuard{},
		LoginLimiterFailClosed: true,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"tenant_code":"demo","username":"teacher","password":"ChangeMe123!"}`))
	response := httptest.NewRecorder()

	handler.Login(response, request)

	if response.Code != http.StatusServiceUnavailable || response.Header().Get("X-EduGrade-Error-Code") != "auth_rate_limiter_unavailable" {
		t.Fatalf("degraded limiter must fail closed, got %d: %s", response.Code, response.Body.String())
	}
}

type degradedLoginGuard struct{}

func (degradedLoginGuard) Check(context.Context, auth.LoginAttempt, time.Time) (auth.LoginLimit, bool) {
	return auth.LoginLimit{}, false
}
func (degradedLoginGuard) RegisterFailure(context.Context, auth.LoginAttempt, time.Time) (auth.LoginLimit, bool) {
	return auth.LoginLimit{}, false
}
func (degradedLoginGuard) RegisterSuccess(context.Context, auth.LoginAttempt) {}
func (degradedLoginGuard) Degraded() bool                                     { return true }

type loginErrorStore struct {
	auth.Store
	err error
}

func (s *loginErrorStore) FindUserByLogin(ctx context.Context, tenantCode string, username string) (auth.UserWithPassword, error) {
	if s.err != nil {
		return auth.UserWithPassword{}, s.err
	}
	return s.Store.FindUserByLogin(ctx, tenantCode, username)
}

func TestListAuditsFiltersByTarget(t *testing.T) {
	store := newTestStore(t)
	addAuditUser(t, store)
	router := newTestRouter(store)
	_ = store.Audit(context.Background(), auth.AuditEvent{
		TenantID:   "t-1",
		ActorID:    "u-1",
		Action:     "arbitration.submitted",
		TargetType: "arbitration_task",
		TargetID:   "task-1",
		Reason:     "submit arbitration final score",
	})
	_ = store.Audit(context.Background(), auth.AuditEvent{
		TenantID:   "t-1",
		ActorID:    "u-1",
		Action:     "final_grade.created",
		TargetType: "final_grade",
		TargetID:   "grade-1",
		Reason:     "create final grade from arbitration",
	})

	token := login(t, router, "demo", "auditor", "ChangeMe123!")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs?target_type=arbitration_task&target_id=task-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("audit list expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AuditLogs []auth.AuditRecord `json:"audit_logs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(response.AuditLogs) != 1 {
		t.Fatalf("expected one filtered audit log, got %#v", response.AuditLogs)
	}
	if response.AuditLogs[0].Action != "arbitration.submitted" || response.AuditLogs[0].TargetID != "task-1" {
		t.Fatalf("unexpected audit log: %#v", response.AuditLogs[0])
	}
}

func TestListAuditsFiltersByIPAndIncludesValues(t *testing.T) {
	store := newTestStore(t)
	addAuditUser(t, store)
	router := newTestRouter(store)
	_ = store.Audit(context.Background(), auth.AuditEvent{
		TenantID:    "t-1",
		ActorID:     "u-1",
		Action:      "exam.updated",
		TargetType:  "exam",
		TargetID:    "exam-1",
		BeforeValue: map[string]any{"status": "draft"},
		AfterValue:  map[string]any{"status": "configured"},
		Reason:      "update exam status",
		IPAddress:   "10.0.0.8",
	})
	_ = store.Audit(context.Background(), auth.AuditEvent{
		TenantID:   "t-1",
		ActorID:    "u-1",
		Action:     "exam.updated",
		TargetType: "exam",
		TargetID:   "exam-2",
		Reason:     "update another exam",
		IPAddress:  "10.0.0.9",
	})

	token := login(t, router, "demo", "auditor", "ChangeMe123!")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs?action=exam.updated&exam_id=exam-1&ip_address=10.0.0.8", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("audit list expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AuditLogs []auth.AuditRecord `json:"audit_logs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(response.AuditLogs) != 1 {
		t.Fatalf("expected one filtered audit log, got %#v", response.AuditLogs)
	}
	if response.AuditLogs[0].BeforeValue["status"] != "draft" || response.AuditLogs[0].AfterValue["status"] != "configured" {
		t.Fatalf("expected before/after values, got %#v", response.AuditLogs[0])
	}
}

func TestAuditListAndExportRedactSensitiveFields(t *testing.T) {
	store := newTestStore(t)
	addAuditUser(t, store)
	router := newTestRouter(store)
	_ = store.Audit(context.Background(), auth.AuditEvent{
		TenantID:   "t-1",
		ActorID:    "u-1",
		Action:     "user.updated",
		TargetType: "user",
		TargetID:   "u-1",
		BeforeValue: map[string]any{
			"password": "plain-text-should-not-leak",
			"profile":  map[string]any{"api_token": "token-should-not-leak", "display_name": "Teacher"},
		},
		AfterValue: map[string]any{"secret": "secret-should-not-leak", "status": "active"},
		Reason:     "update user",
	})

	token := login(t, router, "demo", "auditor", "ChangeMe123!")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs?action=user.updated", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit list expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"plain-text-should-not-leak", "token-should-not-leak", "secret-should-not-leak"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("audit list leaked %s: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "[REDACTED]") || !strings.Contains(body, "Teacher") {
		t.Fatalf("audit list should redact sensitive fields and preserve non-sensitive context: %s", body)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/audit-logs/export?action=user.updated", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit export expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	body = rec.Body.String()
	for _, forbidden := range []string{"plain-text-should-not-leak", "token-should-not-leak", "secret-should-not-leak"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("audit export leaked %s: %s", forbidden, body)
		}
	}
}

func TestExportAuditsWritesAuditAndWatermark(t *testing.T) {
	store := newTestStore(t)
	addAuditUser(t, store)
	router := newTestRouter(store)
	_ = store.Audit(context.Background(), auth.AuditEvent{
		TenantID:   "t-1",
		ActorID:    "u-1",
		Action:     "score.published",
		TargetType: "exam",
		TargetID:   "exam-1",
		Reason:     "publish scores",
		IPAddress:  "10.0.0.8",
	})

	token := login(t, router, "demo", "auditor", "ChangeMe123!")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/audit-logs/export?action=score.published&limit=20", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("audit export expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-EduGrade-Watermark") == "" {
		t.Fatalf("expected watermark header")
	}
	if !strings.Contains(rec.Body.String(), "score.published") || !strings.Contains(rec.Body.String(), "EduGrade audit export") {
		t.Fatalf("expected CSV body with audit row and watermark, got %s", rec.Body.String())
	}
	audits := store.Audits()
	if len(audits) == 0 || audits[len(audits)-1].Action != "audit.exported" {
		t.Fatalf("expected audit.exported event, got %#v", audits)
	}
}

func TestAuditLogsCannotBeDeletedThroughOrdinaryAPI(t *testing.T) {
	store := newTestStore(t)
	addAuditUser(t, store)
	router := newTestRouter(store)
	_ = store.Audit(context.Background(), auth.AuditEvent{
		TenantID:   "t-1",
		ActorID:    "u-1",
		Action:     "score.exported",
		TargetType: "exam",
		TargetID:   "exam-1",
		Reason:     "export grades",
	})

	token := login(t, router, "demo", "auditor", "ChangeMe123!")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/audit-logs/memory-audit-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("audit delete route must not exist, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestListAuditsRequiresPermission(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	token := login(t, router, "demo", "teacher", "ChangeMe123!")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPasswordChangeRevokesEveryExistingSession(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	firstToken := login(t, router, "demo", "teacher", "ChangeMe123!")
	secondToken := login(t, router, "demo", "teacher", "ChangeMe123!")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(`{"current_password":"ChangeMe123!","new_password":"NewPassword456!"}`))
	req.Header.Set("Authorization", "Bearer "+firstToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("password change expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	for _, token := range []string{firstToken, secondToken} {
		me := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		me.Header.Set("Authorization", "Bearer "+token)
		meRec := httptest.NewRecorder()
		router.ServeHTTP(meRec, me)
		if meRec.Code != http.StatusUnauthorized {
			t.Fatalf("password change must revoke existing token, got %d %s", meRec.Code, meRec.Body.String())
		}
	}
	if old := loginResponseRaw(router, "demo", "teacher", "ChangeMe123!"); old.Code != http.StatusUnauthorized {
		t.Fatalf("old password must fail, got %d %s", old.Code, old.Body.String())
	}
	_ = login(t, router, "demo", "teacher", "NewPassword456!")
}

func TestMeRequiresAuthentication(t *testing.T) {
	router := newTestRouter(newTestStore(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestPermissionMiddlewareRejectsMissingPermission(t *testing.T) {
	store := newTestStore(t)
	token, tokenHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if _, err := store.CreateSession(context.Background(), auth.CreateSessionInput{
		TenantID: "t-1", UserID: "u-1", TokenHash: tokenHash,
		SessionType: auth.SessionTypeStandard, DeviceID: "test-device",
		DeviceName: "test", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	handler := auth.AuthMiddleware(store)(auth.RequirePermission("audit:read")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func login(t *testing.T, router http.Handler, tenant string, username string, password string) string {
	t.Helper()
	payload := map[string]string{"tenant_code": tenant, "username": username, "password": password, "client_type": "desktop"}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("token login expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		t.Fatal("expected access token")
	}
	return response.AccessToken
}

func loginResponse(t *testing.T, router http.Handler, tenant string, username string, password string) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]string{"tenant_code": tenant, "username": username, "password": password}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	return rec
}

func loginResponseRaw(router http.Handler, tenant, username, password string) *httptest.ResponseRecorder {
	payload := map[string]string{"tenant_code": tenant, "username": username, "password": password, "client_type": "desktop"}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == auth.DefaultSessionCookieName {
			return cookie
		}
	}
	t.Fatalf("expected %s cookie, got %#v", auth.DefaultSessionCookieName, rec.Result().Cookies())
	return nil
}

func addAuditUser(t *testing.T, store *auth.MemoryStore) {
	t.Helper()
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	store.AddUser(auth.UserWithPassword{
		User: auth.User{
			ID:          "u-2",
			TenantID:    "t-1",
			TenantCode:  "demo",
			Username:    "auditor",
			DisplayName: "Auditor",
			Status:      "active",
			Roles:       []string{"auditor"},
			Permissions: []string{"audit:read", "audit:export"},
			DataScope:   map[string]any{"scope": "tenant"},
		},
		PasswordHash: hash,
	})
}
