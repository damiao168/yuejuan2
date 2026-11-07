package server

import (
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// 学生端发布只写发布记录、不改 exam.status：经真实路由读取时，工作区和首页都必须按当前发布版本显示已发布。
// 发布门禁由 scorerelease 自身测试覆盖；这里与考试管理验收一样，直接写入不可变的正式发布事实。
func TestStudentReleaseStatusE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping PostgreSQL student release status workflow")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	school := e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", token,
		`{"name":"Release Status School","code":"release-status-`+suffix+`"}`, http.StatusCreated)["school"].(map[string]any)
	schoolID := e2eString(t, school, "id")
	createFinalized := func(name string) string {
		t.Helper()
		created := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams", token,
			`{"school_id":"`+schoolID+`","name":"`+name+`","subject":"math","exam_type":"midterm","total_score":100,"grading_mode":"human_review_required","appeal_enabled":true,"publish_policy":"manual_after_confirmation","class_ids":[]}`,
			http.StatusCreated)["exam"].(map[string]any)
		examID := e2eString(t, created, "id")
		// 阅卷和定稿流程不是本测试对象；直接置为 finalized，即学生端发布前最常见的阶段。
		if _, err := db.Exec(`UPDATE exam SET status='finalized' WHERE id=$1::uuid`, examID); err != nil {
			t.Fatal(err)
		}
		return examID
	}
	releasedID, waitingID := createFinalized("Released Exam"), createFinalized("Waiting Exam")
	var tenantID, actorID string
	if err := db.QueryRow(`SELECT tenant_id::text,created_by::text FROM exam WHERE id=$1::uuid`, releasedID).Scan(&tenantID, &actorID); err != nil {
		t.Fatal(err)
	}
	releaseID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO score_release(id,tenant_id,exam_id,version,source,reason,status,idempotency_key,created_by,published_by,published_at)
VALUES($1::uuid,$2::uuid,$3::uuid,1,'initial','student release status acceptance','published',$4,$5::uuid,$5::uuid,now())`, releaseID, tenantID, releasedID, uuid.NewString(), actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO score_release_current(tenant_id,exam_id,release_id) VALUES($1::uuid,$2::uuid,$3::uuid)`, tenantID, releasedID, releaseID); err != nil {
		t.Fatal(err)
	}

	e2eAssertReleaseWorkspace(t, router, token, releasedID, "published", "成绩已发布", "view_results", "release_scores")
	e2eAssertReleaseWorkspace(t, router, token, waitingID, "finalized", "成绩已确认，等待发布", "release_scores", "view_results")

	summary := e2eGetJSON(t, router, "/api/v1/dashboard/summary", token, http.StatusOK)
	statistics := summary["statistics"].(map[string]any)
	if statistics["active_exam_count"] != float64(1) || statistics["finalized_exam_count"] != float64(1) {
		t.Fatalf("released exam is still counted as active or awaiting release: %#v", statistics)
	}
	rows := summary["active_exams"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != waitingID {
		t.Fatalf("active exams=%#v, want only %s", rows, waitingID)
	}
}

func e2eAssertReleaseWorkspace(t *testing.T, router http.Handler, token, examID, wantStatus, wantSummary, wantAction, absentAction string) {
	t.Helper()
	workspace := e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/workspace", token, http.StatusOK)["workspace"].(map[string]any)
	progress := workspace["stage_progress"].([]any)
	if workspace["exam_status"] != wantStatus || workspace["stage"] != "results" || len(progress) != 5 || progress[4].(map[string]any)["summary"] != wantSummary {
		t.Fatalf("workspace %s=%#v, want status %q and results summary %q", examID, workspace, wantStatus, wantSummary)
	}
	codes := []string{}
	for _, raw := range workspace["next_actions"].([]any) {
		codes = append(codes, raw.(map[string]any)["code"].(string))
	}
	if !slices.Contains(codes, wantAction) || slices.Contains(codes, absentAction) {
		t.Fatalf("workspace %s next actions=%v, want %q without %q", examID, codes, wantAction, absentAction)
	}
}
