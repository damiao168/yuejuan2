package server

import (
	"bytes"
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/report"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"encoding/csv"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

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
