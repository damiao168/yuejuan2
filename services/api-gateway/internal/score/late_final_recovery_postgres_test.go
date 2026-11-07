package score

import (
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"github.com/google/uuid"
)

func TestPostgresLateDoubleMarkRecoversConfirmedTotalsAndPublishesLatestOnly(t *testing.T) {
	db := confirmationTestDB(t)
	f := newAutomaticReadFixture(t, db)
	ctx := t.Context()
	for index, score := range []int{4, 3, 2} {
		f.submitSingleReview(t, index, score, "submitted")
	}
	scores := NewPostgresStore(db)
	if _, err := scores.FinalizeExam(ctx, f.tenant, f.exam, f.actor); err != nil {
		t.Fatal(err)
	}
	if _, err := scores.ConfirmGrades(ctx, f.tenant, f.exam, f.actor, ConfirmInput{Reason: "initial totals"}); err != nil {
		t.Fatal(err)
	}
	firstReviewer, secondReviewer := addIndependentReviewTeacher(t, f), addIndependentReviewTeacher(t, f)
	reviews := review.NewPostgresStore(db)
	if _, err := reviews.SetExamDoubleMarkPolicy(ctx, f.tenant, f.exam, f.actor, review.SetDoubleMarkPolicyInput{Enabled: true, Threshold: 2, ResolutionStrategy: "average"}); err != nil {
		t.Fatal(err)
	}
	session, err := reviews.CreateDoubleMarkSession(ctx, f.tenant, f.actor, review.CreateDoubleMarkSessionInput{AnswerSegmentID: f.segmentIDs[0], FirstReviewerID: firstReviewer, SecondReviewerID: secondReviewer})
	if err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{session.FirstReviewTaskID, session.SecondReviewTaskID} {
		task, err := reviews.GetTask(ctx, f.tenant, id)
		if err != nil {
			t.Fatal(err)
		}
		reviewer := []string{firstReviewer, secondReviewer}[index]
		if _, err := reviews.SubmitGrade(ctx, f.tenant, id, reviewer, review.SubmitGradeInput{Score: 3, ScoreOnly: true, Reason: "late double marking", ExpectedRevision: task.Revision}); err != nil {
			t.Fatal(err)
		}
	}
	releases := scorerelease.NewService(scorerelease.NewPostgresStore(db, nil).WithGradePreparer(scores))
	draft, err := releases.Create(ctx, f.tenant, f.exam, f.actor, scorerelease.CreateInput{Reason: "publish latest double mark", IdempotencyKey: "late-double-mark-recovery"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := releases.Publish(ctx, f.tenant, draft.ID, f.actor); err != nil {
		t.Fatal(err)
	}
	detail, err := releases.CurrentPublished(ctx, f.tenant, f.exam)
	if err != nil || len(detail.Questions) != 3 || len(detail.Items) != 1 || detail.Items[0].TotalScore != 8 {
		t.Fatalf("publication did not use latest final exactly once: %+v %v", detail, err)
	}
}

func addIndependentReviewTeacher(t *testing.T, f *automaticReadFixture) string {
	t.Helper()
	id := uuid.NewString()
	f.exec(t, `INSERT INTO app_user(id,tenant_id,username,display_name,password_hash,status,school_id,teaching_subjects)
SELECT $1::uuid,$2::uuid,$1::uuid::text,'Independent test reviewer',password_hash,'active',$3::uuid,ARRAY['physics'] FROM app_user WHERE id=$4::uuid`, id, f.tenant, f.school, f.actor)
	f.exec(t, `INSERT INTO user_role(tenant_id,user_id,role_id,data_scope) SELECT $1::uuid,$2::uuid,id,'{"scope":"exam_task"}' FROM role WHERE tenant_id=$1::uuid AND code IN ('teacher','grader')`, f.tenant, id)
	return id
}

func TestPostgresLateFinalRecoveryUsesPendingLatestFactNotTransactionTimestamp(t *testing.T) {
	db := confirmationTestDB(t)
	f := newAutomaticReadFixture(t, db)
	ctx := t.Context()
	for index, score := range []int{4, 3, 2} {
		f.submitSingleReview(t, index, score, "submitted")
	}
	scores := NewPostgresStore(db)
	if _, err := scores.FinalizeExam(ctx, f.tenant, f.exam, f.actor); err != nil {
		t.Fatal(err)
	}
	// 终评事务先开始，确认事务后提交；now()来自事务开始，不能代表新终评的提交先后。
	late, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer late.Rollback()
	if _, err := late.ExecContext(ctx, `SELECT now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := scores.ConfirmGrades(ctx, f.tenant, f.exam, f.actor, ConfirmInput{Reason: "confirmation overlaps a pending final transaction"}); err != nil {
		t.Fatal(err)
	}
	_, err = late.ExecContext(ctx, `INSERT INTO final_grade(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,score,max_score,source,status,created_by)
SELECT tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,3,max_score,'double_mark_auto','pending_confirmation',created_by
FROM final_grade WHERE tenant_id=$1 AND answer_segment_id=$2::uuid ORDER BY created_at DESC,id DESC LIMIT 1`, f.tenant, f.segmentIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := late.Commit(); err != nil {
		t.Fatal(err)
	}
	releases := scorerelease.NewService(scorerelease.NewPostgresStore(db, nil).WithGradePreparer(scores))
	draft, err := releases.Create(ctx, f.tenant, f.exam, f.actor, scorerelease.CreateInput{Reason: "recover committed late final", IdempotencyKey: "late-final-transaction-recovery"})
	if err != nil {
		t.Fatal(err)
	}
	if detail, err := releases.Get(ctx, f.tenant, draft.ID); err != nil || detail.Items[0].TotalScore != 8 {
		t.Fatalf("transaction timestamp blocked valid recovery: %+v %v", detail, err)
	}
	// 历史 pending 记录如果已被更晚的 confirmed 终评覆盖，不能成为修复无来源总分改动的借口。
	f.exec(t, `INSERT INTO final_grade(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,score,max_score,source,status,created_by,created_at)
SELECT tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,1,max_score,source,'pending_confirmation',created_by,created_at-interval '1 day'
FROM final_grade WHERE tenant_id=$1 AND answer_segment_id=$2::uuid ORDER BY created_at DESC,id DESC LIMIT 1`, f.tenant, f.segmentIDs[0])
	f.exec(t, `UPDATE submission_grade SET total_score=7 WHERE tenant_id=$1 AND exam_id=$2::uuid`, f.tenant, f.exam)
	if _, err := releases.Create(ctx, f.tenant, f.exam, f.actor, scorerelease.CreateInput{Reason: "reject unexplained modification", IdempotencyKey: "old-pending-not-a-recovery-source"}); !errors.Is(err, scorerelease.ErrGateBlocked) {
		t.Fatalf("historical pending final allowed unexplained total repair: %v", err)
	}
}
