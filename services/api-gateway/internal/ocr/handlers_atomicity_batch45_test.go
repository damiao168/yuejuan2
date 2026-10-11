package ocr_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type unpairedOCRSourceStore struct{ *ocrpkg.MemoryStore }

func TestCreateOCRTaskRejectsUnpairedRuntimeWithoutWrites(t *testing.T) {
	ctx := context.Background()
	authStore := authStoreWithPermissions(t, []string{"ocr:manage"})
	submissionStore := submission.NewMemoryStore()
	page := readySubmission(t, submissionStore)
	// 包装来源存储使其不属于协调器支持的配对，底层存储仍可供断言检查是否被降级写入。
	source := &unpairedOCRSourceStore{MemoryStore: ocrpkg.NewMemoryStore()}
	runtimeStore := workerruntime.NewMemoryStore()
	queue := ocrpkg.NewMemoryQueue()
	router := testRouter(authStore, submissionStore, source, queue, runtimeStore)
	token := login(t, router)

	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+page.SubmissionID+"/ocr-tasks", bytes.NewBufferString(validTaskJSON()), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `"code":"ocr_runtime_atomicity_unavailable"`) {
		t.Fatalf("expected atomic coordination rejection, got %d %s", rec.Code, rec.Body.String())
	}
	tasks, err := source.ListBySubmission(ctx, tenantID, page.SubmissionID, ocrpkg.TaskListFilter{})
	if err != nil || len(tasks) != 0 {
		t.Fatalf("source tasks changed after rejection: %v %#v", err, tasks)
	}
	metrics, err := runtimeStore.Metrics(ctx, tenantID)
	if err != nil || len(metrics.Queues) != 0 {
		t.Fatalf("runtime tasks changed after rejection: %v %#v", err, metrics)
	}
	if len(queue.Tasks()) != 0 {
		t.Fatalf("unpaired runtime fell back to legacy queue: %#v", queue.Tasks())
	}
}
