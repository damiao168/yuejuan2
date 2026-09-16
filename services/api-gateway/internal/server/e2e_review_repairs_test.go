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

	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type repairAnswer struct{ student, submission, page, grade string }

// Freeze the roster through the API, then seed complete downstream grading
// facts. Unlike the worker-volume fixture, every candidate answers every question.
func repairFixture(t *testing.T) (*sql.DB, http.Handler, string, story056AcceptanceFixture, []repairAnswer) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin", "grader"})
	router := e2ePostgresRouter(db)
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
	return db, router, token, f, answers
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
	db, _, _, f, answers := repairFixture(t)
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
	// Optional exemplar/analytics policies still read the required cohort totals
	// and the exemplar's questions, with small-cohort privacy preserved.
	analytics, err := svc.Create(ctx, f.TenantID, f.ExamID, f.AdminID, scorerelease.CreateInput{Source: scorerelease.SourceMigration, Reason: "analytics and exemplar", IdempotencyKey: "repair-release-analytics", VisibilityPolicy: scorerelease.VisibilityPolicy{ShowQuestionScores: true, ShowHighScorePaper: true, ShowCohortStatistics: true, ShowQuestionStatistics: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Publish(ctx, f.TenantID, analytics.ID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	all, err := svc.Get(ctx, f.TenantID, analytics.ID)
	if err != nil {
		t.Fatal(err)
	}
	// All fixture totals are zero, so the first item is the selected exemplar.
	otherStudent := all.Items[1].StudentID
	trace.queries, trace.rows = 0, 0
	result, err = reader.StudentResult(ctx, f.TenantID, f.ExamID, otherStudent)
	if err != nil || result.HighScorePaper == nil || len(result.HighScorePaper.ScoreMarks) != 3 || result.Reference == nil || result.Reference.SampleSize != 2 || result.Reference.StatisticsAvailable {
		t.Fatalf("analytics: %+v %v", result, err)
	}
	if trace.queries != 2 || trace.rows != 6 {
		t.Fatalf("exemplar queries=%d rows=%d", trace.queries, trace.rows)
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
