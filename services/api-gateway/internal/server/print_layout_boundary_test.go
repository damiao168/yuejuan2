package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type printLayoutBoundaryStore struct {
	*capture.MemoryStore
	confirmed   []string
	sourceReads []string
}

func (s *printLayoutBoundaryStore) ConfirmStudentPrintLayout(_ context.Context, tenantID, batchID string, input capture.StudentPrintLayout) (capture.StudentPrintLayout, error) {
	s.confirmed = append(s.confirmed, tenantID+"/"+batchID)
	return input, nil
}

func (s *printLayoutBoundaryStore) GetStudentPrintSource(_ context.Context, tenantID, templateID string) (capture.StudentPrintSourceContent, error) {
	s.sourceReads = append(s.sourceReads, tenantID+"/"+templateID)
	return capture.StudentPrintSourceContent{ContentType: "application/pdf", SHA256: strings.Repeat("a", 64), Payload: []byte("%PDF-1.7\nsynthetic verified source")}, nil
}

func TestPrintLayoutConfirmationRouteRequiresManagedBatchAndPermission(t *testing.T) {
	stores := NewMemoryApplicationStores()
	identities := stores.Identity.Auth.(*auth.MemoryStore)
	confirmations := &printLayoutBoundaryStore{MemoryStore: capture.NewMemoryStore()}
	stores.Capture.Capture = confirmations
	for _, actor := range []struct {
		id          string
		permissions []string
	}{
		{"manager", []string{"exam:manage"}},
		{"no-manage", []string{"review:work"}},
	} {
		backendRegressionSession(t, identities, auth.UserWithPassword{User: auth.User{
			ID: actor.id, TenantID: "tenant-a", Username: actor.id, Status: "active", Roles: []string{"school_admin"}, Permissions: actor.permissions,
			DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}},
		}, SchoolID: "school-a", SecurityEpoch: 1}, actor.id+"-session")
	}
	for batchID, boundary := range map[string]auth.ResourceBoundary{
		"owned":        {TenantID: "tenant-a", SchoolID: "school-a", ExamID: "exam-a"},
		"other-exam":   {TenantID: "tenant-a", SchoolID: "school-b", ExamID: "exam-b"},
		"other-tenant": {TenantID: "tenant-b", SchoolID: "school-a", ExamID: "exam-a"},
	} {
		identities.AddResourceBoundary("answer_sheet_print_batch", batchID, boundary)
	}
	router := NewRouterWithApplicationStores(backendRegressionConfig(), logger.New(io.Discard, "error"), nil, nil, stores)
	// 使用正式路由注册和认证/权限/对象边界链。拒绝必须发生在确认存储调用前；
	// 内存边界只替代归属查询，真实 PG 确认、状态和锁已由 Story060 集成测试覆盖。
	for _, tc := range []struct {
		name, batch, token string
		want               int
	}{
		{"managed", "owned", "manager-session", http.StatusOK},
		{"other-exam", "other-exam", "manager-session", http.StatusForbidden},
		{"query-cannot-rebind-exam", "other-exam?exam_id=exam-a", "manager-session", http.StatusForbidden},
		{"other-tenant", "other-tenant", "manager-session", http.StatusForbidden},
		{"no-manage-permission", "owned", "no-manage-session", http.StatusForbidden},
		{"unauthenticated", "owned", "", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(confirmations.confirmed)
			batchID, query, _ := strings.Cut(tc.batch, "?")
			path := "/api/v1/answer-sheet-print-batches/" + batchID + "/confirm-layout"
			if query != "" {
				path += "?" + query
			}
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"source_sha256":"`+strings.Repeat("a", 64)+`","pages":[{"page_no":1,"x":0.1,"y":0.7,"width":0.2,"height":0.2}]}`))
			if tc.token != "" {
				request.Header.Set("Authorization", "Bearer "+tc.token)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("got %d want %d: %s", response.Code, tc.want, response.Body.String())
			}
			calls := len(confirmations.confirmed) - before
			if tc.want == http.StatusOK {
				if calls != 1 || confirmations.confirmed[before] != "tenant-a/owned" {
					t.Fatalf("wrong confirmation target: %#v", confirmations.confirmed)
				}
			} else if calls != 0 {
				t.Fatalf("unauthorized confirmation reached storage: %#v", confirmations.confirmed)
			}
		})
	}
}

func TestPrintSourceRouteNeedsExamPermissionAndBoundTemplate(t *testing.T) {
	stores := NewMemoryApplicationStores()
	identities := stores.Identity.Auth.(*auth.MemoryStore)
	source := &printLayoutBoundaryStore{MemoryStore: capture.NewMemoryStore()}
	stores.Capture.Capture = source
	for _, actor := range []struct {
		id          string
		permissions []string
	}{
		{"manager", []string{"exam:manage"}},
		{"no-manage", []string{"review:work"}},
	} {
		backendRegressionSession(t, identities, auth.UserWithPassword{User: auth.User{
			ID: actor.id, TenantID: "tenant-a", Username: actor.id, Status: "active", Roles: []string{"school_admin"}, Permissions: actor.permissions,
			DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}},
		}, SchoolID: "school-a", SecurityEpoch: 1}, actor.id+"-session")
	}
	for templateID, boundary := range map[string]auth.ResourceBoundary{
		"owned":        {TenantID: "tenant-a", SchoolID: "school-a", ExamID: "exam-a"},
		"other-exam":   {TenantID: "tenant-a", SchoolID: "school-b", ExamID: "exam-b"},
		"other-tenant": {TenantID: "tenant-b", SchoolID: "school-a", ExamID: "exam-a"},
	} {
		identities.AddResourceBoundary("answer_sheet_template", templateID, boundary)
	}
	router := NewRouterWithApplicationStores(backendRegressionConfig(), logger.New(io.Discard, "error"), nil, nil, stores)
	// 管理打印的角色仅有 exam:manage；该入口必须按模板归属取底图，不能依赖 file:manage 或客户端文件ID。
	for _, tc := range []struct {
		name, template, token string
		want                  int
	}{
		{"exam-manage-without-file-manage", "owned", "manager-session", http.StatusOK},
		{"file-query-cannot-rebind-source", "owned?file_asset_id=other-file", "manager-session", http.StatusOK},
		{"other-exam", "other-exam", "manager-session", http.StatusForbidden},
		{"other-tenant", "other-tenant", "manager-session", http.StatusForbidden},
		{"no-manage-permission", "owned", "no-manage-session", http.StatusForbidden},
		{"unauthenticated", "owned", "", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(source.sourceReads)
			templateID, query, _ := strings.Cut(tc.template, "?")
			path := "/api/v1/answer-sheet-templates/" + templateID + "/print-source"
			if query != "" {
				path += "?" + query
			}
			request := httptest.NewRequest(http.MethodGet, path, nil)
			if tc.token != "" {
				request.Header.Set("Authorization", "Bearer "+tc.token)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("got %d want %d: %s", response.Code, tc.want, response.Body.String())
			}
			calls := len(source.sourceReads) - before
			if tc.want == http.StatusOK {
				if calls != 1 || source.sourceReads[before] != "tenant-a/owned" || response.Header().Get("Content-Type") != "application/pdf" || !bytes.Equal(response.Body.Bytes(), []byte("%PDF-1.7\nsynthetic verified source")) {
					t.Fatalf("source was not bound to the template: calls=%#v headers=%v body=%s", source.sourceReads, response.Header(), response.Body.String())
				}
			} else if calls != 0 {
				t.Fatalf("unauthorized source access reached storage: %#v", source.sourceReads)
			}
		})
	}
}
