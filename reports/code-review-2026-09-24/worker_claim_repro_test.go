package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
)

func TestWorkerRuntimeCrossQueueClaimRepro(t *testing.T) {
	dsn := "postgres://review_admin@127.0.0.1:55439/postgres?sslmode=disable"
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	ctx := context.Background()

	var platformTenantID, schoolTenantID string
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM tenant WHERE code='platform' AND deleted_at IS NULL`).Scan(&platformTenantID); err != nil {
		t.Fatalf("lookup platform tenant: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM tenant WHERE code='demo' AND deleted_at IS NULL`).Scan(&schoolTenantID); err != nil {
		t.Fatalf("lookup demo tenant: %v", err)
	}

	const workerUsername = "repro_page_processing_worker"
	const workerPassword = "ReproOnly-Worker-Password-2026!"
	passwordHash, err := auth.HashPassword(workerPassword)
	if err != nil {
		t.Fatalf("hash synthetic worker credential: %v", err)
	}
	var workerID string
	if err := db.QueryRowContext(ctx, `
INSERT INTO app_user (tenant_id, username, display_name, password_hash, status)
VALUES ($1::uuid, $2, 'Isolated Worker Claim Repro', $3, 'active')
RETURNING id::text`, platformTenantID, workerUsername, passwordHash).Scan(&workerID); err != nil {
		t.Fatalf("insert synthetic isolated worker identity: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO user_role (tenant_id, user_id, role_id, data_scope)
SELECT $1::uuid, $2::uuid, r.id, jsonb_build_object('scope', 'service')
FROM role r
WHERE r.tenant_id=$1::uuid AND r.code='page_processing_worker' AND r.deleted_at IS NULL`, platformTenantID, workerID); err != nil {
		t.Fatalf("assign page_processing_worker role: %v", err)
	}
	var assignedRoles int
	if err := db.QueryRowContext(ctx, `
SELECT count(*) FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id
WHERE ur.tenant_id=$1::uuid AND ur.user_id=$2::uuid AND ur.deleted_at IS NULL
  AND r.code='page_processing_worker' AND r.deleted_at IS NULL`, platformTenantID, workerID).Scan(&assignedRoles); err != nil || assignedRoles != 1 {
		t.Fatalf("verify synthetic worker role assignment: count=%d err=%v", assignedRoles, err)
	}

	router := e2ePostgresRouter(db)
	workerToken := workerReproLogin(t, router, workerUsername, workerPassword)

	segmentID := uuid.NewString()
	runtimeStore := workerruntime.NewPostgresStore(db)
	created, err := runtimeStore.CreateTask(ctx, schoolTenantID, "", workerruntime.CreateTaskInput{
		TaskType: "ai_grade", QueueName: "subjective-grading", SourceType: "subjective_grading_run",
		SourceID: uuid.NewString(), PayloadSchemaVersion: "subjective_grading.v1",
		IdempotencyKey: "worker-cross-queue-repro-" + uuid.NewString(),
		Payload: map[string]any{"answer_segment_id": segmentID},
	})
	if err != nil {
		t.Fatalf("create synthetic subjective-grading task: %v", err)
	}

	response := e2ePostJSON(t, router, http.MethodPost, "/api/v1/internal/worker/tasks/claim", workerToken,
		`{"queue_name":"subjective-grading","worker_service":"subjective-grading-worker","worker_instance_id":"repro-instance","limit":1,"lease_seconds":300}`, http.StatusOK)
	rawTasks, ok := response["tasks"].([]any)
	if !ok || len(rawTasks) != 1 {
		t.Fatalf("expected one cross-tenant subjective-grading task claim; returned task count=%d", len(rawTasks))
	}
	claimed, ok := rawTasks[0].(map[string]any)
	if !ok {
		t.Fatalf("claim response task has unexpected JSON shape: %T", rawTasks[0])
	}
	if claimed["id"] != created.ID || claimed["tenant_id"] != schoolTenantID || claimed["queue_name"] != "subjective-grading" || claimed["worker_service"] != "subjective-grading-worker" || claimed["status"] != workerruntime.StatusLeased {
		t.Fatalf("unexpected task lease: id=%v tenant=%v queue=%v worker_service=%v status=%v", claimed["id"], claimed["tenant_id"], claimed["queue_name"], claimed["worker_service"], claimed["status"])
	}
	if token, ok := claimed["lease_token"].(string); !ok || token == "" {
		t.Fatal("claimed task did not include a lease token")
	}

	var persistedService, persistedInstance, persistedStatus string
	if err := db.QueryRowContext(ctx, `SELECT worker_service, worker_instance_id, status FROM agent_worker_task WHERE tenant_id=$1::uuid AND id=$2::uuid`, schoolTenantID, created.ID).Scan(&persistedService, &persistedInstance, &persistedStatus); err != nil {
		t.Fatalf("read persisted isolated task lease: %v", err)
	}
	if persistedService != "subjective-grading-worker" || persistedInstance != "repro-instance" || persistedStatus != workerruntime.StatusLeased {
		t.Fatalf("unexpected persisted lease: worker_service=%q instance=%q status=%q", persistedService, persistedInstance, persistedStatus)
	}
}

func workerReproLogin(t *testing.T, router http.Handler, username string, password string) string {
	t.Helper()
	body := `{"tenant_code":"platform","username":"` + username + `","password":"` + password + `","client_type":"service"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", strings.NewReader(body))
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("service login expected 200, got %d %s", res.Code, res.Body.String())
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode synthetic service login response: %v", err)
	}
	if response.AccessToken == "" {
		t.Fatal("service login response missing access token")
	}
	return response.AccessToken
}
