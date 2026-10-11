package org_test

import (
	"bytes"
	"context"
	"encoding/json"
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
)

func TestCreateAndListSchoolUsesTenantScope(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"org:manage", "student:import"})
	orgStore := org.NewMemoryStore()
	router := testRouter(authStore, orgStore)
	token := login(t, router)

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/schools", bytes.NewBufferString(`{"name":"第一中学","code":"school-001"}`))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create school expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/schools", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list school expected 200, got %d: %s", listRec.Code, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), "school-001") {
		t.Fatalf("expected created school in list: %s", listRec.Body.String())
	}
}

func TestMemoryStoreTenantIsolation(t *testing.T) {
	store := org.NewMemoryStore()
	if _, err := store.CreateSchool(context.Background(), "tenant-a", org.School{Name: "A", Code: "a"}); err != nil {
		t.Fatalf("create school a: %v", err)
	}
	if _, err := store.CreateSchool(context.Background(), "tenant-b", org.School{Name: "B", Code: "b"}); err != nil {
		t.Fatalf("create school b: %v", err)
	}

	aSchools, err := store.ListSchools(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("list tenant a: %v", err)
	}
	if len(aSchools) != 1 || aSchools[0].Code != "a" {
		t.Fatalf("tenant isolation failed: %#v", aSchools)
	}
}

func TestSchoolAdministratorListsOnlyOwnSchoolAndCannotCreateAnother(t *testing.T) {
	orgStore := org.NewMemoryStore()
	schoolA, _ := orgStore.CreateSchool(context.Background(), "tenant-org", org.School{Name: "A", Code: "a"})
	schoolB, _ := orgStore.CreateSchool(context.Background(), "tenant-org", org.School{Name: "B", Code: "b"})
	hash, _ := auth.HashPassword("ChangeMe123!")
	authStore := auth.NewMemoryStore()
	authStore.AddUser(auth.UserWithPassword{User: auth.User{
		ID: "admin-a", TenantID: "tenant-org", TenantCode: "demo", Username: "school_admin", DisplayName: "School A Admin", Status: "active",
		Roles: []string{"school_admin"}, Permissions: []string{"org:manage"}, DataScope: map[string]any{"scope": "school", "school_id": schoolA.ID},
	}, PasswordHash: hash})
	router := testRouter(authStore, orgStore)
	token := login(t, router)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/schools", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), schoolA.ID) || strings.Contains(rec.Body.String(), schoolB.ID) {
		t.Fatalf("school scoped list leaked another school: %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/schools", bytes.NewBufferString(`{"name":"C","code":"c"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("school administrator create school expected 403, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestCSVImportReportsRowErrors(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"org:manage", "student:import"})
	orgStore := org.NewMemoryStore()
	router := testRouter(authStore, orgStore)
	token := login(t, router)

	body := "student_no,name,school_id,class_id\nS001,张三,school-1,class-1\nS002,,school-1,class-1\nbad,row\n"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/students/import-csv", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("import expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"created":1`) || !strings.Contains(rec.Body.String(), `"errors"`) {
		t.Fatalf("expected created count and errors: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "不能为空") || !strings.Contains(rec.Body.String(), "列不完整") {
		t.Fatalf("import errors should be actionable Chinese messages: %s", rec.Body.String())
	}
}

func TestOrganizationDuplicateCodesReturnSpecificConflicts(t *testing.T) {
	t.Run("school code", assertOrganizationSchoolCodeConflict)

	t.Run("class code", assertOrganizationClassCodeConflict)
}

func TestCreateStudentReturnsConflictForDuplicateStudentNumber(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"org:manage"})
	orgStore := org.NewMemoryStore()
	school, err := orgStore.CreateSchool(context.Background(), "tenant-org", org.School{Name: "第一中学", Code: "school-001"})
	if err != nil {
		t.Fatal(err)
	}
	grade, err := orgStore.CreateGrade(context.Background(), "tenant-org", org.Grade{SchoolID: school.ID, Name: "高一年级"})
	if err != nil {
		t.Fatal(err)
	}
	class, err := orgStore.CreateClass(context.Background(), "tenant-org", org.Class{SchoolID: school.ID, GradeID: grade.ID, Name: "高一（1）班"})
	if err != nil {
		t.Fatal(err)
	}
	router := testRouter(authStore, orgStore)
	token := login(t, router)
	payload := `{"school_id":"` + school.ID + `","class_id":"` + class.ID + `","student_no":"1","name":"测试学生"}`

	for attempt, expectedStatus := range []int{http.StatusCreated, http.StatusConflict} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/students", bytes.NewBufferString(payload))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != expectedStatus {
			t.Fatalf("attempt %d expected %d, got %d: %s", attempt+1, expectedStatus, rec.Code, rec.Body.String())
		}
		if expectedStatus == http.StatusConflict && !strings.Contains(rec.Body.String(), "student_no_conflict") {
			t.Fatalf("duplicate student number should return explicit conflict code: %s", rec.Body.String())
		}
	}
}

func TestOrganizationPermissionDenied(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"system:read"})
	router := testRouter(authStore, org.NewMemoryStore())
	token := login(t, router)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/schools", bytes.NewBufferString(`{"name":"第一中学","code":"school-001"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGraderCannotListStudentNamesWithoutOrgManage(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"system:read", "review:manage", "grading:manage"})
	router := testRouter(authStore, org.NewMemoryStore())
	token := login(t, router)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/students", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("grader without org:manage should not list students, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestNonPlatformTenantCannotAdministerOtherTenants(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"tenant:manage"})
	router := testRouter(authStore, org.NewMemoryStore())
	token := login(t, router)

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/v1/tenants", bytes.NewBufferString(`{"name":"Other Tenant","code":"other"}`)),
		httptest.NewRequest(http.MethodPatch, "/api/v1/tenants/tenant-other", bytes.NewBufferString(`{"status":"disabled"}`)),
	} {
		request.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "platform_tenant_required") {
			t.Fatalf("non-platform tenant administration expected 403, got %d %s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestPlatformTenantCanCreateTenant(t *testing.T) {
	store := org.NewMemoryStore()
	handler := org.NewHandler(store, auth.NewMemoryStore())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenants", bytes.NewBufferString(`{"name":"Managed Tenant","code":"managed","admin_username":"school_admin","admin_display_name":"School Admin","admin_password":"SchoolStart123!"}`))
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "platform-admin", TenantID: auth.PlatformTenantID}))
	rec := httptest.NewRecorder()

	handler.CreateTenant(rec, req)

	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"code":"managed"`) {
		t.Fatalf("platform tenant create expected 201, got %d %s", rec.Code, rec.Body.String())
	}
}

func testRouter(authStore *auth.MemoryStore, orgStore *org.MemoryStore) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
	}
	return server.NewRouter(cfg, logger.New(io.Discard, "error"), nil, authStore, orgStore, exam.NewMemoryStore(), paper.NewMemoryStore())
}

func authStoreWithPermissions(t *testing.T, permissions []string) *auth.MemoryStore {
	t.Helper()
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store := auth.NewMemoryStore()
	store.AddUser(auth.UserWithPassword{
		User: auth.User{
			ID:          "u-org",
			TenantID:    "tenant-org",
			TenantCode:  "demo",
			Username:    "school_admin",
			DisplayName: "Tenant Admin",
			Status:      "active",
			Roles:       []string{"tenant_admin"},
			Permissions: permissions,
			DataScope:   map[string]any{"scope": "tenant"},
		},
		PasswordHash: hash,
	})
	return store
}

func login(t *testing.T, router http.Handler) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"tenant_code": "demo", "username": "school_admin", "password": "ChangeMe123!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.AccessToken
}

func assertOrganizationSchoolCodeConflict(t *testing.T) {
	t.Helper()
	authStore := authStoreWithPermissions(t, []string{"org:manage"})
	router := testRouter(authStore, org.NewMemoryStore())
	token := login(t, router)
	payload := `{"name":"第一中学","code":"school-001"}`
	for attempt, expectedStatus := range []int{http.StatusCreated, http.StatusConflict} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/schools", bytes.NewBufferString(payload))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != expectedStatus {
			t.Fatalf("attempt %d expected %d, got %d: %s", attempt+1, expectedStatus, rec.Code, rec.Body.String())
		}
		if expectedStatus == http.StatusConflict && !strings.Contains(rec.Body.String(), "school_code_conflict") {
			t.Fatalf("duplicate school code should return explicit conflict code: %s", rec.Body.String())
		}
	}
}

func assertOrganizationClassCodeConflict(t *testing.T) {
	t.Helper()
	authStore := authStoreWithPermissions(t, []string{"org:manage"})
	orgStore := org.NewMemoryStore()
	school, err := orgStore.CreateSchool(context.Background(), "tenant-org", org.School{Name: "第一中学", Code: "school-001"})
	if err != nil {
		t.Fatal(err)
	}
	grade, err := orgStore.CreateGrade(context.Background(), "tenant-org", org.Grade{SchoolID: school.ID, Name: "高一年级"})
	if err != nil {
		t.Fatal(err)
	}
	router := testRouter(authStore, orgStore)
	token := login(t, router)
	payload := `{"school_id":"` + school.ID + `","grade_id":"` + grade.ID + `","name":"高一（1）班","code":"G10-01"}`
	for attempt, expectedStatus := range []int{http.StatusCreated, http.StatusConflict} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/classes", bytes.NewBufferString(payload))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != expectedStatus {
			t.Fatalf("attempt %d expected %d, got %d: %s", attempt+1, expectedStatus, rec.Code, rec.Body.String())
		}
		if expectedStatus == http.StatusConflict && !strings.Contains(rec.Body.String(), "class_code_conflict") {
			t.Fatalf("duplicate class code should return explicit conflict code: %s", rec.Body.String())
		}
	}
}
