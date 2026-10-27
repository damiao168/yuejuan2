package regrade

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 实际运行锁、状态更新和事件事务；随机 schema 只克隆正式表类型，不复制业务数据或触发器。
func TestPostgresRegradeRecovery(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	schema := "regrade_recovery_" + uuid.NewString()[:8]
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	defer db.Close()
	for _, table := range []string{"regrade_job", "regrade_item", "regrade_event"} {
		if _, err := db.ExecContext(ctx, `CREATE TABLE `+table+` AS TABLE public.`+table+` WITH NO DATA`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE regrade_event ALTER COLUMN id SET DEFAULT gen_random_uuid(), ALTER COLUMN created_at SET DEFAULT now()`); err != nil {
		t.Fatal(err)
	}
	tenant, actor := uuid.NewString(), uuid.NewString()
	insert := func(table string, fields map[string]any) {
		t.Helper()
		fields["tenant_id"] = tenant
		fields["created_at"], fields["updated_at"] = time.Now().UTC(), time.Now().UTC()
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO `+table+` SELECT * FROM jsonb_populate_record(NULL::`+table+`,$1::jsonb)`, raw); err != nil {
			t.Fatal(err)
		}
	}
	seed := func(status string, items []Item) (string, []string) {
		t.Helper()
		jobID := uuid.NewString()
		insert("regrade_job", map[string]any{"id": jobID, "exam_id": uuid.NewString(), "question_id": uuid.NewString(), "source_release_id": uuid.NewString(), "reason_code": "quality_incident", "reason_text": "synthetic recovery", "strategy": "human_recheck", "selector_json": map[string]any{}, "severity_delta": 2, "idempotency_key": uuid.NewString(), "status": status, "affected_count": len(items), "created_by": actor})
		ids := []string{}
		for _, item := range items {
			id := uuid.NewString()
			ids = append(ids, id)
			insert("regrade_item", map[string]any{"id": id, "job_id": jobID, "submission_id": uuid.NewString(), "old_final_grade_id": uuid.NewString(), "old_score": 1, "max_score": 5, "candidate_score": item.CandidateScore, "candidate_rubric_selections": []any{}, "candidate_comment": "synthetic", "status": item.Status, "review_note": item.ReviewNote, "revision": 1})
		}
		return jobID, ids
	}
	store := NewPostgresStore(db)
	score := 3.0
	for _, tc := range []struct {
		name        string
		items       []Item
		want        string
		finalizable bool
	}{
		{"no candidate", []Item{{Status: ItemPending}}, StatusRunning, false},
		{"partial candidates", []Item{{Status: ItemAwaitingReview, CandidateScore: &score}, {Status: ItemPending}}, StatusDiffReview, false},
		{"all candidates", []Item{{Status: ItemAwaitingReview, CandidateScore: &score}}, StatusDiffReview, false},
		{"resolved", []Item{{Status: ItemResolved, CandidateScore: &score}}, StatusDiffReview, true},
		{"resolved without candidate", []Item{{Status: ItemResolved}}, StatusDiffReview, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jobID, _ := seed(StatusPaused, tc.items)
			job, err := store.Resume(ctx, tenant, jobID, actor)
			if err != nil || job.Status != tc.want {
				t.Fatalf("resume=%#v %v", job, err)
			}
			if _, err := store.Resume(ctx, tenant, jobID, actor); err != nil {
				t.Fatal(err)
			}
			_, err = store.Finalize(ctx, tenant, jobID, actor)
			if (err == nil) != tc.finalizable {
				t.Fatalf("finalize=%v want allowed=%t", err, tc.finalizable)
			}
		})
	}
	for _, decision := range []string{ReviewAccept, ReviewReject} {
		t.Run("exception "+decision, func(t *testing.T) {
			jobID, ids := seed(StatusDiffReview, []Item{{Status: ItemAwaitingReview, CandidateScore: &score}})
			item, err := store.Review(ctx, tenant, ids[0], actor, ReviewInput{Decision: ReviewException, Note: "原异常说明", ExpectedRevision: 1})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Finalize(ctx, tenant, jobID, actor); !errors.Is(err, ErrStateConflict) {
				t.Fatalf("unresolved exception bypassed: %v", err)
			}
			if _, err := store.Review(ctx, tenant, item.ID, actor, ReviewInput{Decision: decision, ExpectedRevision: item.Revision}); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("missing explanation: %v", err)
			}
			if _, err := store.Review(ctx, tenant, item.ID, actor, ReviewInput{Decision: decision, Note: "处理说明", ExpectedRevision: item.Revision - 1}); !errors.Is(err, ErrRevisionConflict) {
				t.Fatalf("stale resolution: %v", err)
			}
			if _, err := store.Pause(ctx, tenant, jobID, actor); err != nil {
				t.Fatal(err)
			}
			job, err := store.Resume(ctx, tenant, jobID, actor)
			if err != nil || job.Status != StatusDiffReview {
				t.Fatalf("resume=%#v %v", job, err)
			}
			resolved, err := store.Review(ctx, tenant, item.ID, actor, ReviewInput{Decision: decision, Note: "重新核对后的处理说明", ExpectedRevision: item.Revision})
			if err != nil || resolved.Status != ItemResolved {
				t.Fatalf("resolution=%#v %v", resolved, err)
			}
			if _, err := store.Finalize(ctx, tenant, jobID, actor); err != nil {
				t.Fatal(err)
			}
			var previous, note string
			if err := db.QueryRowContext(ctx, `SELECT payload->>'previous_review_note',payload->>'note' FROM regrade_event WHERE job_id=$1::uuid AND payload->>'previous_status'='exception'`, jobID).Scan(&previous, &note); err != nil {
				t.Fatal(err)
			}
			if previous != "原异常说明" || note != "重新核对后的处理说明" {
				t.Fatalf("audit history lost: %q %q", previous, note)
			}
		})
	}
	// 事件持久化失败必须撤销异常处理，保留异常及其修订号，不能单独放行收尾。
	t.Run("audit rollback", func(t *testing.T) {
		jobID, ids := seed(StatusDiffReview, []Item{{Status: ItemException, CandidateScore: &score, ReviewNote: "original"}})
		if _, err := db.ExecContext(ctx, `ALTER TABLE regrade_event ADD CONSTRAINT reject_resolution CHECK (NOT (payload ? 'previous_status')) NOT VALID`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Review(ctx, tenant, ids[0], actor, ReviewInput{Decision: ReviewAccept, Note: "resolution", ExpectedRevision: 1}); err == nil {
			t.Fatal("failed audit committed resolution")
		}
		var status string
		var revision int
		if err := db.QueryRowContext(ctx, `SELECT status,revision FROM regrade_item WHERE id=$1::uuid`, ids[0]).Scan(&status, &revision); err != nil {
			t.Fatal(err)
		}
		if status != ItemException || revision != 1 {
			t.Fatalf("resolution survived rollback: %s %d", status, revision)
		}
		if _, err := store.Finalize(ctx, tenant, jobID, actor); !errors.Is(err, ErrStateConflict) {
			t.Fatalf("rollback removed blocker: %v", err)
		}
	})
}
