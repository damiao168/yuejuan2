package review

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func doubleMarkCollectionFixture(t *testing.T) (*MemoryStore, *auth.MemoryStore) {
	t.Helper()
	store, identities := NewMemoryStore(), auth.NewMemoryStore()
	for _, item := range []struct{ exam, school, class string }{
		{"a", "school-a", "class-a"}, {"b", "school-b", "class-b"}, {"c", "school-a", "class-c"},
	} {
		current := reviewContext()
		current.ExamID, current.Question.ExamID, current.Question.ID = "exam-"+item.exam, "exam-"+item.exam, "question-"+item.exam
		store.AddContext(tenantID, "segment-"+item.exam, current)
		if _, err := store.SetExamDoubleMarkPolicy(context.Background(), tenantID, current.ExamID, "manager", SetDoubleMarkPolicyInput{Enabled: true, Threshold: 1, ResolutionStrategy: "average"}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateDoubleMarkSession(context.Background(), tenantID, "manager", CreateDoubleMarkSessionInput{AnswerSegmentID: "segment-" + item.exam, FirstReviewerID: "first", SecondReviewerID: "second"}); err != nil {
			t.Fatal(err)
		}
		identities.AddResourceBoundary("exam", current.ExamID, auth.ResourceBoundary{TenantID: tenantID, ExamID: current.ExamID, SchoolID: item.school, ClassIDs: []string{item.class}})
	}
	return store, identities
}

func doubleMarkSchoolScope(t *testing.T) auth.AccessScope {
	t.Helper()
	scope, err := auth.ResolveDeclaredAccessScope(auth.User{ID: "manager", TenantID: tenantID, Roles: []string{"school_admin"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}})
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestDoubleMarkMemoryCollectionsApplyAuthenticatedBoundaryBeforeOrdering(t *testing.T) {
	store, identities := doubleMarkCollectionFixture(t)
	for _, test := range []struct {
		name, exam string
		scope      auth.AccessScope
		want       []string
	}{
		{"school", "", doubleMarkSchoolScope(t), []string{"exam-a", "exam-c"}},
		{"foreign explicit exam", "exam-b", doubleMarkSchoolScope(t), nil},
		{"native explicit exam", "exam-a", doubleMarkSchoolScope(t), []string{"exam-a"}},
		{"class cannot inherit entire school", "", auth.AccessScope{TenantID: tenantID, ActorID: "manager", SchoolIDs: []string{"school-a"}, ClassIDs: []string{"class-a"}}, []string{"exam-a"}},
		{"exact exam", "", auth.AccessScope{TenantID: tenantID, ActorID: "manager", ExamIDs: []string{"exam-a"}}, []string{"exam-a"}},
		{"empty whitelist", "", auth.AccessScope{TenantID: tenantID, ActorID: "manager"}, nil},
		{"assigned only", "", auth.AccessScope{TenantID: tenantID, ActorID: "manager", AssignedOnly: true}, nil},
		{"tenant administrator", "", auth.AccessScope{TenantID: tenantID, ActorID: "manager", TenantWide: true}, []string{"exam-a", "exam-b", "exam-c"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			access := auth.ExamCollectionAccess{Scope: &test.scope, Resolver: identities}
			policies, err := store.ListDoubleMarkPolicies(context.Background(), tenantID, PolicyFilter{ExamID: test.exam, Access: access})
			if err != nil || len(policies) != len(test.want) {
				t.Fatalf("policies=%#v err=%v", policies, err)
			}
			sessions, err := store.ListDoubleMarkSessions(context.Background(), tenantID, DoubleMarkSessionFilter{ExamID: test.exam, Access: access})
			if err != nil || len(sessions) != len(test.want) {
				t.Fatalf("sessions=%#v err=%v", sessions, err)
			}
			allowed := map[string]bool{}
			for _, exam := range test.want {
				allowed[exam] = true
			}
			for _, policy := range policies {
				if !allowed[policy.ExamID] {
					t.Fatalf("foreign policy: %#v", policy)
				}
			}
			for _, session := range sessions {
				if !allowed[session.ExamID] {
					t.Fatalf("foreign session: %#v", session)
				}
			}
		})
	}
	wrong := auth.AccessScope{TenantID: "other-tenant", ActorID: "manager", TenantWide: true}
	if _, err := store.ListDoubleMarkPolicies(context.Background(), tenantID, PolicyFilter{Access: auth.ExamCollectionAccess{Scope: &wrong}}); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("wrong policy tenant accepted: %v", err)
	}
	if _, err := store.ListDoubleMarkSessions(context.Background(), tenantID, DoubleMarkSessionFilter{Access: auth.ExamCollectionAccess{Scope: &wrong}}); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("wrong session tenant accepted: %v", err)
	}
}

func TestDoubleMarkCollectionHandlersRequireAndNarrowServerScope(t *testing.T) {
	store, identities := doubleMarkCollectionFixture(t)
	handler := NewHandler(store, identities, nil, nil)
	user := auth.User{ID: "manager", TenantID: tenantID, Roles: []string{"school_admin"}, Permissions: []string{"review:manage"}}
	school := doubleMarkSchoolScope(t)
	empty := auth.AccessScope{TenantID: tenantID, ActorID: user.ID}
	for _, endpoint := range []struct {
		path, key string
		handler   http.HandlerFunc
	}{
		{"/api/v1/double-mark-policies", "policies", handler.ListDoubleMarkPolicies},
		{"/api/v1/double-mark-sessions", "double_mark_sessions", handler.ListDoubleMarkSessions},
	} {
		for _, test := range []struct {
			name, query string
			scope       *auth.AccessScope
			status, n   int
		}{
			{"missing scope", "", nil, http.StatusForbidden, 0},
			{"empty whitelist", "", &empty, http.StatusOK, 0},
			{"native school", "", &school, http.StatusOK, 2},
			{"foreign exam filter", "?exam_id=exam-b", &school, http.StatusOK, 0},
			{"foreign question filter", "?question_id=question-b", &school, http.StatusOK, 0},
		} {
			t.Run(endpoint.key+"/"+test.name, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodGet, endpoint.path+test.query, nil)
				ctx := auth.WithUser(request.Context(), user)
				if test.scope != nil {
					ctx = auth.WithAccessScope(ctx, *test.scope)
				}
				response := httptest.NewRecorder()
				endpoint.handler(response, request.WithContext(ctx))
				if response.Code != test.status || strings.Contains(response.Body.String(), `"exam_id":"exam-b"`) {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
				if response.Code == http.StatusOK {
					var result map[string][]json.RawMessage
					if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result[endpoint.key]) != test.n {
						t.Fatalf("result=%s err=%v", response.Body.String(), err)
					}
				}
			})
		}
	}
}
