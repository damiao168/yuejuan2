package server

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/idempotency"
	"edugrade-enterprise/services/api-gateway/internal/report"
	"edugrade-enterprise/services/api-gateway/internal/review"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const reservationCrashMarker = "EDUGRADE_E2E_RESERVATION_COMMITTED"

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

// 仲裁夹具把同一场双评的租户、考试、答题片段与角色身份一起传入；每个角色仍保持独立。
type arbitrationCommandFixture struct {
	Tenant, Exam, Segment, FirstActor, SecondActor, Arbitrator string
}

func e2eArbitrationAndExportCommands(t *testing.T, db *sql.DB, fixture arbitrationCommandFixture) {
	t.Helper()
	tenant, exam, arbitrator := fixture.Tenant, fixture.Exam, fixture.Arbitrator
	e2eArbitrationReceiptAfterDeletion(t, db, fixture)
	e2eConcurrentExportReceipts(t, db, fixture)
	e2eReportExportUsesSingleSnapshot(t, db, tenant, exam, arbitrator)
	e2eReportExportRollsBackWithoutReceipt(t, db, tenant, exam, arbitrator)
	e2eBusinessCommandRecoveryStates(t, db, tenant, arbitrator)
	e2eInterruptedBusinessCommandRecovery(t, db, tenant, exam, arbitrator)
	t.Log("arbitration replay/conflict plus report concurrency, snapshot, rollback, and crash recovery passed")
}

// 双评产生仲裁后先提交，再删除任务；同一命令仍须重放原成绩并拒绝改分。
func e2eArbitrationReceiptAfterDeletion(t *testing.T, db *sql.DB, fixture arbitrationCommandFixture) {
	t.Helper()
	tenant, exam, segment, firstActor, secondActor, arbitrator := fixture.Tenant, fixture.Exam, fixture.Segment, fixture.FirstActor, fixture.SecondActor, fixture.Arbitrator
	ctx := context.Background()
	store := review.NewPostgresStore(db)
	if _, err := store.SetExamDoubleMarkPolicy(ctx, tenant, exam, arbitrator, review.SetDoubleMarkPolicyInput{Enabled: true, Threshold: 1, ResolutionStrategy: "average"}); err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateDoubleMarkSession(ctx, tenant, arbitrator, review.CreateDoubleMarkSessionInput{AnswerSegmentID: segment, FirstReviewerID: firstActor, SecondReviewerID: secondActor})
	if err != nil {
		t.Fatal(err)
	}
	for index, actor := range []string{firstActor, secondActor} {
		taskID := session.FirstReviewTaskID
		score := 5.0
		if index == 1 {
			taskID = session.SecondReviewTaskID
			score = 2
		}
		if _, err = store.SubmitGrade(ctx, tenant, taskID, actor, review.SubmitGradeInput{ExpectedRevision: 1, Score: score, RubricSelections: []review.RubricSelection{{PointID: "p1", Score: score}}}); err != nil {
			t.Fatal(err)
		}
	}
	var arbitrationID string
	if err = db.QueryRow(`SELECT id::text FROM arbitration_task WHERE tenant_id=$1::uuid AND double_mark_session_id=$2::uuid`, tenant, session.ID).Scan(&arbitrationID); err != nil {
		t.Fatal(err)
	}
	task, err := store.AssignArbitrationTask(ctx, tenant, arbitrationID, arbitrator, review.AssignArbitrationTaskInput{AssignedTo: arbitrator, ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	commandCtx := commandreceipt.WithID(ctx, uuid.NewString())
	input := review.SubmitArbitrationInput{ExpectedRevision: task.Revision, FinalScore: 4, Reason: "original arbitration"}
	_, grade, err := store.SubmitArbitration(commandCtx, tenant, task.ID, arbitrator, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE arbitration_task SET deleted_at=now() WHERE id=$1::uuid`, task.ID); err != nil {
		t.Fatal(err)
	}
	_, replay, err := review.NewPostgresStore(db).SubmitArbitration(commandCtx, tenant, task.ID, arbitrator, input)
	if err != nil || replay.ID != grade.ID {
		t.Fatalf("deleted arbitration replay: %+v %v", replay, err)
	}
	input.FinalScore = 3
	if _, _, err = store.SubmitArbitration(commandCtx, tenant, task.ID, arbitrator, input); !errors.Is(err, commandreceipt.ErrConflict) {
		t.Fatalf("changed arbitration: %v", err)
	}
}

// 并发共用命令键；收齐两份响应并恢复连接上限后，才验证删除后的回执重放。
func e2eConcurrentExportReceipts(t *testing.T, db *sql.DB, fixture arbitrationCommandFixture) {
	t.Helper()
	tenant, exam, arbitrator := fixture.Tenant, fixture.Exam, fixture.Arbitrator
	ctx := context.Background()
	exports := report.NewPostgresStore(db)
	concurrentCtx, cancelConcurrent := context.WithTimeout(ctx, 10*time.Second)
	exportCommandID := uuid.NewString()
	type outcome struct {
		result report.ExportResult
		err    error
	}
	gate := make(chan struct{})
	results := make(chan outcome, 2)
	db.SetMaxOpenConns(2)
	for i := 0; i < 2; i++ {
		go func() {
			<-gate
			result, err := exports.Export(commandreceipt.WithID(concurrentCtx, exportCommandID), tenant, exam, arbitrator)
			results <- outcome{result, err}
		}()
	}
	close(gate)
	a, b := <-results, <-results
	db.SetMaxOpenConns(5)
	cancelConcurrent()
	if a.err != nil || b.err != nil || a.result.ReportID != b.result.ReportID || !bytes.Equal(a.result.Content, b.result.Content) {
		t.Fatalf("export concurrency: a.report=%s a.err=%T %v b.report=%s b.err=%T %v", a.result.ReportID, a.err, a.err, b.result.ReportID, b.err, b.err)
	}
	if _, err := db.Exec(`UPDATE report SET deleted_at=now() WHERE id=$1::uuid`, a.result.ReportID); err != nil {
		t.Fatal(err)
	}
	recovered, err := report.NewPostgresStore(db).Export(commandreceipt.WithID(ctx, exportCommandID), tenant, exam, arbitrator)
	if err != nil || !bytes.Equal(recovered.Content, a.result.Content) {
		t.Fatalf("deleted artifact replay: %v", err)
	}
	next, err := exports.Export(commandreceipt.WithID(ctx, uuid.NewString()), tenant, exam, arbitrator)
	if err != nil || next.ReportID == a.result.ReportID {
		t.Fatalf("new export command: %v", err)
	}
	func() {
		db.SetMaxOpenConns(1)
		defer db.SetMaxOpenConns(5)
		singleCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, err := exports.Export(commandreceipt.WithID(singleCtx, uuid.NewString()), tenant, exam, arbitrator); err != nil {
			t.Fatalf("single-connection export: %v (wait count=%d)", err, db.Stats().WaitCount)
		}
	}()
}

func e2eReportExportUsesSingleSnapshot(t *testing.T, db *sql.DB, tenant, exam, actor string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var gradeID string
	var originalScore, maxScore, averageBefore float64
	if err := db.QueryRowContext(ctx, `SELECT id::text,total_score::float8,max_score::float8
FROM submission_grade
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND status='published' AND locked=true AND deleted_at IS NULL
ORDER BY id LIMIT 1`, tenant, exam).Scan(&gradeID, &originalScore, &maxScore); err != nil {
		t.Fatalf("select published grade for snapshot test: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT AVG(total_score)::float8 FROM submission_grade
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND status='published' AND locked=true AND deleted_at IS NULL`, tenant, exam).Scan(&averageBefore); err != nil {
		t.Fatalf("read pre-mutation average: %v", err)
	}
	mutatedScore := 0.0
	if originalScore == 0 {
		mutatedScore = maxScore
	}
	if mutatedScore == originalScore {
		t.Fatalf("snapshot fixture needs a mutable non-zero max score, score=%.2f max=%.2f", originalScore, maxScore)
	}

	locker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Rollback()
	if _, err = locker.ExecContext(ctx, `LOCK TABLE submission_grade IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock report source: %v", err)
	}
	if _, err = locker.ExecContext(ctx, `UPDATE submission_grade SET total_score=$1,updated_at=now() WHERE id=$2::uuid`, mutatedScore, gradeID); err != nil {
		t.Fatalf("stage report source mutation: %v", err)
	}

	type exportOutcome struct {
		result report.ExportResult
		err    error
	}
	resultCh := make(chan exportOutcome, 1)
	go func() {
		result, exportErr := report.NewPostgresStore(db).Export(commandreceipt.WithID(ctx, uuid.NewString()), tenant, exam, actor)
		resultCh <- exportOutcome{result: result, err: exportErr}
	}()

	waiting := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_locks
			WHERE relation='submission_grade'::regclass AND mode='AccessShareLock' AND NOT granted
		)`).Scan(&waiting); err != nil {
			t.Fatalf("inspect blocked export: %v", err)
		}
		if waiting {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("export did not reach the locked report source after establishing its transaction snapshot")
	}
	if err := locker.Commit(); err != nil {
		t.Fatalf("commit concurrent report mutation: %v", err)
	}

	var exported exportOutcome
	select {
	case exported = <-resultCh:
	case <-ctx.Done():
		t.Fatalf("snapshot export did not finish: %v", ctx.Err())
	}
	if exported.err != nil {
		t.Fatalf("snapshot export: %v", exported.err)
	}
	var averageAfter float64
	if err := db.QueryRowContext(ctx, `SELECT AVG(total_score)::float8 FROM submission_grade
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND status='published' AND locked=true AND deleted_at IS NULL`, tenant, exam).Scan(&averageAfter); err != nil {
		t.Fatalf("read post-mutation average: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE submission_grade SET total_score=$1,updated_at=now() WHERE id=$2::uuid`, originalScore, gradeID); err != nil {
		t.Fatalf("restore report source after snapshot test: %v", err)
	}
	if fmt.Sprintf("%.2f", averageBefore) == fmt.Sprintf("%.2f", averageAfter) {
		t.Fatalf("snapshot mutation did not change the observable average: before=%.2f after=%.2f", averageBefore, averageAfter)
	}
	average := e2eExportCSVValue(t, exported.result.Content, "overview", "average")
	if average != fmt.Sprintf("%.2f", averageBefore) {
		t.Fatalf("export mixed snapshots: average=%s want pre-commit %.2f (post-commit %.2f)", average, averageBefore, averageAfter)
	}
}

func e2eExportCSVValue(t *testing.T, content []byte, section, key string) string {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(content)).ReadAll()
	if err != nil {
		t.Fatalf("parse report export CSV: %v", err)
	}
	for _, row := range rows {
		if len(row) >= 3 && row[0] == section && row[1] == key {
			return row[2]
		}
	}
	t.Fatalf("report export CSV does not contain %s/%s", section, key)
	return ""
}

func e2eReportExportRollsBackWithoutReceipt(t *testing.T, db *sql.DB, tenant, exam, actor string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	commandID := "rollback-export-" + uuid.NewString()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := "e2e_fail_receipt_" + suffix
	triggerName := "e2e_fail_receipt_" + suffix

	if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	RAISE EXCEPTION 'forced report receipt failure';
END
$$`, functionName)); err != nil {
		t.Fatalf("create receipt failure function: %v", err)
	}
	defer func() {
		_, _ = db.Exec(`DROP TRIGGER IF EXISTS ` + triggerName + ` ON business_command_receipt`)
		_, _ = db.Exec(`DROP FUNCTION IF EXISTS ` + functionName + `()`)
	}()
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON business_command_receipt
FOR EACH ROW WHEN (NEW.command_id = '%s') EXECUTE FUNCTION %s()`, triggerName, commandID, functionName)); err != nil {
		t.Fatalf("create receipt failure trigger: %v", err)
	}

	var reportsBefore int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM report
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND generated_by=$3::uuid AND report_type='export'`, tenant, exam, actor).Scan(&reportsBefore); err != nil {
		t.Fatal(err)
	}
	_, err := report.NewPostgresStore(db).Export(commandreceipt.WithID(ctx, commandID), tenant, exam, actor)
	if err == nil || !strings.Contains(err.Error(), "forced report receipt failure") {
		t.Fatalf("export should fail at receipt persistence, got %v", err)
	}
	var reportsAfter, receipts int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM report
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND generated_by=$3::uuid AND report_type='export'`, tenant, exam, actor).Scan(&reportsAfter); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM business_command_receipt
WHERE tenant_id=$1::uuid AND actor_id=$2::uuid AND command_id=$3`, tenant, actor, commandID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if reportsAfter != reportsBefore || receipts != 0 {
		t.Fatalf("failed export was not atomic: reports before=%d after=%d receipts=%d", reportsBefore, reportsAfter, receipts)
	}
}

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

func e2eBusinessCommandRecoveryStates(t *testing.T, db *sql.DB, tenant, actor string) {
	t.Helper()
	insert := func(command, route, state string, status int, body string) {
		t.Helper()
		var responseStatus any
		if status > 0 {
			responseStatus = status
		}
		if _, err := db.Exec(`INSERT INTO idempotency_record
(tenant_id,actor_id,method,route,idempotency_key,request_hash,request_body,state,response_status,response_body,expires_at)
VALUES($1::uuid,$2::uuid,'POST',$3,$4,$5,$6,$7,$8,$9,now()+interval '1 day')`,
			tenant, actor, route, command, strings.Repeat("a", 64), []byte(`{"reason":"server-frozen"}`), state, responseStatus, []byte(body)); err != nil {
			t.Fatalf("seed transport receipt %s: %v", command, err)
		}
	}

	e2eAssertProcessingCommandRecovery(t, db, tenant, actor, insert)
	e2eAssertTerminalCommandRecovery(t, db, tenant, actor, insert)
	t.Log("business command processing/rejected/unknown/domain-isolation recovery passed")
}

// 有效占位仍在处理中；过期占位带回服务器冻结载荷，别的租户或操作者不能接管。
func e2eAssertProcessingCommandRecovery(t *testing.T, db *sql.DB, tenant, actor string, insert func(string, string, string, int, string)) {
	t.Helper()
	processingID := "processing-" + uuid.NewString()
	insert(processingID, "/api/v1/review-tasks/{id}/submit", "processing", 0, "")
	processing, err := commandreceipt.Recover(context.Background(), db, tenant, actor, processingID, "review.")
	if err != nil || processing.Status != "processing" {
		t.Fatalf("processing recovery=%#v err=%v", processing, err)
	}

	staleID := "stale-" + uuid.NewString()
	insert(staleID, "/api/v1/exams/{examId}/publish", "processing", 0, "")
	if _, err := db.Exec(`UPDATE idempotency_record SET updated_at=now()-interval '3 minutes',expires_at=now()-interval '1 minute' WHERE tenant_id=$1::uuid AND actor_id=$2::uuid AND idempotency_key=$3`, tenant, actor, staleID); err != nil {
		t.Fatal(err)
	}
	stale, err := commandreceipt.Recover(context.Background(), db, tenant, actor, staleID, "score.")
	if err != nil || stale.Status != "takeover_ready" || string(stale.Payload) != `{"reason":"server-frozen"}` {
		t.Fatalf("stale recovery=%#v err=%v", stale, err)
	}
	for name, identity := range map[string][2]string{
		"other tenant": {uuid.NewString(), actor},
		"other actor":  {tenant, uuid.NewString()},
	} {
		isolated, err := commandreceipt.Recover(context.Background(), db, identity[0], identity[1], staleID, "score.")
		if err != nil || isolated.Status != "not_accepted" {
			t.Fatalf("%s command scope=%#v err=%v", name, isolated, err)
		}
	}

}

// 已拒绝、缺失业务回执、跨领域与身份歧义分别返回原终态，不能假定传输完成等于业务成功。
func e2eAssertTerminalCommandRecovery(t *testing.T, db *sql.DB, tenant, actor string, insert func(string, string, string, int, string)) {
	t.Helper()
	rejectedID := "rejected-" + uuid.NewString()
	insert(rejectedID, "/api/v1/exams/{examId}/publish", "completed", http.StatusBadRequest, `{"error":{"code":"publish_input_rejected"}}`)
	rejected, err := commandreceipt.Recover(context.Background(), db, tenant, actor, rejectedID, "score.")
	if err != nil || rejected.Status != "rejected" || rejected.HTTPStatus != http.StatusBadRequest || rejected.ErrorCode != "publish_input_rejected" {
		t.Fatalf("rejected recovery=%#v err=%v", rejected, err)
	}

	unknownID := "unknown-" + uuid.NewString()
	insert(unknownID, "/api/v1/exams/{examId}/reports/export", "completed", http.StatusOK, `{}`)
	unknown, err := commandreceipt.Recover(context.Background(), db, tenant, actor, unknownID, "report.")
	if err != nil || unknown.Status != "unknown" || unknown.ErrorCode != "business_receipt_missing" {
		t.Fatalf("unknown recovery=%#v err=%v", unknown, err)
	}

	unrelatedID := "unrelated-" + uuid.NewString()
	insert(unrelatedID, "/api/v1/exams/{examId}/publish", "processing", 0, "")
	unrelated, err := commandreceipt.Recover(context.Background(), db, tenant, actor, unrelatedID, "review.")
	if err != nil || unrelated.Status != "not_accepted" {
		t.Fatalf("cross-domain recovery=%#v err=%v", unrelated, err)
	}

	ambiguousID := "ambiguous-" + uuid.NewString()
	insert(ambiguousID, "/api/v1/review-tasks/{id}/submit", "processing", 0, "")
	insert(ambiguousID, "/api/v1/arbitration-tasks/{id}/submit", "processing", 0, "")
	ambiguous, err := commandreceipt.Recover(context.Background(), db, tenant, actor, ambiguousID, "review.")
	if err != nil || ambiguous.Status != "unknown" || ambiguous.ErrorCode != "ambiguous_command_identity" {
		t.Fatalf("ambiguous recovery=%#v err=%v", ambiguous, err)
	}
}
