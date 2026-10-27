package capture

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 每次使用随机 schema 及合成记录；复制正式列/检查约束/索引，不接入真实账号、对象或业务数据。
func captureRecoveryRemediationDB(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL is not configured")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	admin := stdlib.OpenDB(*cfg)
	schema := "capture_recovery_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema + ",public"
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		_, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		if err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	for _, table := range []string{"capture_batch", "capture_file", "capture_page", "submission", "page_registration_run", "agent_worker_task", "agent_worker_task_attempt", "agent_worker_heartbeat"} {
		if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, db
}

func TestPostgresCaptureFailureAndRegistrationRetryRollbackTogether(t *testing.T) {
	ctx, db := captureRecoveryRemediationDB(t)
	tenant, actor, batch, file, page, exam := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO capture_batch(id,tenant_id,exam_id,name,source_type,operator_id,status) VALUES($1,$2,$3,'synthetic','web_upload',$4,'processing')`, batch, tenant, exam, actor)
	exec(`INSERT INTO capture_file(id,tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,idempotency_key,uploaded_by,status) VALUES($1,$2,$3,$4,'synthetic.pdf','application/pdf',$5,10,'fixture',$6,'queued')`, file, tenant, batch, uuid.NewString(), strings.Repeat("a", 64), actor)
	runtime := workerruntime.NewPostgresStore(db)
	newTask := func(source, id string) workerruntime.Task {
		t.Helper()
		task, err := runtime.CreateTask(ctx, tenant, actor, workerruntime.CreateTaskInput{TaskType: "preprocess", QueueName: "page-processing", SourceType: source, SourceID: id, PayloadSchemaVersion: "fixture-v1", IdempotencyKey: id, MaxAttempts: 1})
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := runtime.Claim(ctx, tenant, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "page-processing-worker", WorkerInstanceID: "synthetic", Limit: 1, LeaseSeconds: 300})
		if err != nil || len(claimed) != 1 || claimed[0].ID != task.ID {
			t.Fatalf("claim: %#v %v", claimed, err)
		}
		return claimed[0]
	}
	store := NewPostgresStore(db)
	fileTask := newTask("capture_file", file)
	input := FileFailureInput{TaskID: fileTask.ID, LeaseToken: fileTask.LeaseToken, ErrorCode: "synthetic_decode_failed"}
	exec(`CREATE FUNCTION reject_fixture_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic projection failure'; END $$`)
	exec(`CREATE TRIGGER reject_fixture BEFORE UPDATE ON capture_file FOR EACH ROW EXECUTE FUNCTION reject_fixture_update()`)
	if _, _, err := store.SubmitFileFailureCommand(ctx, tenant, file, input, runtime); err == nil {
		t.Fatal("domain failure was ignored")
	}
	before, err := runtime.Get(ctx, tenant, fileTask.ID)
	if err != nil || before.Status != workerruntime.StatusLeased {
		t.Fatalf("failed projection consumed lease: %#v %v", before, err)
	}
	exec(`DROP TRIGGER reject_fixture ON capture_file`)
	failed, status, err := store.SubmitFileFailureCommand(ctx, tenant, file, input, runtime)
	if err != nil || failed.Status != "failed" || status != workerruntime.StatusFailed {
		t.Fatalf("same callback retry: %#v %s %v", failed, status, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT failed_count FROM capture_batch WHERE id=$1`, batch).Scan(&count); err != nil || count != 1 {
		t.Fatalf("batch aggregate: %d %v", count, err)
	}

	run := uuid.NewString()
	exec(`INSERT INTO capture_page(id,tenant_id,capture_batch_id,capture_file_id,source_index,sequence_no,decoded_file_asset_id) VALUES($1,$2,$3,$4,1,1,$5)`, page, tenant, batch, file, uuid.NewString())
	registrationTask := newTask("page_registration_run", run)
	if _, err := runtime.Fail(ctx, tenant, registrationTask.ID, workerruntime.FailInput{LeaseToken: registrationTask.LeaseToken, ErrorCode: "synthetic_terminal"}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO page_registration_run(id,tenant_id,capture_page_id,submission_page_id,source_file_asset_id,source_sha256,template_id,template_content_hash,page_no,profile_version,processing_status,runtime_task_id) VALUES($1,$2,$3,$4,$5,$6,$7,$6,1,'fixture','terminal_error',$8)`, run, tenant, page, uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64), uuid.NewString(), registrationTask.ID)
	exec(`CREATE TRIGGER reject_fixture BEFORE UPDATE ON page_registration_run FOR EACH ROW EXECUTE FUNCTION reject_fixture_update()`)
	if _, err := store.RetryRegistrationTask(ctx, tenant, run, registrationTask.ID, runtime); err == nil {
		t.Fatal("failed domain retry was ignored")
	}
	before, err = runtime.Get(ctx, tenant, registrationTask.ID)
	if err != nil || before.Status != workerruntime.StatusFailed {
		t.Fatalf("retry leaked runtime queue state: %#v %v", before, err)
	}
	exec(`DROP TRIGGER reject_fixture ON page_registration_run`)
	// 真实工作台 Service 选择同一配准来源协调器，不仅验证直接存储调用。
	projection := processing.NewMemoryStore()
	projection.PutState(tenant, processing.PageState{PageID: page, ExamID: exam, IssueCode: processing.IssueBadAlignment, Blocking: true, Retryable: true, RetrySourceType: "page_registration_run", RetrySourceID: run})
	items, err := projection.ListExceptions(ctx, tenant, processing.ExceptionFilter{ExamID: exam})
	if err != nil || len(items.Exceptions) != 1 {
		t.Fatalf("exception fixture: %#v %v", items, err)
	}
	service := processing.NewService(projection, runtime).WithRegistrationRetryStore(store)
	queued, err := service.Retry(ctx, tenant, items.Exceptions[0].ID)
	if err != nil || queued.Status != workerruntime.StatusQueued {
		t.Fatalf("workbench retry: %#v %v", queued, err)
	}
	reg, err := store.GetRegistrationRun(ctx, tenant, run)
	if err != nil || reg.ProcessingStatus != "processing" {
		t.Fatalf("source was not restored: %#v %v", reg, err)
	}
	if _, err := service.Retry(ctx, tenant, items.Exceptions[0].ID); err != nil {
		t.Fatalf("repeat retry: %v", err)
	}
	exec(`UPDATE capture_page SET submission_id=$2::uuid WHERE id=$1::uuid`, page, uuid.NewString())
	claimed, err := runtime.Claim(ctx, tenant, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "page-processing-worker", WorkerInstanceID: "synthetic", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim after retry: %#v %v", claimed, err)
	}
	completed, err := store.SubmitRegistrationResultCommand(ctx, tenant, run, RegistrationResultInput{TaskID: registrationTask.ID, LeaseToken: claimed[0].LeaseToken, ResultVersion: "fixture-v1", Confidence: .99, Method: "fixture", RegisteredFileAssetID: uuid.NewString(), SourceToTemplate: []any{}, TemplateToSource: []any{}, GuardReport: map[string]any{}}, runtime)
	if err != nil || completed.ProcessingStatus != "completed" {
		t.Fatalf("result after retry rejected: %#v %v", completed, err)
	}
}
