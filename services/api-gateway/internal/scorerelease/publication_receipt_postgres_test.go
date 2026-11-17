package scorerelease

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
)

// 正式迁移库验证回执写入后再失败会回滚公开事实，并验证回执记录的是合并后的最终题分。
func TestPostgresPublicationReceiptRollsBackLateFailureAndNeverRewindsCurrent(t *testing.T) {
	db := formalExportTestDB(t)
	f := seedConcurrentPublication(t, db)
	ctx := t.Context()
	service := NewService(NewPostgresStore(db, nil))
	base, err := service.Create(ctx, f.tenantID, f.examID, f.actorID, CreateInput{Reason: "initial", IdempotencyKey: "receipt-pg-base"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, f.tenantID, base.ID, f.actorID); err != nil {
		t.Fatal(err)
	}
	makeDraft := func(question, key string, score float64) Release {
		t.Helper()
		draft, err := service.CreateFromRegrade(ctx, f.tenantID, f.examID, f.actorID, CreateRegradeInput{
			SourceReleaseID: base.ID, QuestionID: question, Reason: "verified correction", IdempotencyKey: key,
			Changes: []RegradeChange{{SubmissionID: f.submissionID, QuestionID: question, Score: score, MaxScore: 5}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return draft
	}
	first := makeDraft(f.questionIDs[0], "receipt-pg-first", 4)
	second := makeDraft(f.questionIDs[1], "receipt-pg-second", 3)
	if _, err = service.Publish(ctx, f.tenantID, first.ID, f.actorID); err != nil {
		t.Fatal(err)
	}
	before, err := service.Get(ctx, f.tenantID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	type storedReceipt struct {
		Release Release
		Facts   []SubmissionFact
	}
	identity := commandreceipt.Identity{TenantID: f.tenantID, ActorID: f.actorID, Operation: "score.publish", TargetID: f.examID}
	input := struct{ Reason string }{"compatibility publication"}
	failSave, saves := true, 0
	failure := errors.New("synthetic failure after business receipt insert")
	receipt := publicationReceiptCallbacks{
		load: func(ctx context.Context, tx *sql.Tx) (Release, bool, error) {
			var stored storedReceipt
			found, err := commandreceipt.Load(ctx, tx, identity, input, &stored)
			return stored.Release, found, err
		},
		save: func(ctx context.Context, tx *sql.Tx, release Release, facts []SubmissionFact, newlyPublished bool) error {
			saves++
			var current string
			if err := tx.QueryRowContext(ctx, `SELECT release_id::text FROM score_release_current WHERE tenant_id=$1 AND exam_id=$2::uuid`, f.tenantID, f.examID).Scan(&current); err != nil {
				return err
			}
			if !newlyPublished || current != second.ID || release.ID != second.ID || release.Status != StatusPublished || release.PublishedAt == nil ||
				len(facts) != 1 || facts[0].TotalScore != 7 || len(facts[0].Questions) != 2 || facts[0].Questions[0].Score != 4 || facts[0].Questions[1].Score != 3 {
				t.Fatalf("receipt was not saved with final merged facts and pointer: current=%s release=%+v facts=%+v", current, release, facts)
			}
			if err := commandreceipt.Save(ctx, tx, identity, input, storedReceipt{Release: release, Facts: facts}); err != nil {
				return err
			}
			if failSave {
				return failure
			}
			return nil
		},
	}
	const commandID = "receipt-pg-command"
	receiptCtx := WithPublicationReceipt(commandreceipt.WithID(ctx, commandID), receipt)
	if _, err = service.Publish(receiptCtx, f.tenantID, second.ID, f.actorID); !errors.Is(err, failure) {
		t.Fatalf("late receipt failure was not returned: %v", err)
	}
	after, err := service.Get(ctx, f.tenantID, second.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed receipt changed the draft snapshot: before=%+v after=%+v err=%v", before, after, err)
	}
	current, err := service.CurrentPublished(ctx, f.tenantID, f.examID)
	if err != nil || current.Release.ID != first.ID || current.Items[0].TotalScore != 6 {
		t.Fatalf("failed receipt left its pointer published: %+v %v", current, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM business_command_receipt WHERE tenant_id=$1 AND actor_id=$2 AND command_id=$3`, f.tenantID, f.actorID, commandID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed publication retained a business receipt: count=%d err=%v", count, err)
	}
	failSave = false
	published, err := service.Publish(receiptCtx, f.tenantID, second.ID, f.actorID)
	if err != nil || saves != 2 {
		t.Fatalf("same command could not recover from rolled back receipt: %+v saves=%d err=%v", published, saves, err)
	}
	rollback, err := service.CreateRollback(ctx, f.tenantID, f.examID, f.actorID, RollbackInput{SourceReleaseID: base.ID, Reason: "restore original", IdempotencyKey: "receipt-pg-rollback"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, f.tenantID, rollback.ID, f.actorID); err != nil {
		t.Fatal(err)
	}
	replay, err := service.Publish(receiptCtx, f.tenantID, second.ID, f.actorID)
	if err != nil || replay.ID != published.ID || replay.Version != published.Version || replay.Status != StatusPublished ||
		replay.SupersedesReleaseID != published.SupersedesReleaseID || replay.SourceReleaseID != published.SourceReleaseID ||
		replay.PublishedAt == nil || !replay.PublishedAt.Equal(*published.PublishedAt) || saves != 2 {
		t.Fatalf("old command did not return its original committed release: %+v saves=%d err=%v", replay, saves, err)
	}
	current, err = service.CurrentPublished(ctx, f.tenantID, f.examID)
	if err != nil || current.Release.ID != rollback.ID || current.Items[0].TotalScore != 4 {
		t.Fatalf("old command replay rewound current release: %+v %v", current, err)
	}
	var stored storedReceipt
	readTx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer readTx.Rollback()
	found, err := commandreceipt.Load(receiptCtx, readTx, identity, input, &stored)
	if err != nil || !found || stored.Release.ID != second.ID || len(stored.Facts) != 1 || stored.Facts[0].TotalScore != 7 {
		t.Fatalf("committed receipt lost the exact released facts: %+v found=%t err=%v", stored, found, err)
	}
}
