package grading

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
)

type failingScoringSchedule struct {
	schedule func() error
}

func (f failingScoringSchedule) Observe(context.Context, string, ScoringObservationInput) (json.RawMessage, error) {
	return nil, errors.New("unexpected observation")
}

func (f failingScoringSchedule) Schedule(context.Context, string, string, ScoringAIInput) (string, string, error) {
	return "", "", f.schedule()
}

// 在入队检查和错因写回之间修改持久状态，模拟取消、重新识别或切换裁图先到达。
// 老执行器的错误不能覆盖当前计划，新鲜的配置错误仍须进入人工处理。
func TestPostgresScoringEnqueueFailurePreservesCurrentState(t *testing.T) {
	ctx, db := newDurableScoringFixture(t)
	tenant, actor, exam, submission := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	aiReviewExec(t, ctx, db, `INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'Synthetic enqueue',$2,'active')`, tenant, "enqueue-"+tenant)
	aiReviewExec(t, ctx, db, `INSERT INTO submission(id,tenant_id,exam_id,candidate_no,source_type,status,collected_by) VALUES($1,$2,$3,'synthetic','manual_import','created',$4)`, submission, tenant, exam, actor)
	transient := errors.New("fixture temporary scheduling failure")
	cases := []struct {
		name          string
		number        string
		scheduleError error
		wantStatus    string
		wantReason    string
		wantAction    string
		wantCalls     int
		wantError     bool
	}{
		{"permanent", "10", ErrInvalidInput, "blocked", "observation_contract_invalid", "check_configuration", 1, true},
		{"temporary", "11", transient, "queued", "observation_ready", "wait", 1, true},
		{"cancelled_before", "12", ErrInvalidInput, "cancelled", "run_cancelled", "none", 0, false},
		{"cancelled_during", "13", ErrInvalidInput, "cancelled", "run_cancelled", "none", 1, false},
		{"new_revision", "14", ErrInvalidInput, "queued", "new_observation_ready", "wait", 1, false},
		{"stale_input_result", "15", ErrRevisionConflict, "queued", "observation_ready", "wait", 1, false},
		{"crop_changed_during", "16", ErrInvalidInput, "queued", "observation_ready", "wait", 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := uuid.NewString()
			aiReviewExec(t, ctx, db, `INSERT INTO scoring_run(id,tenant_id,exam_id,idempotency_key,status,total_count,queued_count,started_by) VALUES($1,$2,$3,$4,'processing',1,1,$5)`, run, tenant, exam, tc.name, actor)
			id := seedBlockedReviewItem(t, ctx, db, tenant, actor, exam, submission, run, tc.number, "image_ai")
			aiReviewExec(t, ctx, db, `UPDATE scoring_run_item SET processing_status='queued',reason_code='observation_ready' WHERE scoring_run_id=$1 AND answer_segment_id=$2`, run, id.segment)
			cancel := func() {
				aiReviewExec(t, ctx, db, `UPDATE scoring_run SET status='cancelled' WHERE id=$1`, run)
				aiReviewExec(t, ctx, db, `UPDATE scoring_run_item SET processing_status='cancelled',reason_code='run_cancelled',next_action='none',revision=revision+1 WHERE scoring_run_id=$1 AND answer_segment_id=$2`, run, id.segment)
			}
			if tc.name == "cancelled_before" {
				cancel()
			}
			calls := 0
			coordinator := NewScoringCoordinator(NewPostgresStore(db), nil, failingScoringSchedule{schedule: func() error {
				calls++
				switch tc.name {
				case "cancelled_during":
					cancel()
				case "new_revision":
					aiReviewExec(t, ctx, db, `UPDATE scoring_run_item SET reason_code='new_observation_ready',revision=revision+1 WHERE scoring_run_id=$1 AND answer_segment_id=$2`, run, id.segment)
				case "stale_input_result", "crop_changed_during":
					aiReviewExec(t, ctx, db, `UPDATE answer_segment SET crop_sha256=$2 WHERE id=$1`, id.segment, strings.Repeat("b", 64))
				}
				return tc.scheduleError
			}}, time.Minute)
			pending := pendingScoringItem{
				tenantID: tenant, runID: run, segmentID: id.segment, inputID: id.input, cropHash: strings.Repeat("a", 64),
				snapshotID: id.snapshot, actorID: actor, route: "image_ai", processingStatus: "queued", observation: json.RawMessage(`{"fixture":true}`),
			}
			err := coordinator.scheduleAI(ctx, pending)
			if calls != tc.wantCalls || (tc.wantError && !errors.Is(err, tc.scheduleError)) || (tc.name == "cancelled_before" && err != nil) {
				t.Fatalf("schedule calls=%d error=%v", calls, err)
			}
			var status, reason, action string
			var linked, retryable bool
			err = db.QueryRowContext(ctx, `SELECT processing_status,reason_code,next_action,subjective_run_id IS NOT NULL OR runtime_task_id IS NOT NULL,retryable FROM scoring_run_item WHERE scoring_run_id=$1 AND answer_segment_id=$2`, run, id.segment).Scan(&status, &reason, &action, &linked, &retryable)
			if err != nil || status != tc.wantStatus || reason != tc.wantReason || action != tc.wantAction || linked || retryable {
				t.Fatalf("unexpected persistent outcome: status=%q reason=%q action=%q linked=%v retryable=%v error=%v", status, reason, action, linked, retryable, err)
			}
			if tc.name == "temporary" {
				runtime := workerruntime.NewPostgresStore(db)
				images := &coordinatorFixtureImages{db: db, runtime: runtime}
				recovered := NewScoringCoordinator(NewPostgresStore(db), runtime, images, time.Minute)
				// 瞬时失败留下原条目供下轮恢复。第二次恢复应复用已关联任务。
				for range 2 {
					if err := recovered.scheduleAI(ctx, pending); err != nil {
						t.Fatal(err)
					}
				}
				var tasks int
				err := db.QueryRowContext(ctx, `SELECT count(*) FROM scoring_run_item item
JOIN agent_worker_task task ON task.tenant_id=item.tenant_id AND task.id=item.runtime_task_id AND task.source_id=item.subjective_run_id
WHERE item.scoring_run_id=$1 AND item.answer_segment_id=$2 AND item.stage='score' AND item.reason_code='ai_suggestion_pending'`, run, id.segment).Scan(&tasks)
				if err != nil || tasks != 1 || images.schedules != 1 {
					t.Fatalf("temporary failure recovery: tasks=%d schedules=%d err=%v", tasks, images.schedules, err)
				}
			}
		})
	}
}
