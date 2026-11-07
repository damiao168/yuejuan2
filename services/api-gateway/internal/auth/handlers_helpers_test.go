package auth_test

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/server"
	"io"
	"net/http"
	"testing"
	"time"
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
	store.AddRole("t-1", auth.AssignableRole{Code: "teacher", Name: "教师", ScopeType: "exam_task"})
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
