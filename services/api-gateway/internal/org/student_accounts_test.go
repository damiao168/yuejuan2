package org

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type studentAccountRequestStore struct {
	*MemoryStore
	created []StudentAccountProvision
	opened  []string
}

func (s *studentAccountRequestStore) CreateStudentWithAccount(_ context.Context, tenantID string, input StudentAccountProvision) (Student, error) {
	s.created = append(s.created, input)
	student := input.Student
	student.ID, student.TenantID, student.AccountStatus, student.MustChangePassword = "created-student", tenantID, "active", true
	student.LoginAccount, student.LoginSchoolCode = student.StudentNo, "synthetic-school"
	return student, nil
}

func (s *studentAccountRequestStore) OpenStudentAccount(_ context.Context, tenantID, studentID, passwordHash string) (Student, error) {
	s.opened = append(s.opened, studentID)
	return Student{ID: studentID, TenantID: tenantID, StudentNo: "00123", AccountStatus: "active", MustChangePassword: true}, nil
}

func studentAccountRequest(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/students", strings.NewReader(body))
	req.SetPathValue("id", "student-a")
	user := auth.User{ID: "manager", TenantID: "tenant", Roles: []string{"tenant_admin"}, DataScope: map[string]any{"scope": "tenant"}}
	scope, _ := auth.ResolveDeclaredAccessScope(user)
	req = req.WithContext(auth.WithAccessScope(auth.WithUser(req.Context(), user), scope))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestStudentCreateRequiresPasswordsAndRejectsCallerIdentity(t *testing.T) {
	for _, tc := range []struct{ name, extra string }{
		{"missing passwords", ""},
		{"missing confirmation", `,"password":"Synthetic123!"`},
		{"different confirmation", `,"password":"Synthetic123!","password_confirm":"different"`},
		{"short password", `,"password":"short","password_confirm":"short"`},
		{"blank password", `,"password":"        ","password_confirm":"        "`},
		{"caller user", `,"password":"Synthetic123!","password_confirm":"Synthetic123!","user_id":"other"`},
		{"caller roles", `,"password":"Synthetic123!","password_confirm":"Synthetic123!","roles":["tenant_admin"]`},
		{"caller student id", `,"password":"Synthetic123!","password_confirm":"Synthetic123!","id":"other"`},
		{"caller status", `,"password":"Synthetic123!","password_confirm":"Synthetic123!","status":"inactive"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &studentAccountRequestStore{MemoryStore: NewMemoryStore()}
			handler := NewHandler(store, auth.NewMemoryStore())
			rec := studentAccountRequest(handler.CreateStudent, `{"school_id":"school","class_id":"class","student_no":"00123","name":"Synthetic"`+tc.extra+`}`)
			if rec.Code != http.StatusBadRequest || len(store.created) != 0 {
				t.Fatalf("invalid account request reached storage: %d %s calls=%d", rec.Code, rec.Body.String(), len(store.created))
			}
		})
	}
}

func TestStudentCreateHashesExactPasswordAndReturnsOnlyAccountProjection(t *testing.T) {
	store := &studentAccountRequestStore{MemoryStore: NewMemoryStore()}
	handler := NewHandler(store, auth.NewMemoryStore())
	rec := studentAccountRequest(handler.CreateStudent, `{"school_id":"school","class_id":"class","student_no":" 00123 ","name":" Synthetic ","password":"  secret123  ","password_confirm":"  secret123  "}`)
	if rec.Code != http.StatusCreated || len(store.created) != 1 {
		t.Fatalf("create=%d %s", rec.Code, rec.Body.String())
	}
	created := store.created[0]
	if created.Student.StudentNo != "00123" || created.Student.Name != "Synthetic" || created.Student.Status != "active" || !auth.CheckPassword(created.PasswordHash, "  secret123  ") || auth.CheckPassword(created.PasswordHash, "secret123") {
		t.Fatalf("identity normalization changed password or status: %+v", created.Student)
	}
	var body map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["student"]["account_status"] != "active" || body["student"]["must_change_password"] != true || strings.Contains(rec.Body.String(), "secret123") || strings.Contains(rec.Body.String(), "$argon2id$") {
		t.Fatalf("unexpected account projection: %s", rec.Body.String())
	}
}

func TestStudentAccountOpeningRejectsIdentityAndUnavailableStorage(t *testing.T) {
	store := &studentAccountRequestStore{MemoryStore: NewMemoryStore()}
	handler := NewHandler(store, auth.NewMemoryStore())
	rec := studentAccountRequest(handler.OpenStudentAccount, `{"password":"Synthetic123!","password_confirm":"Synthetic123!","student_id":"other"}`)
	if rec.Code != http.StatusBadRequest || len(store.opened) != 0 {
		t.Fatalf("opening accepted caller identity: %d %s", rec.Code, rec.Body.String())
	}
	handler = NewHandler(NewMemoryStore(), auth.NewMemoryStore())
	rec = studentAccountRequest(handler.OpenStudentAccount, `{"password":"Synthetic123!","password_confirm":"Synthetic123!"}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "student_account_unavailable") {
		t.Fatalf("separate memory stores accepted account opening: %d %s", rec.Code, rec.Body.String())
	}
}
