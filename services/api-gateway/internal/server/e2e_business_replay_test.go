package server

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/idempotency"
	"edugrade-enterprise/services/api-gateway/internal/report"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type blockAfterBeginStore struct {
	idempotency.Store
}

func (s *blockAfterBeginStore) Begin(ctx context.Context, input idempotency.BeginInput) (idempotency.Record, bool, error) {
	record, execute, err := s.Store.Begin(ctx, input)
	if err == nil && execute {
		_, _ = fmt.Fprintln(os.Stdout, reservationCrashMarker)
		time.Sleep(time.Hour)
	}
	return record, execute, err
}

// TestBusinessCommandReservationCrashHelper 只作为子进程运行。
// 它先通知传输预留已提交，再有意停留在 Begin 内，让父进程能在 handler 启动前杀死该进程。
func TestBusinessCommandReservationCrashHelper(t *testing.T) {
	if os.Getenv("EDUGRADE_E2E_CRASH_HELPER") != "1" {
		t.Skip("child-process crash helper")
	}
	dbConfig, err := pgx.ParseConfig(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	dbConfig.Database = os.Getenv("EDUGRADE_E2E_CRASH_DATABASE")
	db := stdlib.OpenDB(*dbConfig)
	defer db.Close()
	handler := report.NewHandler(report.NewPostgresStore(db), nil)
	transport := &blockAfterBeginStore{Store: idempotency.NewPostgresStore(db)}
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/exams/{examId}/reports/export", idempotency.Middleware(transport, idempotency.Options{Enforce: true})(http.HandlerFunc(handler.Export)))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+os.Getenv("EDUGRADE_E2E_CRASH_EXAM")+"/reports/export", nil)
	req.Header.Set(idempotency.Header, os.Getenv("EDUGRADE_E2E_CRASH_COMMAND"))
	req = req.WithContext(auth.WithUser(context.Background(), auth.User{
		ID:       os.Getenv("EDUGRADE_E2E_CRASH_ACTOR"),
		TenantID: os.Getenv("EDUGRADE_E2E_CRASH_TENANT"),
	}))
	mux.ServeHTTP(httptest.NewRecorder(), req)
	t.Fatal("crash helper unexpectedly returned")
}

// 通过实际认证 handler 调用两次，第二次已越过目标的初始状态。
// 核心 PostgreSQL 夹具在原始业务转换时调用此处。
func e2eBusinessCommandReplay(t *testing.T, router http.Handler, path, token, body, domain string, want int) map[string]any {
	t.Helper()
	command := uuid.NewString()
	send := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", command)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	first := send(body)
	if first.Code != want {
		t.Fatalf("command first %s: %d %s", path, first.Code, first.Body)
	}
	second := send(body)
	if second.Code != want || !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatalf("terminal replay %s: %d %s", path, second.Code, second.Body)
	}
	receipt := e2eGetJSON(t, router, "/api/v1/"+domain+"-commands/"+command, token, http.StatusOK)
	if receipt["status"] != "succeeded" || receipt["command_id"] != command {
		t.Fatalf("receipt %s: %+v", path, receipt)
	}
	var changed map[string]any
	if err := json.Unmarshal([]byte(body), &changed); err != nil {
		t.Fatal(err)
	}
	changed["reason"] = "different command input"
	raw, _ := json.Marshal(changed)
	conflict := send(string(raw))
	if conflict.Code != 409 {
		t.Fatalf("changed request %s: %d %s", path, conflict.Code, conflict.Body)
	}
	var result map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	t.Logf("business command replay/conflict/recovery passed: %s command=%s", path, command)
	return result
}

// 完成唯一任务后，阅卷员的分派作用域会消失；已提交结果通过本人回执恢复，不重新授予题目写权限。
func e2eReviewCommandRecoveryAfterAssignmentEnds(t *testing.T, router http.Handler, path, token, body string) {
	t.Helper()
	command := uuid.NewString()
	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", command)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	first := send()
	if first.Code != http.StatusCreated {
		t.Fatalf("first review command: %d %s", first.Code, first.Body)
	}
	if replay := send(); replay.Code != http.StatusForbidden || !strings.Contains(replay.Body.String(), "access_scope_forbidden") {
		t.Fatalf("completed assignment must not retain write scope: %d %s", replay.Code, replay.Body)
	}
	receipt := e2eGetJSON(t, router, "/api/v1/review-commands/"+command, token, http.StatusOK)
	if receipt["status"] != "succeeded" || receipt["command_id"] != command {
		t.Fatalf("completed review receipt: %+v", receipt)
	}
	var response map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	recovered := receipt["result"].(map[string]any)
	want, _ := json.Marshal(response["human_grade"])
	got, _ := json.Marshal(recovered["human_grade"])
	if !bytes.Equal(got, want) {
		t.Fatalf("recovered grade changed: want=%s got=%s", want, got)
	}
}
