package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"github.com/google/uuid"
)

// 使用隔离数据库验证持久化回执和并发锁；不会归档开发库里的真实考试。
func TestExamArchiveCommandsE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for archive command PostgreSQL acceptance")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := uuid.NewString()
	school := e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", token, fmt.Sprintf(`{"name":"Archive commands","code":"archive-%s"}`, suffix), http.StatusCreated)["school"].(map[string]any)
	schoolID := e2eString(t, school, "id")
	grade := e2ePostJSON(t, router, http.MethodPost, "/api/v1/grades", token, fmt.Sprintf(`{"school_id":"%s","name":"Archive grade","level_no":10,"academic_year":"2026"}`, schoolID), http.StatusCreated)["grade"].(map[string]any)
	gradeID := e2eString(t, grade, "id")
	var tenantID, actor string
	if err := db.QueryRow(`SELECT t.id::text,u.id::text FROM tenant t JOIN app_user u ON u.tenant_id=t.id WHERE t.code='demo' AND u.username='tenant_admin'`).Scan(&tenantID, &actor); err != nil {
		t.Fatal(err)
	}
	scope := auth.AccessScope{TenantID: tenantID, TenantWide: true}
	store := exam.NewPostgresStore(db)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := store.CreateExamSession(ctx, scope, actor, exam.CreateSessionInput{
		SchoolID: schoolID, GradeID: gradeID, Name: "归档回执测试", ExamType: "midterm_exam",
		GradingMode: "ai_assisted", PublishPolicy: "after_admin_approval",
		Subjects: []exam.SessionSubjectInput{{Subject: "math", TotalScore: 100}, {Subject: "english", TotalScore: 100}},
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.GetManagementExam(ctx, scope, "session", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	inputFor := func(item exam.ManagementExam, command string) exam.ManagementArchiveInput {
		revisions := map[string]int64{}
		for _, child := range item.Exams {
			revisions[child.ID] = child.Revision
		}
		return exam.ManagementArchiveInput{ExpectedRevision: item.Revision, ExpectedSubjectRevisions: revisions, CommandID: command, Reason: "整场结束"}
	}
	fixture := archiveCommandFixture{DB: db, Store: store, AccessScope: scope, ActorID: actor, TenantID: tenantID, SchoolID: schoolID, Suffix: suffix, Original: item, InputFor: inputFor}
	restored := e2eAssertArchiveCommandReceipts(t, ctx, fixture)
	current := e2eAssertArchiveChildRevisionSet(t, ctx, fixture, restored)
	projection := e2ePrepareArchiveProjectionClaim(t, ctx, fixture, current)
	// 在归档事务已持有共享业务锁时开始答卷写入；等待归档提交后，触发器必须重新检查而拒绝。
	archiveTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer archiveTx.Rollback()
	e2eHoldArchiveBusinessLocks(t, ctx, archiveTx, fixture, current)
	writerStarted, writerDone := make(chan struct{}), make(chan error, 1)
	writerConn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writerConn.Close()
	var writerPID int
	if err := writerConn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&writerPID); err != nil {
		t.Fatal(err)
	}
	go func() {
		close(writerStarted)
		_, err := writerConn.ExecContext(ctx, `INSERT INTO submission(tenant_id,exam_id,source_type,status,collected_by) VALUES($1,$2::uuid,'manual_import','created',$3::uuid)`, tenantID, current.Exams[0].ID, actor)
		writerDone <- err
	}()
	<-writerStarted
	e2eAwaitArchiveWriterLock(t, ctx, db, writerPID, writerDone)
	if err := archiveTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-writerDone; err == nil || !strings.Contains(err.Error(), "exam_archived") {
		t.Fatalf("write crossed archive boundary after waiting: %v", err)
	}
	e2eAssertArchivedProjectionAcknowledgement(t, ctx, fixture, projection)
}

type archiveCommandFixture struct {
	DB                                  *sql.DB
	Store                               *exam.PostgresStore
	AccessScope                         auth.AccessScope
	ActorID, TenantID, SchoolID, Suffix string
	Original                            exam.ManagementExam
	InputFor                            func(exam.ManagementExam, string) exam.ManagementArchiveInput
}

type archiveProjectionFixture struct {
	ExamID        string
	Store         *processing.PostgresStore
	Snapshot      func() string
	Before, Owner string
	Refresh       processing.ProjectionRefresh
}

// 归档、恢复和迟到重放按原顺序执行；回执不改变当前恢复状态，部分考试范围和其他操作者不能取到整场回执。
func e2eAssertArchiveCommandReceipts(t *testing.T, ctx context.Context, fixture archiveCommandFixture) exam.ManagementExam {
	t.Helper()
	db, store, scope, actor, tenantID, suffix, item, inputFor := fixture.DB, fixture.Store, fixture.AccessScope, fixture.ActorID, fixture.TenantID, fixture.Suffix, fixture.Original, fixture.InputFor
	archiveInput := inputFor(item, "archive-command-"+suffix)
	archived, err := store.ArchiveManagementExam(ctx, scope, actor, "session", item.ID, archiveInput)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.ArchiveManagementExam(ctx, scope, actor, "session", item.ID, archiveInput)
	firstJSON, _ := json.Marshal(archived)
	replayJSON, _ := json.Marshal(replayed)
	if err != nil || string(firstJSON) != string(replayJSON) {
		t.Fatalf("receipt changed on repeat: %v", err)
	}
	changed := archiveInput
	changed.Reason = "不同请求"
	if _, err := store.ArchiveManagementExam(ctx, scope, actor, "session", item.ID, changed); !errors.Is(err, exam.ErrCommandConflict) {
		t.Fatalf("different request reused archive command: %v", err)
	}
	partial := auth.AccessScope{TenantID: tenantID, ExamIDs: []string{item.Exams[0].ID}}
	if _, err := store.RestoreManagementExam(ctx, partial, actor, "session", item.ID, inputFor(archived, "partial-restore-"+suffix)); !errors.Is(err, exam.ErrScopeForbidden) {
		t.Fatalf("partial scope restored whole exam: %v", err)
	}
	if _, err := store.RecoverManagementArchiveCommand(ctx, partial, actor, archiveInput.CommandID); !errors.Is(err, exam.ErrScopeForbidden) {
		t.Fatalf("partial scope recovered full receipt: %v", err)
	}
	restored, err := store.RestoreManagementExam(ctx, scope, actor, "session", item.ID, inputFor(archived, "restore-command-"+suffix))
	if err != nil || restored.ArchivedAt != nil || !restored.CanArchive {
		t.Fatalf("restore: %v", err)
	}
	// 旧归档命令在恢复后重放，仍返回原回执，当前归档事实必须保持已恢复。
	late, err := store.ArchiveManagementExam(ctx, scope, actor, "session", item.ID, archiveInput)
	lateJSON, _ := json.Marshal(late)
	current, _ := store.GetManagementExam(ctx, scope, "session", item.ID)
	if err != nil || string(firstJSON) != string(lateJSON) || current.ArchivedAt != nil {
		t.Fatalf("late replay changed live archive state: %v", err)
	}
	unknown, err := store.RecoverManagementArchiveCommand(ctx, scope, uuid.NewString(), archiveInput.CommandID)
	if err != nil || unknown.Status != "not_accepted" {
		t.Fatalf("receipt leaked across actors: %+v %v", unknown, err)
	}
	var auditCount int
	if err := db.QueryRow(`SELECT count(*) FROM audit_log WHERE tenant_id=$1 AND target_id=$2::uuid AND action='exam.management.archive'`, tenantID, item.ID).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("archive audit must be exactly one record: count=%d err=%v", auditCount, err)
	}
	return restored
}

// 子科修订集合必须完整；新增科目不改变父级版本，也必须让用户确认过的旧子集归档请求冲突。
func e2eAssertArchiveChildRevisionSet(t *testing.T, ctx context.Context, fixture archiveCommandFixture, restored exam.ManagementExam) exam.ManagementExam {
	t.Helper()
	db, store, scope, actor, tenantID, schoolID, suffix, item, inputFor := fixture.DB, fixture.Store, fixture.AccessScope, fixture.ActorID, fixture.TenantID, fixture.SchoolID, fixture.Suffix, fixture.Original, fixture.InputFor
	missing := inputFor(restored, "missing-child-"+suffix)
	delete(missing.ExpectedSubjectRevisions, restored.Exams[0].ID)
	if _, err := store.ArchiveManagementExam(ctx, scope, actor, "session", item.ID, missing); !errors.Is(err, exam.ErrRevisionConflict) {
		t.Fatalf("incomplete subject revision set accepted: %v", err)
	}
	// 新增学科不会改变场次版本；仅比对父级版本会漏掉用户没有确认过的归档对象。
	if _, err := db.Exec(`INSERT INTO exam(tenant_id,school_id,exam_session_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by)
VALUES($1,$2::uuid,$3::uuid,'新学科','chinese','midterm_exam',100,'draft','ai_assisted','after_admin_approval',$4::uuid)`, tenantID, schoolID, item.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ArchiveManagementExam(ctx, scope, actor, "session", item.ID, inputFor(restored, "old-page-child-"+suffix)); !errors.Is(err, exam.ErrRevisionConflict) {
		t.Fatalf("old page omitted newly added subject: %v", err)
	}
	current, err := store.GetManagementExam(ctx, scope, "session", item.ID)
	if err != nil {
		t.Fatal(err)
	}
	return current
}

// 先生成有开放异常的投影并领取原请求版本；冻结快照在归档和确认领取之后再次比较。
func e2ePrepareArchiveProjectionClaim(t *testing.T, ctx context.Context, fixture archiveCommandFixture, current exam.ManagementExam) archiveProjectionFixture {
	t.Helper()
	db, actor, tenantID, suffix := fixture.DB, fixture.ActorID, fixture.TenantID, fixture.Suffix
	// 先生成一条有未结异常的投影，再模拟归档发生于已领取的投影任务之前。
	// 手动设置归档仅用于验证数据库边界；真实归档 API 会拒绝这些尚未结束的业务记录。
	projectionExamID := current.Exams[0].ID
	var submissionID, assetID, pageID string
	if err := db.QueryRowContext(ctx, `INSERT INTO submission(tenant_id,exam_id,source_type,status,collected_by)
VALUES($1,$2::uuid,'manual_import','created',$3::uuid) RETURNING id::text`, tenantID, projectionExamID, actor).Scan(&submissionID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO file_asset(tenant_id,exam_id,submission_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by)
VALUES($1,$2::uuid,$3::uuid,'submission',$3::uuid,'projection.png','image/png',1,$4,'e2e',$5,'private',$6::uuid) RETURNING id::text`, tenantID, projectionExamID, submissionID, strings.Repeat("0", 64), suffix, actor).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO submission_page(tenant_id,submission_id,file_asset_id,page_no,status)
VALUES($1,$2::uuid,$3::uuid,1,'uploaded') RETURNING id::text`, tenantID, submissionID, assetID).Scan(&pageID); err != nil {
		t.Fatal(err)
	}
	projectionStore := processing.NewPostgresStore(db)
	if err := projectionStore.RefreshExam(ctx, tenantID, projectionExamID); err != nil {
		t.Fatal(err)
	}
	projectionSnapshot := func() string {
		t.Helper()
		var snapshot string
		if err := db.QueryRowContext(ctx, `SELECT jsonb_build_object(
'states',(SELECT jsonb_agg(to_jsonb(p) ORDER BY page_id) FROM submission_page_processing_state p WHERE tenant_id=$1 AND exam_id=$2::uuid),
'exceptions',(SELECT jsonb_agg(to_jsonb(e) ORDER BY id) FROM operational_exception e WHERE tenant_id=$1 AND exam_id=$2::uuid))::text`, tenantID, projectionExamID).Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	beforeProjection := projectionSnapshot()
	var openCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM operational_exception WHERE tenant_id=$1 AND exam_id=$2::uuid AND status='open'`, tenantID, projectionExamID).Scan(&openCount); err != nil || openCount != 1 {
		t.Fatalf("missing unresolved projection fixture: count=%d err=%v", openCount, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE processing_projection_cursor SET available_at='epoch'::timestamptz WHERE tenant_id=$1 AND exam_id=$2::uuid`, tenantID, projectionExamID); err != nil {
		t.Fatal(err)
	}
	projectionOwner := "archive-projection-" + suffix
	refresh, claimed, err := projectionStore.ClaimProjection(ctx, projectionOwner, time.Minute)
	if err != nil || !claimed || refresh.ExamID != projectionExamID {
		t.Fatalf("projection claim: %+v claimed=%v err=%v", refresh, claimed, err)
	}
	return archiveProjectionFixture{ExamID: projectionExamID, Store: projectionStore, Snapshot: projectionSnapshot, Before: beforeProjection, Owner: projectionOwner, Refresh: refresh}
}

// 父场归档前先持有所有子科共享业务锁；事务创建、提交和 defer 回滚仍由主测试持有，不能提前释放。
func e2eHoldArchiveBusinessLocks(t *testing.T, ctx context.Context, archiveTx *sql.Tx, fixture archiveCommandFixture, current exam.ManagementExam) {
	t.Helper()
	actor, tenantID, item := fixture.ActorID, fixture.TenantID, fixture.Original
	for _, child := range current.Exams {
		if _, err := archiveTx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))`, tenantID, child.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := archiveTx.ExecContext(ctx, `UPDATE exam_session SET archived_at=now(),archived_by=$2::uuid,revision=revision+1 WHERE id=$1::uuid`, item.ID, actor); err != nil {
		t.Fatal(err)
	}
}

// 原写入连接仍由主测试持有；先观察数据库确实等待锁，才允许主测试提交归档事务。
func e2eAwaitArchiveWriterLock(t *testing.T, ctx context.Context, db *sql.DB, writerPID int, writerDone <-chan error) {
	t.Helper()
	waitDeadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, writerPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-writerDone:
			t.Fatalf("writer should wait for archive business lock, returned %v", err)
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(waitDeadline) {
			t.Fatal("writer did not reach the archive lock")
		}
	}
}

// 归档后的投影确认只释放本次领取并保留旧异常快照，后来请求的版本仍不能被吞掉。
func e2eAssertArchivedProjectionAcknowledgement(t *testing.T, ctx context.Context, fixture archiveCommandFixture, projection archiveProjectionFixture) {
	t.Helper()
	db, tenantID := fixture.DB, fixture.TenantID
	projectionStore, projectionExamID, projectionOwner, refresh, projectionSnapshot, beforeProjection := projection.Store, projection.ExamID, projection.Owner, projection.Refresh, projection.Snapshot, projection.Before
	if err := projectionStore.RefreshExam(ctx, tenantID, projectionExamID); err != nil {
		t.Fatalf("archived projection refresh should preserve records: %v", err)
	}
	if _, err := db.ExecContext(ctx, `SELECT request_processing_projection_refresh($1::uuid,$2::uuid)`, tenantID, projectionExamID); err != nil {
		t.Fatal(err)
	}
	if err := projectionStore.ApplyProjection(ctx, projectionOwner, refresh); err != nil {
		t.Fatalf("archived projection should acknowledge exact claim without rewriting exceptions: %v", err)
	}
	if after := projectionSnapshot(); after != beforeProjection {
		t.Fatalf("archived projection modified frozen states or exceptions: before=%s after=%s", beforeProjection, after)
	}
	var projectedVersion, requestedVersion int64
	var leaseReleased bool
	if err := db.QueryRowContext(ctx, `SELECT projected_version,requested_version,lease_owner IS NULL AND lease_expires_at IS NULL FROM processing_projection_cursor WHERE tenant_id=$1 AND exam_id=$2::uuid`, tenantID, projectionExamID).Scan(&projectedVersion, &requestedVersion, &leaseReleased); err != nil {
		t.Fatal(err)
	}
	if projectedVersion != refresh.RequestedVersion || requestedVersion <= projectedVersion || !leaseReleased {
		t.Fatalf("projection swallowed later source version or kept lease: projected=%d requested=%d released=%v", projectedVersion, requestedVersion, leaseReleased)
	}

}
