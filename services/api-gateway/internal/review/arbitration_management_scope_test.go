package review

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type arbitrationScopeCaptureStore struct {
	Store
	filter ArbitrationFilter
}

func TestArbitrationMemoryManagementListFiltersActualSchoolBeforePagination(t *testing.T) {
	store, identities := NewMemoryStore(), auth.NewMemoryStore()
	manager := auth.User{ID: "manager", TenantID: "tenant", Roles: []string{"school_admin"}, Permissions: []string{"arbitration:manage"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}}
	scope, err := auth.ResolveDeclaredAccessScope(manager)
	if err != nil {
		t.Fatal(err)
	}
	scope.SchoolIDs = append(scope.SchoolIDs, "school-b")
	// 导航列表故意只有外校考试，本校任务仍应按实际学校归属返回。
	scope.ExamIDs = []string{"exam-b"}
	for _, school := range []string{"a", "b"} {
		identities.AddResourceBoundary("exam", "exam-"+school, auth.ResourceBoundary{TenantID: "tenant", SchoolID: "school-" + school, ExamID: "exam-" + school})
	}
	for i, school := range []string{"a", "b", "a", "b"} {
		id := string(rune('1' + i))
		store.arbitrations[key("tenant", id)] = ArbitrationTask{ID: id, TenantID: "tenant", ExamID: "exam-" + school, CreatedAt: time.Unix(int64(i), 0)}
	}
	handler := NewHandler(store, identities, nil, nil)
	type page struct {
		Tasks      []ArbitrationTask `json:"arbitration_tasks"`
		NextCursor string            `json:"next_cursor"`
		HasMore    bool              `json:"has_more"`
	}
	read := func(query url.Values, handler *Handler) page {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/arbitration-tasks?"+query.Encode(), nil)
		request = request.WithContext(auth.WithAccessScope(auth.WithUser(request.Context(), manager), scope))
		response := httptest.NewRecorder()
		handler.ListArbitrationTasks(response, request)
		var result page
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		return result
	}
	first := read(url.Values{"limit": {"1"}}, handler)
	if len(first.Tasks) != 1 || first.Tasks[0].ID != "3" || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first managed page=%#v", first)
	}
	second := read(url.Values{"limit": {"1"}, "cursor": {first.NextCursor}}, handler)
	if len(second.Tasks) != 1 || second.Tasks[0].ID != "1" || second.HasMore {
		t.Fatalf("second managed page=%#v", second)
	}
	if foreign := read(url.Values{"exam_id": {"exam-b"}}, handler); len(foreign.Tasks) != 0 {
		t.Fatalf("explicit foreign exam escaped scope: %#v", foreign)
	}
	if unresolved := read(nil, NewHandler(store, nil, nil, nil)); len(unresolved.Tasks) != 0 {
		t.Fatalf("missing school resolver expanded access: %#v", unresolved)
	}
}

func (s *arbitrationScopeCaptureStore) ListArbitrationTasks(_ context.Context, _ string, filter ArbitrationFilter) ([]ArbitrationTask, error) {
	s.filter = filter
	return []ArbitrationTask{}, nil
}

func TestArbitrationListUsesManagedSchoolsWithoutChangingAssignedWorkerScope(t *testing.T) {
	manager := auth.User{ID: "manager", TenantID: "tenant", Roles: []string{"school_admin"}, Permissions: []string{"arbitration:manage"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}}
	managedScope, err := auth.ResolveDeclaredAccessScope(manager)
	if err != nil {
		t.Fatal(err)
	}
	// 同一人另有外校导航关系；该关系不得把管理列表扩大到外校。
	managedScope.SchoolIDs = append(managedScope.SchoolIDs, "school-b")
	managedScope.ExamIDs = []string{"foreign-exam"}
	worker := auth.User{ID: "worker", TenantID: "tenant", Roles: []string{"arbitrator"}, Permissions: []string{"arbitration:work"}}
	for _, tc := range []struct {
		name, query, mode, assignee string
		user                        auth.User
		scope                       auth.AccessScope
		schools                     []string
	}{
		{"mixed manager", "", "school", "", manager, managedScope, []string{"school-a"}},
		{"explicit foreign exam", "?exam_id=foreign-exam", "school", "", manager, managedScope, []string{"school-a"}},
		{"assigned worker", "?assigned_to=someone-else", "assigned", worker.ID, worker, auth.AccessScope{TenantID: "tenant", ActorID: worker.ID, AssignedOnly: true}, nil},
		{"tenant manager", "", "tenant", "", auth.User{ID: "tenant-manager", TenantID: "tenant", Roles: []string{"tenant_admin"}, Permissions: []string{"arbitration:manage"}}, auth.AccessScope{TenantID: "tenant", ActorID: "tenant-manager", TenantWide: true}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &arbitrationScopeCaptureStore{}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/arbitration-tasks"+tc.query, nil)
			request = request.WithContext(auth.WithAccessScope(auth.WithUser(request.Context(), tc.user), tc.scope))
			response := httptest.NewRecorder()
			NewHandler(store, nil, nil, nil).ListArbitrationTasks(response, request)
			if response.Code != http.StatusOK || store.filter.ScopeMode != tc.mode || store.filter.AssignedTo != tc.assignee || !reflect.DeepEqual(store.filter.ScopeSchoolIDs, tc.schools) {
				t.Fatalf("status=%d filter=%#v body=%s", response.Code, store.filter, response.Body.String())
			}
		})
	}
}
