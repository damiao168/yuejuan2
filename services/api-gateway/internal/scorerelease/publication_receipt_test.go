package scorerelease

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"
)

type publicationReceiptCallbacks struct {
	load func(context.Context, *sql.Tx) (Release, bool, error)
	save func(context.Context, *sql.Tx, Release, []SubmissionFact, bool) error
}

func (r publicationReceiptCallbacks) Load(ctx context.Context, tx *sql.Tx) (Release, bool, error) {
	return r.load(ctx, tx)
}

func (r publicationReceiptCallbacks) Save(ctx context.Context, tx *sql.Tx, release Release, facts []SubmissionFact, newlyPublished bool) error {
	return r.save(ctx, tx, release, facts, newlyPublished)
}

func TestPublicationReceiptLoadsCommandBeforeReleaseRowLockAndReplaysWithoutExamLock(t *testing.T) {
	const targetID = "pending-release"
	const commandKey = "tenant:actor:command:business-command"
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	row := []driver.Value{targetID, tenant, exam, int64(2), SourceInitial, "pending target", StatusDraft, "receipt-lock-order",
		[]byte(`{}`), []byte(`{}`), []byte(`{}`), "", "", actor, now, "", nil}
	steps := []studentBatch50Step{
		{fragment: "pg_advisory_xact_lock(hashtextextended", args: []any{commandKey}},
		{fragment: "FOR UPDATE", args: []any{tenant, targetID}, columns: len(row), rows: [][]driver.Value{row}},
	}
	probe := &publicationTxProbe{studentBatch50Script: studentBatch50Script{steps: steps}}
	db := sql.OpenDB(publicationTxConnector{probe: probe})
	t.Cleanup(func() { _ = db.Close() })
	original := Release{ID: "original-committed-release", TenantID: tenant, ExamID: exam, Version: 1, Status: StatusPublished}
	receipt := publicationReceiptCallbacks{
		load: func(ctx context.Context, tx *sql.Tx) (Release, bool, error) {
			_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, commandKey)
			return original, true, err
		},
		save: func(context.Context, *sql.Tx, Release, []SubmissionFact, bool) error {
			t.Fatal("replayed command attempted another receipt write")
			return nil
		},
	}
	// SQL 替身固定适配器的取锁顺序；真实数据库回执回滚与版本重放另由 PostgreSQL 测试验证。
	replay, err := NewPostgresStore(db, nil).PublishGuarded(WithPublicationReceipt(t.Context(), receipt), tenant, targetID, actor,
		func(context.Context, *sql.Tx, Release, Gate) error {
			t.Fatal("replayed command reran the publication gate")
			return nil
		})
	if err != nil || replay.ID != original.ID || probe.err != nil || probe.next != len(steps) || !probe.committed || probe.aborted {
		t.Fatalf("receipt did not precede row lock or replay acquired new publication state: replay=%+v err=%v SQL=%v operations=%d/%d commit=%t rollback=%t",
			replay, err, probe.err, probe.next, len(steps), probe.committed, probe.aborted)
	}
}

func TestInitialOnlyPublicationReceiptsConcurrentWinner(t *testing.T) {
	const targetID, currentID = "legacy-draft", "concurrent-current"
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	row := func(id, status string, version int64, publishedBy string, publishedAt driver.Value) []driver.Value {
		return []driver.Value{id, tenant, exam, version, SourceInitial, "verified publication", status, "receipt-initial-only",
			[]byte(`{}`), []byte(`{}`), []byte(`{}`), "", "", actor, now, publishedBy, publishedAt}
	}
	draftRow := row(targetID, StatusDraft, 1, "", nil)
	publishedRow := row(currentID, StatusPublished, 2, actor, now)
	steps := []studentBatch50Step{
		{fragment: "FOR UPDATE", args: []any{tenant, targetID}, columns: len(draftRow), rows: [][]driver.Value{draftRow}},
		{fragment: "pg_advisory_xact_lock(hashtext", args: []any{tenant, exam}},
		{fragment: "SELECT release_id FROM score_release_current", args: []any{tenant, exam}, columns: len(publishedRow), rows: [][]driver.Value{publishedRow}},
		{fragment: "FROM score_release_item item", args: []any{tenant, currentID}, columns: 13, rows: [][]driver.Value{{
			"student", "submission", float64(9), float64(10), "confirmed", "question", "1", "final", float64(9), float64(10), "single_review", "", []byte(`{}`),
		}}},
	}
	probe := &publicationTxProbe{studentBatch50Script: studentBatch50Script{steps: steps}}
	db := sql.OpenDB(publicationTxConnector{probe: probe})
	t.Cleanup(func() { _ = db.Close() })
	saveCalls := 0
	receipt := publicationReceiptCallbacks{
		load: func(context.Context, *sql.Tx) (Release, bool, error) { return Release{}, false, nil },
		save: func(_ context.Context, tx *sql.Tx, release Release, facts []SubmissionFact, newlyPublished bool) error {
			saveCalls++
			if tx == nil || newlyPublished || release.ID != currentID || release.Status != StatusPublished || len(facts) != 1 || facts[0].TotalScore != 9 {
				t.Fatalf("receipt did not bind concurrent current facts: release=%+v facts=%+v", release, facts)
			}
			return nil
		},
	}
	ctx := WithInitialPublicationOnly(WithPublicationReceipt(t.Context(), receipt))
	result, err := NewPostgresStore(db, nil).PublishGuarded(ctx, tenant, targetID, actor, func(context.Context, *sql.Tx, Release, Gate) error {
		t.Fatal("receipt for an existing release attempted to publish a draft")
		return nil
	})
	if err != nil || result.ID != currentID || saveCalls != 1 || probe.err != nil || probe.next != len(steps) || !probe.committed || probe.aborted {
		t.Fatalf("initial-only publish did not reuse winner: %+v err=%v save=%d SQL=%v operations=%d/%d commit=%t rollback=%t",
			result, err, saveCalls, probe.err, probe.next, len(steps), probe.committed, probe.aborted)
	}
}

func TestMemoryInitialOnlyPublicationCannotReplaceNewerVersion(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	ctx := t.Context()
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	loser, err := service.Create(ctx, tenant, exam, actor, CreateInput{Reason: "older compatibility draft", IdempotencyKey: "legacy-loser"})
	if err != nil {
		t.Fatal(err)
	}
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(5)})
	winner, err := service.Create(ctx, tenant, exam, actor, CreateInput{Reason: "new publication", IdempotencyKey: "new-winner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, tenant, winner.ID, actor); err != nil {
		t.Fatal(err)
	}
	receipt := publicationReceiptCallbacks{
		load: func(context.Context, *sql.Tx) (Release, bool, error) { return Release{}, false, nil },
		save: func(_ context.Context, tx *sql.Tx, release Release, facts []SubmissionFact, newlyPublished bool) error {
			if tx != nil || newlyPublished || release.ID != winner.ID || len(facts) != 1 || facts[0].TotalScore != 5 {
				t.Fatalf("compatibility command recovered the wrong snapshot: %+v %+v", release, facts)
			}
			return nil
		},
	}
	out, err := service.Publish(WithInitialPublicationOnly(WithPublicationReceipt(ctx, receipt)), tenant, loser.ID, actor)
	if err != nil || out.ID != winner.ID {
		t.Fatalf("older draft replaced concurrent publication: %+v %v", out, err)
	}
	draft, err := service.Get(ctx, tenant, loser.ID)
	if err != nil || draft.Release.Status != StatusDraft || len(draft.Items) != 1 || draft.Items[0].TotalScore != 4 {
		t.Fatalf("losing draft was accidentally published or modified: %+v %v", draft, err)
	}
}

func TestMemoryPublicationReceiptFailureKeepsDraftAndReplayKeepsNewerCurrent(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	ctx := t.Context()
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	base, err := service.Create(ctx, tenant, exam, actor, CreateInput{Reason: "initial", IdempotencyKey: "receipt-base"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, tenant, base.ID, actor); err != nil {
		t.Fatal(err)
	}
	draft, err := service.Create(ctx, tenant, exam, actor, CreateInput{Reason: "compatibility publication", IdempotencyKey: "receipt-ordinary"})
	if err != nil {
		t.Fatal(err)
	}
	correction, err := service.CreateFromRegrade(ctx, tenant, exam, actor, CreateRegradeInput{
		SourceReleaseID: base.ID, QuestionID: "question-1", Reason: "verified correction", IdempotencyKey: "receipt-correction",
		Changes: []RegradeChange{{SubmissionID: "submission-1", QuestionID: "question-1", Score: 5, MaxScore: 5}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, tenant, correction.ID, actor); err != nil {
		t.Fatal(err)
	}
	before, err := service.Get(ctx, tenant, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("synthetic compatibility receipt failure")
	failSave, saves := true, 0
	var saved Release
	receipt := publicationReceiptCallbacks{
		load: func(_ context.Context, tx *sql.Tx) (Release, bool, error) {
			if tx != nil {
				t.Fatal("memory receipt unexpectedly received a database transaction")
			}
			return cloneRelease(saved), saved.ID != "", nil
		},
		save: func(_ context.Context, tx *sql.Tx, release Release, facts []SubmissionFact, newlyPublished bool) error {
			saves++
			if tx != nil || !newlyPublished || release.Status != StatusPublished || release.PublishedAt == nil || release.SourceReleaseID != correction.ID ||
				len(facts) != 1 || facts[0].TotalScore != 5 || len(facts[0].Questions) != 1 || facts[0].Questions[0].Score != 5 {
				t.Fatalf("receipt did not receive final rebased publication: release=%+v facts=%+v", release, facts)
			}
			if failSave {
				return failure
			}
			saved = cloneRelease(release)
			return nil
		},
	}
	receiptCtx := WithPublicationReceipt(ctx, receipt)
	if _, err = service.Publish(receiptCtx, tenant, draft.ID, actor); !errors.Is(err, failure) {
		t.Fatalf("receipt failure was not propagated: %v", err)
	}
	after, err := service.Get(ctx, tenant, draft.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed receipt modified draft or its frozen facts: before=%+v after=%+v err=%v", before, after, err)
	}
	current, err := service.CurrentPublished(ctx, tenant, exam)
	if err != nil || current.Release.ID != correction.ID {
		t.Fatalf("failed receipt advanced current release: %+v %v", current, err)
	}
	failSave = false
	if _, err = service.Publish(receiptCtx, tenant, draft.ID, actor); err != nil || saves != 2 {
		t.Fatalf("receipt retry could not publish exactly once: saves=%d err=%v", saves, err)
	}
	rollback, err := service.CreateRollback(ctx, tenant, exam, actor, RollbackInput{SourceReleaseID: base.ID, Reason: "restore initial version", IdempotencyKey: "receipt-rollback"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, tenant, rollback.ID, actor); err != nil {
		t.Fatal(err)
	}
	replay, err := service.Publish(receiptCtx, tenant, draft.ID, actor)
	if err != nil || !reflect.DeepEqual(replay, saved) || saves != 2 {
		t.Fatalf("old command did not restore its original receipt: %+v saves=%d err=%v", replay, saves, err)
	}
	current, err = service.CurrentPublished(ctx, tenant, exam)
	if err != nil || current.Release.ID != rollback.ID || current.Items[0].TotalScore != 4 {
		t.Fatalf("receipt replay undid the later rollback: %+v %v", current, err)
	}
}

func TestMemoryPublicationReceiptRejectsLoadFailureAndUnrelatedReplay(t *testing.T) {
	store := NewMemoryStore()
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	service := NewService(store)
	draft, err := service.Create(t.Context(), tenant, exam, actor, CreateInput{Reason: "receipt scope", IdempotencyKey: "receipt-scope"})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("synthetic command identity conflict")
	for _, scenario := range []struct {
		name   string
		replay Release
		err    error
		want   error
	}{
		{"load failure", Release{}, failure, failure},
		{"foreign tenant", Release{ID: "old", TenantID: "another-tenant", ExamID: exam, Status: StatusPublished}, nil, ErrInvalidInput},
		{"foreign exam", Release{ID: "old", TenantID: tenant, ExamID: "another-exam", Status: StatusPublished}, nil, ErrInvalidInput},
		{"unpublished receipt", Release{ID: "old", TenantID: tenant, ExamID: exam, Status: StatusDraft}, nil, ErrInvalidInput},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			receipt := publicationReceiptCallbacks{
				load: func(context.Context, *sql.Tx) (Release, bool, error) {
					return scenario.replay, scenario.err == nil, scenario.err
				},
				save: func(context.Context, *sql.Tx, Release, []SubmissionFact, bool) error {
					t.Fatal("invalid command reached receipt write")
					return nil
				},
			}
			if _, err := service.Publish(WithPublicationReceipt(t.Context(), receipt), tenant, draft.ID, actor); !errors.Is(err, scenario.want) {
				t.Fatalf("receipt identity failure ignored: %v", err)
			}
			if _, err := service.CurrentPublished(t.Context(), tenant, exam); !errors.Is(err, ErrNotFound) {
				t.Fatalf("invalid receipt published a current result: %v", err)
			}
		})
	}
}
