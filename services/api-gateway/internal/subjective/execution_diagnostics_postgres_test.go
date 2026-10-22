package subjective

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 完整迁移只应用到本次创建的随机隔离库；运行连接使用真实无 BYPASSRLS 角色。
// 固定租户与单连接让会话故障开关可控，不把超级用户连接当作运行权限验证。
func diagnosticsPostgresDB(t *testing.T) (*sql.DB, *sql.DB, string) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for execution diagnostics PostgreSQL integration")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	rootCfg := cfg.Copy()
	rootCfg.Database = "postgres"
	root := stdlib.OpenDB(*rootCfg)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	dbName, roleName := "ai_diagnostics_test_"+suffix, "ai_diagnostics_role_"+suffix
	quotedDB, quotedRole := pgx.Identifier{dbName}.Sanitize(), pgx.Identifier{roleName}.Sanitize()
	if _, err := root.Exec(`CREATE DATABASE ` + quotedDB); err != nil {
		root.Close()
		t.Fatal(err)
	}
	ownerCfg := cfg.Copy()
	ownerCfg.Database = dbName
	owner := stdlib.OpenDB(*ownerCfg)
	owner.SetMaxOpenConns(1)
	var runtime *sql.DB
	t.Cleanup(func() {
		if runtime != nil {
			_ = runtime.Close()
		}
		_ = owner.Close()
		if _, err := root.Exec(`DROP DATABASE ` + quotedDB + ` WITH (FORCE)`); err != nil {
			t.Errorf("clean isolated diagnostics database: %v", err)
		}
		if _, err := root.Exec(`DROP ROLE IF EXISTS ` + quotedRole); err != nil {
			t.Errorf("clean isolated diagnostics role: %v", err)
		}
		_ = root.Close()
	})
	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("migration inventory: %v", err)
	}
	sort.Strings(files)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := owner.ExecContext(ctx, string(raw)); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(path), err)
		}
	}
	var tenantID string
	if err := owner.QueryRow(`SELECT id::text FROM tenant WHERE code='demo'`).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(`CREATE ROLE ` + quotedRole + ` LOGIN NOSUPERUSER NOBYPASSRLS`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(`GRANT edugrade_tenant_runtime TO ` + quotedRole); err != nil {
		t.Fatal(err)
	}
	runtimeCfg := ownerCfg.Copy()
	runtimeCfg.User, runtimeCfg.Password = roleName, ""
	runtimeCfg.RuntimeParams["edugrade.tenant_id"] = tenantID
	runtimeCfg.RuntimeParams["statement_timeout"] = "10000"
	runtimeCfg.RuntimeParams["lock_timeout"] = "3000"
	runtime = stdlib.OpenDB(*runtimeCfg)
	runtime.SetMaxOpenConns(1)
	var actualDB, actualRole string
	var superuser, bypass bool
	if err := runtime.QueryRow(`SELECT current_database(),current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&actualDB, &actualRole, &superuser, &bypass); err != nil || actualDB != dbName || actualRole != roleName || superuser || bypass {
		t.Fatalf("restricted diagnostics login mismatch: db=%s role=%s superuser=%v bypass=%v err=%v", actualDB, actualRole, superuser, bypass, err)
	}
	t.Logf("applied %d migrations through %s; runtime login is NOSUPERUSER NOBYPASSRLS", len(files), filepath.Base(files[len(files)-1]))
	return owner, runtime, tenantID
}

func TestPostgresExecutionDiagnosticsWorkerFailureAtomic(t *testing.T) {
	admin, db, tenantID := diagnosticsPostgresDB(t)
	ctx := context.Background()
	t.Run("store_queries_scope_diagnostics_by_tenant", func(t *testing.T) {
		store, _, _, prepared, _ := seedRemediationCompletion(t, db, tenantID)
		if _, err := store.UpdateRun(ctx, tenantID, prepared.Run.ID, UpdateRunInput{Status: RunFailed, ExecutionDiagnostics: truncatedDiagnostics()}); err != nil {
			t.Fatal(err)
		}
		// 运行表当前依赖显式 tenant 条件，未启用表级 RLS；本断言只证明 Store 的查询隔离。
		if _, err := store.GetRun(ctx, uuid.NewString(), prepared.Run.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("another tenant read diagnostics through Store: %v", err)
		}
		if run, err := store.GetRun(ctx, tenantID, prepared.Run.ID); err != nil || run.ExecutionDiagnostics == nil {
			t.Fatalf("tenant could not read own diagnostics: %#v %v", run, err)
		}
		// 准备新尝试时仍保留最近失败事实，成功的旧协议响应没有诊断才清除它。
		if run, err := store.UpdateRun(ctx, tenantID, prepared.Run.ID, UpdateRunInput{Status: RunProcessing}); err != nil || run.ExecutionDiagnostics == nil {
			t.Fatalf("processing unexpectedly cleared known diagnostics: %#v %v", run, err)
		}
		if run, err := store.UpdateRun(ctx, tenantID, prepared.Run.ID, UpdateRunInput{Status: RunSucceeded}); err != nil || run.ExecutionDiagnostics != nil {
			t.Fatalf("legacy success retained prior failure diagnostics: %#v %v", run, err)
		}
		var cleared bool
		if err := db.QueryRow(`SELECT execution_diagnostics IS NULL FROM subjective_grading_run WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, prepared.Run.ID).Scan(&cleared); err != nil || !cleared {
			t.Fatalf("success response hid rather than cleared stored diagnostics: cleared=%v err=%v", cleared, err)
		}
	})
	t.Run("reported_partial_and_unknown_usage", func(t *testing.T) {
		for _, usageStatus := range []string{"reported", "partial", "unavailable"} {
			t.Run(usageStatus, func(t *testing.T) {
				store, runtime, _, prepared, input := seedRemediationCompletion(t, db, tenantID)
				d := truncatedDiagnostics()
				d.UsageStatus = usageStatus
				if usageStatus == "unavailable" {
					d.Usage = nil
				} else if usageStatus == "partial" {
					d.Usage.OutputTokens, d.Usage.TotalTokens = nil, nil
				}
				result, err := NewWorkerCompletionService(store, runtime).Fail(ctx, tenantID, prepared.Run.ID, diagnosticsWorkerFailure(input, d))
				if err != nil || result.Run.Status != RunFailed || result.Task.Status != workerruntime.StatusFailed || result.Run.GradeID != "" {
					t.Fatalf("failure did not atomically terminate: run=%s task=%s err=%v", result.Run.Status, result.Task.Status, err)
				}
				run, err := store.GetRun(ctx, tenantID, prepared.Run.ID)
				if err != nil || !reflect.DeepEqual(run.ExecutionDiagnostics, d) {
					t.Fatalf("diagnostics round trip: %#v %v", run.ExecutionDiagnostics, err)
				}
				replayInput := serviceTestRunInput(prepared.Context)
				replayInput.RequestID = prepared.Run.RequestID
				replayed, err := store.GetOrCreateRun(ctx, tenantID, "", replayInput)
				if err != nil || replayed.ID != run.ID || !reflect.DeepEqual(replayed.ExecutionDiagnostics, d) {
					t.Fatalf("same request replay lost failure diagnostics: %#v %v", replayed, err)
				}
				task, err := runtime.Get(ctx, tenantID, input.TaskID)
				if err != nil || len(task.Attempts) != 1 {
					t.Fatalf("read recorded attempt: %#v %v", task, err)
				}
				for _, detail := range []map[string]any{task.ErrorDetail, task.Attempts[0].ErrorDetail} {
					got, err := diagnosticsFromFailureDetail(detail)
					if err != nil || !reflect.DeepEqual(got, d) {
						t.Fatalf("task/attempt lost bounded diagnostic facts: %#v %v", got, err)
					}
				}
				var gradeCount, usageCount int
				if err := db.QueryRow(`SELECT count(*) FROM ai_grade WHERE tenant_id=$1::uuid AND adapter_request_id=$2`, tenantID, prepared.Run.RequestID).Scan(&gradeCount); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow(`SELECT count(*) FROM model_usage_event WHERE tenant_id=$1::uuid AND request_id=$2`, tenantID, prepared.Run.RequestID).Scan(&usageCount); err != nil || gradeCount != 0 || usageCount != 0 {
					t.Fatalf("failed result fabricated grade or usage event: grade=%d usage=%d err=%v", gradeCount, usageCount, err)
				}
				late := truncatedDiagnostics()
				late.UsageStatus, late.Usage = "unavailable", nil
				if _, err := NewWorkerCompletionService(store, runtime).Fail(ctx, tenantID, prepared.Run.ID, diagnosticsWorkerFailure(input, late)); !errors.Is(err, workerruntime.ErrInvalidTransition) {
					t.Fatalf("terminal failure accepted a second diagnostic settlement: %v", err)
				}
				if after, err := store.GetRun(ctx, tenantID, prepared.Run.ID); err != nil || !reflect.DeepEqual(after.ExecutionDiagnostics, d) || !after.UpdatedAt.Equal(run.UpdatedAt) {
					t.Fatalf("terminal replay overwrote original diagnostic facts: %#v %v", after, err)
				}
			})
		}
	})
	t.Run("migration_rejects_non_object_and_oversized_json", func(t *testing.T) {
		store, _, _, prepared, _ := seedRemediationCompletion(t, db, tenantID)
		for _, raw := range []string{`[]`, `null`, `{"unsafe":"` + strings.Repeat("x", 8192) + `"}`} {
			if _, err := db.Exec(`UPDATE subjective_grading_run SET execution_diagnostics=$2::jsonb WHERE id=$1::uuid`, prepared.Run.ID, raw); err == nil {
				t.Fatal("migration accepted invalid diagnostic storage shape")
			}
		}
		run, err := store.GetRun(ctx, tenantID, prepared.Run.ID)
		if err != nil || run.ExecutionDiagnostics != nil {
			t.Fatalf("failed diagnostic write changed legacy NULL: %v", err)
		}
	})
	t.Run("run_write_failure_rolls_back_task_attempt_and_diagnostics", func(t *testing.T) {
		store, runtime, _, prepared, input := seedRemediationCompletion(t, db, tenantID)
		// runtime 已执行 Fail 后才使来源 run 的 UPDATE 失败，检验的是事务回滚而非调用顺序。
		if _, err := admin.Exec(`CREATE FUNCTION diagnostics_test_run_fault() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.id::text=current_setting('edugrade.diagnostics_failure_target',true) THEN
  RAISE EXCEPTION 'synthetic diagnostics run write failure';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER diagnostics_test_run_fault BEFORE UPDATE ON subjective_grading_run FOR EACH ROW EXECUTE FUNCTION diagnostics_test_run_fault()`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`SELECT set_config('edugrade.diagnostics_failure_target',$1,false)`, prepared.Run.ID); err != nil {
			t.Fatal(err)
		}
		service := NewWorkerCompletionService(store, runtime)
		_, err := service.Fail(ctx, tenantID, prepared.Run.ID, diagnosticsWorkerFailure(input, truncatedDiagnostics()))
		if err == nil || !strings.Contains(err.Error(), "synthetic diagnostics run write failure") {
			t.Fatalf("failure missed transaction fault: %v", err)
		}
		run, runErr := store.GetRun(ctx, tenantID, prepared.Run.ID)
		task, taskErr := runtime.Get(ctx, tenantID, input.TaskID)
		if runErr != nil || taskErr != nil || run.ExecutionDiagnostics != nil || run.Status != prepared.Run.Status || task.Status != workerruntime.StatusLeased || task.ErrorCode != "" || task.Attempts[0].CompletedAt != nil || len(task.Attempts[0].ErrorDetail) != 0 {
			t.Fatalf("failed transaction left diagnostic/state changes: run=%#v task=%#v errors=%v,%v", run, task, runErr, taskErr)
		}
		if _, err := db.Exec(`SELECT set_config('edugrade.diagnostics_failure_target','',false)`); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Fail(ctx, tenantID, prepared.Run.ID, diagnosticsWorkerFailure(input, truncatedDiagnostics())); err != nil {
			t.Fatalf("current lease could not settle after fault cleared: %v", err)
		}
	})
	t.Run("old_lease_cannot_write_current_attempt_diagnostics", func(t *testing.T) {
		store, runtime, _, prepared, input := seedRemediationCompletion(t, db, tenantID)
		if _, err := runtime.Fail(ctx, tenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: true, ErrorCode: "synthetic_transport_retry"}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE agent_worker_task SET not_before=now()-interval '1 minute' WHERE id=$1::uuid`, input.TaskID); err != nil {
			t.Fatal(err)
		}
		claimed, err := runtime.Claim(ctx, tenantID, workerruntime.ClaimInput{QueueName: prepared.Task.QueueName, WorkerService: prepared.Task.WorkerService, WorkerInstanceID: "diagnostics-worker-2", LeaseSeconds: 300})
		if err != nil || len(claimed) != 1 || claimed[0].LeaseToken == input.LeaseToken {
			t.Fatalf("replacement attempt was not leased: %v", err)
		}
		if _, _, err := store.MarkWorkerProcessing(ctx, tenantID, prepared.Run.ID, input.TaskID, claimed[0].LeaseToken, runtime); err != nil {
			t.Fatal(err)
		}
		service := NewWorkerCompletionService(store, runtime)
		if _, err := service.Fail(ctx, tenantID, prepared.Run.ID, diagnosticsWorkerFailure(input, truncatedDiagnostics())); !errors.Is(err, workerruntime.ErrLeaseMismatch) {
			t.Fatalf("old lease failure was accepted: %v", err)
		}
		run, runErr := store.GetRun(ctx, tenantID, prepared.Run.ID)
		task, taskErr := runtime.Get(ctx, tenantID, input.TaskID)
		if runErr != nil || taskErr != nil || run.ExecutionDiagnostics != nil || task.Status != workerruntime.StatusLeased || task.Attempts[1].CompletedAt != nil {
			t.Fatalf("old lease polluted current attempt: run=%#v task=%#v errors=%v,%v", run, task, runErr, taskErr)
		}
		input.LeaseToken = claimed[0].LeaseToken
		if _, err := service.Fail(ctx, tenantID, prepared.Run.ID, diagnosticsWorkerFailure(input, truncatedDiagnostics())); err != nil {
			t.Fatalf("current lease could not record diagnostics: %v", err)
		}
	})
}

func diagnosticsWorkerFailure(input WorkerResultInput, d *ExecutionDiagnostics) WorkerFailureInput {
	return WorkerFailureInput{TaskID: input.TaskID, LeaseToken: input.LeaseToken, Retryable: true, ErrorCode: "ai_output_incomplete", ErrorDetail: executionFailureDetail(d), DurationMS: 15}
}

func TestPostgresBatchDiagnosticsOnlyAuthorizedActiveFailures(t *testing.T) {
	_, db, tenantID := diagnosticsPostgresDB(t)
	ctx := context.Background()
	store, _, actorID, prepared, _ := seedRemediationCompletion(t, db, tenantID)
	batch, err := store.CreateBatch(ctx, tenantID, actorID, CreateBatchInput{IdempotencyKey: uuid.NewString(), SegmentIDs: []string{prepared.Run.AnswerSegmentID}})
	if err != nil {
		t.Fatal(err)
	}
	d := truncatedDiagnostics()
	raw, _ := json.Marshal(d)
	if _, err := db.Exec(`UPDATE subjective_grading_run SET batch_id=$2::uuid,status='failed',execution_diagnostics=$3::jsonb WHERE id=$1::uuid`, prepared.Run.ID, batch.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	summaries, err := store.LoadBatchFailureSummaries(ctx, tenantID, []GradingBatch{batch, {ID: uuid.NewString(), TenantID: uuid.NewString()}})
	if err != nil || len(summaries) != 1 || len(summaries[batch.ID]) != 1 || summaries[batch.ID][0].FailureKind != "output_truncated" || summaries[batch.ID][0].Count != 1 {
		t.Fatalf("batch failure query: %#v %v", summaries, err)
	}
	wrong := batch
	wrong.TenantID = uuid.NewString()
	if got, err := store.LoadBatchFailureSummaries(ctx, tenantID, []GradingBatch{wrong}); err != nil || len(got) != 0 {
		t.Fatalf("cross tenant batch was queried: %#v %v", got, err)
	}
	if _, err := db.Exec(`UPDATE subjective_grading_run SET deleted_at=now() WHERE id=$1::uuid`, prepared.Run.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.LoadBatchFailureSummaries(ctx, tenantID, []GradingBatch{batch}); err != nil || len(got[batch.ID]) != 0 {
		t.Fatalf("deleted run entered summary: %#v %v", got, err)
	}
}
