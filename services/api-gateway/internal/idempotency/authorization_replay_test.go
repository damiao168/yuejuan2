package idempotency

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestAuthorizedRequestHashIncludesPrivateSchoolQueryMode(t *testing.T) {
	user := auth.User{ID: "actor", TenantID: "tenant", DataScope: map[string]any{"scope": "school", "school_id": "school-a"}}
	wide, err := auth.ResolveDeclaredAccessScope(user)
	if err != nil {
		t.Fatal(err)
	}
	// 学校 ID 相同，但未获学校级权限的投影不会带私有 schoolWide；旧 JSON 编码无法区分两者。
	wideJSON, _ := json.Marshal(wide)
	var narrow auth.AccessScope
	if err := json.Unmarshal(wideJSON, &narrow); err != nil {
		t.Fatal(err)
	}
	narrowJSON, _ := json.Marshal(narrow)
	if string(wideJSON) != string(narrowJSON) || wide.QueryMode() == narrow.QueryMode() {
		t.Fatal("fixture must differ only in private collection authority")
	}
	wideHash, err := authorizedRequestHash("command", user, &wide)
	if err != nil {
		t.Fatal(err)
	}
	narrowHash, err := authorizedRequestHash("command", user, &narrow)
	if err != nil || wideHash == narrowHash {
		t.Fatalf("different query authority received the same fingerprint: %v", err)
	}
}

func TestAuthorizedRequestHashCanonicalizesDerivedIDSetsWithoutMutation(t *testing.T) {
	user := auth.User{ID: "actor", TenantID: "tenant"}
	first := auth.AccessScope{TenantID: "tenant", ActorID: "actor", ExamIDs: []string{"exam-b", "exam-a", "exam-a"}}
	second := auth.AccessScope{TenantID: "tenant", ActorID: "actor", ExamIDs: []string{"exam-a", "exam-b"}, FileIDs: []string{}}
	firstHash, err := authorizedRequestHash("command", user, &first)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := authorizedRequestHash("command", user, &second)
	if err != nil || firstHash != secondHash {
		t.Fatalf("equivalent derived scope received different fingerprints: %v", err)
	}
	if strings.Join(first.ExamIDs, ",") != "exam-b,exam-a,exam-a" {
		t.Fatal("fingerprint generation mutated the request scope")
	}
}

func TestReadMethodsNeverPersistOrReplayCommandReceipts(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			store := NewMemoryStore()
			calls := 0
			handler := Middleware(store, Options{Enforce: true})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(http.StatusOK)
			}))
			for range 2 {
				req := httptest.NewRequest(method, "/api/v1/users", nil)
				req.Header.Set(Header, "read-command")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK || rec.Header().Get("Idempotency-Replayed") != "" {
					t.Fatalf("read method status=%d replay=%q", rec.Code, rec.Header().Get("Idempotency-Replayed"))
				}
			}
			if calls != 2 || len(store.records) != 0 {
				t.Fatalf("read method calls=%d receipts=%d", calls, len(store.records))
			}
		})
	}
}

func TestCommandReceiptRejectsChangedAuthorizationAndResolvedAssignments(t *testing.T) {
	mutations := []struct {
		name   string
		change func(*auth.User, *auth.AccessScope)
	}{
		{"roles", func(u *auth.User, _ *auth.AccessScope) { u.Roles = []string{"teacher"} }},
		{"permissions", func(u *auth.User, _ *auth.AccessScope) { u.Permissions = []string{"exam:read"} }},
		{"declared scope", func(u *auth.User, _ *auth.AccessScope) {
			u.DataScope = map[string]any{"scope": "class", "class_id": "class-b"}
		}},
		{"organization scope", func(u *auth.User, _ *auth.AccessScope) { u.OrganizationScope.ClassIDs = []string{"class-b"} }},
		{"resolved assignment", func(_ *auth.User, s *auth.AccessScope) { s.SubmissionIDs = []string{"submission-b"} }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			calls := 0
			mux := http.NewServeMux()
			mux.Handle("POST /api/v1/exams", Middleware(NewMemoryStore(), Options{Enforce: true})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(http.StatusCreated)
			})))
			user := auth.User{ID: "actor", TenantID: "tenant", Roles: []string{"school_admin"}, Permissions: []string{"exam:manage"},
				DataScope: map[string]any{"scope": "class", "class_id": "class-a"}, OrganizationScope: &auth.OrganizationScope{ClassIDs: []string{"class-a"}}}
			scope := auth.AccessScope{TenantID: "tenant", ActorID: "actor", SubmissionIDs: []string{"submission-a"}}
			request := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/exams", strings.NewReader(`{"name":"exam"}`))
				req.Header.Set(Header, "authorized-command")
				req = req.WithContext(auth.WithAccessScope(auth.WithUser(req.Context(), user), scope))
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				return rec
			}
			if first := request(); first.Code != http.StatusCreated {
				t.Fatalf("first command=%d %s", first.Code, first.Body.String())
			}
			if replay := request(); replay.Code != http.StatusCreated || replay.Header().Get("Idempotency-Replayed") != "true" {
				t.Fatalf("same grant replay=%d %s", replay.Code, replay.Body.String())
			}
			mutation.change(&user, &scope)
			conflict := request()
			if conflict.Code != http.StatusConflict || calls != 1 || conflict.Header().Get("Idempotency-Replayed") == "true" {
				t.Fatalf("changed grant response=%d calls=%d replay=%q", conflict.Code, calls, conflict.Header().Get("Idempotency-Replayed"))
			}
		})
	}
}
