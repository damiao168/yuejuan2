package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"github.com/google/uuid"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReliabilityMigrationFrom000122PostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set outside the PostgreSQL CI job")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrationsThrough(t, db, "000122_processing_projection_refresh.sql")
	tenantID, userID, schoolID, examID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1::uuid,$1::uuid,'Migration tenant',$2,'active')`, tenantID, "migration-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO app_user(id,tenant_id,username,display_name,password_hash,status) VALUES($1::uuid,$2::uuid,'migration-admin','Migration admin','not-used','active')`, userID, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'Migration school',$3,'active')`, schoolID, tenantID, "migration-school-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,appeal_enabled,publish_policy,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'Migration exam','math','formal_exam',100,'draft','ai_assisted',true,'after_admin_approval',$4::uuid)`, examID, tenantID, schoolID, userID); err != nil {
		t.Fatal(err)
	}
	reviewImportID, processingImportID := uuid.NewString(), uuid.NewString()
	draft := []byte(`[{"candidate_id":"q1","stem":"人工确认内容","human_confirmed_fields":["stem"]}]`)
	if _, err := db.Exec(`INSERT INTO paper_import_job(id,tenant_id,exam_id,status,subject,draft_questions,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'review_required','math',$4::jsonb,$5::uuid),($6::uuid,$2::uuid,$3::uuid,'processing','math','[]'::jsonb,$5::uuid)`, reviewImportID, tenantID, examID, string(draft), userID, processingImportID); err != nil {
		t.Fatal(err)
	}
	e2eApplyPostgresMigrations(t, db)
	examCommandID, examSession, transportRequestHash := e2eSeedMigrationReplayCommand(t, db, tenantID, userID, schoolID)
	scenario := reliabilityMigrationScenario{DB: db, TenantID: tenantID, UserID: userID, ExamID: examID,
		ReviewImportID: reviewImportID, ProcessingImportID: processingImportID, Draft: draft,
		CommandID: examCommandID, Session: examSession, RequestHash: transportRequestHash}
	e2eAssertUnverifiedMigrationHistory(t, scenario)

	var databaseName string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	databaseURL, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	databaseURL.Path = "/" + databaseName
	repairBinary := filepath.Join(t.TempDir(), "reliability-repair.exe")
	build := exec.Command("go", "build", "-o", repairBinary, "../../cmd/reliability-repair")
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("build reliability repair tool: %v\n%s", buildErr, output)
	}
	runRepair := func(arguments ...string) string {
		t.Helper()
		base := []string{"--database-url", databaseURL.String(), "--tenant-id", tenantID}
		command := exec.Command(repairBinary, append(base, arguments...)...)
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			t.Fatalf("run reliability repair tool: %v\n%s", runErr, output)
		}
		return string(output)
	}
	scenario.RunRepair = runRepair
	e2eRepairOrphanedImportGeneration(t, scenario)
	firstRepairStatus, firstRepairUpdated := e2eRepairLegacyCommandAndReview(t, scenario)
	e2eAssertReliabilityRepairRepeatNoOp(t, scenario, firstRepairStatus, firstRepairUpdated)
}

// 全部迁移完成后才种下可恢复的新业务事实及未完成传输收据，旧历史的验证规则仍独立核对。
func e2eSeedMigrationReplayCommand(t *testing.T, db *sql.DB, tenantID, userID, schoolID string) (string, exam.ExamSession, string) {
	t.Helper()
	gradeID, academicYearID, cohortID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO academic_year(id,tenant_id,school_id,name,start_year,end_year,starts_at,ends_at,is_current,status) VALUES($1::uuid,$2::uuid,$3::uuid,'2026-2027',2026,2027,'2026-09-01','2027-08-31',true,'active')`, academicYearID, tenantID, schoolID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO grade_cohort(id,tenant_id,school_id,education_stage,entry_year,expected_graduation_year,name,status) VALUES($1::uuid,$2::uuid,$3::uuid,'senior',2026,2029,'Migration cohort','active')`, cohortID, tenantID, schoolID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO grade(id,tenant_id,school_id,name,level_no,academic_year,status,education_stage,academic_year_id,grade_cohort_id) VALUES($1::uuid,$2::uuid,$3::uuid,'Migration grade',10,'2026-2027','active','senior',$4::uuid,$5::uuid)`, gradeID, tenantID, schoolID, academicYearID, cohortID); err != nil {
		t.Fatal(err)
	}
	examCommandID := "repair-command-" + uuid.NewString()
	examStore := exam.NewPostgresStore(db)
	examSession, err := examStore.CreateExamSession(context.Background(), auth.AccessScope{TenantID: tenantID, TenantWide: true}, userID, exam.CreateSessionInput{
		SchoolID: schoolID, GradeID: gradeID, Name: "Repairable exam session", ExamType: "formal_exam",
		GradingMode: "ai_assisted", PublishPolicy: "after_admin_approval", CommandID: examCommandID,
		Subjects: []exam.SessionSubjectInput{{Subject: "math", TotalScore: 100, DurationMinutes: 90}},
	})
	if err != nil {
		t.Fatalf("seed durable exam command: %v", err)
	}
	transportRequestHash := "transport-" + uuid.NewString()
	if _, err = db.Exec(`INSERT INTO idempotency_record(tenant_id,actor_id,method,route,idempotency_key,request_hash,state,expires_at) VALUES($1::uuid,$2::uuid,'POST','/api/v1/exam-sessions',$3,$4,'processing',now()+interval '1 day')`, tenantID, userID, examCommandID, transportRequestHash); err != nil {
		t.Fatal(err)
	}
	return examCommandID, examSession, transportRequestHash
}

type reliabilityMigrationScenario struct {
	DB                                                                                   *sql.DB
	TenantID, UserID, ExamID, ReviewImportID, ProcessingImportID, CommandID, RequestHash string
	Draft                                                                                []byte
	Session                                                                              exam.ExamSession
	RunRepair                                                                            func(...string) string
}

// 升级保留人工确认草稿，未知历史来源保持 NULL；无法验证的处理中记录明确停止并保留协议原因。
func e2eAssertUnverifiedMigrationHistory(t *testing.T, scenario reliabilityMigrationScenario) {
	t.Helper()
	db, tenantID, reviewImportID, processingImportID, draft := scenario.DB, scenario.TenantID, scenario.ReviewImportID, scenario.ProcessingImportID, scenario.Draft
	var draftPreserved bool
	var reviewRunStatus, reviewRevision, processingJobStatus, processingRunStatus, processingError string
	if err := db.QueryRow(`SELECT job.draft_questions=$3::jsonb,job.source_revision,run.status FROM paper_import_job job JOIN paper_import_run run ON run.tenant_id=job.tenant_id AND run.paper_import_id=job.id AND run.generation=job.current_generation WHERE job.tenant_id=$1::uuid AND job.id=$2::uuid`, tenantID, reviewImportID, string(draft)).Scan(&draftPreserved, &reviewRevision, &reviewRunStatus); err != nil {
		t.Fatal(err)
	}
	if !draftPreserved || reviewRevision != "legacy-unverified" || reviewRunStatus != "review_required" {
		t.Fatalf("review history changed during migration: preserved=%v revision=%s run=%s", draftPreserved, reviewRevision, reviewRunStatus)
	}
	var historicalSnapshotUnknown bool
	if err := db.QueryRow(`SELECT source_snapshot IS NULL FROM paper_import_run WHERE paper_import_id=$1::uuid`, reviewImportID).Scan(&historicalSnapshotUnknown); err != nil || !historicalSnapshotUnknown {
		t.Fatalf("migration invented historical sources: %v %v", historicalSnapshotUnknown, err)
	}
	if err := db.QueryRow(`SELECT job.status,run.status,COALESCE(run.error_code,'') FROM paper_import_job job JOIN paper_import_run run ON run.tenant_id=job.tenant_id AND run.paper_import_id=job.id AND run.generation=job.current_generation WHERE job.tenant_id=$1::uuid AND job.id=$2::uuid`, tenantID, processingImportID).Scan(&processingJobStatus, &processingRunStatus, &processingError); err != nil {
		t.Fatal(err)
	}
	if processingJobStatus != "failed" || processingRunStatus != "failed" || processingError != "paper_import_protocol_upgrade_required" {
		t.Fatalf("unverifiable processing history was not safely stopped: job=%s run=%s error=%s", processingJobStatus, processingRunStatus, processingError)
	}

}

// 修复工具先按导入范围预演，再建立独立恢复代次；重复执行不生成新代次且保留旧取消任务。
func e2eRepairOrphanedImportGeneration(t *testing.T, scenario reliabilityMigrationScenario) {
	t.Helper()
	db, tenantID, userID, examID, examCommandID, runRepair := scenario.DB, scenario.TenantID, scenario.UserID, scenario.ExamID, scenario.CommandID, scenario.RunRepair
	var repairFileID string
	if err := db.QueryRow(`INSERT INTO file_asset(tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,'exam',$2::uuid,'repair.pdf','application/pdf',1,repeat('a',64),'test','repair.pdf','private',$3::uuid) RETURNING id::text`, tenantID, examID, userID).Scan(&repairFileID); err != nil {
		t.Fatal(err)
	}
	paperStore := paper.NewPostgresStore(db)
	orphan, err := paperStore.CreatePaperImport(context.Background(), tenantID, examID, userID, paper.CreatePaperImportInput{CommandID: "repair-orphan-" + uuid.NewString(), Subject: "math", Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: repairFileID, DocumentIndex: 0, RoleHint: "question"}}})
	if err != nil {
		t.Fatal(err)
	}
	assets := []paper.PaperImportOCRAsset{{SourceID: orphan.Sources[0].ID, FileAssetID: repairFileID, DocumentIndex: 0, RoleHint: "question", ContentType: "application/pdf"}}
	if err := paperStore.QueuePaperImportOCR(context.Background(), tenantID, orphan, userID, assets); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agent_worker_task SET status='cancelled',cancelled_at=now(),completed_at=now() WHERE paper_import_run_id=$1::uuid`, orphan.RunID); err != nil {
		t.Fatal(err)
	}
	scopedDryRun := runRepair("--import-id", orphan.ID)
	if !strings.Contains(scopedDryRun, "create_recovery_generation") || strings.Contains(scopedDryRun, examCommandID) {
		t.Fatalf("repair scope/action mismatch: %s", scopedDryRun)
	}
	_ = runRepair("--import-id", orphan.ID, "--apply")
	repaired, err := paperStore.GetPaperImport(context.Background(), tenantID, orphan.ID)
	if err != nil || repaired.Generation != orphan.Generation+1 || repaired.RunID == orphan.RunID {
		t.Fatalf("terminal-only import not given new run: %#v %v", repaired, err)
	}
	if repeated := runRepair("--import-id", orphan.ID, "--apply"); !strings.Contains(repeated, `"mutations":0`) {
		t.Fatalf("duplicate recovery generation: %s", repeated)
	}
	var cancelledHistory int
	if err := db.QueryRow(`SELECT count(*) FROM agent_worker_task WHERE paper_import_run_id=$1::uuid AND status='cancelled'`, orphan.RunID).Scan(&cancelledHistory); err != nil || cancelledHistory != 1 {
		t.Fatalf("repair rewrote cancelled task history: %d %v", cancelledHistory, err)
	}
}

// 全租户预演包含可恢复业务事实，应用只修复原请求哈希的传输收据并标记旧来源待复核。
func e2eRepairLegacyCommandAndReview(t *testing.T, scenario reliabilityMigrationScenario) (string, time.Time) {
	t.Helper()
	db, tenantID, userID, reviewImportID, examCommandID, examSession, transportRequestHash, runRepair := scenario.DB, scenario.TenantID, scenario.UserID, scenario.ReviewImportID, scenario.CommandID, scenario.Session, scenario.RequestHash, scenario.RunRepair
	dryRunOutput := runRepair()
	if !strings.Contains(dryRunOutput, `"mode": "dry-run"`) || !strings.Contains(dryRunOutput, reviewImportID) || !strings.Contains(dryRunOutput, examCommandID) || !strings.Contains(dryRunOutput, `"action": "complete_replay_from_business_fact"`) {
		t.Fatalf("repair dry-run did not report scoped findings: %s", dryRunOutput)
	}
	firstApplyOutput := runRepair("--apply")
	if !strings.Contains(firstApplyOutput, `"applied":true`) {
		t.Fatalf("repair apply did not report completion: %s", firstApplyOutput)
	}
	var replayState string
	var replayStatus int
	var replayRequestHash string
	var replayBody []byte
	if err := db.QueryRow(`SELECT state,response_status,request_hash,response_body FROM idempotency_record WHERE tenant_id=$1::uuid AND actor_id=$2::uuid AND method='POST' AND route='/api/v1/exam-sessions' AND idempotency_key=$3`, tenantID, userID, examCommandID).Scan(&replayState, &replayStatus, &replayRequestHash, &replayBody); err != nil {
		t.Fatal(err)
	}
	if replayState != "completed" || replayStatus != 201 || replayRequestHash != transportRequestHash || !strings.Contains(string(replayBody), examSession.ID) {
		t.Fatalf("exam replay receipt not repaired safely: state=%s status=%d hash=%s body=%s", replayState, replayStatus, replayRequestHash, replayBody)
	}
	var firstRepairStatus string
	var firstRepairUpdated time.Time
	if err := db.QueryRow(`SELECT status,updated_at FROM paper_import_run WHERE tenant_id=$1::uuid AND paper_import_id=$2::uuid`, tenantID, reviewImportID).Scan(&firstRepairStatus, &firstRepairUpdated); err != nil {
		t.Fatal(err)
	}
	if firstRepairStatus != "needs_review" {
		t.Fatalf("legacy candidate provenance was not marked for review: %s", firstRepairStatus)
	}
	return firstRepairStatus, firstRepairUpdated
}

// 第二次正式修复报告零变更，旧复核运行状态和更新时间必须保持不动。
func e2eAssertReliabilityRepairRepeatNoOp(t *testing.T, scenario reliabilityMigrationScenario, firstRepairStatus string, firstRepairUpdated time.Time) {
	t.Helper()
	db, tenantID, reviewImportID, runRepair := scenario.DB, scenario.TenantID, scenario.ReviewImportID, scenario.RunRepair
	secondApplyOutput := runRepair("--apply")
	if !strings.Contains(secondApplyOutput, `"mutations":0`) {
		t.Fatalf("second repair should be a no-op: %s", secondApplyOutput)
	}
	var secondRepairStatus string
	var secondRepairUpdated time.Time
	if err := db.QueryRow(`SELECT status,updated_at FROM paper_import_run WHERE tenant_id=$1::uuid AND paper_import_id=$2::uuid`, tenantID, reviewImportID).Scan(&secondRepairStatus, &secondRepairUpdated); err != nil {
		t.Fatal(err)
	}
	if secondRepairStatus != firstRepairStatus || !secondRepairUpdated.Equal(firstRepairUpdated) {
		t.Fatalf("second repair execution was not idempotent: first=%s/%s second=%s/%s", firstRepairStatus, firstRepairUpdated, secondRepairStatus, secondRepairUpdated)
	}

}
