package capture

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type captureIdentityFixture struct {
	ctx                                 context.Context
	db                                  *sql.DB
	store                               *PostgresStore
	tenant, exam, actor, class, student string
}

// 复制生产列、检查约束和索引，并安装实际收卷触发器，让版本和封存边界也参与事务。
// 这里验证 PostgreSQL 业务事务，不把复制表当成全部外键、RLS 或真实设备断电验证。
func newCaptureIdentityFixture(t *testing.T) captureIdentityFixture {
	t.Helper()
	ctx, db := captureRecoveryRemediationDB(t)
	f := captureIdentityFixture{ctx: ctx, db: db, store: NewPostgresStore(db), tenant: uuid.NewString(), exam: uuid.NewString(), actor: uuid.NewString(), class: uuid.NewString(), student: uuid.NewString()}
	for _, table := range []string{"exam", "exam_candidate_snapshot", "capture_operation", "capture_collection", "capture_collection_completion", "submission_page", "answer_sheet_template", "exam_answer_sheet_template_binding", "capture_upload_session", "page_template_match_run"} {
		f.exec(t, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)")
	}
	// LIKE 会给索引重新命名；适配器将这两个正式唯一索引的错误映射为业务冲突。
	for _, index := range []struct{ columns, name string }{
		{"(tenant_id, exam_id, student_id)", "uq_submission_exam_student_active"},
		{"(tenant_id, exam_id, candidate_no)", "idx_submission_candidate"},
	} {
		var name string
		if err := db.QueryRowContext(ctx, `SELECT indexname FROM pg_indexes WHERE schemaname=current_schema() AND tablename='submission' AND indexdef LIKE $1`, "%"+index.columns+"%").Scan(&name); err != nil {
			t.Fatal(err)
		}
		f.exec(t, "ALTER INDEX "+pgx.Identifier{name}.Sanitize()+" RENAME TO "+pgx.Identifier{index.name}.Sanitize())
	}
	for _, trigger := range []struct{ table, name string }{
		{"capture_batch", "capture_collection_batch"}, {"capture_file", "capture_collection_file"},
		{"capture_page", "capture_collection_page"}, {"submission", "capture_collection_submission"},
		{"submission_page", "capture_collection_submission_page"}, {"exam_candidate_snapshot", "capture_collection_roster"},
		{"capture_upload_session", "capture_collection_upload"},
	} {
		var definition string
		if err := db.QueryRowContext(ctx, `SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgrelid=$1::regclass AND tgname=$2`, "public."+trigger.table, trigger.name).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(definition, " ON public."+trigger.table+" ") {
			t.Fatalf("production trigger target was not explicitly qualified: %s", definition)
		}
		f.exec(t, strings.Replace(definition, " ON public."+trigger.table+" ", " ON "+trigger.table+" ", 1))
	}
	f.exec(t, `INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by)
VALUES($1,$2,$3,'Synthetic capture reliability','math','quiz',10,'collecting','ai_assisted','manual_after_confirmation',$4)`, f.exam, f.tenant, uuid.NewString(), f.actor)
	f.roster(t, f.tenant, f.exam, f.student, "001")
	return f
}

func (f captureIdentityFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.ExecContext(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f captureIdentityFixture) roster(t *testing.T, tenant, exam, student, number string) {
	t.Helper()
	f.exec(t, `INSERT INTO exam_candidate_snapshot(tenant_id,exam_id,student_id,student_no_snapshot,student_name_snapshot,grade_id_snapshot,grade_name_snapshot,class_id_snapshot,class_name_snapshot)
VALUES($1,$2,$3,$4,'Synthetic student',$5,'Synthetic grade',$6,'Synthetic class')`, tenant, exam, student, number, uuid.NewString(), f.class)
}

func (f captureIdentityFixture) batchAndFile(t *testing.T) (string, string) {
	t.Helper()
	batch, file := uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO capture_batch(id,tenant_id,exam_id,name,source_type,operator_id,status)
VALUES($1,$2,$3,'Synthetic answer sheets','web_upload',$4,'matching')`, batch, f.tenant, f.exam, f.actor)
	f.exec(t, `INSERT INTO capture_file(id,tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,page_count,idempotency_key,uploaded_by,status)
VALUES($1,$2,$3,$4,'synthetic.png','image/png',$5,64,2,$1::uuid::text,$6,'completed')`, file, f.tenant, batch, uuid.NewString(), strings.Repeat("a", 64), f.actor)
	return batch, file
}

func (f captureIdentityFixture) submission(t *testing.T, student string, pages int) string {
	t.Helper()
	id, identity, number := uuid.NewString(), "unassigned", ""
	if student != "" {
		identity, number = "matched", "001"
	}
	f.exec(t, `INSERT INTO submission(id,tenant_id,exam_id,student_id,candidate_no,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by,identity_status)
VALUES($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,''),'image_upload','pages_uploaded',$6,$6,'passed',$7,$8)`, id, f.tenant, f.exam, student, number, pages, f.actor, identity)
	return id
}

func (f captureIdentityFixture) page(t *testing.T, batch, file, submission string, number int) string {
	t.Helper()
	page, submissionPage, asset := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status,quality_status)
VALUES($1,$2,$3,$4,$5,'accepted','passed')`, submissionPage, f.tenant, submission, asset, number)
	f.exec(t, `INSERT INTO capture_page(id,tenant_id,capture_batch_id,capture_file_id,source_index,submission_id,submission_page_id,assigned_page_no,sequence_no,decoded_file_asset_id,status)
VALUES($1,$2,$3,$4,$5,$6,$7,$5,$5,$8,'ready')`, page, f.tenant, batch, file, number, submission, submissionPage, asset)
	return page
}

func (f captureIdentityFixture) facts(t *testing.T) string {
	t.Helper()
	var facts string
	err := f.db.QueryRowContext(f.ctx, `SELECT jsonb_build_object(
 'exam',(SELECT to_jsonb(e) FROM exam e WHERE tenant_id=$1 AND id=$2),
 'collection',(SELECT to_jsonb(c) FROM capture_collection c WHERE tenant_id=$1 AND exam_id=$2),
 'batches',(SELECT jsonb_agg(to_jsonb(b) ORDER BY id) FROM capture_batch b WHERE tenant_id=$1 AND exam_id=$2),
 'files',(SELECT jsonb_agg(to_jsonb(f) ORDER BY id) FROM capture_file f WHERE tenant_id=$1),
 'submissions',(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM submission s WHERE tenant_id=$1 AND exam_id=$2),
 'capture_pages',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM capture_page p WHERE tenant_id=$1),
 'submission_pages',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM submission_page p WHERE tenant_id=$1),
 'operations',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM capture_operation o WHERE tenant_id=$1),
 'receipts',(SELECT jsonb_agg(to_jsonb(r) ORDER BY command_id) FROM capture_collection_completion r WHERE tenant_id=$1 AND exam_id=$2)
)::text`, f.tenant, f.exam).Scan(&facts)
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func TestPostgresStudentIdentityPreventsCrossRosterAndConcurrentDoubleBinding(t *testing.T) {
	f := newCaptureIdentityFixture(t)
	secondStudent, otherExamStudent, otherTenantStudent := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.roster(t, f.tenant, f.exam, secondStudent, "002")
	f.roster(t, f.tenant, uuid.NewString(), otherExamStudent, "003")
	f.roster(t, uuid.NewString(), uuid.NewString(), otherTenantStudent, "004")
	var submissions, batches, pages [2]string
	for i := range submissions {
		var file string
		batches[i], file = f.batchAndFile(t)
		submissions[i] = f.submission(t, "", 1)
		pages[i] = f.page(t, batches[i], file, submissions[i], 1)
	}
	before := f.facts(t)
	for _, student := range []string{otherExamStudent, otherTenantStudent} {
		_, err := f.store.ConfirmStudentMatch(f.ctx, f.tenant, submissions[0], f.actor, ConfirmStudentMatchInput{StudentID: student, Revision: 1, Reason: "check frozen roster"})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("student outside the frozen exam roster was accepted: %v", err)
		}
	}
	if after := f.facts(t); after != before {
		t.Fatal("rejected cross-roster binding changed identity, pages or collection version")
	}

	// 两批答卷同时争同一名册学生；只允许一个身份提交，失败方必须保持原页归属和版本。
	type result struct {
		index int
		value MatchingSubmission
		err   error
	}
	start, results := make(chan struct{}), make(chan result, 2)
	for i := range submissions {
		go func() {
			<-start
			value, err := f.store.ConfirmStudentMatch(f.ctx, f.tenant, submissions[i], f.actor, ConfirmStudentMatchInput{StudentID: f.student, Revision: 1, Reason: "reviewed original sheet"})
			results <- result{i, value, err}
		}()
	}
	close(start)
	winner, loser := -1, -1
	for range submissions {
		out := <-results
		if out.err == nil && out.value.StudentID == f.student && out.value.IdentityRevision == 2 {
			if winner != -1 {
				t.Error("two active submissions were bound to the same student")
			}
			winner = out.index
		} else if errors.Is(out.err, ErrConflict) {
			loser = out.index
		} else {
			t.Errorf("unexpected concurrent binding: %+v %v", out.value, out.err)
		}
	}
	if t.Failed() {
		return
	}
	if winner < 0 || loser < 0 || winner == loser {
		t.Fatalf("expected one success and one conflict: winner=%d loser=%d", winner, loser)
	}
	var bound, unassigned, operations int
	if err := f.db.QueryRowContext(f.ctx, `SELECT
 (SELECT count(*) FROM submission WHERE tenant_id=$1 AND exam_id=$2 AND student_id=$3 AND identity_status='matched' AND identity_revision=2),
 (SELECT count(*) FROM submission WHERE tenant_id=$1 AND id=$4 AND student_id IS NULL AND identity_status='unassigned' AND identity_revision=1),
 (SELECT count(*) FROM capture_operation WHERE tenant_id=$1)`, f.tenant, f.exam, f.student, submissions[loser]).Scan(&bound, &unassigned, &operations); err != nil || bound != 1 || unassigned != 1 || operations != 1 {
		t.Fatalf("concurrent loser left partial identity facts: bound=%d unassigned=%d audits=%d err=%v", bound, unassigned, operations, err)
	}

	before = f.facts(t)
	f.exec(t, `CREATE FUNCTION reject_identity_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM submission WHERE tenant_id=NEW.tenant_id AND id=NEW.target_id AND identity_status='matched' AND identity_revision=2) THEN
  RAISE EXCEPTION 'identity audit reached before identity change';
 END IF;
 RAISE EXCEPTION 'synthetic identity audit failure';
END $$`)
	f.exec(t, `CREATE TRIGGER reject_identity_audit BEFORE INSERT ON capture_operation FOR EACH ROW EXECUTE FUNCTION reject_identity_audit()`)
	input := ConfirmStudentMatchInput{StudentID: secondStudent, Revision: 1, Reason: "reviewed second student's original sheet"}
	_, err := f.store.ConfirmStudentMatch(f.ctx, f.tenant, submissions[loser], f.actor, input)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Message != "synthetic identity audit failure" {
		t.Fatalf("expected late identity audit failure, got %v", err)
	}
	if after := f.facts(t); after != before {
		t.Fatal("failed identity audit changed identities, page ownership, batch counts or collection version")
	}
	f.exec(t, `DROP TRIGGER reject_identity_audit ON capture_operation`)
	matched, err := f.store.ConfirmStudentMatch(f.ctx, f.tenant, submissions[loser], f.actor, input)
	if err != nil || matched.StudentID != secondStudent || matched.CandidateNo != "002" || matched.IdentityRevision != 2 {
		t.Fatalf("same reviewed binding could not recover: %+v %v", matched, err)
	}
	afterRecovery := f.facts(t)
	if _, err := f.store.ConfirmStudentMatch(f.ctx, f.tenant, submissions[loser], f.actor, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale identity revision was accepted after recovery: %v", err)
	}
	if f.facts(t) != afterRecovery {
		t.Fatal("stale binding created another identity or audit write")
	}
	for i, submission := range submissions {
		queue, err := f.store.GetMatchingQueue(f.ctx, f.tenant, batches[i])
		student := f.student
		if i == loser {
			student = secondStudent
		}
		if err != nil || len(queue.Candidates) != 2 || len(queue.Submissions) != 1 || queue.Submissions[0].ID != submission || queue.Submissions[0].StudentID != student || len(queue.Submissions[0].Pages) != 1 || queue.Submissions[0].Pages[0].ID != pages[i] || queue.Submissions[0].Pages[0].SubmissionID != submission {
			t.Fatalf("matching queue crossed original sheet ownership: %+v %v", queue, err)
		}
	}
}

func TestPostgresCollectionMissingPagesAndLateReceiptFailureRecoverAtomically(t *testing.T) {
	f := newCaptureIdentityFixture(t)
	f.roster(t, f.tenant, f.exam, uuid.NewString(), "002")
	f.exec(t, `INSERT INTO answer_sheet_template(id,tenant_id,exam_id,exam_paper_id,version_no,name,status,page_count,layout,content_hash,created_by)
VALUES($1,$2,$3,$4,1,'Synthetic two-page sheet','locked',2,'{}',$5,$6)`, uuid.NewString(), f.tenant, f.exam, uuid.NewString(), strings.Repeat("a", 64), f.actor)
	batch, file := f.batchAndFile(t)
	submission := f.submission(t, f.student, 2)
	f.page(t, batch, file, submission, 1)
	summary, err := f.store.GetCollectionSummary(f.ctx, f.tenant, f.exam)
	if err != nil || summary.CanComplete || summary.PendingCount != 1 || summary.AbsentCount != 1 || len(summary.Entries) != 2 || summary.Entries[0].Code != "missing_pages" || summary.Entries[0].ActualPages != 1 {
		t.Fatalf("stored page-count counters hid missing original page: %+v %v", summary, err)
	}
	before := f.facts(t)
	_, err = f.store.CompleteCollection(f.ctx, f.tenant, f.exam, f.actor, CompleteCollectionInput{Revision: summary.Revision, IdempotencyKey: "missing-page", Reason: "checked original pages"})
	if !errors.Is(err, ErrInvalidTransition) || f.facts(t) != before {
		t.Fatalf("missing-page collection changed persisted facts: %v", err)
	}

	f.page(t, batch, file, submission, 2)
	ready, err := f.store.GetCollectionSummary(f.ctx, f.tenant, f.exam)
	if err != nil || !ready.CanComplete || ready.ReadyCount != 1 || ready.AbsentCount != 1 || ready.PendingCount != 0 {
		t.Fatalf("recovered page did not make only present student ready: %+v %v", ready, err)
	}
	before = f.facts(t)
	// 回执触发器先确认封存和考试状态已经写入，避免把提前拒绝当作末尾事务回滚。
	f.exec(t, `CREATE FUNCTION reject_collection_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM capture_collection WHERE tenant_id=NEW.tenant_id AND exam_id=NEW.exam_id AND completed_at IS NOT NULL)
 OR NOT EXISTS(SELECT 1 FROM exam WHERE tenant_id=NEW.tenant_id AND id=NEW.exam_id AND status='grading')
 OR EXISTS(SELECT 1 FROM capture_batch WHERE tenant_id=NEW.tenant_id AND exam_id=NEW.exam_id AND status<>'completed') THEN
  RAISE EXCEPTION 'collection receipt reached before sealing';
 END IF;
 RAISE EXCEPTION 'synthetic collection receipt failure';
END $$`)
	f.exec(t, `CREATE TRIGGER reject_collection_receipt BEFORE INSERT ON capture_collection_completion FOR EACH ROW EXECUTE FUNCTION reject_collection_receipt()`)
	input := CompleteCollectionInput{Revision: ready.Revision, IdempotencyKey: "close-checked-originals", Reason: "checked all present students' original pages"}
	_, err = f.store.CompleteCollection(f.ctx, f.tenant, f.exam, f.actor, input)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Message != "synthetic collection receipt failure" {
		t.Fatalf("expected late receipt failure, got %v", err)
	}
	if f.facts(t) != before {
		t.Fatal("late receipt failure retained seal, exam status, revision or material changes")
	}
	f.exec(t, `DROP TRIGGER reject_collection_receipt ON capture_collection_completion`)
	closed, err := f.store.CompleteCollection(f.ctx, f.tenant, f.exam, f.actor, input)
	if err != nil || !closed.Closed || closed.CanComplete || closed.CompletedBy != f.actor || closed.Revision <= ready.Revision || closed.ReadyCount != 1 || closed.AbsentCount != 1 {
		t.Fatalf("original completion request could not recover: %+v %v", closed, err)
	}
	sealedFacts := f.facts(t)
	asset := FileAssetSnapshot{ID: uuid.NewString(), SHA256: strings.Repeat("b", 64), SizeBytes: 64, OriginalName: "late.png", ContentType: "image/png"}
	if _, err := f.store.RegisterFile(f.ctx, f.tenant, batch, f.actor, RegisterFileInput{FileAssetID: asset.ID, IdempotencyKey: "late-original"}, asset); !errors.Is(err, ErrInvalidTransition) || f.facts(t) != sealedFacts {
		t.Fatalf("sealed batch accepted more original material: %v", err)
	}
	var receipts int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM capture_collection_completion WHERE tenant_id=$1 AND exam_id=$2`, f.tenant, f.exam).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("completion did not persist exactly one receipt: count=%d err=%v", receipts, err)
	}

	// 补传会重新打开实际台账；旧命令重放仍只读旧回执，不能把新批次再次封存。
	f.exec(t, `INSERT INTO capture_batch(tenant_id,exam_id,name,source_type,operator_id,status,purpose,supplement_reason)
VALUES($1,$2,'Synthetic supplement','web_upload',$3,'uploading','supplement','missing materials discovered after close')`, f.tenant, f.exam, f.actor)
	withSupplement := f.facts(t)
	replay, err := f.store.CompleteCollection(f.ctx, f.tenant, f.exam, f.actor, input)
	closedJSON, marshalErr := json.Marshal(closed)
	replayJSON, replayMarshalErr := json.Marshal(replay)
	if err != nil || marshalErr != nil || replayMarshalErr != nil || !bytes.Equal(closedJSON, replayJSON) || f.facts(t) != withSupplement {
		t.Fatalf("completion replay altered new supplement or lost stored result: %+v %v", replay, err)
	}
	input.Reason = "changed reviewed reason"
	if _, err := f.store.CompleteCollection(f.ctx, f.tenant, f.exam, f.actor, input); !errors.Is(err, ErrConflict) || f.facts(t) != withSupplement {
		t.Fatalf("same command key accepted a changed completion request: %v", err)
	}
	current, err := f.store.GetCollectionSummary(f.ctx, f.tenant, f.exam)
	if err != nil || current.Closed || current.Revision <= closed.Revision {
		t.Fatalf("replayed receipt hid actual reopened collection: %+v %v", current, err)
	}
	t.Logf("missing page blocked; late receipt rollback and retry succeeded; one saved receipt at revision %d, supplement now revision %d", closed.Revision, current.Revision)
}
