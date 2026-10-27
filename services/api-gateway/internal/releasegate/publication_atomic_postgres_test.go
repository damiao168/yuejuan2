package releasegate

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/qualitydashboard"
	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type publicationFixture struct {
	db                                                                                *sql.DB
	tenant, exam, actor, question, student, submission, segment, final, source, draft string
}

// 只复制本任务临时集群的表结构，所有身份和成绩都是随机 schema 中的合成数据。
// 不复制外键与跨模块触发器；这些测试针对真实发布事务、行约束和考试咨询锁。
func newPublicationFixture(t *testing.T) publicationFixture {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL test DSN required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	schema := "publication_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema + ",public"
	cfg.RuntimeParams["application_name"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	for _, table := range []string{"exam", "question", "submission", "answer_segment", "final_grade", "submission_grade", "review_task", "arbitration_task", "ocr_task", "exam_candidate_snapshot", "exam_student_attendance", "exam_question_snapshot", "score_release", "score_release_current", "score_release_item", "score_release_question", "release_gate_policy", "release_gate_evidence", "release_gate_waiver_request", "release_gate_waiver_decision", "regrade_job", "regrade_item", "regrade_event"} {
		if _, err := db.ExecContext(t.Context(), "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatalf("clone %s: %v", table, err)
		}
	}
	f := publicationFixture{db: db, tenant: uuid.NewString(), exam: uuid.NewString(), actor: uuid.NewString(), question: uuid.NewString(), student: uuid.NewString(), submission: uuid.NewString(), segment: uuid.NewString(), final: uuid.NewString(), source: uuid.NewString(), draft: uuid.NewString()}
	f.exec(t, `INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2,$3,'synthetic publication','math','exam',5,'finalized','human_review_required','manual',$3)`, f.exam, f.tenant, f.actor)
	f.exec(t, `INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1,$2,$3,'1','short_answer',5,1,'active')`, f.question, f.tenant, f.exam)
	f.exec(t, `INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1,$2,$3,$4,1,'short_constructed','[]','R1','{}','{}','{}','{}',repeat('a',64))`, f.tenant, f.exam, f.question, f.actor)
	f.addSubmission(t, f.student, f.submission)
	f.addGrades(t, f.student, f.submission, f.segment, f.final)
	for version, id := range []string{f.source, f.draft} {
		f.exec(t, `INSERT INTO score_release(id,tenant_id,exam_id,version,source,reason,idempotency_key,created_by) VALUES($1,$2,$3,$4,'initial','synthetic verification',$5,$6)`, id, f.tenant, f.exam, version+1, "synthetic-"+id, f.actor)
		f.exec(t, `INSERT INTO score_release_item(tenant_id,release_id,student_id,submission_id,total_score,max_score,status,snapshot_hash) VALUES($1,$2,$3,$4,4,5,'confirmed',repeat('a',64))`, f.tenant, id, f.student, f.submission)
		f.exec(t, `INSERT INTO score_release_question(tenant_id,release_id,submission_id,question_id,question_no,final_grade_id,score,max_score,source_type) VALUES($1,$2,$3,$4,'1',$5,4,5,'single_review')`, f.tenant, id, f.submission, f.question, f.final)
	}
	f.exec(t, `UPDATE score_release SET status='published',published_by=$2,published_at=now() WHERE id=$1`, f.source, f.actor)
	f.exec(t, `INSERT INTO score_release_current(tenant_id,exam_id,release_id) VALUES($1,$2,$3)`, f.tenant, f.exam, f.source)
	return f
}

func (f publicationFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("fixture SQL: %v", err)
	}
}
func (f publicationFixture) addSubmission(t *testing.T, student, submission string) {
	f.exec(t, `INSERT INTO exam_candidate_snapshot(tenant_id,exam_id,student_id,student_no_snapshot,student_name_snapshot,grade_id_snapshot,grade_name_snapshot,class_id_snapshot,class_name_snapshot) VALUES($1,$2,$3::uuid,$3::uuid::text,'synthetic',$4,'synthetic',$4,'synthetic')`, f.tenant, f.exam, student, f.actor)
	f.exec(t, `INSERT INTO submission(id,tenant_id,exam_id,student_id,candidate_no,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by) VALUES($1,$2,$3,$4::uuid,$4::uuid::text,'manual_import','quality_checked',0,0,'passed',$5)`, submission, f.tenant, f.exam, student, f.actor)
}
func (f publicationFixture) addGrades(t *testing.T, student, submission, segment, final string) {
	f.exec(t, `INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1,$2,$3,$1,$4,'1','{}','manual','accepted')`, segment, f.tenant, submission, f.question)
	f.exec(t, `INSERT INTO final_grade(id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,score,max_score,source,status,created_by) VALUES($1,$2,$3,$4,'1',$5,$6::uuid,$6::uuid::text,4,5,'single_review','confirmed',$7)`, final, f.tenant, f.exam, f.question, segment, submission, f.actor)
	f.exec(t, `INSERT INTO submission_grade(tenant_id,exam_id,submission_id,student_id,anonymous_code,total_score,max_score,status,created_by) VALUES($1,$2,$3::uuid,$4,$3::uuid::text,4,5,'confirmed',$5) ON CONFLICT(tenant_id,exam_id,submission_id) DO NOTHING`, f.tenant, f.exam, submission, student, f.actor)
}

func TestPostgresLegacyPublishRequiresEveryMatchedSubmission(t *testing.T) {
	f := newPublicationFixture(t)
	store := score.NewPostgresStore(f.db)
	student, submission := uuid.NewString(), uuid.NewString()
	f.addSubmission(t, student, submission)
	assertBlocked := func(wantMissingTotal bool) {
		t.Helper()
		result, err := store.PublishGrades(t.Context(), f.tenant, f.exam, f.actor, score.PublishInput{Reason: "synthetic"})
		if !errors.Is(err, score.ErrQualityGateFailed) {
			t.Fatalf("incomplete matched submission published: result=%#v err=%v", result, err)
		}
		codes := map[string]int{}
		for _, issue := range result.Quality.Issues {
			codes[issue.Code] = issue.Count
		}
		if (codes["missing_submission_grades"] == 1) != wantMissingTotal || codes["question_coverage_incomplete"] != 1 {
			t.Fatalf("wrong missing coverage: %#v", codes)
		}
		var status string
		if err = f.db.QueryRowContext(t.Context(), `SELECT status FROM exam WHERE id=$1`, f.exam).Scan(&status); err != nil || status != "finalized" {
			t.Fatalf("blocked exam mutated: %s %v", status, err)
		}
	}
	assertBlocked(true)
	// 人工补入总分仍不能代替逐题覆盖；随后补全题分才允许公开。
	f.exec(t, `INSERT INTO submission_grade(tenant_id,exam_id,submission_id,student_id,anonymous_code,total_score,max_score,status,created_by) VALUES($1,$2,$3::uuid,$4,$3::uuid::text,4,5,'confirmed',$5)`, f.tenant, f.exam, submission, student, f.actor)
	assertBlocked(false)
	f.addGrades(t, student, submission, uuid.NewString(), uuid.NewString())
	result, err := store.PublishGrades(t.Context(), f.tenant, f.exam, f.actor, score.PublishInput{Reason: "synthetic complete"})
	if err != nil || result.Status != "published" || len(result.SubmissionGrades) != 2 {
		t.Fatalf("complete control failed: %#v %v", result, err)
	}
}

type publicationRegradeReader struct{ db *sql.DB }

func (r publicationRegradeReader) BlockingRegradeCount(ctx context.Context, tenantID, examID string) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	return regrade.BlockingReleaseCountTx(ctx, tx, tenantID, examID)
}
func (r publicationRegradeReader) BlockingRegradeCountTx(ctx context.Context, tx *sql.Tx, tenantID, examID string) (int, error) {
	return regrade.BlockingReleaseCountTx(ctx, tx, tenantID, examID)
}

type publicationWindow struct {
	*scorerelease.Service
	before     func()
	afterCheck func()
}

func (p publicationWindow) PublishGuarded(ctx context.Context, tenantID, id, actorID string, guard scorerelease.PublishGuard) (scorerelease.Release, error) {
	if p.before != nil {
		p.before()
	}
	return p.Service.PublishGuarded(ctx, tenantID, id, actorID, func(ctx context.Context, tx *sql.Tx, release scorerelease.Release, base scorerelease.Gate) error {
		err := guard(ctx, tx, release, base)
		if p.afterCheck != nil {
			p.afterCheck()
		}
		return err
	})
}
func (f publicationFixture) services() (*Service, *scorerelease.Service) {
	releases := scorerelease.NewService(scorerelease.NewPostgresStore(f.db, nil))
	return NewService(NewPostgresStore(f.db), releases).WithRegradeBlockerReader(publicationRegradeReader{f.db}), releases
}
func (f publicationFixture) createRegrade(ctx context.Context) (regrade.Job, error) {
	delta := 2.0
	job, _, err := regrade.NewPostgresStore(f.db).Create(ctx, f.tenant, f.exam, f.question, f.actor, regrade.CreateInput{SourceReleaseID: f.source, ReasonCode: "other", ReasonText: "synthetic window", Strategy: "human_recheck", SeverityDelta: &delta, IdempotencyKey: "synthetic-" + uuid.NewString()}, nil)
	return job, err
}

func TestPostgresPublicationRejectsRegradeCreatedAfterPassingEvidence(t *testing.T) {
	f := newPublicationFixture(t)
	gates, releases := f.services()
	old, err := gates.RecheckForPublish(t.Context(), f.tenant, f.exam, f.draft, f.actor)
	if err != nil || !old.Evaluation.Passed {
		t.Fatalf("initial check: %#v %v", old, err)
	}
	publisher := publicationWindow{Service: releases, before: func() {
		if _, err := f.createRegrade(t.Context()); err != nil {
			t.Fatal(err)
		}
	}}
	_, evidence, err := NewPublicationCoordinator(gates, publisher).Publish(t.Context(), f.tenant, f.exam, f.draft, f.actor)
	if !errors.Is(err, scorerelease.ErrGateBlocked) || evidence.Evaluation.Passed || evidence.Evaluation.BaseGate.Counts["regrade_release_pending"] != 1 {
		t.Fatalf("new regrade bypassed final lock: %#v %v", evidence, err)
	}
	saved, err := gates.GetEvidence(t.Context(), f.tenant, evidence.ID)
	if err != nil || saved.Hash != evidence.Hash {
		t.Fatalf("blocked evidence not committed: %#v %v", saved, err)
	}
	detail, err := releases.Get(t.Context(), f.tenant, f.draft)
	if err != nil || detail.Release.Status != scorerelease.StatusDraft {
		t.Fatalf("blocked draft changed: %#v %v", detail, err)
	}
	oldSaved, err := gates.GetEvidence(t.Context(), f.tenant, old.ID)
	if err != nil || !oldSaved.Evaluation.Passed || oldSaved.Hash != old.Hash {
		t.Fatalf("historical evidence changed: %#v %v", oldSaved, err)
	}
}

func TestPostgresPublicationCommitsEvidenceAndStatusTogether(t *testing.T) {
	f := newPublicationFixture(t)
	gates, releases := f.services()
	// 模拟最终公开写入失败；门禁通过证据不能独立留下，重试才能生成新的已提交证据。
	f.exec(t, `CREATE FUNCTION fail_publication() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic publication failure'; END $$; CREATE TRIGGER fail_publication BEFORE UPDATE ON score_release FOR EACH ROW EXECUTE FUNCTION fail_publication()`)
	_, evidence, err := NewPublicationCoordinator(gates, releases).Publish(t.Context(), f.tenant, f.exam, f.draft, f.actor)
	if err == nil || evidence.ID != "" {
		t.Fatalf("failed transaction returned evidence: %#v %v", evidence, err)
	}
	var count int
	if err = f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM release_gate_evidence`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed publication left evidence: %d %v", count, err)
	}
	f.exec(t, `DROP TRIGGER fail_publication ON score_release`)
	release, evidence, err := NewPublicationCoordinator(gates, releases).Publish(t.Context(), f.tenant, f.exam, f.draft, f.actor)
	if err != nil || release.Status != scorerelease.StatusPublished || evidence.ID == "" || !evidence.Evaluation.Passed {
		t.Fatalf("retry failed: %#v %#v %v", release, evidence, err)
	}
}

type publicationWarningDashboard struct{}

func (publicationWarningDashboard) Get(context.Context, string, string) (qualitydashboard.Dashboard, error) {
	return qualitydashboard.Dashboard{Warnings: []qualitydashboard.Finding{{Code: "quality_warning", Reason: "synthetic warning"}}}, nil
}

func TestPostgresPublicationRechecksPolicyAndDoesNotReuseOldWaiver(t *testing.T) {
	f := newPublicationFixture(t)
	releases := scorerelease.NewService(scorerelease.NewPostgresStore(f.db, publicationWarningDashboard{}))
	gates := NewService(NewPostgresStore(f.db), releases).WithRegradeBlockerReader(publicationRegradeReader{f.db})
	input := CreatePolicyInput{Version: "synthetic-v1", RequireWarningAcknowledgement: true, WaivableWarningCodes: []string{"quality_warning"}}
	if _, err := gates.CreatePolicy(t.Context(), f.tenant, f.exam, f.actor, input); err != nil {
		t.Fatal(err)
	}
	approveCurrentWarning := func() Waiver {
		t.Helper()
		preview, err := gates.Preview(t.Context(), f.tenant, f.exam, f.draft, f.actor)
		if err != nil {
			t.Fatal(err)
		}
		waiver, err := gates.RequestWaiver(t.Context(), f.tenant, f.exam, f.actor, RequestWaiverInput{EvidenceID: preview.ID, IssueCode: "quality_warning", Reason: "synthetic review"})
		if err != nil {
			t.Fatal(err)
		}
		waiver, err = gates.DecideWaiver(t.Context(), f.tenant, waiver.ID, f.actor, DecideWaiverInput{Approve: true, Reason: "synthetic approval"})
		if err != nil {
			t.Fatal(err)
		}
		return waiver
	}
	oldWaiver := approveCurrentWarning()
	old, err := gates.RecheckForPublish(t.Context(), f.tenant, f.exam, f.draft, f.actor)
	if err != nil || len(old.Evaluation.AppliedWaiverIDs) != 1 || old.Evaluation.AppliedWaiverIDs[0] != oldWaiver.ID {
		t.Fatalf("old policy control: %#v %v", old, err)
	}
	var current Policy
	publisher := publicationWindow{Service: releases, before: func() {
		input.Version = "synthetic-v2"
		var err error
		current, err = gates.CreatePolicy(t.Context(), f.tenant, f.exam, f.actor, input)
		if err != nil {
			t.Fatal(err)
		}
	}}
	_, evidence, err := NewPublicationCoordinator(gates, publisher).Publish(t.Context(), f.tenant, f.exam, f.draft, f.actor)
	if !errors.Is(err, scorerelease.ErrGateBlocked) || evidence.Evaluation.Policy.ID != current.ID || len(evidence.Evaluation.AppliedWaiverIDs) != 0 || len(evidence.Evaluation.PendingWarningCodes) != 1 {
		t.Fatalf("old waiver bypassed final policy: %#v %v", evidence, err)
	}
	newWaiver := approveCurrentWarning()
	release, evidence, err := NewPublicationCoordinator(gates, releases).Publish(t.Context(), f.tenant, f.exam, f.draft, f.actor)
	if err != nil || release.Status != scorerelease.StatusPublished || evidence.Evaluation.Policy.ID != current.ID || len(evidence.Evaluation.AppliedWaiverIDs) != 1 || evidence.Evaluation.AppliedWaiverIDs[0] != newWaiver.ID {
		t.Fatalf("new waiver control failed: %#v %#v %v", release, evidence, err)
	}
}

func TestPostgresPublicationLockSerializesNewGateFacts(t *testing.T) {
	for _, kind := range []string{"regrade", "policy"} {
		t.Run(kind, func(t *testing.T) { assertPublicationLockSerializesWriter(t, kind) })
	}
}

func assertPublicationLockSerializesWriter(t *testing.T, kind string) {
	t.Helper()
	f := newPublicationFixture(t)
	gates, releases := f.services()
	checked, finish := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	publisher := publicationWindow{Service: releases, afterCheck: func() {
		close(checked)
		select {
		case <-finish:
		case <-ctx.Done():
		}
	}}
	published := make(chan error, 1)
	go func() {
		_, _, err := NewPublicationCoordinator(gates, publisher).Publish(ctx, f.tenant, f.exam, f.draft, f.actor)
		published <- err
	}()
	select {
	case <-checked:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	created := make(chan error, 1)
	go func() {
		var err error
		if kind == "regrade" {
			_, err = f.createRegrade(ctx)
		} else {
			_, err = gates.CreatePolicy(ctx, f.tenant, f.exam, f.actor, CreatePolicyInput{Version: "synthetic-concurrent"})
		}
		created <- err
	}()
	// 使用数据库实际等待状态证明新作业在考试锁上排队，不把 goroutine 启动当成竞争已经发生。
	for {
		var waiting bool
		err := f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=current_setting('application_name') AND wait_event='advisory')`).Scan(&waiting)
		if err != nil {
			close(finish)
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-created:
			close(finish)
			t.Fatalf("new %s did not wait for publication: %v", kind, err)
		case <-ctx.Done():
			close(finish)
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(finish)
	if err := <-published; err != nil {
		t.Fatal(err)
	}
	if err := <-created; err != nil {
		t.Fatal(err)
	}
}
