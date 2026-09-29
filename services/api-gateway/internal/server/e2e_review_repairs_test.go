package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/report"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type repairAnswer struct{ student, submission, page, grade string }

// Freeze the roster through the API, then seed complete downstream grading
// facts. Unlike the worker-volume fixture, every candidate answers every question.
func repairFixture(t *testing.T) (*sql.DB, http.Handler, string, story056AcceptanceFixture, []repairAnswer, *files.MemoryObjectStorage) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin", "grader"})
	objects := files.NewMemoryObjectStorage()
	router := e2ePostgresRouterWithObjects(db, objects)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	f := e2eCreateStory056MutableTemplateFixture(t, db, router, token, suffix, nil)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/students", token, story056JSON(t, map[string]any{
		"school_id": f.SchoolID, "class_id": f.ClassID, "student_no": "REPAIR-" + suffix, "name": "第二名学生",
	}), http.StatusCreated)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+f.ExamID+"/readiness/confirm", token, `{}`, http.StatusOK)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+f.ExamID+"/start-collection", token, `{}`, http.StatusOK)
	rows, err := db.Query(`SELECT student_id::text FROM exam_candidate_snapshot WHERE tenant_id=$1 AND exam_id=$2 ORDER BY student_id`, f.TenantID, f.ExamID)
	if err != nil {
		t.Fatal(err)
	}
	var answers []repairAnswer
	for rows.Next() {
		var a repairAnswer
		if err = rows.Scan(&a.student); err != nil {
			t.Fatal(err)
		}
		answers = append(answers, a)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(answers) != 2 {
		t.Fatalf("candidates=%d", len(answers))
	}
	for i := range answers {
		a := &answers[i]
		err = db.QueryRow(`INSERT INTO submission(tenant_id,exam_id,student_id,candidate_no,source_type,status,expected_page_count,actual_page_count,quality_status,quality_issues,collected_by,identity_status,identity_evidence)
VALUES($1,$2,$3::uuid,$3::text,'scanner_upload','ready_for_ocr',1,1,'passed','[]',$4,'matched','{}') RETURNING id::text`, f.TenantID, f.ExamID, a.student, f.AdminID).Scan(&a.submission)
		if err != nil {
			t.Fatal(err)
		}
		err = db.QueryRow(`INSERT INTO submission_page(tenant_id,submission_id,file_asset_id,page_no,status,quality_status) VALUES($1,$2,$3,1,'accepted','passed') RETURNING id::text`, f.TenantID, a.submission, f.PaperFileAssetID).Scan(&a.page)
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"single_choice", "true_false", "multiple_choice"} {
			q := f.QuestionIDs[kind]
			var segment, grade string
			err = db.QueryRow(`INSERT INTO answer_segment(tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status)
SELECT $1,$2,$3,id,question_no,'{"x":0,"y":0,"width":1,"height":1}','configured_answer_area','accepted' FROM question WHERE tenant_id=$1 AND id=$4 RETURNING id::text`, f.TenantID, a.submission, a.page, q).Scan(&segment)
			if err != nil {
				t.Fatal(err)
			}
			err = db.QueryRow(`INSERT INTO question_grade(tenant_id,exam_id,submission_id,question_id,answer_segment_id,source,status,score,max_score,confirmed_by,exam_question_snapshot_id)
SELECT $1,$2,$3,$4,$5,'rule_confirmed','confirmed',0,1,$6,id FROM exam_question_snapshot WHERE tenant_id=$1 AND exam_id=$2 AND question_id=$4 RETURNING id::text`, f.TenantID, f.ExamID, a.submission, q, segment, f.AdminID).Scan(&grade)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "single_choice" {
				a.grade = grade
			}
		}
	}
	store := score.NewPostgresStore(db)
	result, err := store.FinalizeExam(context.Background(), f.TenantID, f.ExamID, f.AdminID)
	if err != nil || !result.Quality.Passed {
		t.Fatalf("finalize: %+v %v", result, err)
	}
	if _, err = store.ConfirmGrades(context.Background(), f.TenantID, f.ExamID, f.AdminID, score.ConfirmInput{Reason: "verified complete fixture"}); err != nil {
		t.Fatal(err)
	}
	return db, router, token, f, answers, objects
}

type repairQueryKey struct{}
type repairQuestionTracer struct {
	mu      sync.Mutex
	queries int
	rows    int64
}

func (t *repairQuestionTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, repairQueryKey{}, d.SQL)
}
func (t *repairQuestionTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	sqlText, _ := ctx.Value(repairQueryKey{}).(string)
	if strings.Contains(sqlText, "SELECT release_id::text, submission_id::text") && strings.Contains(sqlText, "FROM score_release_question") {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.queries++
		t.rows += d.CommandTag.RowsAffected()
	}
}

func TestReviewRepairsReleaseAndStudentReadWithPostgresTestDatabase(t *testing.T) {
	db, _, _, f, answers, _ := repairFixture(t)
	ctx := context.Background()
	svc := scorerelease.NewService(scorerelease.NewPostgresStore(db, nil))
	base, err := svc.Create(ctx, f.TenantID, f.ExamID, f.AdminID, scorerelease.CreateInput{Reason: "initial", IdempotencyKey: "repair-release-base", VisibilityPolicy: scorerelease.VisibilityPolicy{ShowQuestionScores: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Publish(ctx, f.TenantID, base.ID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	makeDraft := func(q, key string) scorerelease.Release {
		r, err := svc.CreateFromRegrade(ctx, f.TenantID, f.ExamID, f.AdminID, scorerelease.CreateRegradeInput{SourceReleaseID: base.ID, QuestionID: q, Reason: "verified correction", IdempotencyKey: key, Changes: []scorerelease.RegradeChange{{SubmissionID: answers[0].submission, QuestionID: q, Score: 1, MaxScore: 1}}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a := makeDraft(f.QuestionIDs["single_choice"], "repair-regrade-a")
	b := makeDraft(f.QuestionIDs["true_false"], "repair-regrade-b")
	if _, err = svc.Publish(ctx, f.TenantID, a.ID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Publish(ctx, f.TenantID, b.ID, f.AdminID); !errors.Is(err, scorerelease.ErrStaleSource) {
		t.Fatalf("stale publish: %v", err)
	}
	current, err := svc.CurrentPublished(ctx, f.TenantID, f.ExamID)
	if err != nil || current.Release.ID != a.ID {
		t.Fatalf("current: %+v %v", current, err)
	}
	draft, err := svc.Get(ctx, f.TenantID, b.ID)
	if err != nil || draft.Release.Status != scorerelease.StatusDraft {
		t.Fatalf("draft: %+v %v", draft, err)
	}
	if _, err = svc.Publish(ctx, f.TenantID, a.ID, f.AdminID); err != nil {
		t.Fatal(err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var cfg *pgx.ConnConfig
	err = conn.Raw(func(raw any) error { cfg = raw.(*stdlib.Conn).Conn().Config().Copy(); return nil })
	conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	trace := &repairQuestionTracer{}
	cfg.Tracer = trace
	traced := stdlib.OpenDB(*cfg)
	defer traced.Close()
	reader := scorerelease.NewPostgresStore(traced, nil)
	result, err := reader.StudentResult(ctx, f.TenantID, f.ExamID, answers[0].student)
	if err != nil || len(result.Questions) != 3 || result.TotalScore != 1 {
		t.Fatalf("student: %+v %v", result, err)
	}
	if trace.queries != 1 || trace.rows != 3 {
		t.Fatalf("student loaded %d question rows in %d queries", trace.rows, trace.queries)
	}
	// An enabled draft cannot publish without verified anonymous assets.
	unsafeDraft, err := svc.Create(ctx, f.TenantID, f.ExamID, f.AdminID, scorerelease.CreateInput{Source: scorerelease.SourceMigration, Reason: "unsafe sharing", IdempotencyKey: "repair-release-unsafe", VisibilityPolicy: scorerelease.VisibilityPolicy{ShowQuestionScores: true, ShowHighScorePaper: true}})
	if err != nil {
		t.Fatalf("high-score sharing draft: %v", err)
	}
	if _, err = svc.Publish(ctx, f.TenantID, unsafeDraft.ID, f.AdminID); !errors.Is(err, scorerelease.ErrAnonymousPaperUnavailable) {
		t.Fatalf("unprepared high-score sharing publish: %v", err)
	}
	analytics, err := svc.Create(ctx, f.TenantID, f.ExamID, f.AdminID, scorerelease.CreateInput{Source: scorerelease.SourceMigration, Reason: "analytics", IdempotencyKey: "repair-release-analytics", VisibilityPolicy: scorerelease.VisibilityPolicy{ShowQuestionScores: true, ShowCohortStatistics: true, ShowQuestionStatistics: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE score_release SET visibility_policy=jsonb_set(visibility_policy,'{show_high_score_paper}','true'::jsonb) WHERE tenant_id=$1 AND id=$2::uuid AND status='draft'`, f.TenantID, analytics.ID); err != nil {
		t.Fatal(err)
	}
	// Model a historical release published before the anonymous asset gate.
	if _, err = scorerelease.NewPostgresStore(db, nil).Publish(ctx, f.TenantID, analytics.ID, f.AdminID); !errors.Is(err, scorerelease.ErrAnonymousPaperUnavailable) {
		t.Fatalf("legacy publication must now fail closed: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE score_release
SET status='published',published_by=$3::uuid,published_at=now(),gate_snapshot='{}'::jsonb
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='draft'`, f.TenantID, analytics.ID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO score_release_current(tenant_id,exam_id,release_id)
VALUES($1::uuid,$2::uuid,$3::uuid)
ON CONFLICT(tenant_id,exam_id) DO UPDATE SET release_id=EXCLUDED.release_id,updated_at=now()`, f.TenantID, f.ExamID, analytics.ID); err != nil {
		t.Fatal(err)
	}
	all, err := svc.Get(ctx, f.TenantID, analytics.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A legacy flag cannot expose another student's page or page metadata.
	otherStudent := all.Items[1].StudentID
	trace.queries, trace.rows = 0, 0
	result, err = reader.StudentResult(ctx, f.TenantID, f.ExamID, otherStudent)
	if err != nil || result.HighScorePaper != nil || result.Reference == nil || result.Reference.SampleSize != 2 || result.Reference.StatisticsAvailable {
		t.Fatalf("analytics: %+v %v", result, err)
	}
	if trace.queries != 1 || trace.rows != 3 {
		t.Fatalf("student question queries=%d rows=%d", trace.queries, trace.rows)
	}
	var unpublishedQuestionID string
	if err := db.QueryRowContext(ctx, `INSERT INTO question(tenant_id,exam_id,question_no,question_type,score,sort_order,status)
VALUES($1,$2::uuid,'UNPUBLISHED','short_answer',1,99,'active') RETURNING id::text`, f.TenantID, f.ExamID).Scan(&unpublishedQuestionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO answer_segment(tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status)
VALUES($1,$2::uuid,$3::uuid,$4::uuid,'UNPUBLISHED','{"x":0,"y":0,"width":1,"height":1}','configured_answer_area','accepted')`, f.TenantID, answers[0].submission, answers[0].page, unpublishedQuestionID); err != nil {
		t.Fatal(err)
	}
	// 只有答题分段、没有发布题目清单关联时，整页图片也必须拒绝返回。
	if _, err := reader.StudentPaperPageImage(ctx, f.TenantID, f.ExamID, answers[0].student, unpublishedQuestionID, false); !errors.Is(err, scorerelease.ErrNotFound) {
		t.Fatalf("unpublished whole-page image: %v", err)
	}
	questionID := f.QuestionIDs["single_choice"]
	if _, err = reader.StudentPaperPageImage(ctx, f.TenantID, f.ExamID, otherStudent, questionID, true); !errors.Is(err, scorerelease.ErrNotFound) {
		t.Fatalf("peer whole-page image: %v", err)
	}
	if own, ownErr := reader.StudentPaperPageImage(ctx, f.TenantID, f.ExamID, otherStudent, questionID, false); ownErr != nil || own.AnswerSegmentID == "" {
		t.Fatalf("own whole-page image: %+v %v", own, ownErr)
	}
	totals, err := svc.Create(ctx, f.TenantID, f.ExamID, f.AdminID, scorerelease.CreateInput{Source: scorerelease.SourceMigration, Reason: "totals only", IdempotencyKey: "repair-release-totals"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Publish(ctx, f.TenantID, totals.ID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	trace.queries, trace.rows = 0, 0
	result, err = reader.StudentResult(ctx, f.TenantID, f.ExamID, answers[0].student)
	if err != nil || len(result.Questions) != 0 || trace.queries != 0 {
		t.Fatalf("totals result: %+v queries=%d err=%v", result, trace.queries, err)
	}
}

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
	detail, err := svc.Get(ctx, f.TenantID, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	var matched bool
	for _, question := range detail.Questions {
		if question.SubmissionID == answers[0].submission && question.QuestionID == questionID {
			matched = true
			if question.SourceType != "arbitration" || question.SourceID != arbitrationID || question.Explanation.Feedback != "final arbitration feedback" {
				t.Fatalf("arbitration snapshot: %+v", question)
			}
		}
	}
	if !matched {
		t.Fatal("arbitrated question missing from release")
	}
	if _, err := svc.Publish(ctx, f.TenantID, release.ID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE arbitration_task SET student_feedback='changed after publication' WHERE tenant_id=$1 AND id=$2::uuid`, f.TenantID, arbitrationID); err != nil {
		t.Fatal(err)
	}
	result, err := svc.StudentResult(ctx, f.TenantID, f.ExamID, answers[0].student)
	if err != nil {
		t.Fatal(err)
	}
	for _, question := range result.Questions {
		if question.QuestionID == questionID {
			if question.Feedback != "final arbitration feedback" {
				t.Fatalf("published feedback changed: %+v", question)
			}
			studentReport, reportErr := report.NewPostgresStore(db).StudentReport(ctx, f.TenantID, f.ExamID, answers[0].student)
			if reportErr != nil {
				t.Fatal(reportErr)
			}
			var reportQuestionFound bool
			for _, reportQuestion := range studentReport.Questions {
				if reportQuestion.QuestionID != questionID {
					continue
				}
				reportQuestionFound = true
				if reportQuestion.Score != question.Score || len(reportQuestion.TeacherFeedback) != 1 || reportQuestion.TeacherFeedback[0].Text != "final arbitration feedback" {
					t.Fatalf("report drifted from release: %+v", reportQuestion)
				}
			}
			if !reportQuestionFound {
				t.Fatal("published report question missing")
			}
			return
		}
	}
	t.Fatal("arbitrated student question missing")
}

func TestCancelledScoringRunCannotReassignOrSubmitReview(t *testing.T) {
	db, _, _, f, answers, _ := repairFixture(t)
	ctx := context.Background()
	var segmentID, questionNo string
	questionID := f.QuestionIDs["single_choice"]
	if err := db.QueryRowContext(ctx, `SELECT answer_segment_id::text,question_no FROM final_grade WHERE tenant_id=$1 AND submission_id=$2::uuid AND question_id=$3::uuid AND deleted_at IS NULL`, f.TenantID, answers[0].submission, questionID).Scan(&segmentID, &questionNo); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := db.QueryRowContext(ctx, `INSERT INTO scoring_run(tenant_id,exam_id,idempotency_key,status,total_count,started_by,cancelled_at) VALUES($1,$2::uuid,'cancelled-review-regression','cancelled',1,$3::uuid,now()) RETURNING id::text`, f.TenantID, f.ExamID, f.AdminID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err := db.QueryRowContext(ctx, `INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,grade_round,scoring_run_id,created_by) VALUES($1,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,'anonymous','manual_sample','cancelled','single',$7::uuid,$8::uuid) RETURNING id::text`, f.TenantID, f.ExamID, questionID, questionNo, segmentID, answers[0].submission, runID, f.AdminID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	store := review.NewPostgresStore(db)
	if _, err := store.AssignTask(ctx, f.TenantID, taskID, f.AdminID, review.AssignTaskInput{AssignedTo: f.AdminID, ExpectedRevision: 1}); !errors.Is(err, review.ErrInvalidTransition) {
		t.Fatalf("assign cancelled task: %v", err)
	}
	if _, err := store.BatchAssignTasks(ctx, f.TenantID, f.AdminID, review.BatchAssignInput{TaskIDs: []string{taskID}, AssignedTo: f.AdminID, ExpectedRevisions: map[string]int64{taskID: 1}}); !errors.Is(err, review.ErrInvalidTransition) {
		t.Fatalf("batch assign cancelled task: %v", err)
	}
	if _, err := store.SubmitGrade(ctx, f.TenantID, taskID, f.AdminID, review.SubmitGradeInput{ExpectedRevision: 1, Score: 1}); !errors.Is(err, review.ErrInvalidTransition) {
		t.Fatalf("submit cancelled task: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO answer_candidate(tenant_id,answer_segment_id,scoring_run_id,source,decision,engine_version,profile_version,input_hash,is_current,created_by) VALUES($1,$2::uuid,$3::uuid,'manual','confirmed','v1','v1','1234567890abcdef',true,$4::uuid)`, f.TenantID, segmentID, runID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	_, err := grading.NewPostgresStore(db).ConfirmRuleGrade(ctx, f.TenantID, segmentID, f.AdminID, grading.Grade{AnswerSegmentID: segmentID, AutoPass: true, SuggestedScore: 1, MaxScore: 1})
	if !errors.Is(err, grading.ErrInvalidTransition) {
		t.Fatalf("rule confirmation after cancellation: %v", err)
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM scoring_run WHERE tenant_id=$1 AND id=$2::uuid`, f.TenantID, runID).Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("run status %q: %v", status, err)
	}
}

func TestRuleConfirmationWaitsForConcurrentCancellation(t *testing.T) {
	db, _, _, f, answers, _ := repairFixture(t)
	ctx := context.Background()
	questionID := f.QuestionIDs["single_choice"]
	var segmentID string
	if err := db.QueryRowContext(ctx, `SELECT answer_segment_id::text FROM final_grade WHERE tenant_id=$1 AND submission_id=$2::uuid AND question_id=$3::uuid AND deleted_at IS NULL`, f.TenantID, answers[0].submission, questionID).Scan(&segmentID); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := db.QueryRowContext(ctx, `INSERT INTO scoring_run(tenant_id,exam_id,idempotency_key,status,total_count,started_by) VALUES($1,$2::uuid,'concurrent-cancel-regression','processing',1,$3::uuid) RETURNING id::text`, f.TenantID, f.ExamID, f.AdminID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO answer_candidate(tenant_id,answer_segment_id,scoring_run_id,source,decision,engine_version,profile_version,input_hash,is_current,created_by) VALUES($1,$2::uuid,$3::uuid,'manual','confirmed','v1','v1','1234567890abcdef',true,$4::uuid)`, f.TenantID, segmentID, runID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE scoring_run SET status='cancelled',cancelled_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, f.TenantID, runID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, confirmErr := grading.NewPostgresStore(db).ConfirmRuleGrade(ctx, f.TenantID, segmentID, f.AdminID, grading.Grade{AnswerSegmentID: segmentID, AutoPass: true, SuggestedScore: 1, MaxScore: 1})
		result <- confirmErr
	}()
	select {
	case early := <-result:
		t.Fatalf("rule confirmation bypassed in-flight cancellation lock: %v", early)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case confirmErr := <-result:
		if !errors.Is(confirmErr, grading.ErrInvalidTransition) {
			t.Fatalf("rule confirmation after cancellation: %v", confirmErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("rule confirmation remained blocked after cancellation committed")
	}
	var runGrades int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM question_grade WHERE tenant_id=$1 AND scoring_run_id=$2::uuid AND is_current`, f.TenantID, runID).Scan(&runGrades); err != nil || runGrades != 0 {
		t.Fatalf("cancelled run gained %d current grades: %v", runGrades, err)
	}
}

func TestScoringAIBatchCancellationBlocksNewTasks(t *testing.T) {
	db, router, token, f, answers, _ := repairFixture(t)
	ctx := context.Background()
	questionID := f.QuestionIDs["single_choice"]
	var segmentID string
	if err := db.QueryRowContext(ctx, `SELECT answer_segment_id::text FROM final_grade WHERE tenant_id=$1 AND submission_id=$2::uuid AND question_id=$3::uuid AND deleted_at IS NULL`, f.TenantID, answers[0].submission, questionID).Scan(&segmentID); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := db.QueryRowContext(ctx, `INSERT INTO scoring_run(tenant_id,exam_id,idempotency_key,status,total_count,started_by) VALUES($1,$2::uuid,'cancel-ai-batch-regression','processing',1,$3::uuid) RETURNING id::text`, f.TenantID, f.ExamID, f.AdminID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	var batchID string
	if err := db.QueryRowContext(ctx, `INSERT INTO subjective_grading_batch(tenant_id,idempotency_key,scoring_run_id,status,segment_ids,total_count,created_by) VALUES($1,'linked-ai-batch',$2::uuid,'processing',jsonb_build_array($3::text),1,$4::uuid) RETURNING id::text`, f.TenantID, runID, segmentID, f.AdminID).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	listed := e2eGetJSON(t, router, "/api/v1/scoring-runs/"+runID+"/ai-batches", token, http.StatusOK)
	batches, ok := listed["batches"].([]any)
	if !ok || len(batches) != 1 || batches[0].(map[string]any)["id"] != batchID {
		t.Fatalf("linked batches not restored from server: %+v", listed)
	}
	var aiRunID string
	if err := db.QueryRowContext(ctx, `INSERT INTO subjective_grading_run(tenant_id,batch_id,answer_segment_id,answer_version,question_id,rubric_version,model_version,prompt_version,request_id) VALUES($1,$2::uuid,$3::uuid,'v1',$4::uuid,'v1','v1','v1','linked-ai-run') RETURNING id::text`, f.TenantID, batchID, segmentID, questionID).Scan(&aiRunID); err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err := db.QueryRowContext(ctx, `INSERT INTO agent_worker_task(tenant_id,task_type,queue_name,source_type,source_id,payload_schema_version,idempotency_key,created_by) VALUES($1,'ai_grade','subjective-grading','subjective_grading_run',$2::uuid,'subjective-grade-v1','linked-ai-task',$3::uuid) RETURNING id::text`, f.TenantID, aiRunID, f.AdminID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE scoring_run SET status='cancelling' WHERE tenant_id=$1 AND id=$2::uuid`, f.TenantID, runID); err != nil {
		t.Fatal(err)
	}
	lateTask := make(chan error, 1)
	go func() {
		_, insertErr := db.ExecContext(ctx, `INSERT INTO agent_worker_task(tenant_id,task_type,queue_name,source_type,source_id,payload_schema_version,idempotency_key,created_by) VALUES($1,'ai_grade','subjective-grading','subjective_grading_run',$2::uuid,'subjective-grade-v1','late-ai-task',$3::uuid)`, f.TenantID, aiRunID, f.AdminID)
		lateTask <- insertErr
	}()
	select {
	case insertErr := <-lateTask:
		t.Fatalf("AI task insertion bypassed in-flight cancellation lock: %v", insertErr)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case insertErr := <-lateTask:
		if insertErr == nil {
			t.Fatal("AI task was created after cancellation began")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("AI task insertion remained blocked after cancellation committed")
	}
	store := grading.NewPostgresStore(db)
	run, taskIDs, err := store.BeginScoringRunCancellation(ctx, f.TenantID, runID)
	if err != nil || run.Status != "cancelling" || len(taskIDs) != 1 || taskIDs[0] != taskID {
		t.Fatalf("begin AI cancellation: run=%+v tasks=%v err=%v", run, taskIDs, err)
	}
	if _, err := store.FinalizeScoringRunCancellation(ctx, f.TenantID, runID); !errors.Is(err, grading.ErrInvalidTransition) {
		t.Fatalf("finalize with active AI task: %v", err)
	}
	cancelled := e2ePostJSON(t, router, http.MethodPost, "/api/v1/scoring-runs/"+runID+"/cancel", token, `{}`, http.StatusOK)
	if cancelled["scoring_run"].(map[string]any)["status"] != "cancelled" {
		t.Fatalf("cancel API did not finalize AI task: %+v", cancelled)
	}
	var taskStatus string
	if err := db.QueryRowContext(ctx, `SELECT status FROM agent_worker_task WHERE id=$1::uuid`, taskID).Scan(&taskStatus); err != nil || taskStatus != "cancelled" {
		t.Fatalf("AI task status %q: %v", taskStatus, err)
	}
	var batchStatus string
	if err := db.QueryRowContext(ctx, `SELECT status FROM subjective_grading_batch WHERE id=$1::uuid`, batchID).Scan(&batchStatus); err != nil || batchStatus != "cancelled" {
		t.Fatalf("AI batch status %q: %v", batchStatus, err)
	}
}

func TestFailedScoringAIBatchSegmentsRequireOpenTeacherTask(t *testing.T) {
	db, router, token, f, answers, _ := repairFixture(t)
	ctx := context.Background()
	questionID := f.QuestionIDs["single_choice"]
	var segmentID, questionNo string
	if err := db.QueryRowContext(ctx, `SELECT answer_segment_id::text,question_no FROM final_grade WHERE tenant_id=$1 AND submission_id=$2::uuid AND question_id=$3::uuid AND deleted_at IS NULL`, f.TenantID, answers[0].submission, questionID).Scan(&segmentID, &questionNo); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := db.QueryRowContext(ctx, `INSERT INTO scoring_run(tenant_id,exam_id,idempotency_key,status,total_count,started_by) VALUES($1,$2::uuid,'failed-ai-segment-regression','needs_review',1,$3::uuid) RETURNING id::text`, f.TenantID, f.ExamID, f.AdminID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err := db.QueryRowContext(ctx, `INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,grade_round,scoring_run_id,created_by) VALUES($1,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,'anonymous','manual_sample','pending','single',$7::uuid,$8::uuid) RETURNING id::text`, f.TenantID, f.ExamID, questionID, questionNo, segmentID, answers[0].submission, runID, f.AdminID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	var batchID string
	if err := db.QueryRowContext(ctx, `INSERT INTO subjective_grading_batch(tenant_id,idempotency_key,scoring_run_id,status,segment_ids,total_count,created_by) VALUES($1,'failed-ai-segments',$2::uuid,'failed',jsonb_build_array($3::text),1,$4::uuid) RETURNING id::text`, f.TenantID, runID, segmentID, f.AdminID).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO subjective_grading_run(tenant_id,batch_id,answer_segment_id,answer_version,question_id,rubric_version,model_version,prompt_version,request_id,status) VALUES($1,$2::uuid,$3::uuid,'v1',$4::uuid,'v1','v1','v1','failed-ai-run','failed')`, f.TenantID, batchID, segmentID, questionID); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/subjective-grading-batches/" + batchID + "/failed-segments"
	listed := e2eGetJSON(t, router, path, token, http.StatusOK)
	segments, ok := listed["segment_ids"].([]any)
	if !ok || len(segments) != 1 || segments[0] != segmentID {
		t.Fatalf("failed segment not available for retry: %+v", listed)
	}
	if _, err := db.ExecContext(ctx, `UPDATE review_task SET status='cancelled' WHERE tenant_id=$1 AND id=$2::uuid`, f.TenantID, taskID); err != nil {
		t.Fatal(err)
	}
	listed = e2eGetJSON(t, router, path, token, http.StatusOK)
	segments, ok = listed["segment_ids"].([]any)
	if !ok || len(segments) != 0 {
		t.Fatalf("closed teacher task remained retryable: %+v", listed)
	}
}
