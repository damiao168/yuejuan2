package auth_test

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInvitedTeacherActivatesAndLogsInWithPhoneIdentifier(t *testing.T) {
	store := newOrganizationAdminStore(t)
	router := newTestRouter(store)
	user, token := legacyTeacherInvitation(t, store, "legacy-phone-teacher", "13800138000", "T-1001")
	var created struct {
		User       auth.ManagedUser `json:"user"`
		Activation struct {
			Token string `json:"token"`
		} `json:"activation"`
	}
	created.User, created.Activation.Token = user, token
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
	user, token := legacyTeacherInvitation(t, store, "legacy-pending-teacher", "13900139000", "T-1002")
	var created struct {
		User       auth.ManagedUser `json:"user"`
		Activation struct {
			Token string `json:"token"`
		} `json:"activation"`
	}
	created.User, created.Activation.Token = user, token
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
		"username":"recover_teacher","display_name":"Recover Teacher","password":"OriginalTeacher123!","password_confirm":"OriginalTeacher123!","teaching_subjects":["mathematics"],"role_code":"teacher","school_id":"school-1"
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

	first := request(`{"username":"teacher01","display_name":"Teacher","password":"TeacherStart123!","password_confirm":"TeacherStart123!","teaching_subjects":["mathematics"],"role_code":"teacher","school_id":"school-1"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create expected 201, got %d: %s", first.Code, first.Body.String())
	}
	duplicate := request(`{"username":"teacher01","display_name":"Teacher 2","password":"TeacherStart123!","password_confirm":"TeacherStart123!","teaching_subjects":["mathematics"],"role_code":"teacher","school_id":"school-1"}`)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate expected 409, got %d: %s", duplicate.Code, duplicate.Body.String())
	}
	crossTenantRole := request(`{"username":"grader01","display_name":"Grader","password":"GraderStart123!","password_confirm":"GraderStart123!","role_code":"grader"}`)
	if crossTenantRole.Code != http.StatusBadRequest {
		t.Fatalf("cross tenant role expected 400, got %d: %s", crossTenantRole.Code, crossTenantRole.Body.String())
	}
	privilegedRole := request(`{"username":"admin02","display_name":"Admin","password":"AdminStart123!!","role_code":"tenant_admin"}`)
	if privilegedRole.Code != http.StatusForbidden {
		t.Fatalf("privileged role expected 403, got %d: %s", privilegedRole.Code, privilegedRole.Body.String())
	}
	forgedTenant := request(`{"tenant_id":"t-2","username":"teacher02","display_name":"Teacher","password":"TeacherStart123!","teaching_subjects":["mathematics"],"role_code":"teacher"}`)
	if forgedTenant.Code != http.StatusBadRequest {
		t.Fatalf("tenant_id field expected 400, got %d: %s", forgedTenant.Code, forgedTenant.Body.String())
	}
}
