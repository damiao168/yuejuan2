package score

import (
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/appeal"
)

func TestPostgresHistoricalPublicationRemainsReadableUntilFormalVersion(t *testing.T) {
	db := confirmationTestDB(t)
	f := newAutomaticReadFixture(t, db)
	ctx := t.Context()
	scores, _, _ := newCompatibilityPostgresStore(db)
	for index, value := range []int{4, 3, 1} {
		f.submitSingleReview(t, index, value, "submitted")
	}
	if _, err := scores.FinalizeExam(ctx, f.tenant, f.exam, f.actor); err != nil {
		t.Fatal(err)
	}
	if _, err := scores.ConfirmGrades(ctx, f.tenant, f.exam, f.actor, ConfirmInput{Reason: "historical confirmation"}); err != nil {
		t.Fatal(err)
	}
	// 直接构造升级前已经公开的行，测试不再执行旧发布实现。
	f.exec(t, `UPDATE submission_grade SET status='published',locked=true,published_by=$3,published_at=now() WHERE tenant_id=$1 AND exam_id=$2`, f.tenant, f.exam, f.actor)
	f.exec(t, `UPDATE final_grade SET status='locked',locked=true WHERE tenant_id=$1 AND exam_id=$2`, f.tenant, f.exam)
	legacy, err := scores.GetStudentGrade(ctx, f.tenant, f.student, f.exam)
	if err != nil || legacy.TotalScore != 8 || len(legacy.Items) != 3 {
		t.Fatalf("upgraded server could not read an already published grade: %+v, %v", legacy, err)
	}
	appeals := appeal.NewPostgresStore(db)
	legacyInput := appeal.CreateAppealInput{ExamID: f.exam, StudentID: f.student, TargetType: "question", FinalGradeID: legacy.Items[0].ID, Reason: "checking historical score"}
	request, err := appeals.CreateAppeal(ctx, f.tenant, f.actor, legacyInput)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟已有正式指针、但当前快照缺少该生的情况。即使旧表仍有公开行，也不能用它补回缺失版本。
	f.exec(t, `WITH published AS (
  INSERT INTO score_release (tenant_id,exam_id,version,source,reason,status,idempotency_key,created_by,published_by,published_at)
  VALUES ($1,$2,1,'migration','fixture for historical visibility','published','historical-visibility-fixture',$3,$3,now()) RETURNING id
) INSERT INTO score_release_current(tenant_id,exam_id,release_id) SELECT $1,$2,id FROM published`, f.tenant, f.exam, f.actor)
	if got, err := scores.GetStudentGrade(ctx, f.tenant, f.student, f.exam); !errors.Is(err, ErrNotFound) {
		t.Fatalf("current release fell back to old student scores: %+v, %v", got, err)
	}
	if _, err := appeals.CreateAppeal(ctx, f.tenant, f.actor, legacyInput); !errors.Is(err, appeal.ErrVersionedAppealRequired) {
		t.Fatalf("old appeal accepted an exam with a current release: %v", err)
	}
	if _, _, err := appeals.ReviewAppeal(ctx, f.tenant, request.ID, f.actor, appeal.ReviewAppealInput{
		Status: "score_adjusted", Reason: "change the old score", FinalGradeID: legacy.Items[0].ID,
		AdjustedScore: new(5.0), ExpectedRevision: request.Revision,
	}); !errors.Is(err, appeal.ErrVersionedAppealRequired) {
		t.Fatalf("old appeal attempted to adjust working scores after migration: %v", err)
	}
	var total float64
	if err := db.QueryRowContext(ctx, `SELECT total_score::float8 FROM submission_grade WHERE tenant_id=$1 AND exam_id=$2 AND student_id=$3`, f.tenant, f.exam, f.student).Scan(&total); err != nil || total != 8 {
		t.Fatalf("legacy adjustment changed the historical total: %v, %v", total, err)
	}
}
