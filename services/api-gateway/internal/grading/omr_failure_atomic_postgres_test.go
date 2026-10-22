package grading

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
)

type omrFailureScenario struct {
	tenant, actor, run, omr string
	first                   workerruntime.Task
}

func seedOMRFailureScenario(t *testing.T, ctx context.Context, db *sql.DB, maxAttempts int) omrFailureScenario {
	t.Helper()
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	s := omrFailureScenario{tenant: uuid.NewString(), actor: uuid.NewString(), run: uuid.NewString(), omr: uuid.NewString()}
	exam, question, submission, segment, snapshot := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	mustExec(`INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'Synthetic OMR failure',$2,'active')`, s.tenant, "omr-failure-"+s.tenant)
	mustExec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1,$2,$3,'1','single_choice',5,1,'draft')`, question, s.tenant, exam)
	mustExec(`INSERT INTO submission(id,tenant_id,exam_id,candidate_no,source_type,status,collected_by) VALUES($1,$2,$3,'synthetic','manual_import','created',$4)`, submission, s.tenant, exam, s.actor)
	mustExec(`INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status,crop_sha256) VALUES($1,$2,$3,$4,$5,'1','{"x":0,"y":0,"width":1,"height":1}','manual','accepted',$6)`, segment, s.tenant, submission, uuid.NewString(), question, strings.Repeat("a", 64))
	mustExec(`INSERT INTO exam_question_snapshot(id,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1,$2,$3,$4,$5,1,'choice','[]','R1','{}','{}','{}','{}',$6)`, snapshot, s.tenant, exam, question, uuid.NewString(), strings.Repeat("b", 64))
	mustExec(`INSERT INTO scoring_run(id,tenant_id,exam_id,idempotency_key,status,total_count,queued_count,started_by) VALUES($1,$2,$3,'synthetic','processing',1,1,$4)`, s.run, s.tenant, exam, s.actor)
	runtime := workerruntime.NewPostgresStore(db)
	task, err := runtime.CreateTask(ctx, s.tenant, s.actor, workerruntime.CreateTaskInput{
		TaskType: "omr_extract", QueueName: "page-processing", SourceType: "omr_run", SourceID: s.omr,
		PayloadSchemaVersion: "omr-task-v1", IdempotencyKey: "omr:" + s.omr, Payload: map[string]any{}, RetryBackoffSeconds: 1, MaxAttempts: maxAttempts,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(`INSERT INTO omr_run(id,tenant_id,scoring_run_id,answer_segment_id,crop_file_asset_id,crop_sha256,template_id,template_content_hash,profile_version,profile_hash,status,runtime_task_id,auto_confirm_eligible,auto_confirm_reason) VALUES($1,$2,$3,$4,$5,$6,$5,$6,'fixture-v1','fixture-hash','processing',$7,false,'requires_review')`, s.omr, s.tenant, s.run, segment, uuid.NewString(), strings.Repeat("a", 64), task.ID)
	items, err := runtime.Claim(ctx, s.tenant, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "page-processing", WorkerInstanceID: "omr-failure-first", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(items) != 1 {
		t.Fatalf("initial claim items=%#v err=%v", items, err)
	}
	s.first = items[0]
	return s
}

func omrFailureRequest(t *testing.T, scenario omrFailureScenario, input OMRFailureInput, service bool) *http.Request {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/internal/omr-runs/"+scenario.omr+"/failure", bytes.NewReader(body))
	r.SetPathValue("runId", scenario.omr)
	user := auth.User{ID: scenario.actor, TenantID: scenario.tenant, Roles: []string{"school_admin"}, Permissions: []string{"ocr:manage"}}
	if service {
		user.Roles = []string{"page_processing_worker"}
	}
	r = r.WithContext(auth.WithUser(r.Context(), user))
	r.Header.Set(workerruntime.TaskIDHeader, scenario.first.ID)
	r.Header.Set(workerruntime.TaskLeaseHeader, scenario.first.LeaseToken)
	r.Header.Set(workerruntime.WorkerServiceHeader, scenario.first.WorkerService)
	r.Header.Set(workerruntime.WorkerInstanceIDHeader, scenario.first.WorkerInstanceID)
	return r
}

// 在来源更新或汇总刷新时由真实 PostgreSQL trigger 抛错，检查生产 Handler 不会先提交 runtime。
// 复用隔离 schema 夹具，不复制线上外键/RLS；人工通用 requeue 只是旁路恢复，不能视为评分重试成功。
func TestPostgresOMRFailureCallbackRollsBackAllStatesOnDomainWriteError(t *testing.T) {
	for _, failAt := range []string{"omr_run", "scoring_run"} {
		for _, kind := range []string{"terminal", "attempts_exhausted", "retryable"} {
			t.Run(failAt+"/"+kind, func(t *testing.T) {
				ctx, db := newOMRRetryFixture(t)
				maxAttempts := 3
				if kind == "attempts_exhausted" {
					maxAttempts = 1
				}
				s := seedOMRFailureScenario(t, ctx, db, maxAttempts)
				store, runtime := NewPostgresStore(db), workerruntime.NewPostgresStore(db)
				handler := NewHandler(store, nil, nil)
				handler.SetProductionDependencies(runtime, nil)
				scoped := workerruntime.TaskScope(runtime)(workerruntime.RequireTaskSource("omr_run", "runId")(http.HandlerFunc(handler.FailOMR)))
				input := OMRFailureInput{TaskID: s.first.ID, LeaseToken: s.first.LeaseToken, Retryable: kind != "terminal", ErrorCode: "synthetic_omr_failure", ErrorDetail: map[string]any{"fixture": true}, DurationMS: 7}
				if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_omr_domain_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic_omr_domain_write_failure'; END $$`); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_omr_domain_write BEFORE UPDATE ON `+failAt+` FOR EACH ROW EXECUTE FUNCTION reject_omr_domain_write()`); err != nil {
					t.Fatal(err)
				}
				response := httptest.NewRecorder()
				scoped.ServeHTTP(response, omrFailureRequest(t, s, input, true))
				if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "grading_operation_failed") {
					t.Fatalf("fault fixture did not reach domain write: %d %s", response.Code, response.Body.String())
				}
				current, err := runtime.Get(ctx, s.tenant, s.first.ID)
				if err != nil {
					t.Fatal(err)
				}
				var omrStatus, scoringStatus string
				var queued, failed int
				if err := db.QueryRowContext(ctx, `SELECT o.status,sr.status,sr.queued_count,sr.failed_count FROM omr_run o JOIN scoring_run sr ON sr.tenant_id=o.tenant_id AND sr.id=o.scoring_run_id WHERE o.tenant_id=$1 AND o.id=$2`, s.tenant, s.omr).Scan(&omrStatus, &scoringStatus, &queued, &failed); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_omr_domain_write ON `+failAt); err != nil {
					t.Fatal(err)
				}
				atomic := current.Status == workerruntime.StatusLeased && current.Revision == s.first.Revision && current.ErrorCode == "" && len(current.Attempts) == 1 && current.Attempts[0].CompletedAt == nil && omrStatus == "processing" && scoringStatus == "processing" && queued == 1 && failed == 0
				if !atomic {
					// 红阶段先保留恢复路径事实，再让原子性断言失败；不把人工底层 requeue 隐瞒成“绝无恢复”。
					workerReplay, humanReplay := httptest.NewRecorder(), httptest.NewRecorder()
					scoped.ServeHTTP(workerReplay, omrFailureRequest(t, s, input, true))
					handler.FailOMR(humanReplay, omrFailureRequest(t, s, input, false))
					claims, claimErr := runtime.Claim(ctx, s.tenant, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "page-processing", WorkerInstanceID: "recovery-probe", Limit: 1, LeaseSeconds: 300})
					failedOMR, listErr := store.ListFailedOMRTasks(ctx, s.tenant, s.run)
					retryRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scoring-runs/"+s.run+"/retry-failed", nil)
					retryRequest.SetPathValue("runId", s.run)
					retryRequest = retryRequest.WithContext(auth.WithUser(retryRequest.Context(), auth.User{ID: s.actor, TenantID: s.tenant}))
					retryResponse := httptest.NewRecorder()
					handler.RetryFailedScoringRun(retryResponse, retryRequest)
					t.Logf("partial commit runtime=%s omr=%s scoring=%s queued=%d failed=%d attempts=%#v; workerReplay=%d humanReplay=%d claim=%d/%v failedList=%d/%v scoringRetry=%d %s", current.Status, omrStatus, scoringStatus, queued, failed, current.Attempts, workerReplay.Code, humanReplay.Code, len(claims), claimErr, len(failedOMR), listErr, retryResponse.Code, retryResponse.Body.String())
					if current.Status == workerruntime.StatusFailed || current.Status == workerruntime.StatusDeadLetter {
						runtimeHandler := workerruntime.NewHandler(runtime, nil)
						manual := httptest.NewRequest(http.MethodPost, "/api/v1/internal/worker/tasks/"+s.first.ID+"/requeue", nil)
						manual.SetPathValue("taskId", s.first.ID)
						manual = manual.WithContext(auth.WithUser(manual.Context(), auth.User{ID: s.actor, TenantID: s.tenant, Permissions: []string{"ocr:manage"}}))
						manualResponse := httptest.NewRecorder()
						auth.RequireAnyPermission("ocr:manage")(workerruntime.TaskScope(runtime)(http.HandlerFunc(runtimeHandler.Requeue))).ServeHTTP(manualResponse, manual)
						t.Logf("manual OCR-authorized generic requeue=%d %s", manualResponse.Code, manualResponse.Body.String())
						if manualResponse.Code == http.StatusOK {
							items, claimErr := runtime.Claim(ctx, s.tenant, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "page-processing", WorkerInstanceID: "manual-recovery", Limit: 1, LeaseSeconds: 300})
							if claimErr == nil && len(items) == 1 {
								completed, _, completeErr := store.ApplyOMRResult(ctx, s.tenant, s.omr, s.actor, OMRResultInput{TaskID: items[0].ID, LeaseToken: items[0].LeaseToken, ResultVersion: "fixture-result-v1", Decision: "selected", Confidence: .95, Selected: []string{"A"}, Measurements: []map[string]any{}, ProfileVersion: "fixture-v1", ProfileHash: "fixture-hash", OverlayFileAssetID: uuid.NewString()}, nil)
								t.Logf("manual fallback actual claim+domain completion status=%s err=%v", completed.Status, completeErr)
							} else {
								t.Logf("manual fallback actual claim count=%d err=%v", len(items), claimErr)
							}
						}
					}
					t.Fatalf("domain write failure partially committed worker state: runtime=%s revision=%d/%d omr=%s scoring=%s", current.Status, current.Revision, s.first.Revision, omrStatus, scoringStatus)
				}
				// 移除故障后用同一租约重新提交；失败前没有消耗租约或关闭 attempt。
				response = httptest.NewRecorder()
				scoped.ServeHTTP(response, omrFailureRequest(t, s, input, true))
				if response.Code != http.StatusOK {
					t.Fatalf("same lease cannot recover after rollback: %d %s", response.Code, response.Body.String())
				}
				wantRuntime, wantOMR, wantScoring := workerruntime.StatusFailed, "terminal_error", "failed"
				wantQueued, wantFailed := 0, 1
				if kind == "retryable" {
					wantRuntime, wantOMR, wantScoring = workerruntime.StatusQueued, "retryable_error", "processing"
					wantQueued, wantFailed = 1, 0
				} else if kind == "attempts_exhausted" {
					wantRuntime = workerruntime.StatusDeadLetter
				}
				current, err = runtime.Get(ctx, s.tenant, s.first.ID)
				if err != nil {
					t.Fatal(err)
				}
				out, err := store.GetOMRRun(ctx, s.tenant, s.omr)
				if err != nil {
					t.Fatal(err)
				}
				var summary ScoringRun
				summary, err = store.RefreshScoringRun(ctx, s.tenant, s.run)
				if err != nil || current.Status != wantRuntime || out.Status != wantOMR || summary.Status != wantScoring || summary.QueuedCount != wantQueued || summary.FailedCount != wantFailed || len(current.Attempts) != 1 || current.Attempts[0].CompletedAt == nil {
					t.Fatalf("failure states diverged: runtime=%#v omr=%#v summary=%#v err=%v", current, out, summary, err)
				}
				t.Logf("rollback then retry committed all states: runtime=%s omr=%s scoring=%s queued=%d failed=%d connections_in_use=%d", current.Status, out.Status, summary.Status, summary.QueuedCount, summary.FailedCount, db.Stats().InUse)
			})
		}
	}
}

// 评分重试的准备、runtime 入队和汇总必须同事务；不能依靠失败后尽力恢复来填补提交窗口。
func TestPostgresOMRScoringRetryRollsBackRuntimeAndSourceOnWriteError(t *testing.T) {
	for _, failAt := range []string{"runtime_and_compensation", "scoring_summary"} {
		t.Run(failAt, func(t *testing.T) {
			ctx, db := newOMRRetryFixture(t)
			s := seedOMRFailureScenario(t, ctx, db, 1)
			store, runtime := NewPostgresStore(db), workerruntime.NewPostgresStore(db)
			handler := NewHandler(store, nil, nil)
			handler.SetProductionDependencies(runtime, nil)
			failure := httptest.NewRecorder()
			handler.FailOMR(failure, omrFailureRequest(t, s, OMRFailureInput{TaskID: s.first.ID, LeaseToken: s.first.LeaseToken, Retryable: true, ErrorCode: "synthetic_exhausted"}, false))
			if failure.Code != http.StatusOK {
				t.Fatalf("initial terminal failure returned %d %s", failure.Code, failure.Body.String())
			}
			before, err := runtime.Get(ctx, s.tenant, s.first.ID)
			if err != nil || before.Status != workerruntime.StatusDeadLetter {
				t.Fatalf("initial runtime state %#v err=%v", before, err)
			}
			mustExec := func(query string) {
				t.Helper()
				if _, err := db.ExecContext(ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			mustExec(`CREATE FUNCTION reject_omr_retry_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic_omr_retry_write_failure'; END $$`)
			if failAt == "runtime_and_compensation" {
				mustExec(`CREATE TRIGGER reject_runtime_requeue BEFORE UPDATE ON agent_worker_task FOR EACH ROW WHEN (NEW.status='queued') EXECUTE FUNCTION reject_omr_retry_write()`)
				mustExec(`CREATE TRIGGER reject_retry_compensation BEFORE UPDATE ON omr_run FOR EACH ROW WHEN (OLD.status='queued' AND NEW.status='terminal_error') EXECUTE FUNCTION reject_omr_retry_write()`)
			} else {
				mustExec(`CREATE TRIGGER reject_retry_summary BEFORE UPDATE ON scoring_run FOR EACH ROW EXECUTE FUNCTION reject_omr_retry_write()`)
			}
			retry := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, "/api/v1/scoring-runs/"+s.run+"/retry-failed", nil)
				r.SetPathValue("runId", s.run)
				r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: s.actor, TenantID: s.tenant}))
				response := httptest.NewRecorder()
				handler.RetryFailedScoringRun(response, r)
				return response
			}
			response := retry()
			wantCode := http.StatusOK
			if failAt == "scoring_summary" {
				wantCode = http.StatusInternalServerError
			}
			if response.Code != wantCode {
				t.Fatalf("retry fault was not reached: %d %s", response.Code, response.Body.String())
			}
			current, err := runtime.Get(ctx, s.tenant, s.first.ID)
			if err != nil {
				t.Fatal(err)
			}
			out, err := store.GetOMRRun(ctx, s.tenant, s.omr)
			if err != nil {
				t.Fatal(err)
			}
			var status string
			var queued, failed int
			if err := db.QueryRowContext(ctx, `SELECT status,queued_count,failed_count FROM scoring_run WHERE tenant_id=$1 AND id=$2`, s.tenant, s.run).Scan(&status, &queued, &failed); err != nil {
				t.Fatal(err)
			}
			if current.Status != workerruntime.StatusDeadLetter || current.Revision != before.Revision || out.Status != "terminal_error" || status != "failed" || queued != 0 || failed != 1 {
				eligible, listErr := store.ListFailedOMRTasks(ctx, s.tenant, s.run)
				t.Fatalf("scoring retry partially committed: runtime=%s revision=%d/%d omr=%s scoring=%s queued=%d failed=%d retryable_items=%d/%v response=%d %s", current.Status, current.Revision, before.Revision, out.Status, status, queued, failed, len(eligible), listErr, response.Code, response.Body.String())
			}
			if failAt == "runtime_and_compensation" {
				mustExec(`DROP TRIGGER reject_runtime_requeue ON agent_worker_task`)
				mustExec(`DROP TRIGGER reject_retry_compensation ON omr_run`)
			} else {
				mustExec(`DROP TRIGGER reject_retry_summary ON scoring_run`)
			}
			response = retry()
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"requeued":1`) {
				t.Fatalf("retry after removing transient fault failed: %d %s", response.Code, response.Body.String())
			}
			current, err = runtime.Get(ctx, s.tenant, s.first.ID)
			if err != nil || current.Status != workerruntime.StatusQueued || current.MaxAttempts != 2 || current.AttemptCount != 1 {
				t.Fatalf("manual retry did not preserve attempt budget semantics: %#v err=%v", current, err)
			}
			items, err := runtime.Claim(ctx, s.tenant, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "page-processing", WorkerInstanceID: "omr-retry-after-rollback", Limit: 1, LeaseSeconds: 300})
			if err != nil || len(items) != 1 || items[0].AttemptCount != 2 {
				t.Fatalf("retried task could not be reclaimed: %#v err=%v", items, err)
			}
			completed, _, err := store.ApplyOMRResult(ctx, s.tenant, s.omr, s.actor, OMRResultInput{
				TaskID: items[0].ID, LeaseToken: items[0].LeaseToken, ResultVersion: "fixture-result-v1", Decision: "selected", Confidence: .95,
				Selected: []string{"A"}, Measurements: []map[string]any{}, ProfileVersion: "fixture-v1", ProfileHash: "fixture-hash", OverlayFileAssetID: uuid.NewString(),
			}, nil)
			if err != nil || completed.Status != "completed" {
				t.Fatalf("recovered scoring retry could not finish: %#v err=%v", completed, err)
			}
			t.Logf("retry rollback preserved terminal pair; later retry+claim+complete succeeded, connections_in_use=%d", db.Stats().InUse)
		})
	}
}

func TestPostgresOMRFailureRejectsInactiveSourceOrLeaseWithoutRuntimeChanges(t *testing.T) {
	for _, blocked := range []string{"cancelled", "cancelling", "terminal_omr", "stale_lease", "expired_lease"} {
		t.Run(blocked, func(t *testing.T) {
			ctx, db := newOMRRetryFixture(t)
			s := seedOMRFailureScenario(t, ctx, db, 3)
			input := OMRFailureInput{TaskID: s.first.ID, LeaseToken: s.first.LeaseToken, ErrorCode: "synthetic_rejected_failure"}
			var err error
			switch blocked {
			case "cancelled", "cancelling":
				_, err = db.ExecContext(ctx, `UPDATE scoring_run SET status=$2 WHERE id=$1`, s.run, blocked)
			case "terminal_omr":
				_, err = db.ExecContext(ctx, `UPDATE omr_run SET status='terminal_error' WHERE id=$1`, s.omr)
			case "stale_lease":
				input.LeaseToken = uuid.NewString()
			case "expired_lease":
				_, err = db.ExecContext(ctx, `UPDATE agent_worker_task SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, s.first.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			runtime := workerruntime.NewPostgresStore(db)
			handler := NewHandler(NewPostgresStore(db), nil, nil)
			handler.SetProductionDependencies(runtime, nil)
			response := httptest.NewRecorder()
			// 人工上下文绕过前置 TaskScope，直接验证存储命令自身仍拒绝过期租约和停止的来源。
			handler.FailOMR(response, omrFailureRequest(t, s, input, false))
			current, err := runtime.Get(ctx, s.tenant, s.first.ID)
			if err != nil || response.Code < 400 || current.Status != workerruntime.StatusLeased || current.Revision != s.first.Revision || len(current.Attempts) != 1 || current.Attempts[0].CompletedAt != nil {
				t.Fatalf("rejected callback changed runtime: response=%d %s task=%#v err=%v", response.Code, response.Body.String(), current, err)
			}
		})
	}
}

func TestOMRFailureWithoutAtomicSourceCommandFailsClosed(t *testing.T) {
	handler := NewHandler(NewMemoryStore(), nil, nil)
	handler.SetProductionDependencies(workerruntime.NewMemoryStore(), nil)
	response := httptest.NewRecorder()
	handler.FailOMR(response, httptest.NewRequest(http.MethodPost, "/failure", strings.NewReader(`{}`)))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "omr_dependencies_unavailable") {
		t.Fatalf("unsupported source store did not fail closed: %d %s", response.Code, response.Body.String())
	}
}
