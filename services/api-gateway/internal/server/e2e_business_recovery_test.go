package server

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/idempotency"
	"edugrade-enterprise/services/api-gateway/internal/report"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func e2eInterruptedBusinessCommandRecovery(t *testing.T, db *sql.DB, tenant, exam, actor string) {
	t.Helper()
	ctx := context.Background()
	commandID := "interrupted-export-" + uuid.NewString()
	var before int
	if err := db.QueryRow(`SELECT count(*) FROM report WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND generated_by=$3::uuid AND report_type='export'`, tenant, exam, actor).Scan(&before); err != nil {
		t.Fatal(err)
	}
	e2eKillProcessAfterTransportReservation(t, db, tenant, exam, actor, commandID)
	handler := report.NewHandler(report.NewPostgresStore(db), nil)
	transport := idempotency.NewPostgresStore(db)
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/exams/{examId}/reports/export", idempotency.Middleware(transport, idempotency.Options{Enforce: true})(http.HandlerFunc(handler.Export)))
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+exam+"/reports/export", nil)
		req.Header.Set(idempotency.Header, commandID)
		req = req.WithContext(auth.WithUser(ctx, auth.User{ID: actor, TenantID: tenant}))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	live, err := commandreceipt.Recover(ctx, db, tenant, actor, commandID, "report.")
	if err != nil || live.Status != "processing" {
		var route, state, storedActor string
		lookupErr := db.QueryRow(`SELECT route,state,actor_id::text FROM idempotency_record WHERE tenant_id=$1::uuid AND idempotency_key=$2`, tenant, commandID).Scan(&route, &state, &storedActor)
		t.Fatalf("live interrupted command=%#v err=%v transport=%s/%s actor=%s lookup=%v", live, err, route, state, storedActor, lookupErr)
	}
	if _, err := db.Exec(`UPDATE idempotency_record SET updated_at=now()-interval '3 minutes',expires_at=now()-interval '1 minute'
WHERE tenant_id=$1::uuid AND actor_id=$2::uuid AND method='POST' AND route='POST /api/v1/exams/{examId}/reports/export' AND idempotency_key=$3`, tenant, actor, commandID); err != nil {
		t.Fatal(err)
	}
	ready, err := commandreceipt.Recover(ctx, db, tenant, actor, commandID, "report.")
	if err != nil || ready.Status != "takeover_ready" {
		t.Fatalf("abandoned command not resumable=%#v err=%v", ready, err)
	}
	e2eAssertConcurrentReportTakeover(t, request)
	var after int
	if err := db.QueryRow(`SELECT count(*) FROM report WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND generated_by=$3::uuid AND report_type='export'`, tenant, exam, actor).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Fatalf("interrupted command created %d reports, want one", after-before)
	}
	receipt, err := commandreceipt.Recover(ctx, db, tenant, actor, commandID, "report.")
	if err != nil || receipt.Status != "succeeded" {
		t.Fatalf("resumed business receipt=%#v err=%v", receipt, err)
	}
}

// 同一过期命令同时接管，至少一份完成；其余只允许 operation_in_progress，之后重放必须成功。
func e2eAssertConcurrentReportTakeover(t *testing.T, request func() *httptest.ResponseRecorder) {
	t.Helper()
	responses := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			responses <- request()
		}()
	}
	close(start)
	succeeded := 0
	for range 2 {
		response := <-responses
		switch response.Code {
		case http.StatusOK:
			succeeded++
		case http.StatusConflict:
			if !strings.Contains(response.Body.String(), "operation_in_progress") {
				t.Fatalf("concurrent resume conflict=%s", response.Body.String())
			}
		default:
			t.Fatalf("concurrent resume response=%d %s", response.Code, response.Body.String())
		}
	}
	if succeeded == 0 {
		t.Fatal("no concurrent resume completed the interrupted command")
	}
	replay := request()
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("resumed command did not replay: %d %s", replay.Code, replay.Body.String())
	}
}

func e2eKillProcessAfterTransportReservation(t *testing.T, db *sql.DB, tenant, exam, actor, commandID string) {
	t.Helper()
	var databaseName string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatalf("read isolated crash-test database name: %v", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestBusinessCommandReservationCrashHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"EDUGRADE_E2E_CRASH_HELPER=1",
		"EDUGRADE_E2E_CRASH_DATABASE="+databaseName,
		"EDUGRADE_E2E_CRASH_TENANT="+tenant,
		"EDUGRADE_E2E_CRASH_EXAM="+exam,
		"EDUGRADE_E2E_CRASH_ACTOR="+actor,
		"EDUGRADE_E2E_CRASH_COMMAND="+commandID,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("open crash helper stdout: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start crash helper: %v", err)
	}
	type markerResult struct {
		line string
		err  error
	}
	marker := make(chan markerResult, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == reservationCrashMarker {
				marker <- markerResult{line: reservationCrashMarker}
				return
			}
		}
		marker <- markerResult{err: scanner.Err()}
	}()
	select {
	case found := <-marker:
		if found.line != reservationCrashMarker {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("crash helper exited before reservation marker: %v stderr=%s", found.err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("timed out waiting for committed reservation; stderr=%s", stderr.String())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill crash helper after reservation commit: %v", err)
	}
	_ = cmd.Wait()
}
