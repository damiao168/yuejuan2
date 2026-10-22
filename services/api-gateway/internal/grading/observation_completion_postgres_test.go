package grading

import (
	"encoding/json"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

// 复用独立 schema 和真实 runtime；只提交离线观察，证明无需后续评分就刷新父汇总。
// 汇总 trigger 故障还必须回滚已完成的 runtime、候选和人工任务，不能留下部分成功。
func TestPostgresObservationCompletionRefreshesSummaryAtomically(t *testing.T) {
	for _, scenario := range []string{"low_confidence", "uncertain", "refresh_failure"} {
		t.Run(scenario, func(t *testing.T) {
			f := seedCompletedObservationFixture(t)
			created, err := f.store.ReprocessCompletedObservation(f.ctx, f.tenant, f.run, f.item, f.oldTask.ID, f.actor)
			if err != nil {
				t.Fatal(err)
			}
			runtime := workerruntime.NewPostgresStore(f.db)
			tasks, err := runtime.Claim(f.ctx, f.tenant, workerruntime.ClaimInput{QueueName: scoringCoordinatorQueue, WorkerService: "api-gateway-scoring", WorkerInstanceID: "summary-fixture", Limit: 1, LeaseSeconds: 300})
			if err != nil || len(tasks) != 1 || tasks[0].ID != created.ID {
				t.Fatalf("claim observation: tasks=%#v err=%v", tasks, err)
			}
			input, runID, actorID, err := f.store.loadScoringObservationInput(f.ctx, tasks[0])
			if err != nil {
				t.Fatal(err)
			}
			var observation scoringObservation
			if err = json.Unmarshal(f.oldRaw, &observation); err != nil {
				t.Fatal(err)
			}
			observation.Members[0].Confidence = .7
			if scenario == "uncertain" {
				observation.Members[0].Presence = "uncertain"
				observation.Members[0].Answer = nil
			}
			raw, err := json.Marshal(observation)
			if err != nil {
				t.Fatal(err)
			}
			facts := func() string {
				t.Helper()
				var value []byte
				if err := f.db.QueryRowContext(f.ctx, `SELECT jsonb_build_array(
 (SELECT to_jsonb(sr) FROM scoring_run sr WHERE id=$1::uuid),
 (SELECT jsonb_agg(to_jsonb(item) ORDER BY item.id) FROM scoring_run_item item),
 (SELECT to_jsonb(wt) FROM agent_worker_task wt WHERE id=$2::uuid),
 (SELECT jsonb_agg(to_jsonb(attempt) ORDER BY attempt.id) FROM agent_worker_task_attempt attempt WHERE task_id=$2::uuid),
 (SELECT jsonb_agg(to_jsonb(ac) ORDER BY ac.id) FROM answer_candidate ac),
 (SELECT jsonb_agg(to_jsonb(rt) ORDER BY rt.id) FROM review_task rt))`, f.run, created.ID).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return string(value)
			}
			before := facts()
			if scenario == "refresh_failure" {
				f.exec(t, `CREATE FUNCTION fixture_fail_summary() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture_summary_failure'; END $$`)
				f.exec(t, `CREATE TRIGGER fixture_fail_summary BEFORE UPDATE ON scoring_run FOR EACH ROW EXECUTE FUNCTION fixture_fail_summary()`)
			}
			err = f.store.completeScoringObservation(f.ctx, tasks[0], input, runID, actorID, raw)
			if scenario == "refresh_failure" {
				if err == nil || !strings.Contains(err.Error(), "fixture_summary_failure") {
					t.Fatalf("expected summary write failure: %v", err)
				}
				if after := facts(); before != after {
					t.Fatalf("summary failure committed partial completion: before=%s after=%s", before, after)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var parent, processing, decision, taskStatus string
			var queued, reviews int
			if err = f.db.QueryRowContext(f.ctx, `SELECT sr.status,sr.queued_count,sr.review_count,item.processing_status,item.decision_status,wt.status FROM scoring_run sr JOIN scoring_run_item item ON item.scoring_run_id=sr.id AND item.id=$2::uuid JOIN agent_worker_task wt ON wt.id=item.observation_task_id WHERE sr.id=$1::uuid`, f.run, f.item).Scan(&parent, &queued, &reviews, &processing, &decision, &taskStatus); err != nil || parent != "needs_review" || queued != 0 || reviews != 2 || processing != "succeeded" || decision != "review_needed" || taskStatus != "succeeded" {
				t.Fatalf("completed observation summary parent=%s queued=%d reviews=%d item=%s/%s task=%s err=%v", parent, queued, reviews, processing, decision, taskStatus, err)
			}
			// 公开维护刷新可重复调用；父 updated_at 可变化，已有条目和教师待办不得反复改写。
			var otherBefore, otherAfter []byte
			query := `SELECT jsonb_build_array(to_jsonb(item),(SELECT jsonb_agg(to_jsonb(rt) ORDER BY rt.id) FROM review_task rt WHERE rt.answer_segment_id=item.answer_segment_id)) FROM scoring_run_item item WHERE item.answer_segment_id=$1::uuid`
			if err = f.db.QueryRowContext(f.ctx, query, f.other).Scan(&otherBefore); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				refreshed, err := f.store.RefreshScoringRun(f.ctx, f.tenant, f.run)
				if err != nil || refreshed.Status != "needs_review" || refreshed.QueuedCount != 0 || refreshed.ReviewCount != 2 {
					t.Fatalf("idempotent refresh: %#v err=%v", refreshed, err)
				}
			}
			if err = f.db.QueryRowContext(f.ctx, query, f.other).Scan(&otherAfter); err != nil || string(otherBefore) != string(otherAfter) {
				t.Fatalf("refresh rewrote unrelated item/review: err=%v", err)
			}
		})
	}
}
