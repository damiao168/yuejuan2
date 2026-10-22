package subjective

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 直接连接明确命名的隔离库，不创建/删除数据库；测试使用完整迁移后的真实表、约束和 RLS。
func remediationGradingDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid grading test database configuration")
	}
	if config.Database != "yuejuan_remediation_20261013_grading_test" {
		t.Fatal("only a dedicated grading_test database is allowed")
	}
	var tenantID string
	initial := stdlib.OpenDB(*config)
	if err := initial.QueryRow(`SELECT id::text FROM tenant WHERE code='demo'`).Scan(&tenantID); err != nil {
		initial.Close()
		t.Fatalf("read synthetic demo tenant: %v", err)
	}
	initial.Close()
	config.RuntimeParams["edugrade.tenant_id"] = tenantID
	config.RuntimeParams["statement_timeout"] = "10000"
	config.RuntimeParams["lock_timeout"] = "3000"
	db := stdlib.OpenDB(*config)
	// 故障开关是会话级 GUC；一条连接让业务事务与设置始终使用同一隔离测试会话。
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	var actual string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&actual); err != nil || actual != config.Database {
		t.Fatal("grading test database identity mismatch")
	}
	var triggerCount int
	if err := db.QueryRow(`SELECT count(*) FROM pg_trigger WHERE tgname IN ('remediation_grading_task_fault','remediation_grading_run_fault','remediation_grading_review_fault','remediation_grading_panel_fault') AND NOT tgisinternal`).Scan(&triggerCount); err != nil || triggerCount != 4 {
		t.Fatal("dedicated database owner must install the four remediation fault triggers before running this test")
	}
	return db, tenantID
}

func seedRemediationCompletion(t *testing.T, db *sql.DB, tenantID string) (*PostgresStore, *workerruntime.PostgresStore, string, WorkerExecutionContext, WorkerResultInput) {
	t.Helper()
	suffix := uuid.NewString()
	var actorID, schoolID, examID, fileID, questionID, submissionID, pageID, segmentID string
	if err := db.QueryRow(`SELECT id::text FROM app_user WHERE tenant_id=$1 AND username='teacher'`, tenantID).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	queries := []struct {
		query  string
		args   []any
		target *string
	}{
		{`INSERT INTO school(tenant_id,name,code,status) VALUES($1,'grading remediation synthetic school',$2,'active') RETURNING id::text`, []any{tenantID, suffix}, &schoolID},
		{`INSERT INTO exam(tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2::uuid,'grading remediation synthetic exam','physics','quiz',5,'draft','ai_assisted','manual_after_confirmation',$3::uuid) RETURNING id::text`, []any{tenantID, &schoolID, actorID}, &examID},
		{`INSERT INTO file_asset(tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1,$2::uuid,'exam',$2::uuid,'synthetic.png','image/png',1,repeat('a',64),'test',$3,'tenant',$4::uuid) RETURNING id::text`, []any{tenantID, &examID, suffix, actorID}, &fileID},
		{`INSERT INTO question(tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1,$2::uuid,'Q1','short_answer',5,1,'active') RETURNING id::text`, []any{tenantID, &examID}, &questionID},
		{`INSERT INTO submission(tenant_id,exam_id,source_type,status,collected_by) VALUES($1,$2::uuid,'manual_import','created',$3::uuid) RETURNING id::text`, []any{tenantID, &examID, actorID}, &submissionID},
		{`INSERT INTO submission_page(tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1,$2::uuid,$3::uuid,1,'accepted') RETURNING id::text`, []any{tenantID, &submissionID, &fileID}, &pageID},
		{`INSERT INTO answer_segment(tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1,$2::uuid,$3::uuid,$4::uuid,'Q1','{}','manual','accepted') RETURNING id::text`, []any{tenantID, &submissionID, &pageID, &questionID}, &segmentID},
	}
	for _, query := range queries {
		for i, value := range query.args {
			if ref, ok := value.(*string); ok {
				query.args[i] = *ref
			}
		}
		if err := db.QueryRow(query.query, query.args...).Scan(query.target); err != nil {
			t.Fatalf("seed synthetic completion: %v", err)
		}
	}
	value := serviceTestContext()
	value.SegmentID, value.Question.ID, value.Rubric.QuestionID = segmentID, questionID, questionID
	store := NewPostgresStore(db)
	runInput := serviceTestRunInput(value)
	runInput.RequestID = "remediation-" + suffix
	run, err := store.GetOrCreateRun(context.Background(), tenantID, actorID, runInput)
	if err != nil {
		t.Fatal(err)
	}
	runtime := workerruntime.NewPostgresStore(db)
	task, err := runtime.CreateTask(context.Background(), tenantID, actorID, workerruntime.CreateTaskInput{TaskType: "ai_grade", QueueName: "grading-remediation-" + suffix,
		SourceType: "subjective_grading_run", SourceID: run.ID, PayloadSchemaVersion: "subjective-grade-v1", Payload: map[string]any{"exam_id": examID}, IdempotencyKey: suffix, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := runtime.Claim(context.Background(), tenantID, workerruntime.ClaimInput{QueueName: task.QueueName, WorkerService: "subjective-grading-worker", WorkerInstanceID: "worker-1", LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %#v %v", claimed, err)
	}
	output := validServiceOutput()
	output.RequestID, output.ModelVersion, output.PromptVersion, output.RubricVersion = run.RequestID, run.ModelVersion, run.PromptVersion, run.RubricVersion
	input := WorkerResultInput{TaskID: task.ID, LeaseToken: claimed[0].LeaseToken, ResultSchemaVersion: "subjective-grade-v1", DurationMS: 15, Output: output}
	return store, runtime, actorID, WorkerExecutionContext{Run: run, Task: claimed[0], Context: value, Policy: ModelPolicy{ModelVersion: run.ModelVersion, PromptVersion: run.PromptVersion, MinConfidence: run.MinConfidence}}, input
}

func TestRemediationPostgresWorkerCompletionAtomicRollbackAndReplay(t *testing.T) {
	db, tenantID := remediationGradingDB(t)
	for _, scenario := range []string{"expired_lease", "complete_failure", "run_update_failure"} {
		t.Run(scenario, func(t *testing.T) {
			store, runtime, actorID, prepared, input := seedRemediationCompletion(t, db, tenantID)
			if scenario == "expired_lease" {
				if _, err := db.Exec(`UPDATE agent_worker_task SET lease_expires_at=now()-interval '1 minute' WHERE id=$1::uuid`, input.TaskID); err != nil {
					t.Fatal(err)
				}
			} else {
				// 成绩已插入后分别让 runtime Complete 和 UpdateRun 抛错，验证整笔事务而非写入顺序。
				stage, target := "complete", input.TaskID
				if scenario == "run_update_failure" {
					stage, target = "run", prepared.Run.ID
				}
				setRemediationFault(t, db, stage, target)
			}
			service := NewWorkerCompletionService(store, runtime)
			_, err := service.Complete(context.Background(), tenantID, actorID, prepared, input, input.Output)
			if scenario == "expired_lease" && !errors.Is(err, workerruntime.ErrLeaseExpired) {
				t.Fatalf("expired lease did not reach the lease guard: %v", err)
			}
			if scenario != "expired_lease" && (err == nil || !strings.Contains(err.Error(), "synthetic remediation failure at ")) {
				t.Fatalf("completion did not reach the real transaction fault: %v", err)
			}
			if _, err := store.GetGradeByAdapterRequestID(context.Background(), tenantID, prepared.Run.RequestID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("failed completion persisted grade: %v", err)
			}
			task, taskErr := runtime.Get(context.Background(), tenantID, input.TaskID)
			run, runErr := store.GetRun(context.Background(), tenantID, prepared.Run.ID)
			if taskErr != nil || runErr != nil || task.Status != workerruntime.StatusLeased || run.Status != RunProcessing || run.GradeID != "" {
				t.Fatalf("atomic rollback failed: task=%s run=%s grade=%s", task.Status, run.Status, run.GradeID)
			}
			clearRemediationFault(t, db)
			if scenario == "expired_lease" {
				if _, err := db.Exec(`UPDATE agent_worker_task SET lease_expires_at=now()+interval '5 minutes' WHERE id=$1::uuid`, input.TaskID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.Complete(context.Background(), tenantID, actorID, prepared, input, input.Output); err != nil {
				t.Fatalf("retry after rejected completion could not settle: %v", err)
			}
		})
	}
	t.Run("same_result_replay_and_conflict", func(t *testing.T) {
		store, runtime, actorID, prepared, input := seedRemediationCompletion(t, db, tenantID)
		service := NewWorkerCompletionService(store, runtime)
		accepted, err := service.Complete(context.Background(), tenantID, actorID, prepared, input, input.Output)
		if err != nil {
			t.Fatal(err)
		}
		replayed, replay, err := service.Replay(context.Background(), tenantID, actorID, prepared.Run.ID, input)
		if err != nil || !replay || replayed.Grade.ID != accepted.Grade.ID || replayed.Run.Status != RunSucceeded {
			t.Fatalf("strict replay failed: %v", err)
		}
		for _, field := range []string{"score", "raw_output", "duration", "schema", "feedback", "telemetry", "model", "lease", "worker", "tenant", "task", "source"} {
			t.Run(field, func(t *testing.T) {
				assertRemediationReplayRejectsChangedField(t, service, tenantID, actorID, prepared, input, field)
			})
		}
		if _, _, err := store.MarkWorkerProcessing(context.Background(), tenantID, prepared.Run.ID, input.TaskID, input.LeaseToken, runtime); !errors.Is(err, ErrWorkerLeaseMismatch) {
			t.Fatalf("successful run regained execute permission: %v", err)
		}
		service.Reject(context.Background(), tenantID, prepared, input, RunFailed, "late_failure", errors.New("synthetic late failure"))
		assertRemediationRunUnchanged(t, store, tenantID, replayed.Run)
	})
}

func assertRemediationReplayRejectsChangedField(t *testing.T, service *WorkerCompletionService, tenantID, actorID string, prepared WorkerExecutionContext, input WorkerResultInput, field string) {
	t.Helper()
	ctx, runID := context.Background(), prepared.Run.ID
	switch field {
	case "score":
		input.Output.SuggestedScore++
	case "raw_output":
		input.Output.RawOutput = map[string]any{"synthetic": "different"}
	case "duration":
		input.DurationMS++
	case "schema":
		input.ResultSchemaVersion = "different-v1"
	case "feedback":
		input.Output.StudentFeedback = "different synthetic feedback"
	case "telemetry":
		input.Output.Telemetry.ElapsedMS++
	case "model":
		input.Output.ModelVersion = "different-model"
	case "lease":
		input.LeaseToken = "different-lease"
	case "worker":
		capability := prepared.Task
		capability.WorkerInstanceID = "different-worker"
		ctx = workerruntime.WithTaskCapability(ctx, capability)
	case "tenant":
		tenantID = uuid.NewString()
	case "task":
		input.TaskID = uuid.NewString()
	case "source":
		runID = uuid.NewString()
	}
	if _, _, err := service.Replay(ctx, tenantID, actorID, runID, input); err == nil {
		t.Fatal("changed completion identity or full result was accepted")
	}
}

func setRemediationFault(t *testing.T, db *sql.DB, stage, target string) {
	t.Helper()
	if _, err := db.Exec(`SELECT set_config('edugrade.remediation_failure_stage',$1,false), set_config('edugrade.remediation_failure_target',$2,false)`, stage, target); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clearRemediationFault(t, db) })
}

func clearRemediationFault(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`SELECT set_config('edugrade.remediation_failure_stage','',false), set_config('edugrade.remediation_failure_target','',false)`); err != nil {
		t.Error(err)
	}
}

func TestRemediationPostgresLeaseRotationDoesNotAcceptLateCallbacks(t *testing.T) {
	db, tenantID := remediationGradingDB(t)
	store, runtime, actorID, prepared, input := seedRemediationCompletion(t, db, tenantID)
	if _, err := runtime.Fail(context.Background(), tenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: true, ErrorCode: "synthetic_retry"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE agent_worker_task SET not_before=now()-interval '1 minute' WHERE id=$1::uuid`, input.TaskID); err != nil {
		t.Fatal(err)
	}
	claimed, err := runtime.Claim(context.Background(), tenantID, workerruntime.ClaimInput{QueueName: prepared.Task.QueueName, WorkerService: prepared.Task.WorkerService, WorkerInstanceID: "worker-2", LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 || claimed[0].LeaseToken == input.LeaseToken {
		t.Fatalf("second attempt was not leased: %v", err)
	}
	current := claimed[0]
	before, _, err := store.MarkWorkerProcessing(context.Background(), tenantID, prepared.Run.ID, current.ID, current.LeaseToken, runtime)
	if err != nil {
		t.Fatal(err)
	}
	service := NewWorkerCompletionService(store, runtime)
	if _, err := service.Complete(context.Background(), tenantID, actorID, prepared, input, input.Output); !errors.Is(err, workerruntime.ErrLeaseMismatch) {
		t.Fatalf("late completion accepted: %v", err)
	}
	if _, _, err := store.MarkWorkerProcessing(context.Background(), tenantID, prepared.Run.ID, input.TaskID, input.LeaseToken, runtime); !errors.Is(err, workerruntime.ErrLeaseMismatch) {
		t.Fatalf("late preparation accepted: %v", err)
	}
	if _, err := service.Fail(context.Background(), tenantID, prepared.Run.ID, WorkerFailureInput{TaskID: input.TaskID, LeaseToken: input.LeaseToken, ErrorCode: "late_failure"}); !errors.Is(err, workerruntime.ErrLeaseMismatch) {
		t.Fatalf("late failure accepted: %v", err)
	}
	assertRemediationRunUnchanged(t, store, tenantID, before)
	if _, err := store.GetGradeByAdapterRequestID(context.Background(), tenantID, prepared.Run.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale callback left a grade: %v", err)
	}
	prepared.Task, prepared.Run, input.LeaseToken = current, before, current.LeaseToken
	if _, err := service.Complete(context.Background(), tenantID, actorID, prepared, input, input.Output); err != nil {
		t.Fatalf("current worker could not complete: %v", err)
	}
}

func TestRemediationPostgresFailureRollbackPreservesActiveTask(t *testing.T) {
	db, tenantID := remediationGradingDB(t)
	store, runtime, _, prepared, input := seedRemediationCompletion(t, db, tenantID)
	setRemediationFault(t, db, "run", prepared.Run.ID)
	service := NewWorkerCompletionService(store, runtime)
	_, err := service.Fail(context.Background(), tenantID, prepared.Run.ID, WorkerFailureInput{TaskID: input.TaskID, LeaseToken: input.LeaseToken, ErrorCode: "synthetic_failed"})
	if err == nil || !strings.Contains(err.Error(), "synthetic remediation failure at run") {
		t.Fatalf("failure callback did not reach the run fault: %v", err)
	}
	assertRemediationRunUnchanged(t, store, tenantID, prepared.Run)
	stored, err := runtime.Get(context.Background(), tenantID, input.TaskID)
	if err != nil || stored.Status != workerruntime.StatusLeased || stored.ErrorCode != "" || stored.Attempts[0].CompletedAt != nil {
		t.Fatalf("failure transaction left task/attempt changes: %v", err)
	}
	clearRemediationFault(t, db)
	if _, err := service.Fail(context.Background(), tenantID, prepared.Run.ID, WorkerFailureInput{TaskID: input.TaskID, LeaseToken: input.LeaseToken, ErrorCode: "synthetic_failed"}); err != nil {
		t.Fatalf("failure retry could not finish: %v", err)
	}
}

func assertRemediationRunUnchanged(t *testing.T, store *PostgresStore, tenantID string, before GradingRun) {
	t.Helper()
	after, err := store.GetRun(context.Background(), tenantID, before.ID)
	if err != nil || after.Status != before.Status || after.GradeID != before.GradeID || after.ErrorCode != before.ErrorCode || after.AttemptCount != before.AttemptCount || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("rejected callback altered the source run: %v", err)
	}
}

func TestRemediationPostgresPanelHumanHandoffRecoversFrozenResults(t *testing.T) {
	for _, stage := range []string{"handoff_task", "handoff_association"} {
		t.Run(stage, func(t *testing.T) { assertRemediationPostgresPanelHandoff(t, stage) })
	}
}

func assertRemediationPostgresPanelHandoff(t *testing.T, stage string) {
	t.Helper()
	db, tenantID := remediationGradingDB(t)
	store, _, actorID, prepared, _ := seedRemediationCompletion(t, db, tenantID)
	value := panelTestContext()
	value.SegmentID, value.Question.ID, value.Rubric.QuestionID = prepared.Run.AnswerSegmentID, prepared.Run.QuestionID, prepared.Run.QuestionID
	panel, err := store.GetOrCreatePanel(context.Background(), tenantID, actorID, CreatePanelInput{AnswerSegmentID: value.SegmentID, AnswerVersion: value.AnswerVersion,
		QuestionID: value.Question.ID, RubricVersion: value.Rubric.Version, MaxScore: value.Question.Score})
	if err != nil {
		t.Fatal(err)
	}
	a := &panelTestAdapter{err: errors.New("synthetic unavailable")}
	b := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .9}
	c := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .99}
	orchestrator := newTestPanelOrchestrator(t, a, b, c)
	orchestrator.store = store
	setRemediationFault(t, db, stage, panel.ID)
	if _, err := orchestrator.Grade(context.Background(), tenantID, actorID, value, PanelDecisionConfig{}); err == nil || !strings.Contains(err.Error(), "synthetic remediation failure at "+stage) {
		t.Fatalf("handoff did not reach its real database fault: %v", err)
	}
	frozen, err := store.GetPanel(context.Background(), tenantID, panel.ID)
	if err != nil || frozen.Status != PanelHumanReview || frozen.ReviewTaskID != "" || frozen.PrimaryBRunID == "" {
		t.Fatalf("handoff failure lost the frozen panel: %v", err)
	}
	clearRemediationFault(t, db)
	result, err := orchestrator.Grade(context.Background(), tenantID, actorID, value, PanelDecisionConfig{})
	if err != nil || result.Panel.Status != PanelHumanReview || result.Panel.ReviewTaskID == "" || result.Panel.PrimaryBRunID != frozen.PrimaryBRunID || result.PrimaryB.ID == "" || len(a.inputs) != 1 || len(b.inputs) != 1 || len(c.inputs) != 0 {
		t.Fatalf("handoff recovery repeated models or lost grade: %v", err)
	}
	again, err := orchestrator.Grade(context.Background(), tenantID, actorID, value, PanelDecisionConfig{})
	if err != nil || again.Panel.ReviewTaskID != result.Panel.ReviewTaskID || len(a.inputs) != 1 || len(b.inputs) != 1 {
		t.Fatalf("completed handoff replay changed identity: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM review_task WHERE tenant_id=$1::uuid AND subjective_panel_id=$2::uuid AND deleted_at IS NULL`, tenantID, panel.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("handoff must have one real review task, count=%d: %v", count, err)
	}
}
