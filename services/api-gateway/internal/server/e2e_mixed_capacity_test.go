//go:build mixed_capacity

package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestMixedCapacityHTTP 用完整测试路由同时执行上传、教师提交和查分。
// PostgreSQL 使用随机数据库，对象存储和部署网络由现有本地夹具替代。
func TestMixedCapacityHTTP(t *testing.T) {
	binary := os.Getenv("EDUGRADE_MIXED_K6")
	if binary == "" {
		t.Fatal("set EDUGRADE_MIXED_K6 to the k6 binary")
	}
	db, router, admin, scores, answers, _ := repairFixture(t)
	scorePath := "/api/v1/exams/" + scores.ExamID
	published := e2ePostJSON(t, router, http.MethodPost, scorePath+"/publish", admin,
		`{"reason":"verify mixed capacity score reads"}`, http.StatusOK)["score_release"].(map[string]any)
	releaseID := e2eString(t, published, "id")
	var schoolCode, studentNo string
	if err := db.QueryRowContext(t.Context(), `
SELECT school.code, candidate.student_no_snapshot
FROM school JOIN exam_candidate_snapshot candidate ON candidate.tenant_id=school.tenant_id
WHERE school.id=$1 AND candidate.exam_id=$2 AND candidate.student_id=$3
`, scores.SchoolID, scores.ExamID, answers[0].student).Scan(&schoolCode, &studentNo); err != nil {
		t.Fatal(err)
	}
	student := e2eLoginProvisionedStudent(t, router, schoolCode, studentNo, "SyntheticStudent123!")
	scoreResult := e2eGetJSON(t, router, "/api/v1/student/exams/"+scores.ExamID+"/result", student, http.StatusOK)["result"].(map[string]any)
	if scoreResult["release_id"] != releaseID {
		t.Fatalf("prepared student result does not reference release %s", releaseID)
	}

	captureExam := e2eCreateStory056AcceptanceFixture(t, db, router, admin, "mixed-capture")
	batch := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+captureExam.ExamID+"/capture-batches", admin,
		`{"name":"mixed-capacity","source_type":"desktop_sync","idempotency_key":"mixed-capacity"}`, http.StatusCreated)["batch"].(map[string]any)
	batchID := e2eString(t, batch, "id")
	const taskCount = 24
	reviewExam := e2eCreateStory056AcceptanceFixture(t, db, router, admin, "mixed-review", taskCount)
	e2eSeedStory056AcceptanceAnswersWithQuestionTypes(t, db, reviewExam, "mixed-review", taskCount, []string{"single_choice"})
	graderID := e2eLookupUserID(t, db, "demo", "grader")
	e2eBindStory056ReviewActor(t, db, reviewExam, graderID)
	grader := e2eLoginWithTenant(t, router, "demo", "grader", "ChangeMe123!")
	runID, tasks := seedMixedCapacityReviews(t, db, reviewExam, graderID, taskCount)
	taskJSON, err := json.Marshal(tasks)
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "scripts", "ci", "load", "mixed-capacity.js"))
	if err != nil {
		t.Fatal(err)
	}
	summary := filepath.Join(t.TempDir(), "mixed-capacity.json")
	httpServer := httptest.NewServer(router)
	t.Cleanup(httpServer.Close)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "run", "--quiet", "--no-color", script)
	command.Env = append(os.Environ(),
		"MIXED_BASE_URL="+httpServer.URL, "MIXED_ADMIN_TOKEN="+admin, "MIXED_GRADER_TOKEN="+grader, "MIXED_STUDENT_TOKEN="+student,
		"MIXED_CAPTURE_EXAM_ID="+captureExam.ExamID, "MIXED_CAPTURE_BATCH_ID="+batchID, "MIXED_SCORE_EXAM_ID="+scores.ExamID,
		"MIXED_RELEASE_ID="+releaseID, fmt.Sprintf("MIXED_TOTAL=%v", scoreResult["total_score"]), "MIXED_REVIEW_TASK_IDS="+string(taskJSON),
		"MIXED_SCAN_PDF="+base64.StdEncoding.EncodeToString([]byte(e2eStory056PrintSource(t, "mixed-capacity"))), "MIXED_SUMMARY_PATH="+summary,
	)
	before := db.Stats()
	output, err := command.CombinedOutput()
	after := db.Stats()
	if err != nil {
		t.Fatalf("mixed HTTP smoke failed: %v\n%s", err, output)
	}
	t.Logf("pool waits=%d, duration=%s\n%s", after.WaitCount-before.WaitCount, after.WaitDuration-before.WaitDuration, output)
	uploadCount, submittedCount := readMixedCapacityCounts(t, summary)
	assertMixedCapacityFacts(t, db, batchID, runID, uploadCount, submittedCount)
}

func seedMixedCapacityReviews(t *testing.T, db *sql.DB, exam story056AcceptanceFixture, graderID string, taskCount int) (string, []string) {
	t.Helper()
	var runID string
	err := db.QueryRowContext(t.Context(), `
INSERT INTO scoring_run(tenant_id,exam_id,idempotency_key,status,total_count,review_count,started_by)
VALUES($1,$2,'mixed-capacity','needs_review',$3,$3,$4) RETURNING id::text
`, exam.TenantID, exam.ExamID, taskCount, exam.AdminID).Scan(&runID)
	if err != nil {
		t.Fatal(err)
	}
	// 独立任务避免用已提交条目循环压测。共享父运行使提交走同一把真实运行锁。
	rows, err := db.QueryContext(t.Context(), `
INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,
 assigned_to,reason_code,scoring_run_id,created_by)
SELECT seg.tenant_id,sub.exam_id,seg.question_id,seg.question_no,seg.id,sub.id,
 'mixed-'||sub.candidate_no,'manual_sample','assigned',$3,'teacher_selected',$4,$5
FROM answer_segment seg JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id
WHERE seg.tenant_id=$1 AND sub.exam_id=$2
RETURNING id::text
`, exam.TenantID, exam.ExamID, graderID, runID, exam.AdminID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := make([]string, 0, taskCount)
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, taskID)
	}
	if err = rows.Err(); err != nil || len(ids) != taskCount {
		t.Fatalf("mixed review fixtures=%d want=%d err=%v", len(ids), taskCount, err)
	}
	return runID, ids
}

func readMixedCapacityCounts(t *testing.T, path string) (uploads, submissions int) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var counters struct {
		Uploads     int `json:"uploads"`
		Submissions int `json:"submissions"`
		ScoreReads  int `json:"score_reads"`
	}
	if err := json.Unmarshal(raw, &counters); err != nil || counters.Uploads == 0 || counters.Submissions == 0 || counters.ScoreReads == 0 {
		t.Fatalf("mixed pressure counts: %s / %v", raw, err)
	}
	return counters.Uploads, counters.Submissions
}

func assertMixedCapacityFacts(t *testing.T, db *sql.DB, batchID, runID string, uploads, submissions int) {
	t.Helper()
	var captures, completed, distinctFiles, reviewed, humanGrades, questionGrades int
	err := db.QueryRowContext(t.Context(), `
SELECT
  (SELECT count(*) FROM capture_file WHERE capture_batch_id=$1),
  (SELECT count(*) FROM capture_upload_session WHERE capture_batch_id=$1 AND status='completed' AND confirmed_offset=total_size),
  (SELECT count(DISTINCT capture_file_id) FROM capture_upload_session WHERE capture_batch_id=$1 AND status='completed'),
  (SELECT count(*) FROM review_task WHERE scoring_run_id=$2 AND status='submitted' AND current_grade_id IS NOT NULL),
  (SELECT count(*) FROM human_grade hg JOIN review_task rt ON rt.tenant_id=hg.tenant_id AND rt.id=hg.review_task_id
   WHERE rt.scoring_run_id=$2 AND hg.deleted_at IS NULL AND hg.score=1),
  (SELECT count(*) FROM question_grade WHERE scoring_run_id=$2 AND source='human' AND is_current AND score=1)
`, batchID, runID).Scan(&captures, &completed, &distinctFiles, &reviewed, &humanGrades, &questionGrades)
	if err != nil || captures != uploads || completed != uploads || distinctFiles != uploads ||
		reviewed != submissions || humanGrades != submissions || questionGrades != submissions {
		t.Fatalf("upload receipts=%d registrations=%d confirmed=%d distinct=%d, review receipts=%d tasks=%d human=%d current=%d: %v",
			uploads, captures, completed, distinctFiles, submissions, reviewed, humanGrades, questionGrades, err)
	}
}
