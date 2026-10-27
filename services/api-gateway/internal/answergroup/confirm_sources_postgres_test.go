package answergroup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/grading"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type groupSourceFixture struct {
	ctx                                     context.Context
	db                                      *sql.DB
	cfg                                     *pgx.ConnConfig
	store                                   *PostgresStore
	tenant, exam, question, actor, snapshot string
	group                                   Group
	unsampled                               string
}

func newGroupSourceFixture(t *testing.T) groupSourceFixture {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL requires an isolated migrated PostgreSQL test database")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	schema := "group_source_fix_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		cancel()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema + ",public"
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(4)
	t.Cleanup(func() {
		db.Close()
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
		cancel()
	})
	f := groupSourceFixture{ctx: ctx, db: db, cfg: cfg, store: NewPostgresStore(db, nil, DefaultPolicy()), tenant: uuid.NewString(), exam: uuid.NewString(), question: uuid.NewString(), actor: uuid.NewString(), snapshot: uuid.NewString()}
	// 仅复制迁移后的结构和索引；数据与相关锁触发器都只存在于随机测试 schema。
	for _, table := range []string{"submission", "question", "exam_question_snapshot", "answer_segment", "answer_segment_answer", "answer_group", "answer_group_member", "answer_group_sample_review", "answer_group_decision", "answer_group_automation_candidate", "review_task", "event_outbox"} {
		f.exec(t, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)")
	}
	for _, name := range []string{"000163_answer_group_v2_candidate_quarantine.sql", "000210_answer_group_source_lock.sql"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, string(raw))
	}
	f.exec(t, `INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1,$2,$3,'1','fill_blank',2,1,'draft')`, f.question, f.tenant, f.exam)
	f.exec(t, `INSERT INTO exam_question_snapshot(id,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1,$2,$3,$4,$5,1,'exact_text','[]','R1','{}','{}','{}','{}',$6)`, f.snapshot, f.tenant, f.exam, f.question, uuid.NewString(), strings.Repeat("a", 64))
	for range 6 {
		submission, segment := uuid.NewString(), uuid.NewString()
		f.exec(t, `INSERT INTO submission(id,tenant_id,exam_id,source_type,status,collected_by) VALUES($1,$2,$3,'manual_import','created',$4)`, submission, f.tenant, f.exam, f.actor)
		f.exec(t, `INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1,$2,$3,$4,$5,'1','{}','manual','accepted')`, segment, f.tenant, submission, uuid.NewString(), f.question)
		f.record(t, segment, "ATP", "manual_entry", nil)
	}
	groups, err := f.store.Build(ctx, f.tenant, f.exam, f.question, f.actor, BuildInput{})
	if err != nil || len(groups) != 1 {
		t.Fatalf("build: groups=%d err=%v", len(groups), err)
	}
	f.group, err = f.store.PutDecision(ctx, f.tenant, groups[0].ID, f.actor, DecisionInput{ScoreCandidate: map[string]any{"score": 2}, RubricSelection: map[string]any{"P1": "matched"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range append([]Member{}, f.group.Members...)[:f.group.MinimumSample] {
		f.group, err = f.store.ReviewSample(ctx, f.tenant, f.group.ID, member.SegmentID, f.actor, SampleReviewInput{Outcome: SampleAccepted})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, member := range f.group.Members {
		if member.SampleStatus == "" {
			f.unsampled = member.SegmentID
			break
		}
	}
	if !f.group.CanConfirm || f.unsampled == "" {
		t.Fatalf("fixture not confirmable with unsampled member: %#v", f.group)
	}
	return f
}

func (f groupSourceFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.ExecContext(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f groupSourceFixture) record(t *testing.T, segment, text, source string, confidence *float64) {
	t.Helper()
	if _, err := grading.NewPostgresStore(f.db).RecordAnswer(f.ctx, f.tenant, segment, f.actor, grading.RecordAnswerInput{AnswerText: text, Source: source, Confidence: confidence}); err != nil {
		t.Fatal(err)
	}
}

func (f groupSourceFixture) assertNoConfirmation(t *testing.T) {
	t.Helper()
	var confirmed bool
	var count int
	if err := f.db.QueryRowContext(f.ctx, `SELECT confirmed_at IS NOT NULL FROM answer_group_decision WHERE tenant_id=$1 AND group_id=$2`, f.tenant, f.group.ID).Scan(&confirmed); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM answer_group_automation_candidate WHERE tenant_id=$1 AND group_id=$2 AND candidate_kind='group_score'`, f.tenant, f.group.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if confirmed || count != 0 {
		t.Fatalf("rejected confirmation left committed state: confirmed=%v candidates=%d", confirmed, count)
	}
}

func TestPostgresConfirmationUsesCurrentMemberSources(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, groupSourceFixture)
		allow  bool
	}{
		{"unchanged", func(*testing.T, groupSourceFixture) {}, true},
		{"same normalized representation", func(t *testing.T, f groupSourceFixture) {
			f.record(t, f.unsampled, "  ＡＴＰ。  ", "manual_entry", nil)
		}, true},
		{"unsampled answer edited", func(t *testing.T, f groupSourceFixture) { f.record(t, f.unsampled, "ADP", "manual_entry", nil) }, false},
		{"sampled answer edited", func(t *testing.T, f groupSourceFixture) {
			f.record(t, f.group.Members[0].SegmentID, "ADP", "manual_entry", nil)
		}, false},
		{"latest answer deleted", func(t *testing.T, f groupSourceFixture) {
			f.exec(t, `UPDATE answer_segment_answer SET deleted_at=now() WHERE tenant_id=$1 AND answer_segment_id=$2`, f.tenant, f.unsampled)
		}, false},
		{"segment removed", func(t *testing.T, f groupSourceFixture) {
			f.exec(t, `UPDATE answer_segment SET deleted_at=now() WHERE tenant_id=$1 AND id=$2`, f.tenant, f.unsampled)
		}, false},
		{"latest low confidence OCR", func(t *testing.T, f groupSourceFixture) { c := 0.3; f.record(t, f.unsampled, "ATP", "ocr_text", &c) }, false},
		{"snapshot changed", func(t *testing.T, f groupSourceFixture) {
			f.exec(t, `UPDATE exam_question_snapshot SET id=$2 WHERE id=$1`, f.snapshot, uuid.NewString())
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGroupSourceFixture(t)
			tc.change(t, f)
			group, candidates, err := f.store.Confirm(f.ctx, f.tenant, f.group.ID, f.actor, ConfirmInput{ExpectedRevision: f.group.Decision.Revision})
			if tc.allow {
				if err != nil || group.Status != StatusConfirmed || len(candidates) != group.MemberCount {
					t.Fatalf("valid sources rejected: status=%s count=%d err=%v", group.Status, len(candidates), err)
				}
				assertGroupingCandidatesAreProposals(t, candidates, group.Decision.RollbackReference)
			} else {
				if !errors.Is(err, ErrRevisionConflict) || len(candidates) != 0 {
					t.Fatalf("changed sources confirmed: count=%d err=%v", len(candidates), err)
				}
				f.assertNoConfirmation(t)
			}
		})
	}
}

type groupConfirmationBarrier struct {
	once             sync.Once
	entered, release chan struct{}
}

func (b *groupConfirmationBarrier) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "INSERT INTO answer_group_automation_candidate") {
		b.once.Do(func() {
			close(b.entered)
			select {
			case <-b.release:
			case <-ctx.Done():
			}
		})
	}
	return ctx
}
func (*groupConfirmationBarrier) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func waitForGroupLock(t *testing.T, f groupSourceFixture, application string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, application).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("expected actual PostgreSQL lock wait for %s", application)
		}
	}
}

func TestPostgresConfirmationSerializesAllAnswerWrites(t *testing.T) {
	for _, operation := range []string{"record", "update", "delete"} {
		t.Run(operation, func(t *testing.T) {
			f := newGroupSourceFixture(t)
			barrier := &groupConfirmationBarrier{entered: make(chan struct{}), release: make(chan struct{})}
			release := sync.OnceFunc(func() { close(barrier.release) })
			defer release()
			confirmCfg := f.cfg.Copy()
			confirmCfg.Tracer = barrier
			confirmDB := stdlib.OpenDB(*confirmCfg)
			defer func() { release(); confirmDB.Close() }()
			confirmed := make(chan error, 1)
			go func() {
				_, _, err := NewPostgresStore(confirmDB, nil, DefaultPolicy()).Confirm(f.ctx, f.tenant, f.group.ID, f.actor, ConfirmInput{ExpectedRevision: f.group.Decision.Revision})
				confirmed <- err
			}()
			select {
			case <-barrier.entered:
			case err := <-confirmed:
				t.Fatalf("confirmation did not reach candidate write: %v", err)
			case <-f.ctx.Done():
				t.Fatal(f.ctx.Err())
			}
			writerCfg := f.cfg.Copy()
			application := "group_source_writer_" + uuid.NewString()
			writerCfg.RuntimeParams["application_name"] = application
			writerDB := stdlib.OpenDB(*writerCfg)
			defer func() { release(); writerDB.Close() }()
			written := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "record":
					_, err = grading.NewPostgresStore(writerDB).RecordAnswer(f.ctx, f.tenant, f.unsampled, f.actor, grading.RecordAnswerInput{AnswerText: "ADP", Source: "manual_entry"})
				case "update":
					_, err = writerDB.ExecContext(f.ctx, `UPDATE answer_segment_answer SET answer_text='ADP' WHERE tenant_id=$1 AND answer_segment_id=$2`, f.tenant, f.unsampled)
				case "delete":
					_, err = writerDB.ExecContext(f.ctx, `DELETE FROM answer_segment_answer WHERE tenant_id=$1 AND answer_segment_id=$2`, f.tenant, f.unsampled)
				}
				written <- err
			}()
			// 屏障位于校验后、确认写入前；以数据库实际锁等待证明新答案不能穿过此窗口。
			waitForGroupLock(t, f, application)
			release()
			if err := <-confirmed; err != nil {
				t.Fatal(err)
			}
			if err := <-written; err != nil {
				t.Fatal(err)
			}
			group, err := f.store.Get(f.ctx, f.tenant, f.group.ID)
			if err != nil || group.Status != StatusConfirmed {
				t.Fatalf("confirmation failed at linearization boundary: %s %v", group.Status, err)
			}
		})
	}
}

func TestPostgresConfirmationReadsCorrectionCommittedWhileWaitingForSegment(t *testing.T) {
	f := newGroupSourceFixture(t)
	correction, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer correction.Rollback()
	_, err = correction.ExecContext(f.ctx, `INSERT INTO answer_segment_answer(tenant_id,answer_segment_id,answer_text,source,recorded_by) VALUES($1,$2,'ADP','manual_entry',$3)`, f.tenant, f.unsampled, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	confirmCfg := f.cfg.Copy()
	application := "group_source_confirm_" + uuid.NewString()
	confirmCfg.RuntimeParams["application_name"] = application
	confirmDB := stdlib.OpenDB(*confirmCfg)
	defer confirmDB.Close()
	confirmed := make(chan error, 1)
	go func() {
		_, _, err := NewPostgresStore(confirmDB, nil, DefaultPolicy()).Confirm(f.ctx, f.tenant, f.group.ID, f.actor, ConfirmInput{ExpectedRevision: f.group.Decision.Revision})
		confirmed <- err
	}()
	waitForGroupLock(t, f, application)
	if err := correction.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-confirmed; !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("lock wait reused old answer snapshot: %v", err)
	}
	f.assertNoConfirmation(t)
}
