package review

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

// 使用已迁移库的隔离 schema，实际穿过 Handler 与 PostgreSQL 的排序、游标和 LIMIT。
// 外校记录排在本校记录之间，防止“先分页后过滤”伪装成正确学校隔离。
func TestPostgresArbitrationManagementListExcludesNavigationOnlySchoolBeforePagination(t *testing.T) {
	f := newSubjectAssignmentFixture(t)
	foreignSchool := uuid.NewString()
	managedExam, foreignExam := uuid.NewString(), uuid.NewString()
	for exam, school := range map[string]string{managedExam: f.school, foreignExam: foreignSchool} {
		f.insert("exam", map[string]any{"id": exam, "tenant_id": f.tenant, "school_id": school, "name": "synthetic arbitration scope"})
	}
	workerID := uuid.NewString()
	base := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	insert := func(exam, assigned string, offset int) string {
		id := uuid.NewString()
		fields := map[string]any{
			"id": id, "tenant_id": f.tenant, "double_mark_session_id": uuid.NewString(), "exam_id": exam,
			"question_id": uuid.NewString(), "question_no": "Q1", "answer_segment_id": uuid.NewString(), "submission_id": uuid.NewString(),
			"anonymous_code": "SYNTHETIC", "first_reviewer_id": uuid.NewString(), "second_reviewer_id": uuid.NewString(),
			"first_score": 1, "second_score": 3, "score_difference": 2, "difference_reason": "synthetic disagreement",
			"status": "assigned", "reason": "", "student_feedback": "", "allow_same_arbitrator": false,
			"context": map[string]any{}, "revision": 1, "created_by": f.manager, "created_at": base.Add(time.Duration(offset) * time.Minute), "updated_at": base,
		}
		if assigned != "" {
			fields["assigned_to"] = assigned
		}
		f.insert("arbitration_task", fields)
		return id
	}
	olderManaged := insert(managedExam, "", 1)
	foreignAssigned := insert(foreignExam, workerID, 2)
	newerManaged := insert(managedExam, "", 3)
	insert(foreignExam, uuid.NewString(), 4)
	manager := auth.User{ID: f.manager, TenantID: f.tenant, Roles: []string{"school_admin"}, Permissions: []string{"arbitration:manage"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": f.school}}}
	managerScope, err := auth.ResolveDeclaredAccessScope(manager)
	if err != nil {
		t.Fatal(err)
	}
	managerScope.SchoolIDs = append(managerScope.SchoolIDs, foreignSchool)
	managerScope.ExamIDs = []string{managedExam, foreignExam}
	handler := NewHandler(NewPostgresStore(f.db), nil, nil, nil)
	type page struct {
		Tasks      []ArbitrationTask `json:"arbitration_tasks"`
		NextCursor string            `json:"next_cursor"`
		HasMore    bool              `json:"has_more"`
	}
	read := func(user auth.User, scope auth.AccessScope, query url.Values) page {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/arbitration-tasks?"+query.Encode(), nil)
		request = request.WithContext(auth.WithAccessScope(auth.WithUser(request.Context(), user), scope))
		response := httptest.NewRecorder()
		handler.ListArbitrationTasks(response, request)
		var result page
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		return result
	}
	first := read(manager, managerScope, url.Values{"limit": {"1"}})
	if len(first.Tasks) != 1 || first.Tasks[0].ID != newerManaged || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first managed page=%#v", first)
	}
	second := read(manager, managerScope, url.Values{"limit": {"1"}, "cursor": {first.NextCursor}})
	if len(second.Tasks) != 1 || second.Tasks[0].ID != olderManaged || second.HasMore || second.NextCursor != "" {
		t.Fatalf("second managed page=%#v", second)
	}
	if foreign := read(manager, managerScope, url.Values{"exam_id": {foreignExam}}); len(foreign.Tasks) != 0 || foreign.HasMore {
		t.Fatalf("explicit foreign exam escaped management boundary: %#v", foreign)
	}
	worker := auth.User{ID: workerID, TenantID: f.tenant, Roles: []string{"arbitrator"}, Permissions: []string{"arbitration:work"}}
	assignedScope := auth.AccessScope{TenantID: f.tenant, ActorID: workerID, AssignedOnly: true}
	if own := read(worker, assignedScope, url.Values{"assigned_to": {f.manager}}); len(own.Tasks) != 1 || own.Tasks[0].ID != foreignAssigned {
		t.Fatalf("assigned worker lost own task or read another assignee: %#v", own)
	}
	tenantManager := auth.User{ID: f.manager, TenantID: f.tenant, Roles: []string{"tenant_admin"}, Permissions: []string{"arbitration:manage"}}
	if all := read(tenantManager, auth.AccessScope{TenantID: f.tenant, ActorID: f.manager, TenantWide: true}, nil); len(all.Tasks) != 4 {
		t.Fatalf("tenant manager scope changed: %#v", all)
	}
}
