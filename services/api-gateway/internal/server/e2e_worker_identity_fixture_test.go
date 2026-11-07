package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

const auditFixtureWorkerUsername = "audit_page_processing_worker"

type e2eWorkerSession struct {
	token string
	mu    sync.Mutex
	tasks map[string]map[string]any
}

var e2eWorkerSessions sync.Map

// 旧验收把管理员令牌用于工作器回调；新夹具使用真实服务登录和实际领取返回的租约，不绕过路由守卫。
func e2ePostWorkerJSON(t *testing.T, router http.Handler, method, path, ownerToken, body string, want int) map[string]any {
	t.Helper()
	var session *e2eWorkerSession
	if value, ok := e2eWorkerSessions.Load(ownerToken); ok {
		session = value.(*e2eWorkerSession)
	} else {
		for _, tenant := range []string{"demo", e2eTenantCode} {
			payload, _ := json.Marshal(map[string]string{"tenant_code": tenant, "username": auditFixtureWorkerUsername, "password": e2ePassword, "client_type": "service"})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(payload))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code == http.StatusOK {
				var result struct {
					AccessToken string `json:"access_token"`
				}
				if json.Unmarshal(rec.Body.Bytes(), &result) == nil && result.AccessToken != "" {
					session = &e2eWorkerSession{token: result.AccessToken, tasks: map[string]map[string]any{}}
					break
				}
			}
		}
		if session == nil {
			t.Fatal("synthetic service worker login failed")
		}
		e2eWorkerSessions.Store(ownerToken, session)
		t.Cleanup(func() { e2eWorkerSessions.Delete(ownerToken) })
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(body), &input); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{}
	if path == "/api/v1/internal/worker/tasks/claim" {
		service := "page-processing"
		if input["queue_name"] == "ocr" {
			service = "ocr-worker"
		}
		input["worker_service"] = service
		encoded, _ := json.Marshal(input)
		body = string(encoded)
	} else {
		taskID, _ := input["task_id"].(string)
		if taskID == "" {
			taskID, _ = input["runtime_task_id"].(string)
		}
		parts := strings.Split(strings.Trim(path, "/"), "/")
		sourceID := parts[len(parts)-2]
		session.mu.Lock()
		var task map[string]any
		for id, candidate := range session.tasks {
			if id == taskID || taskID == "" && candidate["source_id"] == sourceID {
				task = candidate
				break
			}
		}
		session.mu.Unlock()
		if task == nil {
			t.Fatalf("no claimed service lease for %s", path)
		}
		for header, key := range map[string]string{workerruntime.TaskIDHeader: "id", workerruntime.TaskLeaseHeader: "lease_token", workerruntime.WorkerServiceHeader: "worker_service", workerruntime.WorkerInstanceIDHeader: "worker_instance_id"} {
			headers[header], _ = task[key].(string)
		}
	}
	result := e2ePostJSON(t, router, method, path, session.token, body, want, headers)
	if path == "/api/v1/internal/worker/tasks/claim" {
		if tasks, ok := result["tasks"].([]any); ok {
			session.mu.Lock()
			for _, value := range tasks {
				task := value.(map[string]any)
				session.tasks[task["id"].(string)] = task
			}
			session.mu.Unlock()
		}
	}
	return result
}

func e2eSeedWorkerAccount(db *sql.DB) error {
	hash, err := auth.HashPassword(e2ePassword)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(context.Background(), `INSERT INTO app_user(tenant_id,username,display_name,password_hash,status)
 SELECT id,$1,'Synthetic processing worker',$2,'active' FROM tenant WHERE code='demo' AND deleted_at IS NULL
 ON CONFLICT(tenant_id,username) DO UPDATE SET password_hash=EXCLUDED.password_hash,status='active',deleted_at=NULL`, auditFixtureWorkerUsername, hash)
	if err != nil {
		return fmt.Errorf("seed worker user: %w", err)
	}
	_, err = db.ExecContext(context.Background(), `INSERT INTO user_role(tenant_id,user_id,role_id,data_scope)
 SELECT u.tenant_id,u.id,r.id,'{"scope":"service"}'::jsonb FROM app_user u JOIN tenant t ON t.id=u.tenant_id JOIN role r ON r.tenant_id=u.tenant_id AND r.code='page_processing_worker'
 WHERE t.code='demo' AND u.username=$1 AND u.deleted_at IS NULL AND r.deleted_at IS NULL
 ON CONFLICT(tenant_id,user_id,role_id) DO UPDATE SET data_scope=EXCLUDED.data_scope,deleted_at=NULL`, auditFixtureWorkerUsername)
	return err
}
