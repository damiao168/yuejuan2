package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
)

// 管理者负责派发，阅卷员负责领取和提交；这份角色夹具不合并权限或替换请求身份。
type story056ReviewActors struct {
	AdminToken, GraderToken, GraderID string
}

// 夹具使用已有阅卷岗位，但每场考试创建独立学校，分派前要补齐同校和任教学科。
func e2eBindStory056ReviewActor(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, userID string) {
	t.Helper()
	result, err := db.Exec(`UPDATE app_user SET school_id=$2::uuid, teaching_subjects=ARRAY['physics']::text[] WHERE tenant_id=$1::uuid AND id=$3::uuid`, fixture.TenantID, fixture.SchoolID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("bind STORY-056 review actor: affected=%d err=%v", count, err)
	}
}

func e2eCompleteStory056HumanReviews(t *testing.T, db *sql.DB, router http.Handler, actors story056ReviewActors, fixture story056AcceptanceFixture, count int) {
	t.Helper()
	adminToken, graderID := actors.AdminToken, actors.GraderID
	taskResponse := e2eGetJSON(t, router, "/api/v1/review-tasks?exam_id="+fixture.ExamID, adminToken, http.StatusOK)
	tasks := taskResponse["tasks"].([]any)
	pendingTasks := make([]map[string]any, 0, count)
	for _, rawTask := range tasks {
		task := rawTask.(map[string]any)
		if task["status"] == "pending" && task["source"] == "omr_ambiguous" && task["question_id"] == fixture.QuestionIDs["multiple_choice"] {
			pendingTasks = append(pendingTasks, task)
		}
	}
	if len(pendingTasks) != count {
		t.Fatalf("expected %d pending OMR review tasks for explicit assignment, got %d", count, len(pendingTasks))
	}
	for _, task := range pendingTasks {
		taskID := e2eString(t, task, "id")
		e2ePostJSON(t, router, http.MethodPost, "/api/v1/review-tasks/"+taskID+"/assign", adminToken, story056JSON(t, map[string]any{
			"assigned_to": graderID, "expected_revision": task["revision"],
		}), http.StatusOK)
	}
	for index := 0; index < count; index++ {
		e2eCompleteStory056AssignedHumanReview(t, db, router, actors, fixture)
	}
}

// 每次领取后立即提交并检查数据库；已提交任务被退回的拒绝响应不能改动成绩或进度。
func e2eCompleteStory056AssignedHumanReview(t *testing.T, db *sql.DB, router http.Handler, actors story056ReviewActors, fixture story056AcceptanceFixture) {
	t.Helper()
	adminToken, graderToken := actors.AdminToken, actors.GraderToken
	claim := e2ePostJSON(t, router, http.MethodPost, "/api/v1/review-tasks/next", graderToken, story056JSON(t, map[string]any{
		"exam_id": fixture.ExamID, "question_id": fixture.QuestionIDs["multiple_choice"],
	}), http.StatusOK)
	task := claim["task"].(map[string]any)
	if task["source"] != "omr_ambiguous" || task["status"] != "assigned" {
		t.Fatalf("human review claim has unexpected state: %#v", task)
	}
	taskID := e2eString(t, task, "id")
	submitted := e2ePostJSON(t, router, http.MethodPost, "/api/v1/review-tasks/"+taskID+"/submit", graderToken, story056JSON(t, map[string]any{
		"expected_revision": task["revision"], "score": 1, "rubric_selections": []any{}, "comments": "STORY-056 multi-select verification", "reason": "confirmed against answer key",
	}), http.StatusCreated)
	if submitted["question_grade_id"] == nil || submitted["task"].(map[string]any)["status"] != "submitted" {
		t.Fatalf("human review must create a durable question grade: %#v", submitted)
	}

	type submittedState struct {
		Status              string
		CurrentGradeID      string
		HumanGradeCount     int
		ReviewCount         int
		HumanConfirmedCount int
		FailedCount         int
	}
	readState := func() submittedState {
		var state submittedState
		if err := db.QueryRow(`
SELECT rt.status, COALESCE(rt.current_grade_id::text, ''), count(hg.id)::int,
  sr.review_count, sr.human_confirmed_count, sr.failed_count
FROM review_task rt
JOIN scoring_run sr ON sr.tenant_id=rt.tenant_id AND sr.id=rt.scoring_run_id
LEFT JOIN human_grade hg ON hg.tenant_id=rt.tenant_id AND hg.review_task_id=rt.id AND hg.deleted_at IS NULL
WHERE rt.tenant_id=$1::uuid AND rt.id=$2::uuid AND rt.deleted_at IS NULL
GROUP BY rt.status, rt.current_grade_id, sr.review_count, sr.human_confirmed_count, sr.failed_count
`, fixture.TenantID, taskID).Scan(
			&state.Status,
			&state.CurrentGradeID,
			&state.HumanGradeCount,
			&state.ReviewCount,
			&state.HumanConfirmedCount,
			&state.FailedCount,
		); err != nil {
			t.Fatalf("read submitted review state: %v", err)
		}
		return state
	}
	beforeReturn := readState()
	if beforeReturn.Status != "submitted" || beforeReturn.CurrentGradeID == "" || beforeReturn.HumanGradeCount != 1 {
		t.Fatalf("submitted review state is incomplete: %#v", beforeReturn)
	}
	e2eExpectStatus(t, router, http.MethodPost, "/api/v1/review-tasks/"+taskID+"/return", adminToken, story056JSON(t, map[string]any{
		"reason": "must not invalidate a submitted grade", "expected_revision": submitted["task"].(map[string]any)["revision"],
	}), http.StatusConflict)
	if afterReturn := readState(); afterReturn != beforeReturn {
		t.Fatalf("rejected return mutated task, grade, or scoring progress: before=%#v after=%#v", beforeReturn, afterReturn)
	}
}

func e2eAssertStory056CancelledWork(t *testing.T, db *sql.DB, tenantID, runID string, want int) {
	t.Helper()
	var cancelledTasks, invalidatedOMR int
	if err := db.QueryRow(`SELECT count(*) FROM agent_worker_task wt JOIN omr_run o ON o.tenant_id=wt.tenant_id AND o.runtime_task_id=wt.id WHERE wt.tenant_id=$1::uuid AND o.scoring_run_id=$2::uuid AND wt.status='cancelled'`, tenantID, runID).Scan(&cancelledTasks); err != nil {
		t.Fatalf("count cancelled worker tasks: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM omr_run WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND status='invalidated'`, tenantID, runID).Scan(&invalidatedOMR); err != nil {
		t.Fatalf("count invalidated OMR runs: %v", err)
	}
	if cancelledTasks != want || invalidatedOMR != want {
		t.Fatalf("cancel must finish all active work: cancelled=%d invalidated=%d want=%d", cancelledTasks, invalidatedOMR, want)
	}
}

func e2eAssertStory056AnonymousItems(t *testing.T, items []map[string]any) {
	t.Helper()
	for _, item := range items {
		for _, prohibited := range []string{"student_id", "student_name", "candidate_no"} {
			if _, found := item[prohibited]; found {
				t.Fatalf("scoring recovery detail must remain anonymous; found %s in %#v", prohibited, item)
			}
		}
	}
}

func e2eAssertStory056ConfirmedScores(t *testing.T, db *sql.DB, tenantID, runID string, wantAuto, wantHuman int) {
	t.Helper()
	var total, fullScore, autoConfirmed, humanConfirmed int
	if err := db.QueryRow(`
SELECT count(*), count(*) FILTER (WHERE score=1 AND max_score=1),
       count(*) FILTER (WHERE source='rule_confirmed'),
       count(*) FILTER (WHERE source='human')
FROM question_grade
WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND is_current AND status='confirmed' AND deleted_at IS NULL
`, tenantID, runID).Scan(&total, &fullScore, &autoConfirmed, &humanConfirmed); err != nil {
		t.Fatalf("count confirmed scores: %v", err)
	}
	wantTotal := wantAuto + wantHuman
	if total != wantTotal || fullScore != wantTotal || autoConfirmed != wantAuto || humanConfirmed != wantHuman {
		t.Fatalf("clear synthetic OMR acceptance answers must preserve score and provenance: total=%d full_score=%d auto=%d human=%d want_auto=%d want_human=%d", total, fullScore, autoConfirmed, humanConfirmed, wantAuto, wantHuman)
	}
}

func e2eAssertStory056ReprocessVersion(t *testing.T, db *sql.DB, tenantID, segmentID, runID string) {
	t.Helper()
	var currentCount, currentVersion int
	if err := db.QueryRow(`
SELECT count(*), COALESCE(max(version), 0)
FROM question_grade
WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND scoring_run_id=$3::uuid
  AND is_current AND status='confirmed' AND deleted_at IS NULL
`, tenantID, segmentID, runID).Scan(&currentCount, &currentVersion); err != nil {
		t.Fatalf("inspect reprocessed score: %v", err)
	}
	if currentCount != 1 || currentVersion < 2 {
		t.Fatalf("reprocess must leave one new current score version: count=%d version=%d", currentCount, currentVersion)
	}
}

func story056JSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal STORY-056 acceptance JSON: %v", err)
	}
	return string(raw)
}
