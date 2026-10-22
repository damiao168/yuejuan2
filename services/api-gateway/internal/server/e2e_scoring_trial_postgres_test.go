package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// 从空库执行全部迁移，再调用生产存储；查询列名、触发器和取消事务都由 PostgreSQL 实际验证。
func TestScoringTrialPostgres(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	ctx := context.Background()
	policy := subjective.ModelPolicy{ModelVersion: "trial-model-v1", PromptVersion: "trial-prompt-v1", MinConfidence: .8}

	t.Run("persist results and expand once", func(t *testing.T) {
		fixture := seedScoringTrialPostgres(t, db)
		store := subjective.NewPostgresStore(db)
		if _, err := store.GetOrCreateRun(ctx, fixture.tenantID, fixture.actorID, subjective.CreateRunInput{AnswerSegmentID: fixture.segments[0], QuestionID: fixture.questionID, AnswerVersion: "answer-v1", RubricVersion: "rubric-v1", ModelVersion: policy.ModelVersion, PromptVersion: policy.PromptVersion, RequestID: uuid.NewString()}); !errors.Is(err, subjective.ErrTrialConflict) {
			t.Fatalf("direct run bypassed required trial: %v", err)
		}
		trial, err := store.CreateScoringTrial(ctx, fixture.tenantID, fixture.actorID, fixture.runID, fixture.input(), policy)
		if err != nil {
			t.Fatal(err)
		}
		if trial.Status != "awaiting_enqueue" || len(trial.RemainingSegmentIDs) != 6 || len(trial.Samples) != 5 {
			t.Fatalf("created trial: %+v", trial)
		}
		// 重建存储代表进程重启；相同命令恢复原基准与批次，不能新建第二份试改。
		store = subjective.NewPostgresStore(db)
		replay, err := store.CreateScoringTrial(ctx, fixture.tenantID, fixture.actorID, fixture.runID, fixture.input(), policy)
		if err != nil || replay.ID != trial.ID || replay.SampleBatchID != trial.SampleBatchID {
			t.Fatalf("replay: %+v %v", replay, err)
		}
		changed := fixture.input()
		changed.Samples[0].HumanScore++
		if _, err := store.CreateScoringTrial(ctx, fixture.tenantID, fixture.actorID, fixture.runID, changed, policy); !errors.Is(err, subjective.ErrIdempotencyConflict) {
			t.Fatalf("changed command accepted: %v", err)
		}
		if _, err := store.CreateBatch(ctx, fixture.tenantID, fixture.actorID, subjective.CreateBatchInput{IdempotencyKey: uuid.NewString(), ScoringRunID: fixture.runID, SegmentIDs: trial.RemainingSegmentIDs}); !errors.Is(err, subjective.ErrTrialConflict) {
			t.Fatalf("direct batch bypassed trial: %v", err)
		}
		trial, err = store.PrepareScoringTrialEnqueue(ctx, fixture.tenantID, fixture.runID, trial.ID, policy)
		if err != nil || trial.Status != "running" {
			t.Fatalf("prepare: %+v %v", trial, err)
		}
		for _, sample := range trial.Samples {
			run := seedScoringTrialRun(t, db, fixture, trial.SampleBatchID, sample.SegmentID, policy)
			var gradeID string
			err := db.QueryRow(`INSERT INTO ai_grade(tenant_id,answer_segment_id,question_id,question_no,question_type,answer_version,grader_type,rule_version,suggested_score,max_score,confidence,needs_human_review,created_by,subjective_grading_run_id,adapter_request_id,adapter_latency_ms,evidence)
VALUES($1::uuid,$2::uuid,$3::uuid,'Q1','short_answer',$7,'llm_subjective','trial-v1',3,5,.9,false,$4::uuid,$5::uuid,$6,125,'[{"type":"text_span","quote":"synthetic evidence"}]'::jsonb) RETURNING id::text`, fixture.tenantID, sample.SegmentID, fixture.questionID, fixture.actorID, run.ID, run.RequestID, run.AnswerVersion).Scan(&gradeID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.UpdateRun(ctx, fixture.tenantID, run.ID, subjective.UpdateRunInput{Status: subjective.RunSucceeded, GradeID: gradeID}); err != nil {
				t.Fatal(err)
			}
			mustScoringTrialExec(t, db, `INSERT INTO model_usage_event(tenant_id,request_id,feature,provider_key,model_name,total_tokens,status) VALUES($1::uuid,$2,'subjective_grading','synthetic','trial-model-v1',42,'succeeded') ON CONFLICT(tenant_id,request_id,feature) DO UPDATE SET total_tokens=42`, fixture.tenantID, run.RequestID)
			var needsReview bool
			if err := db.QueryRow(`SELECT needs_human_review FROM ai_grade WHERE id=$1::uuid`, gradeID).Scan(&needsReview); err != nil || !needsReview {
				t.Fatalf("sample grade must retain human review: %v %v", needsReview, err)
			}
		}
		trials, err := store.ListScoringTrials(ctx, fixture.tenantID, fixture.runID, policy)
		if err != nil || len(trials) != 1 || trials[0].Status != "reviewing" {
			t.Fatalf("results: %+v %v", trials, err)
		}
		trial = trials[0]
		for _, sample := range trial.Samples {
			result := sample.Result
			if result == nil || result.SuggestedScore == nil || *result.SuggestedScore != 3 || result.LatencyMS == nil || *result.LatencyMS != 125 || result.TotalTokens == nil || *result.TotalTokens != 42 || len(result.Evidence) != 1 || !slices.Contains(result.RiskFlags, "trial_sample") {
				t.Fatalf("persisted sample result: %+v", result)
			}
		}
		decision := subjective.TrialDecisionInput{Decision: "expand", Reason: "人工核对样本后扩大建议生成", ExpectedRevision: trial.Revision}
		expanded, err := store.DecideScoringTrial(ctx, fixture.tenantID, fixture.actorID, fixture.runID, trial.ID, decision, policy)
		if err != nil || expanded.Status != "expanded" || expanded.ExpansionBatchID == "" {
			t.Fatalf("expand: %+v %v", expanded, err)
		}
		batch, err := store.GetBatch(ctx, fixture.tenantID, expanded.ExpansionBatchID)
		if err != nil || !slices.Equal(batch.SegmentIDs, trial.RemainingSegmentIDs) {
			t.Fatalf("expansion scope: %+v %v", batch, err)
		}
		replay, err = store.DecideScoringTrial(ctx, fixture.tenantID, fixture.actorID, fixture.runID, trial.ID, decision, policy)
		if err != nil || replay.ExpansionBatchID != expanded.ExpansionBatchID || replay.Revision != expanded.Revision {
			t.Fatalf("expansion replay: %+v %v", replay, err)
		}
		if err := store.AuthorizeTrialBatch(ctx, fixture.tenantID, expanded.ExpansionBatchID, policy); err != nil {
			t.Fatalf("authorized expansion: %v", err)
		}
		assertScoringTrialRLSAndArchive(t, db, fixture, trial.ID)
	})

	for _, scenario := range []string{"stop", "answer version changed", "policy version changed", "assessment version changed"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := seedScoringTrialPostgres(t, db)
			store := subjective.NewPostgresStore(db)
			trial, err := store.CreateScoringTrial(ctx, fixture.tenantID, fixture.actorID, fixture.runID, fixture.input(), policy)
			if err != nil {
				t.Fatal(err)
			}
			trial, err = store.PrepareScoringTrialEnqueue(ctx, fixture.tenantID, fixture.runID, trial.ID, policy)
			if err != nil {
				t.Fatal(err)
			}
			run := seedScoringTrialRun(t, db, fixture, trial.SampleBatchID, trial.Samples[0].SegmentID, policy)
			runtime := workerruntime.NewPostgresStore(db)
			task, err := runtime.CreateTask(ctx, fixture.tenantID, fixture.actorID, workerruntime.CreateTaskInput{TaskType: "ai_grade", QueueName: "trial-" + trial.ID, SourceType: "subjective_grading_run", SourceID: run.ID, PayloadSchemaVersion: "subjective-grade-v1", Payload: map[string]any{"run_id": run.ID}, IdempotencyKey: run.RequestID, MaxAttempts: 3})
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := runtime.Claim(ctx, fixture.tenantID, workerruntime.ClaimInput{QueueName: task.QueueName, WorkerService: "subjective-grading-worker", WorkerInstanceID: "trial-worker", LeaseSeconds: 300, Limit: 1})
			if err != nil || len(claimed) != 1 {
				t.Fatalf("claim: %+v %v", claimed, err)
			}
			wantStatus := "invalidated"
			currentPolicy := policy
			if scenario == "stop" {
				wantStatus = "stopped"
				trial, err = store.DecideScoringTrial(ctx, fixture.tenantID, fixture.actorID, fixture.runID, trial.ID, subjective.TrialDecisionInput{Decision: "stop", Reason: "样本证据需要调整", ExpectedRevision: trial.Revision}, policy)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if scenario == "answer version changed" {
					mustScoringTrialExec(t, db, `INSERT INTO answer_segment_answer(tenant_id,answer_segment_id,answer_text,source,recorded_by) VALUES($1::uuid,$2::uuid,'changed synthetic answer','manual_entry',$3::uuid)`, fixture.tenantID, run.AnswerSegmentID, fixture.actorID)
				} else if scenario == "assessment version changed" {
					mustScoringTrialExec(t, db, `INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,snapshot_version,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) SELECT tenant_id,exam_id,question_id,snapshot_version+1,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,repeat('b',64) FROM exam_question_snapshot WHERE tenant_id=$1::uuid AND question_id=$2::uuid`, fixture.tenantID, fixture.questionID)
				} else {
					currentPolicy.ModelVersion = "trial-model-v2"
				}
				if _, err := store.PrepareScoringTrialEnqueue(ctx, fixture.tenantID, fixture.runID, trial.ID, currentPolicy); !errors.Is(err, subjective.ErrTrialConflict) {
					t.Fatalf("changed version re-enqueued: %v", err)
				}
			}
			// 冲突命令也必须提交作废与取消事实；再次从数据库读取，防止只修改了返回对象。
			trials, err := subjective.NewPostgresStore(db).ListScoringTrials(ctx, fixture.tenantID, fixture.runID, currentPolicy)
			if err != nil || len(trials) != 1 || trials[0].Status != wantStatus {
				t.Fatalf("persisted terminal trial: %+v %v", trials, err)
			}
			batch, batchErr := store.GetBatch(ctx, fixture.tenantID, trial.SampleBatchID)
			closedRun, runErr := store.GetRun(ctx, fixture.tenantID, run.ID)
			closedTask, taskErr := runtime.Get(ctx, fixture.tenantID, task.ID)
			if batchErr != nil || runErr != nil || taskErr != nil || batch.Status != "cancelled" || closedRun.Status != "failed" || closedRun.ErrorCode != "scoring_trial_stopped" || closedTask.Status != "cancelled" {
				t.Fatalf("cancelled facts: batch=%+v run=%+v task=%+v errors=%v/%v/%v", batch, closedRun, closedTask, batchErr, runErr, taskErr)
			}
			var attemptStatus, errorCode string
			if err := db.QueryRow(`SELECT status,error_code FROM agent_worker_task_attempt WHERE tenant_id=$1::uuid AND task_id=$2::uuid`, fixture.tenantID, task.ID).Scan(&attemptStatus, &errorCode); err != nil || attemptStatus != "cancelled" || errorCode != "scoring_trial_stopped" {
				t.Fatalf("cancelled attempt: %s %s %v", attemptStatus, errorCode, err)
			}
			if _, err := runtime.Complete(ctx, fixture.tenantID, task.ID, workerruntime.CompleteInput{LeaseToken: claimed[0].LeaseToken, ResultSchemaVersion: "subjective-grade-v1", Result: map[string]any{}}); err == nil {
				t.Fatal("late worker completion revived a cancelled task")
			}
			if _, err := store.CompleteWorkerAtomic(ctx, fixture.tenantID, fixture.actorID, subjective.WorkerExecutionContext{Run: run, Task: claimed[0]}, subjective.WorkerResultInput{TaskID: task.ID, LeaseToken: claimed[0].LeaseToken, ResultSchemaVersion: "subjective-grade-v1"}, subjective.Grade{}, runtime); err == nil {
				t.Fatal("late domain completion revived a cancelled run")
			}
			if _, err := store.GetOrCreateRun(ctx, fixture.tenantID, fixture.actorID, subjective.CreateRunInput{BatchID: trial.SampleBatchID, AnswerSegmentID: run.AnswerSegmentID, QuestionID: fixture.questionID, AnswerVersion: "answer-v1", RubricVersion: "rubric-v1", ModelVersion: policy.ModelVersion, PromptVersion: policy.PromptVersion, RequestID: uuid.NewString()}); err == nil {
				t.Fatal("cancelled sample batch accepted a new run")
			}
			if err := store.AuthorizeTrialBatch(ctx, fixture.tenantID, trial.SampleBatchID, currentPolicy); !errors.Is(err, subjective.ErrTrialConflict) {
				t.Fatalf("terminal trial authorized batch: %v", err)
			}
			if scenario == "stop" || scenario == "answer version changed" {
				replacement := fixture.input()
				replacement.CommandID = uuid.NewString()
				if scenario == "stop" {
					if _, err := store.CreateScoringTrial(ctx, fixture.tenantID, fixture.actorID, fixture.runID, replacement, currentPolicy); !errors.Is(err, subjective.ErrInvalidInput) {
						t.Fatalf("previously AI-exposed sample reused as independent baseline: %v", err)
					}
					for index := range replacement.Samples {
						replacement.Samples[index].SegmentID = fixture.segments[index+5]
					}
				}
				// 停止不抹去旧版答案的 AI 暴露事实；未运行的新样本或新答案版本仍可独立试改。
				if next, err := store.CreateScoringTrial(ctx, fixture.tenantID, fixture.actorID, fixture.runID, replacement, currentPolicy); err != nil || next.ID == trial.ID || len(next.Samples) != 5 {
					t.Fatalf("fresh baseline rejected: %+v %v", next, err)
				}
			}
		})
	}
}

type scoringTrialPostgresFixture struct {
	tenantID, actorID, runID, examID, questionID string
	segments                                     []string
}

func (f scoringTrialPostgresFixture) input() subjective.CreateTrialInput {
	samples := make([]subjective.TrialSampleInput, 5)
	for index := range samples {
		samples[index] = subjective.TrialSampleInput{SegmentID: f.segments[index], HumanScore: float64(index), Roles: []string{"representative"}}
	}
	samples[0].Roles = append(samples[0].Roles, "boundary", "incomplete_evidence")
	return subjective.CreateTrialInput{CommandID: "trial-" + f.runID, QuestionID: f.questionID, Samples: samples}
}

// 只预置业务前提，不调用评分编排器；测试对象是试改持久化事务，避免其他 worker 自动改写范围。
func seedScoringTrialPostgres(t *testing.T, db *sql.DB) scoringTrialPostgresFixture {
	t.Helper()
	var f scoringTrialPostgresFixture
	if err := db.QueryRow(`SELECT tenant.id::text,app_user.id::text FROM tenant JOIN app_user ON app_user.tenant_id=tenant.id WHERE tenant.code='demo' AND app_user.username='tenant_admin'`).Scan(&f.tenantID, &f.actorID); err != nil {
		t.Fatal(err)
	}
	var schoolID, fileID, submissionID, pageID string
	for _, query := range []struct {
		sql    string
		args   []any
		target *string
	}{
		{`INSERT INTO school(tenant_id,name,code,status) VALUES($1::uuid,'试改合成测试学校',$2,'active') RETURNING id::text`, []any{f.tenantID, uuid.NewString()}, &schoolID},
		{`INSERT INTO exam(tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1::uuid,$2::uuid,'试改合成测试','physics','quiz',5,'draft','ai_assisted','manual_after_confirmation',$3::uuid) RETURNING id::text`, []any{f.tenantID, &schoolID, f.actorID}, &f.examID},
		{`INSERT INTO question(tenant_id,exam_id,question_no,question_type,score,stem,sort_order,status) VALUES($1::uuid,$2::uuid,'Q1','short_answer',5,'合成题目',1,'active') RETURNING id::text`, []any{f.tenantID, &f.examID}, &f.questionID},
		{`INSERT INTO scoring_run(tenant_id,exam_id,idempotency_key,status,started_by) VALUES($1::uuid,$2::uuid,$3,'needs_review',$4::uuid) RETURNING id::text`, []any{f.tenantID, &f.examID, uuid.NewString(), f.actorID}, &f.runID},
		{`INSERT INTO file_asset(tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,'exam',$2::uuid,'synthetic.png','image/png',1,repeat('a',64),'test',$3,'tenant',$4::uuid) RETURNING id::text`, []any{f.tenantID, &f.examID, uuid.NewString(), f.actorID}, &fileID},
	} {
		for index, arg := range query.args {
			if reference, ok := arg.(*string); ok {
				query.args[index] = *reference
			}
		}
		if err := db.QueryRow(query.sql, query.args...).Scan(query.target); err != nil {
			t.Fatalf("seed trial prerequisite: %v", err)
		}
	}
	mustScoringTrialExec(t, db, `INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) SELECT $1::uuid,$2::uuid,$3::uuid,id,version,'structured_steps','["text_span"]'::jsonb,'R2','{}'::jsonb,'{}'::jsonb,'{"max_score":5}'::jsonb,'{"mode":"AI_ASSIST"}'::jsonb,repeat('a',64) FROM subject_profile WHERE tenant_id=$1::uuid AND subject_code='physics' AND education_stage='senior' LIMIT 1`, f.tenantID, f.examID, f.questionID)
	for index := 0; index < 11; index++ {
		var segmentID string
		if err := db.QueryRow(`INSERT INTO submission(tenant_id,exam_id,source_type,status,collected_by) VALUES($1::uuid,$2::uuid,'manual_import','created',$3::uuid) RETURNING id::text`, f.tenantID, f.examID, f.actorID).Scan(&submissionID); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`INSERT INTO submission_page(tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,1,'accepted') RETURNING id::text`, f.tenantID, submissionID, fileID).Scan(&pageID); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`INSERT INTO answer_segment(tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Q1','{}'::jsonb,'manual','accepted') RETURNING id::text`, f.tenantID, submissionID, pageID, f.questionID).Scan(&segmentID); err != nil {
			t.Fatal(err)
		}
		f.segments = append(f.segments, segmentID)
		mustScoringTrialExec(t, db, `INSERT INTO answer_segment_answer(tenant_id,answer_segment_id,answer_text,source,recorded_by) VALUES($1::uuid,$2::uuid,$3,'manual_entry',$4::uuid)`, f.tenantID, segmentID, fmt.Sprintf("synthetic answer %d", index), f.actorID)
		mustScoringTrialExec(t, db, `INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,created_by,scoring_run_id) VALUES($1::uuid,$2::uuid,$3::uuid,'Q1',$4::uuid,$5::uuid,$6,'subjective_default_review','pending',$7::uuid,$8::uuid)`, f.tenantID, f.examID, f.questionID, segmentID, submissionID, uuid.NewString(), f.actorID, f.runID)
	}
	return f
}

func seedScoringTrialRun(t *testing.T, db *sql.DB, f scoringTrialPostgresFixture, batchID, segmentID string, policy subjective.ModelPolicy) subjective.GradingRun {
	t.Helper()
	var answerVersion string
	if err := db.QueryRow(`SELECT id::text FROM answer_segment_answer WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1`, f.tenantID, segmentID).Scan(&answerVersion); err != nil {
		t.Fatal(err)
	}
	run, err := subjective.NewPostgresStore(db).GetOrCreateRun(context.Background(), f.tenantID, f.actorID, subjective.CreateRunInput{BatchID: batchID, AnswerSegmentID: segmentID, QuestionID: f.questionID, AnswerVersion: answerVersion, RubricVersion: "rubric-v1", ModelVersion: policy.ModelVersion, PromptVersion: policy.PromptVersion, MinConfidence: policy.MinConfidence, RequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func mustScoringTrialExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("trial database mutation: %v", err)
	}
}

func assertScoringTrialRLSAndArchive(t *testing.T, db *sql.DB, f scoringTrialPostgresFixture, trialID string) {
	t.Helper()
	for _, scope := range []struct {
		tenantID string
		count    int
	}{{"", 0}, {uuid.NewString(), 0}, {f.tenantID, 1}} {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`SET LOCAL ROLE edugrade_tenant_runtime`); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if _, err := tx.Exec(`SELECT set_config('edugrade.tenant_id',$1,true)`, scope.tenantID); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		var count int
		err = tx.QueryRow(`SELECT count(*) FROM scoring_ai_trial WHERE id=$1::uuid`, trialID).Scan(&count)
		var affected int64
		if err == nil {
			result, updateErr := tx.Exec(`UPDATE scoring_ai_trial SET revision=revision+1 WHERE id=$1::uuid`, trialID)
			err = updateErr
			if err == nil {
				affected, err = result.RowsAffected()
			}
		}
		tx.Rollback()
		if err != nil || count != scope.count || affected != int64(scope.count) {
			t.Fatalf("trial RLS scope=%q count=%d affected=%d want=%d err=%v", scope.tenantID, count, affected, scope.count, err)
		}
	}
	var canDelete bool
	if err := db.QueryRow(`SELECT has_table_privilege('edugrade_tenant_runtime','scoring_ai_trial','DELETE')`).Scan(&canDelete); err != nil || canDelete {
		t.Fatalf("runtime can delete frozen human baselines: %v %v", canDelete, err)
	}
	if _, err := subjective.NewPostgresStore(db).ListScoringTrials(context.Background(), uuid.NewString(), f.runID, subjective.ModelPolicy{}); !errors.Is(err, subjective.ErrNotFound) {
		t.Fatalf("foreign tenant recovered run: %v", err)
	}
	if _, err := db.Exec(`UPDATE scoring_ai_trial SET sample_batch_id=$2::uuid WHERE id=$1::uuid`, trialID, uuid.NewString()); err == nil {
		t.Fatal("trial accepted an absent sample batch")
	} else {
		var pgError *pgconn.PgError
		if !errors.As(err, &pgError) || pgError.Code != "23503" {
			t.Fatalf("sample batch must be protected by its real foreign key: %v", err)
		}
	}
	mustScoringTrialExec(t, db, `UPDATE exam SET archived_at=now(),archived_by=$2::uuid WHERE id=$1::uuid`, f.examID, f.actorID)
	if _, err := db.Exec(`UPDATE scoring_ai_trial SET revision=revision+1 WHERE id=$1::uuid`, trialID); err == nil {
		t.Fatal("archived exam accepted trial mutation")
	}
}
