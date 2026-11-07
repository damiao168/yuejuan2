package score

import (
	"database/sql"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"github.com/google/uuid"
)

// 复用已完整迁移、已经确认的双学生夹具。这里只准备已复核任务与待结案申诉状态，
// 实际调用发布服务及申诉结案事务，验证两项独立更正不再因共用原发布而互相堵死。
func assertPublishedRegradeContinuity(t *testing.T, db *sql.DB, tenant, exam, actor, question string, submissions []string) {
	t.Helper()
	ctx := t.Context()
	store := scorerelease.NewPostgresStore(db, nil)
	service := scorerelease.NewService(store)
	base, err := service.Create(ctx, tenant, exam, actor, scorerelease.CreateInput{Reason: "synthetic initial", IdempotencyKey: "continuity-initial"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, tenant, base.ID, actor); err != nil {
		t.Fatal(err)
	}
	ordinary, err := service.Create(ctx, tenant, exam, actor, scorerelease.CreateInput{Reason: "ordinary before regrade", IdempotencyKey: "continuity-ordinary"})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	drafts := []scorerelease.Release{}
	appeals := []string{}
	for index, submission := range submissions {
		job, reviewed, appealID := uuid.NewString(), uuid.NewString(), uuid.NewString()
		score := []float64{9, 7}[index]
		exec(`INSERT INTO regrade_job(id,tenant_id,exam_id,question_id,source_release_id,reason_code,reason_text,strategy,idempotency_key,status,affected_count,created_by,finalized_by,finalized_at)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'other','synthetic parallel appeal','human_recheck',$1::text,'ready_for_release',1,$6::uuid,$6::uuid,now())`, job, tenant, exam, question, base.ID, actor)
		exec(`INSERT INTO regrade_item(tenant_id,job_id,submission_id,old_final_grade_id,old_score,max_score,reviewed_grade_id,reviewed_score,status)
SELECT $1,$2,submission_id,final_grade_id,score,max_score,$4,$5,'resolved' FROM score_release_question WHERE tenant_id=$1 AND release_id=$3 AND submission_id=$6 AND question_id=$7`, tenant, job, base.ID, reviewed, score, submission, question)
		exec(`INSERT INTO question_appeal(id,tenant_id,exam_id,student_id,submission_id,source_release_id,source_release_version,question_id,question_no,source_final_grade_id,source_score,source_max_score,reason_code,reason,status,decision,regrade_job_id,created_by)
SELECT $1,q.tenant_id,$2,i.student_id,q.submission_id,q.release_id,$3,q.question_id,q.question_no,q.final_grade_id,q.score,q.max_score,'other','synthetic appeal','upheld_pending_regrade','refer_regrade',$4,$5
FROM score_release_question q JOIN score_release_item i ON i.tenant_id=q.tenant_id AND i.release_id=q.release_id AND i.submission_id=q.submission_id
WHERE q.tenant_id=$6 AND q.release_id=$7 AND q.submission_id=$8 AND q.question_id=$9`, appealID, exam, base.Version, job, actor, tenant, base.ID, submission, question)
		draft, err := service.CreateFromRegrade(ctx, tenant, exam, actor, scorerelease.CreateRegradeInput{SourceReleaseID: base.ID, QuestionID: question, Reason: "reviewed correction", IdempotencyKey: "regrade-job:" + job,
			Changes: []scorerelease.RegradeChange{{SubmissionID: submission, QuestionID: question, Score: score, MaxScore: 10, ReviewedGradeID: reviewed}}})
		if err != nil {
			t.Fatal(err)
		}
		drafts = append(drafts, draft)
		appeals = append(appeals, appealID)
	}
	appealStore := appeal.NewPublishedQuestionAppealPostgresStore(db)
	for index, draft := range drafts {
		published, err := service.Publish(ctx, tenant, draft.ID, actor)
		if err != nil || published.SourceReleaseID != base.ID {
			t.Fatalf("parallel publish %d: %+v %v", index, published, err)
		}
		resolved, err := appealStore.ResolvePublishedQuestionAppeal(ctx, tenant, appeals[index], actor, appeal.ResolveQuestionAppealInput{NewReleaseID: draft.ID, ExpectedRevision: 1})
		if err != nil || resolved.Status != appeal.QuestionAppealResolved {
			t.Fatalf("parallel appeal %d cannot resolve: %+v %v", index, resolved, err)
		}
	}
	current, err := service.CurrentPublished(ctx, tenant, exam)
	if err != nil || current.Release.SupersedesReleaseID != drafts[0].ID {
		t.Fatalf("current lineage: %+v %v", current.Release, err)
	}
	for _, value := range current.Questions {
		expected := 9.0
		if value.SubmissionID == submissions[1] {
			expected = 7
		}
		if value.Score != expected {
			t.Fatalf("earlier correction lost: %+v", value)
		}
	}
	rebuilt, err := service.Create(ctx, tenant, exam, actor, scorerelease.CreateInput{Reason: "old working facts", IdempotencyKey: "continuity-rebuild"})
	if err != nil || rebuilt.SourceReleaseID != current.Release.ID {
		t.Fatalf("ordinary rebuild did not inherit published correction: %+v %v", rebuilt, err)
	}
	if result, err := service.Publish(ctx, tenant, ordinary.ID, actor); err != nil || result.SourceReleaseID != current.Release.ID {
		t.Fatalf("ordinary old draft cannot recover: %+v %v", result, err)
	}
	// 不修改工作表追平更正，验证实际普通发布路径能够继承正式版本。
	if _, err := service.Publish(ctx, tenant, rebuilt.ID, actor); err != nil {
		t.Fatal(err)
	}
	matching, err := service.Create(ctx, tenant, exam, actor, scorerelease.CreateInput{Reason: "working facts match corrections", IdempotencyKey: "continuity-matching"})
	if err != nil {
		t.Fatalf("matching ordinary draft: %v", err)
	}
	if _, err := service.Publish(ctx, tenant, matching.ID, actor); err != nil {
		t.Fatalf("matching ordinary publish: %v", err)
	}
	next, err := service.Create(ctx, tenant, exam, actor, scorerelease.CreateInput{Reason: "stale facts after matching ordinary publish", IdempotencyKey: "continuity-after-matching"})
	if err != nil || next.SourceReleaseID != matching.ID {
		t.Fatalf("ordinary source change erased inheritance: %+v %v", next, err)
	}
	if _, err := service.Publish(ctx, tenant, next.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(ctx, tenant, ordinary.ID, actor); err != nil {
		t.Fatalf("published retry must keep its original facts: %v", err)
	}
	rollback, err := service.CreateRollback(ctx, tenant, exam, actor, scorerelease.RollbackInput{SourceReleaseID: base.ID, Reason: "explicit rollback", IdempotencyKey: "continuity-explicit-rollback"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(ctx, tenant, rollback.ID, actor); err != nil {
		t.Fatal(err)
	}
	afterRollback, err := service.Create(ctx, tenant, exam, actor, scorerelease.CreateInput{Reason: "facts after explicit rollback", IdempotencyKey: "continuity-after-rollback"})
	if err != nil {
		t.Fatalf("explicit rollback retained discarded correction branch: %v", err)
	}
	if _, err := service.Publish(ctx, tenant, afterRollback.ID, actor); err != nil {
		t.Fatal(err)
	}
	var pendingAppeals, pendingRegrades, outdatedDrafts int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM question_appeal WHERE tenant_id=$1 AND exam_id=$2 AND status NOT IN ('resolved','rejected')`, tenant, exam).Scan(&pendingAppeals); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM regrade_job WHERE tenant_id=$1 AND exam_id=$2 AND status NOT IN ('cancelled','ready_for_release')`, tenant, exam).Scan(&pendingRegrades); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM score_release WHERE tenant_id=$1 AND exam_id=$2 AND status='draft'`, tenant, exam).Scan(&outdatedDrafts); err != nil {
		t.Fatal(err)
	}
	if pendingAppeals != 0 || pendingRegrades != 0 || outdatedDrafts != 0 {
		t.Fatalf("parallel flow left archive blockers: appeals=%d regrades=%d drafts=%d", pendingAppeals, pendingRegrades, outdatedDrafts)
	}
}
