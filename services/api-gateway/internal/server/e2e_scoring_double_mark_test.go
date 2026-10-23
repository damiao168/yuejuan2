package server

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/review"
)

func TestScoringDoubleMarkLifecycleWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("dedicated PostgreSQL required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin", "grader"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	f := e2eCreateStory056MutableTemplateFixture(t, db, router, token, suffix, nil, 2)
	if _, err := db.Exec(`UPDATE exam SET grading_mode='blind_double_mark' WHERE tenant_id=$1::uuid AND id=$2::uuid`, f.TenantID, f.ExamID); err != nil {
		t.Fatal(err)
	}
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+f.ExamID+"/readiness/confirm", token, `{}`, http.StatusOK)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+f.ExamID+"/start-collection", token, `{}`, http.StatusOK)
	e2eSeedStory056AcceptanceAnswersWithQuestionTypes(t, db, f, suffix, 2, []string{"multiple_choice"})
	first := e2eLookupUserID(t, db, "demo", "grader")
	if _, err := db.Exec(`UPDATE app_user SET school_id=$2::uuid WHERE tenant_id=$1::uuid AND id=$3::uuid`, f.TenantID, f.SchoolID, first); err != nil {
		t.Fatal(err)
	}
	readiness, err := grading.NewPostgresStore(db).GetScoringReadiness(context.Background(), f.TenantID, f.ExamID)
	if err != nil || readiness.Ready {
		t.Fatalf("one grader must block dual mark: %+v %v", readiness, err)
	}
	var second string
	if err := db.QueryRow(`INSERT INTO app_user(tenant_id,school_id,username,display_name,password_hash,status) VALUES($1::uuid,$2::uuid,$3,'第二阅卷员',crypt('ChangeMe123!',gen_salt('bf')),'active') RETURNING id::text`, f.TenantID, f.SchoolID, "dual_grader_"+strings.ReplaceAll(suffix, ".", "")).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO user_role(tenant_id,user_id,role_id) SELECT $1::uuid,$2::uuid,id FROM role WHERE tenant_id=$1::uuid AND code='grader'`, f.TenantID, second); err != nil {
		t.Fatal(err)
	}
	readiness, err = grading.NewPostgresStore(db).GetScoringReadiness(context.Background(), f.TenantID, f.ExamID)
	if err != nil || !readiness.Ready {
		t.Fatalf("two graders should be ready: %+v %v", readiness, err)
	}
	run, err := grading.NewPostgresStore(db).StartScoringRun(context.Background(), f.TenantID, f.ExamID, f.AdminID, grading.StartScoringRunInput{IdempotencyKey: "double-mark-" + suffix})
	if err != nil || run.TotalCount != 2 || run.ReviewCount != 2 {
		t.Fatalf("dual start: %+v %v", run, err)
	}
	tasks := e2eCollectBlindDoubleMarkTasks(t, db, f, run.ID)
	reviews := review.NewPostgresStore(db)
	e2eSubmitBlindDoubleMarks(t, reviews, f, tasks)
	e2eResolveAndAssertDoubleMarkArbitration(t, db, reviews, f, run.ID, tasks)
	e2eCancelMixedDoubleMarkRun(t, db, router, token, f, reviews, suffix)
}

type scoringDoubleMarkTask struct {
	id, segment, reviewer, round string
	revision                     int64
}

// 原始四个任务按答案和轮次读取，核对双人分离与轮转分配后才允许评分。
func e2eCollectBlindDoubleMarkTasks(t *testing.T, db *sql.DB, f story056AcceptanceFixture, runID string) []scoringDoubleMarkTask {
	t.Helper()
	rows, err := db.Query(`SELECT id::text,answer_segment_id::text,assigned_to::text,grade_round,revision FROM review_task WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid ORDER BY answer_segment_id,grade_round`, f.TenantID, runID)
	if err != nil {
		t.Fatal(err)
	}
	var tasks []scoringDoubleMarkTask
	for rows.Next() {
		var item scoringDoubleMarkTask
		if err := rows.Scan(&item.id, &item.segment, &item.reviewer, &item.round, &item.revision); err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(tasks) != 4 || tasks[0].reviewer == tasks[1].reviewer || tasks[2].reviewer == tasks[3].reviewer || tasks[0].reviewer == tasks[2].reviewer {
		t.Fatalf("round robin/blind assignment: %+v", tasks)
	}
	return tasks
}

// 同分组先最终确认，分歧组再产生仲裁；每个原结果分支都保留在对应提交之后。
func e2eSubmitBlindDoubleMarks(t *testing.T, reviews *review.PostgresStore, f story056AcceptanceFixture, tasks []scoringDoubleMarkTask) {
	t.Helper()
	for i, item := range tasks {
		score := 0.0
		if i == 3 {
			score = 1
		}
		result, err := reviews.SubmitGrade(context.Background(), f.TenantID, item.id, item.reviewer, review.SubmitGradeInput{Score: score, ExpectedRevision: item.revision, Reason: "dual mark regression"})
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		if i == 1 && (result.FinalGrade == nil || result.QuestionGradeID == "") {
			t.Fatalf("matching marks should finalize: %+v", result)
		}
		if i == 3 && (result.ArbitrationTask == nil || result.FinalGrade != nil) {
			t.Fatalf("disagreement should arbitrate: %+v", result)
		}
	}
}

// 先拒绝未分配仲裁，再由指定负责人提交；运行汇总与两个正式成绩同时核对。
func e2eResolveAndAssertDoubleMarkArbitration(t *testing.T, db *sql.DB, reviews *review.PostgresStore, f story056AcceptanceFixture, runID string, tasks []scoringDoubleMarkTask) {
	t.Helper()
	detail, err := grading.NewPostgresStore(db).GetScoringRunDetail(context.Background(), f.TenantID, runID)
	if err != nil {
		t.Fatal(err)
	}
	markedForArbitration := false
	for _, item := range detail.Items {
		if item.AnswerSegmentID == tasks[3].segment && item.State == "review" && item.ReviewStatus == "needs_arbitration" {
			markedForArbitration = true
		}
	}
	if !markedForArbitration {
		t.Fatalf("run detail should expose pending arbitration: %+v", detail.Items)
	}
	var arbID string
	var arbRevision int64
	if err := db.QueryRow(`SELECT id::text,revision FROM arbitration_task WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND status='pending'`, f.TenantID, f.ExamID).Scan(&arbID, &arbRevision); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reviews.SubmitArbitration(context.Background(), f.TenantID, arbID, f.AdminID, review.SubmitArbitrationInput{FinalScore: 1, ExpectedRevision: arbRevision, Reason: "resolve discrepancy"}); err == nil {
		t.Fatal("unassigned arbitration submitted")
	}
	assigned, err := reviews.AssignArbitrationTask(context.Background(), f.TenantID, arbID, f.AdminID, review.AssignArbitrationTaskInput{AssignedTo: f.AdminID, ExpectedRevision: arbRevision})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reviews.SubmitArbitration(context.Background(), f.TenantID, arbID, f.AdminID, review.SubmitArbitrationInput{FinalScore: 1, ExpectedRevision: assigned.Revision, Reason: "resolve discrepancy"}); err != nil {
		t.Fatal(err)
	}
	var status string
	var reviewCount, confirmed int
	if err := db.QueryRow(`SELECT status,review_count,human_confirmed_count FROM scoring_run WHERE id=$1::uuid`, runID).Scan(&status, &reviewCount, &confirmed); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || reviewCount != 0 || confirmed != 2 {
		t.Fatalf("run after arbitration: %s review=%d confirmed=%d", status, reviewCount, confirmed)
	}
	var grades int
	if err := db.QueryRow(`SELECT count(*) FROM question_grade WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND source='human' AND status='confirmed' AND is_current`, f.TenantID, runID).Scan(&grades); err != nil {
		t.Fatal(err)
	}
	if grades != 2 {
		t.Fatalf("finalized question grades=%d", grades)
	}
}

// 新运行重复原四次提交后才取消，保留已确认组和待仲裁组同时存在的触发条件。
func e2eCancelMixedDoubleMarkRun(t *testing.T, db *sql.DB, router http.Handler, token string, f story056AcceptanceFixture, reviews *review.PostgresStore, suffix string) {
	t.Helper()
	// 后续运行可能在一对评分已最终确认、另一对已进入仲裁后取消。
	// 两组事实都不能保持可发布状态。
	secondRun, err := grading.NewPostgresStore(db).StartScoringRun(context.Background(), f.TenantID, f.ExamID, f.AdminID, grading.StartScoringRunInput{IdempotencyKey: "double-mark-cancel-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT id::text,answer_segment_id::text,assigned_to::text,grade_round,revision FROM review_task WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid ORDER BY answer_segment_id,grade_round`, f.TenantID, secondRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	var tasks []scoringDoubleMarkTask
	for rows.Next() {
		var item scoringDoubleMarkTask
		if err := rows.Scan(&item.id, &item.segment, &item.reviewer, &item.round, &item.revision); err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for i, item := range tasks {
		score := 0.0
		if i == 3 {
			score = 1
		}
		if _, err := reviews.SubmitGrade(context.Background(), f.TenantID, item.id, item.reviewer, review.SubmitGradeInput{Score: score, ExpectedRevision: item.revision, Reason: "cancel regression"}); err != nil {
			t.Fatal(err)
		}
	}
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/scoring-runs/"+secondRun.ID+"/cancel", token, `{}`, http.StatusOK)
	e2eAssertCancelledDoubleMarkFacts(t, db, f, secondRun.ID, tasks[3].segment)
}

// 取消必须同时取消仲裁、撤销当前题目成绩并移除可发布的最终成绩。
func e2eAssertCancelledDoubleMarkFacts(t *testing.T, db *sql.DB, f story056AcceptanceFixture, runID, segmentID string) {
	t.Helper()
	var status string
	var grades int
	if err := db.QueryRow(`SELECT status FROM arbitration_task WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid ORDER BY created_at DESC LIMIT 1`, f.TenantID, segmentID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatalf("arbitration after cancellation=%s", status)
	}
	if err := db.QueryRow(`SELECT count(*) FROM question_grade WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND is_current`, f.TenantID, runID).Scan(&grades); err != nil {
		t.Fatal(err)
	}
	if grades != 0 {
		t.Fatalf("cancelled run has %d current grades", grades)
	}
	if err := db.QueryRow(`SELECT count(*) FROM final_grade fg JOIN double_mark_session dm ON dm.tenant_id=fg.tenant_id AND dm.id=fg.double_mark_session_id JOIN review_task rt ON rt.tenant_id=dm.tenant_id AND rt.id=dm.first_review_task_id WHERE fg.tenant_id=$1::uuid AND rt.scoring_run_id=$2::uuid AND fg.deleted_at IS NULL`, f.TenantID, runID).Scan(&grades); err != nil {
		t.Fatal(err)
	}
	if grades != 0 {
		t.Fatalf("cancelled run has %d publishable final grades", grades)
	}

}
