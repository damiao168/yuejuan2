package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/report"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"testing"
)

func TestReleaseFeedbackUsesFinalArbitrationAndFreezesIt(t *testing.T) {
	db, _, _, f, answers, _ := repairFixture(t)
	ctx := context.Background()
	questionID := f.QuestionIDs["single_choice"]
	var segmentID, questionNo, graderID string
	if err := db.QueryRowContext(ctx, `SELECT fg.answer_segment_id::text,fg.question_no FROM final_grade fg WHERE fg.tenant_id=$1 AND fg.submission_id=$2::uuid AND fg.question_id=$3::uuid AND fg.deleted_at IS NULL`, f.TenantID, answers[0].submission, questionID).Scan(&segmentID, &questionNo); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM app_user WHERE tenant_id=$1 AND username='grader'`, f.TenantID).Scan(&graderID); err != nil {
		t.Fatal(err)
	}
	insertTask := func(reviewerID, round string) string {
		t.Helper()
		var id string
		err := db.QueryRowContext(ctx, `INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,assigned_to,created_by,grade_round)
VALUES($1,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,'anonymous','manual_sample','completed',$7::uuid,$8::uuid,$9) RETURNING id::text`, f.TenantID, f.ExamID, questionID, questionNo, segmentID, answers[0].submission, reviewerID, f.AdminID, round).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	firstTask := insertTask(f.AdminID, "first_mark")
	secondTask := insertTask(graderID, "second_mark")
	if _, err := db.ExecContext(ctx, `INSERT INTO human_grade(tenant_id,review_task_id,answer_segment_id,reviewer_id,score,max_score,grade_round,student_feedback)
VALUES($1,$2::uuid,$3::uuid,$4::uuid,0,1,'first_mark','obsolete first marker feedback')`, f.TenantID, firstTask, segmentID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	var sessionID, arbitrationID string
	if err := db.QueryRowContext(ctx, `INSERT INTO double_mark_session(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,first_review_task_id,second_review_task_id,first_reviewer_id,second_reviewer_id,threshold,resolution_strategy,status,created_by)
VALUES($1,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,'anonymous',$7::uuid,$8::uuid,$9::uuid,$10::uuid,0,'average','arbitrated',$9::uuid) RETURNING id::text`, f.TenantID, f.ExamID, questionID, questionNo, segmentID, answers[0].submission, firstTask, secondTask, f.AdminID, graderID).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO arbitration_task(tenant_id,double_mark_session_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,first_reviewer_id,second_reviewer_id,first_score,second_score,score_difference,status,final_score,student_feedback,created_by)
VALUES($1,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7::uuid,'anonymous',$8::uuid,$9::uuid,0,0,0,'submitted',0,'final arbitration feedback',$8::uuid) RETURNING id::text`, f.TenantID, sessionID, f.ExamID, questionID, questionNo, segmentID, answers[0].submission, f.AdminID, graderID).Scan(&arbitrationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE final_grade SET source='arbitration',double_mark_session_id=$4::uuid,arbitration_task_id=$5::uuid WHERE tenant_id=$1 AND submission_id=$2::uuid AND question_id=$3::uuid AND deleted_at IS NULL`, f.TenantID, answers[0].submission, questionID, sessionID, arbitrationID); err != nil {
		t.Fatal(err)
	}
	svc := scorerelease.NewService(scorerelease.NewPostgresStore(db, nil))
	release, err := svc.Create(ctx, f.TenantID, f.ExamID, f.AdminID, scorerelease.CreateInput{Reason: "arbitration feedback", IdempotencyKey: "repair-arbitration-feedback", VisibilityPolicy: scorerelease.VisibilityPolicy{ShowQuestionScores: true, ShowFeedback: true}})
	if err != nil {
		t.Fatal(err)
	}
	identity := releaseArbitrationIdentity{ReleaseID: release.ID, QuestionID: questionID, ArbitrationID: arbitrationID}
	e2eAssertArbitrationReleaseSnapshot(t, ctx, svc, f, answers, identity)
	if _, err := svc.Publish(ctx, f.TenantID, release.ID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE arbitration_task SET student_feedback='changed after publication' WHERE tenant_id=$1 AND id=$2::uuid`, f.TenantID, arbitrationID); err != nil {
		t.Fatal(err)
	}
	e2eAssertPublishedArbitrationStudentRead(t, ctx, db, svc, f, answers, questionID)
}

type releaseArbitrationIdentity struct {
	ReleaseID, QuestionID, ArbitrationID string
}

// 发布草稿只选最终仲裁的来源和反馈，不能取首轮已过时的批语。
func e2eAssertArbitrationReleaseSnapshot(t *testing.T, ctx context.Context, svc *scorerelease.Service, f story056AcceptanceFixture, answers []repairAnswer, identity releaseArbitrationIdentity) {
	t.Helper()
	detail, err := svc.Get(ctx, f.TenantID, identity.ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	var matched bool
	for _, question := range detail.Questions {
		if question.SubmissionID == answers[0].submission && question.QuestionID == identity.QuestionID {
			matched = true
			if question.SourceType != "arbitration" || question.SourceID != identity.ArbitrationID || question.Explanation.Feedback != "final arbitration feedback" {
				t.Fatalf("arbitration snapshot: %+v", question)
			}
		}
	}
	if !matched {
		t.Fatal("arbitrated question missing from release")
	}
}

// 发布后修改源任务批语，学生仍读取原冻结反馈；找到目标题目后才继续核对报告。
func e2eAssertPublishedArbitrationStudentRead(t *testing.T, ctx context.Context, db *sql.DB, svc *scorerelease.Service, f story056AcceptanceFixture, answers []repairAnswer, questionID string) {
	t.Helper()
	result, err := svc.StudentResult(ctx, f.TenantID, f.ExamID, answers[0].student)
	if err != nil {
		t.Fatal(err)
	}
	for _, question := range result.Questions {
		if question.QuestionID == questionID {
			if question.Feedback != "final arbitration feedback" {
				t.Fatalf("published feedback changed: %+v", question)
			}
			e2eAssertPublishedArbitrationReport(t, ctx, db, f, answers[0].student, questionID, question.Score)
			return
		}
	}
	t.Fatal("arbitrated student question missing")

}

// 报告题目必须与正式版本同分，且仅包含一条冻结仲裁反馈；没有目标题目时保留 Fatal。
func e2eAssertPublishedArbitrationReport(t *testing.T, ctx context.Context, db *sql.DB, f story056AcceptanceFixture, studentID, questionID string, expectedScore float64) {
	t.Helper()
	studentReport, reportErr := report.NewPostgresStore(db).StudentReport(ctx, f.TenantID, f.ExamID, studentID)
	if reportErr != nil {
		t.Fatal(reportErr)
	}
	var reportQuestionFound bool
	for _, reportQuestion := range studentReport.Questions {
		if reportQuestion.QuestionID != questionID {
			continue
		}
		reportQuestionFound = true
		if reportQuestion.Score != expectedScore || len(reportQuestion.TeacherFeedback) != 1 || reportQuestion.TeacherFeedback[0].Text != "final arbitration feedback" {
			t.Fatalf("report drifted from release: %+v", reportQuestion)
		}
	}
	if !reportQuestionFound {
		t.Fatal("published report question missing")
	}
}
