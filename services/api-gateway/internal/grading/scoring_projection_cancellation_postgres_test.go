package grading

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 只在业务事务已提交、投影尚未取得父运行锁时暂停；取消仍走正式存储方法。
type reviewCancellationBarrier struct {
	locks   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (b *reviewCancellationBarrier) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "SELECT status FROM scoring_run") && strings.Contains(data.SQL, "FOR UPDATE") && b.locks.Add(1) == 2 {
		close(b.entered)
		select {
		case <-b.release:
		case <-ctx.Done():
		}
	}
	return ctx
}
func (*reviewCancellationBarrier) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestRuleProjectionAfterCancellationRemainsCancelled(t *testing.T) {
	f := seedCompletedOMRFixture(t)
	var item, question, exam string
	if err := f.db.QueryRowContext(f.ctx, `SELECT item.id::text,item.question_id::text,sr.exam_id::text FROM scoring_run_item item JOIN scoring_run sr ON sr.id=item.scoring_run_id WHERE item.answer_segment_id=$1::uuid`, f.segment).Scan(&item, &question, &exam); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2,$3,'Synthetic cancellation','math','quiz',10,'draft','ai_assisted','manual_after_confirmation',$4)`, exam, f.tenant, uuid.NewString(), f.actor)
	f.exec(t, `UPDATE question SET question_type='fill_blank' WHERE id=$1::uuid`, question)
	f.exec(t, `INSERT INTO question_answer_key(tenant_id,question_id,standard_answer,equivalent_answers,tolerance,answer_version,created_by) VALUES($1,$2,'"1/2"','[]','{}','fixture-v1',$3)`, f.tenant, question, f.actor)
	_, ruleHash, err := marshalRule("fill_blank", map[string]any{"answer": "1/2", "max_score": 5.0})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO scoring_rule(tenant_id,exam_id,question_id,version,rule_type,config,status,content_hash,created_by,published_by,published_at) VALUES($1,$2,$3,1,'fill_blank','{"answer":"1/2","max_score":5}','published',$4,$5,$5,now())`, f.tenant, exam, question, ruleHash, f.actor)
	f.exec(t, `UPDATE scoring_run SET auto_confirmed_count=0 WHERE id=$1::uuid`, f.run)
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = freezeScoringRunRulesTx(f.ctx, tx, f.tenant, exam, f.run); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE question_grade SET is_current=false,status='invalidated' WHERE answer_segment_id=$1::uuid`, f.segment)
	f.exec(t, `UPDATE answer_candidate SET source='ocr',decision='confirmed',confidence=.4,payload='{}',display_text='1/2' WHERE answer_segment_id=$1::uuid`, f.segment)
	f.exec(t, `UPDATE scoring_run_item SET route='rule_input',processing_status='waiting_dependency',decision_status='undecided' WHERE id=$1::uuid`, item)
	var searchPath string
	if err := f.db.QueryRowContext(f.ctx, `SHOW search_path`).Scan(&searchPath); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = searchPath
	barrier := &reviewCancellationBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	cfg.Tracer = barrier
	traced := stdlib.OpenDB(*cfg)
	defer traced.Close()
	store := NewPostgresStore(traced)
	result := make(chan error, 1)
	go func() {
		_, err := store.processRuleCandidate(f.ctx, f.tenant, f.run, f.segment, f.actor, NewEngine())
		result <- err
	}()
	select {
	case <-barrier.entered:
	case err := <-result:
		t.Fatalf("projection barrier not reached: %v", err)
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	if _, _, err := f.store.BeginScoringRunCancellation(f.ctx, f.tenant, f.run); err != nil {
		close(barrier.release)
		t.Fatal(err)
	}
	if _, err := f.store.FinalizeScoringRunCancellation(f.ctx, f.tenant, f.run); err != nil {
		close(barrier.release)
		t.Fatal(err)
	}
	close(barrier.release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	run, err := f.store.RefreshScoringRun(f.ctx, f.tenant, f.run)
	if err != nil {
		t.Fatal(err)
	}
	var processing, decision string
	if err := f.db.QueryRowContext(f.ctx, `SELECT processing_status,decision_status FROM scoring_run_item WHERE id=$1::uuid`, item).Scan(&processing, &decision); err != nil {
		t.Fatal(err)
	}
	t.Logf("after formal cancellation and delayed rule projection: run=%s item=%s/%s", run.Status, processing, decision)
	detail, err := f.store.GetScoringRunDetail(f.ctx, f.tenant, f.run)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range detail.Items {
		if value.AnswerSegmentID == f.segment {
			t.Logf("API item state=%s next_action=%s review_task_status=%s", value.State, value.NextAction, value.ReviewStatus)
		}
	}
	if run.Status != "cancelled" || processing != "cancelled" {
		t.Fatalf("late rule projection resurrected cancelled item: run=%s item=%s/%s", run.Status, processing, decision)
	}
}

func TestRuleProjectionPreservesCancellationAndSupersededItems(t *testing.T) {
	f := seedCompletedOMRFixture(t)
	for _, tc := range []struct {
		name, runStatus, itemStatus string
		stopped, projected          bool
	}{
		{"active review", "needs_review", "waiting_dependency", false, true},
		{"cancellation in progress", "cancelling", "waiting_dependency", true, false},
		{"cancelled", "cancelled", "cancelled", true, false},
		{"cancelled child", "needs_review", "cancelled", false, false},
		{"superseded child", "needs_review", "superseded", false, false},
		{"committed auto confirmation", "completed", "succeeded", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.exec(t, `UPDATE scoring_run SET status=$2 WHERE id=$1::uuid`, f.run, tc.runStatus)
			f.exec(t, `UPDATE scoring_run_item SET processing_status=$2,decision_status='undecided',reason_code='before',next_action='none' WHERE answer_segment_id=$1::uuid`, f.segment, tc.itemStatus)
			stopped, err := f.store.projectRuleCandidate(f.ctx, f.tenant, f.run, f.segment, "review_needed", "after", "teacher_review")
			if err != nil || stopped != tc.stopped {
				t.Fatalf("stopped=%v err=%v", stopped, err)
			}
			var status, reason string
			if err := f.db.QueryRowContext(f.ctx, `SELECT processing_status,reason_code FROM scoring_run_item WHERE answer_segment_id=$1::uuid`, f.segment).Scan(&status, &reason); err != nil {
				t.Fatal(err)
			}
			if tc.projected {
				if status != "succeeded" || reason != "after" {
					t.Fatalf("active projection missing: %s/%s", status, reason)
				}
			} else if status != tc.itemStatus || reason != "before" {
				t.Fatalf("terminal fact overwritten: %s/%s", status, reason)
			}
		})
	}
}
