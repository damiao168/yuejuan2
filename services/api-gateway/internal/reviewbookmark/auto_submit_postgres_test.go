package reviewbookmark

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"github.com/google/uuid"
)

type autoSubmitPausingPostgresSource struct {
	*PostgresSourceProvider
	locked chan struct{}
	resume chan struct{}
}

func (p *autoSubmitPausingPostgresSource) ResolveTx(ctx context.Context, tx *sql.Tx, user auth.User, task string, lock bool) (Source, error) {
	source, err := p.PostgresSourceProvider.ResolveTx(ctx, tx, user, task, lock)
	if err == nil && lock {
		close(p.locked)
		<-p.resume
	}
	return source, err
}

// 用真实任务行锁保护首次标记尚无记录的窗口，并核对完整迁移、RLS、正式写分和回执重放。
func TestPostgresAutomaticSubmitAndFirstBookmarkAreAtomic(t *testing.T) {
	admin, runtimeDB, user, task := bookmarkIntegrationDB(t)
	// 夹具没有考试年级，不走依赖学段的默认匹配；显式固定物理计算题配置，让本测试只验证标记与提交竞争。
	bookmarkEvidenceExec(t, admin, `UPDATE question SET question_type='calculation' WHERE id=(SELECT question_id FROM review_task WHERE id=$1::uuid)`, task)
	bookmarkEvidenceExec(t, admin, `INSERT INTO question_assessment_config(tenant_id,exam_id,question_id,subject_profile_id,archetype_code,allowed_evidence_types,risk_tier,scoring_policy_json)
SELECT t.tenant_id,t.exam_id,t.question_id,p.id,'structured_steps','["text_span","math_step"]','R2','{"mode":"AI_ASSIST","require_evidence":true}'
FROM review_task t JOIN subject_profile p ON p.tenant_id=t.tenant_id AND p.subject_code='physics' AND p.education_stage='senior' AND p.status='active'
WHERE t.id=$1::uuid ORDER BY p.version DESC,p.id LIMIT 1`, task)
	f := bookmarkSeedEvidenceFixture(t, admin, user, task)
	runID := uuid.NewString()
	bookmarkEvidenceExec(t, admin, `INSERT INTO scoring_run(id,tenant_id,exam_id,idempotency_key,status,total_count,review_count,started_by) VALUES($1::uuid,$2::uuid,$3::uuid,$1::text,'needs_review',1,1,$4::uuid)`, runID, user.TenantID, f.exam, user.ID)
	bookmarkEvidenceExec(t, admin, `UPDATE review_task SET scoring_run_id=$2::uuid WHERE id=$1::uuid`, task, runID)
	ctx := auth.WithUser(context.Background(), user)
	provider := &autoSubmitPausingPostgresSource{PostgresSourceProvider: NewPostgresSourceProvider(runtimeDB), locked: make(chan struct{}), resume: make(chan struct{})}
	bookmarks := NewPostgresStore(runtimeDB, provider)
	reviews := review.NewPostgresStore(runtimeDB)
	type markedResult struct {
		ack Acknowledgement
		err error
	}
	marked := make(chan markedResult, 1)
	go func() {
		ack, err := bookmarks.Put(commandreceipt.WithID(ctx, "mark-before-automatic"), user, task, PutInput{})
		marked <- markedResult{ack, err}
	}()
	select {
	case <-provider.locked:
	case <-time.After(10 * time.Second):
		t.Fatal("bookmark did not acquire parent and task locks")
	}
	submitted := make(chan error, 1)
	input := review.SubmitGradeInput{AutoSubmit: true, Score: 0, ExpectedRevision: 1}
	submitCtx := commandreceipt.WithID(ctx, "automatic-after-mark")
	go func() {
		_, err := reviews.SubmitGrade(submitCtx, user.TenantID, task, user.ID, input)
		submitted <- err
	}()
	close(provider.resume)
	mark := <-marked
	if mark.err != nil {
		t.Fatal(mark.err)
	}
	select {
	case err := <-submitted:
		if !errors.Is(err, review.ErrTaskMarked) {
			t.Fatalf("first bookmark did not atomically block automatic score: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("parent/task/bookmark lock order deadlocked")
	}
	var humanCount, questionCount int
	if err := admin.QueryRow(`SELECT (SELECT count(*) FROM human_grade WHERE review_task_id=$1::uuid),(SELECT count(*) FROM question_grade WHERE review_task_id=$1::uuid)`, task).Scan(&humanCount, &questionCount); err != nil || humanCount != 0 || questionCount != 0 {
		t.Fatalf("blocked auto submit wrote grades: human=%d question=%d err=%v", humanCount, questionCount, err)
	}
	bookmarks.WithProvider(NewPostgresSourceProvider(runtimeDB))
	if _, err := bookmarks.Delete(ctx, user, mark.ack.BookmarkID, DeleteInput{ExpectedRevision: mark.ack.Revision}); err != nil {
		t.Fatal(err)
	}
	first, err := reviews.SubmitGrade(submitCtx, user.TenantID, task, user.ID, input)
	if err != nil || first.Grade.Score != 0 || first.Task.Status != "submitted" {
		t.Fatalf("direct final zero score after unmark: %#v %v", first, err)
	}
	bookmarkEvidenceExec(t, admin, `UPDATE scoring_run SET status='completed' WHERE id=$1::uuid`, runID)
	replay, err := reviews.SubmitGrade(submitCtx, user.TenantID, task, user.ID, input)
	if err != nil || replay.Grade.ID != first.Grade.ID {
		t.Fatalf("successful auto command did not replay: %#v %v", replay, err)
	}
	readback, err := reviews.SubmitGrade(commandreceipt.WithID(ctx, "automatic-readback"), user.TenantID, task, user.ID, review.SubmitGradeInput{AutoSubmit: true, Score: 5, ExpectedRevision: 1})
	if err != nil || readback.Grade.ID != first.Grade.ID || readback.Grade.Score != 0 {
		t.Fatalf("terminal auto call accepted score edit: %#v %v", readback, err)
	}
	if err := admin.QueryRow(`SELECT count(*) FROM human_grade WHERE review_task_id=$1::uuid`, task).Scan(&humanCount); err != nil || humanCount != 1 {
		t.Fatalf("auto replay/readback wrote a second grade: count=%d err=%v", humanCount, err)
	}
}
