package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type repairAnswer struct{ student, submission, page, grade string }

// 通过 API 冻结名册后，再预置完整下游评分事实。
// 与 worker 批量夹具不同，此处每位考生回答每道题。
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
		"password": "SyntheticStudent123!", "password_confirm": "SyntheticStudent123!",
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
	e2eAssertRegradeSourceAndReceipt(t, ctx, svc, f, answers, base)
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
	fixture := releaseStudentReadFixture{DB: db, Service: svc, Reader: reader, Trace: trace, Acceptance: f, Answers: answers}
	otherStudent := e2ePrepareLegacyUnsafeAnalyticsRelease(t, ctx, fixture)
	// 旧版标记不能暴露其他学生的页面或页面元数据。
	trace.queries, trace.rows = 0, 0
	result, err = reader.StudentResult(ctx, f.TenantID, f.ExamID, otherStudent)
	if err != nil || result.HighScorePaper != nil || result.Reference == nil || result.Reference.SampleSize != 2 || result.Reference.StatisticsAvailable {
		t.Fatalf("analytics: %+v %v", result, err)
	}
	if trace.queries != 1 || trace.rows != 3 {
		t.Fatalf("student question queries=%d rows=%d", trace.queries, trace.rows)
	}
	unpublishedQuestionID := e2eAssertUnpublishedQuestionIsolation(t, ctx, fixture)
	questionID := f.QuestionIDs["single_choice"]
	if _, err = reader.StudentPaperPageImage(ctx, f.TenantID, f.ExamID, otherStudent, questionID, true); !errors.Is(err, scorerelease.ErrNotFound) {
		t.Fatalf("peer whole-page image: %v", err)
	}
	if own, ownErr := reader.StudentPaperPageImage(ctx, f.TenantID, f.ExamID, otherStudent, questionID, false); ownErr != nil || own.AnswerSegmentID == "" {
		t.Fatalf("own whole-page image: %+v %v", own, ownErr)
	}
	e2eAssertTotalsOnlyStudentRead(t, ctx, fixture, unpublishedQuestionID)
}

type releaseStudentReadFixture struct {
	DB         *sql.DB
	Service    *scorerelease.Service
	Reader     *scorerelease.PostgresStore
	Trace      *repairQuestionTracer
	Acceptance story056AcceptanceFixture
	Answers    []repairAnswer
}

// 两个草稿对同一题给出不同分数，不能像互不冲突的更正一样合并。首个发布重放仍成功。
func e2eAssertRegradeSourceAndReceipt(t *testing.T, ctx context.Context, svc *scorerelease.Service, f story056AcceptanceFixture, answers []repairAnswer, base scorerelease.Release) {
	t.Helper()
	var err error
	q := f.QuestionIDs["single_choice"]
	makeDraft := func(key string, score float64) scorerelease.Release {
		r, err := svc.CreateFromRegrade(ctx, f.TenantID, f.ExamID, f.AdminID, scorerelease.CreateRegradeInput{SourceReleaseID: base.ID, QuestionID: q, Reason: "verified correction", IdempotencyKey: key, Changes: []scorerelease.RegradeChange{{SubmissionID: answers[0].submission, QuestionID: q, Score: score, MaxScore: 1}}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a := makeDraft("repair-regrade-a", 1)
	b := makeDraft("repair-regrade-b", 0.5)
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

}

// 先验证服务和存储均拒绝未准备的匿名分享，再用明确的旧版发布夹具核对后续学生投影。
func e2ePrepareLegacyUnsafeAnalyticsRelease(t *testing.T, ctx context.Context, fixture releaseStudentReadFixture) string {
	t.Helper()
	db, svc, f := fixture.DB, fixture.Service, fixture.Acceptance
	// 启用的草稿在匿名资源未经核验时不能发布。
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
	// 模拟匿名资源门禁引入前发布的历史版本。
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
	return all.Items[1].StudentID
}

// 冻结后新增题目即使产生分段和终评，也不进入正式题目清单；整页图仍需发布关联才可读取。
func e2eAssertUnpublishedQuestionIsolation(t *testing.T, ctx context.Context, fixture releaseStudentReadFixture) string {
	t.Helper()
	db, svc, reader, f, answers := fixture.DB, fixture.Service, fixture.Reader, fixture.Acceptance, fixture.Answers
	var unpublishedQuestionID string
	if err := db.QueryRowContext(ctx, `INSERT INTO question(tenant_id,exam_id,question_no,question_type,score,sort_order,status)
VALUES($1,$2::uuid,'UNPUBLISHED','short_answer',1,99,'active') RETURNING id::text`, f.TenantID, f.ExamID).Scan(&unpublishedQuestionID); err != nil {
		t.Fatal(err)
	}
	var unpublishedSegmentID string
	if err := db.QueryRowContext(ctx, `INSERT INTO answer_segment(tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status)
VALUES($1,$2::uuid,$3::uuid,$4::uuid,'UNPUBLISHED','{"x":0,"y":0,"width":1,"height":1}','configured_answer_area','accepted') RETURNING id::text`, f.TenantID, answers[0].submission, answers[0].page, unpublishedQuestionID).Scan(&unpublishedSegmentID); err != nil {
		t.Fatal(err)
	}
	// 冻结后新增的题目没有进入正式清单；无论只有分段还是又产生终评，都不应阻断发布或混入发布事实。
	gate, err := svc.Gate(ctx, f.TenantID, f.ExamID)
	if err != nil || !gate.Passed {
		t.Fatalf("unpublished segment gate: %+v %v", gate, err)
	}
	var unpublishedFinalGradeID string
	if err := db.QueryRowContext(ctx, `INSERT INTO final_grade(
  tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,
  score,max_score,source,status,locked,created_by)
SELECT fg.tenant_id,fg.exam_id,$3::uuid,'UNPUBLISHED',$4::uuid,fg.submission_id,fg.anonymous_code,
  1,1,'rule_auto','confirmed',false,$5::uuid
FROM final_grade fg WHERE fg.tenant_id=$1::uuid AND fg.submission_id=$2::uuid AND fg.deleted_at IS NULL
LIMIT 1 RETURNING id::text`, f.TenantID, answers[0].submission, unpublishedQuestionID, unpublishedSegmentID, f.AdminID).Scan(&unpublishedFinalGradeID); err != nil {
		t.Fatal(err)
	}
	gate, err = svc.Gate(ctx, f.TenantID, f.ExamID)
	if err != nil || !gate.Passed {
		t.Fatalf("unpublished final grade gate: %+v %v", gate, err)
	}
	// 题目没有发布清单关联时，即使存在答题分段和终评，整页图片也必须拒绝返回。
	if _, err := reader.StudentPaperPageImage(ctx, f.TenantID, f.ExamID, answers[0].student, unpublishedQuestionID, false); !errors.Is(err, scorerelease.ErrNotFound) {
		t.Fatalf("unpublished whole-page image: %v", err)
	}
	return unpublishedQuestionID
}

// 仅总分版本既不能混入未发布题目，也不能查询或返回题目明细。
func e2eAssertTotalsOnlyStudentRead(t *testing.T, ctx context.Context, fixture releaseStudentReadFixture, unpublishedQuestionID string) {
	t.Helper()
	db, svc, reader, trace, f, answers := fixture.DB, fixture.Service, fixture.Reader, fixture.Trace, fixture.Acceptance, fixture.Answers
	totals, err := svc.Create(ctx, f.TenantID, f.ExamID, f.AdminID, scorerelease.CreateInput{Source: scorerelease.SourceMigration, Reason: "totals only", IdempotencyKey: "repair-release-totals"})
	if err != nil {
		t.Fatal(err)
	}
	var unpublishedFacts int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM score_release_question
WHERE tenant_id=$1::uuid AND release_id=$2::uuid AND question_id=$3::uuid`, f.TenantID, totals.ID, unpublishedQuestionID).Scan(&unpublishedFacts); err != nil || unpublishedFacts != 0 {
		t.Fatalf("unpublished release facts=%d: %v", unpublishedFacts, err)
	}
	if _, err = svc.Publish(ctx, f.TenantID, totals.ID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	trace.queries, trace.rows = 0, 0
	result, err := reader.StudentResult(ctx, f.TenantID, f.ExamID, answers[0].student)
	if err != nil || len(result.Questions) != 0 || trace.queries != 0 {
		t.Fatalf("totals result: %+v queries=%d err=%v", result, trace.queries, err)
	}

}
