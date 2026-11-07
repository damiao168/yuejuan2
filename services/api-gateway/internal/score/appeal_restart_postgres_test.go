package score

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/opiniondraft"
	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"edugrade-enterprise/services/api-gateway/internal/regraderelease"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"github.com/google/uuid"
)

// 只把旧版已复核任务作为起点夹具；恢复、重新复核、发布与结案都调用生产命令，不修改工作表追平分数。
func TestPostgresAppealRestartKeepsOriginalEvidenceAndCompletesNewReview(t *testing.T) {
	db := confirmationTestDB(t)
	f := newAutomaticReadFixture(t, db)
	ctx := t.Context()
	for index, score := range []int{4, 3, 2} {
		f.submitSingleReview(t, index, score, "submitted")
	}
	scores := NewPostgresStore(db)
	releases := scorerelease.NewService(scorerelease.NewPostgresStore(db, nil).WithGradePreparer(scores))
	base, err := releases.Create(ctx, f.tenant, f.exam, f.actor, scorerelease.CreateInput{Reason: "initial", IdempotencyKey: "restart-original-release", VisibilityPolicy: scorerelease.VisibilityPolicy{ShowQuestionScores: true}, AppealWindow: scorerelease.AppealWindow{Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := releases.Publish(ctx, f.tenant, base.ID, f.actor); err != nil {
		t.Fatal(err)
	}
	reviewer := addIndependentReviewTeacher(t, f)
	appeals := appeal.NewPublishedQuestionAppealService(appeal.NewPublishedQuestionAppealPostgresStore(db))
	item, err := appeals.Create(ctx, f.tenant, f.student, f.actor, appeal.CreatePublishedQuestionAppealInput{ExamID: f.exam, SourceReleaseID: base.ID, QuestionID: f.questionIDs[0], ReasonCode: "other", Reason: "verify original four-point result"})
	if err != nil {
		t.Fatal(err)
	}
	item, err = appeals.StartReview(ctx, f.tenant, item.ID, f.actor, appeal.StartQuestionAppealReviewInput{AssignedTo: reviewer, ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	private := opiniondraft.NewPostgresStore(db)
	if _, err := private.Put(ctx, f.tenant, reviewer, "question-appeal", item.ID, 0, &opiniondraft.Draft{Source: base.ID, SourceRevision: item.Revision, Values: map[string]any{"reviewExplanation": "old round draft"}, UpdatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	item, err = appeals.SubmitReviewOpinion(ctx, f.tenant, item.ID, reviewer, appeal.SubmitQuestionAppealReviewOpinionInput{Opinion: appeal.QuestionAppealOpinionSupport, Explanation: "old round opinion", ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	oldJob, oldReviewed := seedRestartReadyJob(t, f, base.ID, 3)
	item, err = appeals.Decide(ctx, f.tenant, item.ID, f.actor, appeal.DecideQuestionAppealInput{Decision: appeal.QuestionAppealDecisionReferRegrade, RegradeJobID: oldJob, PublicResponse: "old round public response", ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := releases.CreateFromRegrade(ctx, f.tenant, f.exam, f.actor, scorerelease.CreateRegradeInput{SourceReleaseID: base.ID, QuestionID: f.questionIDs[0], Reason: "old pending correction", IdempotencyKey: "regrade-job:" + oldJob, Changes: []scorerelease.RegradeChange{{SubmissionID: f.submission, QuestionID: f.questionIDs[0], Score: 3, MaxScore: 5, ReviewedGradeID: oldReviewed}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appeals.RestartReview(ctx, f.tenant, item.ID, f.actor, appeal.RestartQuestionAppealReviewInput{Reason: "not outdated", ExpectedRevision: item.Revision}); !errors.Is(err, appeal.ErrRestartReviewUnavailable) {
		t.Fatalf("fresh source was cancelled: %v", err)
	}
	_, otherReviewed := seedRestartReadyJob(t, f, base.ID, 2)
	other, err := releases.CreateFromRegrade(ctx, f.tenant, f.exam, f.actor, scorerelease.CreateRegradeInput{SourceReleaseID: base.ID, QuestionID: f.questionIDs[0], Reason: "another approved correction", IdempotencyKey: "restart-competing-correction", Changes: []scorerelease.RegradeChange{{SubmissionID: f.submission, QuestionID: f.questionIDs[0], Score: 2, MaxScore: 5, ReviewedGradeID: otherReviewed}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := releases.Publish(ctx, f.tenant, other.ID, f.actor); err != nil {
		t.Fatal(err)
	}
	if _, err := releases.Publish(ctx, f.tenant, stale.ID, f.actor); !errors.Is(err, scorerelease.ErrStaleSource) {
		t.Fatalf("conflicting old score overwrote current correction: %v", err)
	}
	item, err = appeals.RestartReview(ctx, f.tenant, item.ID, f.actor, appeal.RestartQuestionAppealReviewInput{Reason: "recheck against the current official correction", ExpectedRevision: item.Revision})
	if err != nil || item.Status != appeal.QuestionAppealUnderReview || item.SourceReleaseID != base.ID || item.SourceScore != 4 || item.ReviewSourceReleaseID != other.ID || item.ReviewSourceScore == nil || *item.ReviewSourceScore != 2 || item.ReviewOpinion != nil || item.PublicResponse != "" {
		t.Fatalf("restart lost original evidence or retained old round response: %+v %v", item, err)
	}
	var jobStatus, draftStatus string
	if err := db.QueryRowContext(ctx, `SELECT status FROM regrade_job WHERE id=$1`, oldJob).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT status FROM score_release WHERE id=$1`, stale.ID).Scan(&draftStatus); err != nil || jobStatus != "cancelled" || draftStatus != "discarded" {
		t.Fatalf("stale work not retired: %s %s %v", jobStatus, draftStatus, err)
	}
	if draft, err := private.Get(ctx, f.tenant, reviewer, "question-appeal", item.ID); err != nil || draft.Draft != nil {
		t.Fatalf("old private draft resurrected: %+v %v", draft, err)
	}
	item, err = appeals.SubmitReviewOpinion(ctx, f.tenant, item.ID, reviewer, appeal.SubmitQuestionAppealReviewOpinionInput{Opinion: appeal.QuestionAppealOpinionSupport, Explanation: "new round considers the corrected two-point source", ExpectedRevision: item.Revision})
	if err != nil || item.ReviewOpinion == nil || item.ReviewOpinion.Explanation == "old round opinion" {
		t.Fatalf("new source opinion is unavailable: %+v %v", item, err)
	}
	jobs := regrade.NewService(regrade.NewPostgresStore(db))
	created, err := jobs.Create(ctx, f.tenant, f.exam, f.questionIDs[0], f.actor, regrade.CreateInput{SourceReleaseID: other.ID, ReasonCode: "other", ReasonText: "new review round", Strategy: "human_recheck", Selector: regrade.Selector{SubmissionIDs: []string{f.submission}}, AssigneeID: reviewer, IdempotencyKey: "restart-new-round-job"})
	if err != nil || len(created.Items) != 1 {
		t.Fatalf("new governed regrade: %+v %v", created, err)
	}
	item, err = appeals.Decide(ctx, f.tenant, item.ID, f.actor, appeal.DecideQuestionAppealInput{Decision: appeal.QuestionAppealDecisionReferRegrade, RegradeJobID: created.Job.ID, PublicResponse: "new round reply", ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Approve(ctx, f.tenant, created.Job.ID, f.actor); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Start(ctx, f.tenant, created.Job.ID, f.actor); err != nil {
		t.Fatal(err)
	}
	claimed, err := jobs.Claim(ctx, f.tenant, created.Items[0].ID, reviewer)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := jobs.RecordCandidate(ctx, f.tenant, claimed.ID, reviewer, regrade.CandidateInput{Score: 3, Comment: "independent re-evaluation", RequireManualReview: true, ExpectedRevision: claimed.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Review(ctx, f.tenant, candidate.ID, f.actor, regrade.ReviewInput{Decision: regrade.ReviewAccept, ReviewedGradeID: uuid.NewString(), Note: "approved independently", ExpectedRevision: candidate.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Finalize(ctx, f.tenant, created.Job.ID, f.actor); err != nil {
		t.Fatal(err)
	}
	newDraft, err := regraderelease.NewService(jobs, releases).Create(ctx, f.tenant, created.Job.ID, f.actor, regraderelease.Input{Reason: "new round official correction", IdempotencyKey: "restart-new-round-release"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := releases.Publish(ctx, f.tenant, newDraft.ID, f.actor); err != nil {
		t.Fatal(err)
	}
	resolved, err := appeals.Resolve(ctx, f.tenant, item.ID, f.actor, appeal.ResolveQuestionAppealInput{NewReleaseID: newDraft.ID, ExpectedRevision: item.Revision})
	if err != nil || resolved.Status != appeal.QuestionAppealResolved || resolved.SourceReleaseID != base.ID {
		t.Fatalf("new round cannot resolve original appeal: %+v %v", resolved, err)
	}
	assertRestartRejectsAResultPublishedWhileWaiting(t, f, releases, appeals, newDraft, reviewer)
}

func seedRestartReadyJob(t *testing.T, f *automaticReadFixture, source string, score float64) (string, string) {
	t.Helper()
	job, reviewed := uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO regrade_job(id,tenant_id,exam_id,question_id,source_release_id,reason_code,reason_text,strategy,idempotency_key,status,affected_count,created_by,finalized_by,finalized_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'other','synthetic ready job','human_recheck',$1::uuid::text,'ready_for_release',1,$6::uuid,$6::uuid,now())`, job, f.tenant, f.exam, f.questionIDs[0], source, f.actor)
	f.exec(t, `INSERT INTO regrade_item(tenant_id,job_id,submission_id,old_final_grade_id,old_score,max_score,reviewed_grade_id,reviewed_score,status) SELECT $1,$2,submission_id,final_grade_id,score,max_score,$3,$4,'resolved' FROM score_release_question WHERE tenant_id=$1 AND release_id=$5 AND submission_id=$6 AND question_id=$7`, f.tenant, job, reviewed, score, source, f.submission, f.questionIDs[0])
	return job, reviewed
}

// 不加产品测试钩子：由事务先持考试锁、在重开等待期间真实提交一个新发布，重开醒来必须重新读取并拒绝取消。
func assertRestartRejectsAResultPublishedWhileWaiting(t *testing.T, f *automaticReadFixture, releases *scorerelease.Service, appeals *appeal.PublishedQuestionAppealService, base scorerelease.Release, reviewer string) {
	t.Helper()
	ctx, stop := context.WithTimeout(t.Context(), 20*time.Second)
	defer stop()
	job, reviewed := seedRestartReadyJob(t, f, base.ID, 4)
	var appealID string
	if err := f.db.QueryRowContext(ctx, `INSERT INTO question_appeal(tenant_id,exam_id,student_id,submission_id,source_release_id,source_release_version,question_id,question_no,source_final_grade_id,source_score,source_max_score,reason_code,reason,status,assigned_to,decision,regrade_job_id,created_by)
SELECT q.tenant_id,$1,i.student_id,q.submission_id,q.release_id,$2,q.question_id,q.question_no,q.final_grade_id,q.score,q.max_score,'other','concurrent publication probe','upheld_pending_regrade',$3,'refer_regrade',$4,$5
FROM score_release_question q JOIN score_release_item i ON i.tenant_id=q.tenant_id AND i.release_id=q.release_id AND i.submission_id=q.submission_id WHERE q.tenant_id=$6 AND q.release_id=$7 AND q.submission_id=$8 AND q.question_id=$9 RETURNING id::text`, f.exam, base.Version, reviewer, job, f.actor, f.tenant, base.ID, f.submission, f.questionIDs[0]).Scan(&appealID); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var holder int
	if err := blocker.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM (SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))) held`, f.tenant, f.exam).Scan(&holder); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := appeals.RestartReview(ctx, f.tenant, appealID, f.actor, appeal.RestartQuestionAppealReviewInput{ExpectedRevision: 1, Reason: "must not cancel an already published result"})
		result <- err
	}()
	waiting := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if err := f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, holder).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("restart never waited for the held exam lock")
	}
	id := uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := blocker.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO score_release(id,tenant_id,exam_id,version,source,reason,status,idempotency_key,source_release_id,created_by) VALUES($1,$2,$3,$4,'regrade','controlled concurrent publish','draft',$5,$6,$7)`, id, f.tenant, f.exam, base.Version+1, "regrade-job:"+job, base.ID, f.actor)
	exec(`INSERT INTO score_release_item(tenant_id,release_id,student_id,submission_id,total_score,max_score,status,snapshot_hash) SELECT tenant_id,$2,student_id,submission_id,total_score+1,max_score,status,repeat('c',64) FROM score_release_item WHERE tenant_id=$1 AND release_id=$3`, f.tenant, id, base.ID)
	exec(`INSERT INTO score_release_question(tenant_id,release_id,submission_id,question_id,question_no,final_grade_id,score,max_score,source_type,source_id,student_explanation) SELECT tenant_id,$2,submission_id,question_id,question_no,final_grade_id,CASE WHEN question_id=$4::uuid THEN 4 ELSE score END,max_score,CASE WHEN question_id=$4::uuid THEN 'single_review' ELSE source_type END,CASE WHEN question_id=$4::uuid THEN $5::uuid ELSE source_id END,student_explanation FROM score_release_question WHERE tenant_id=$1 AND release_id=$3`, f.tenant, id, base.ID, f.questionIDs[0], reviewed)
	exec(`UPDATE score_release SET status='published',published_by=$2,published_at=now(),supersedes_release_id=$3 WHERE id=$1`, id, f.actor, base.ID)
	exec(`UPDATE score_release_current SET release_id=$3,updated_at=now() WHERE tenant_id=$1 AND exam_id=$2`, f.tenant, f.exam, id)
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, appeal.ErrRestartReviewUnavailable) {
			t.Fatalf("published-while-waiting job was cancelled: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("restart did not finish after publication committed")
	}
	var status string
	if err := f.db.QueryRowContext(ctx, `SELECT status FROM regrade_job WHERE id=$1`, job).Scan(&status); err != nil || status != "ready_for_release" {
		t.Fatalf("already published work was changed: %s %v", status, err)
	}
	if _, err := appeals.Resolve(ctx, f.tenant, appealID, f.actor, appeal.ResolveQuestionAppealInput{ExpectedRevision: 1, NewReleaseID: id}); err != nil {
		t.Fatal(fmt.Errorf("concurrent published result must remain resolvable: %w", err))
	}
}
