package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// 使用完整迁移与生产路由验证真实回执、墓碑和授权；所有数据位于本次创建的隔离库。
func TestPersonalReviewBookmarksPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for real PostgreSQL bookmark acceptance")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"grader", "teacher", "tenant_admin"})
	f := seedBookmarkPostgresFixture(t, db)
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "grader", "ChangeMe123!")
	path := "/api/v1/me/review-bookmarks/by-task/" + f.task
	state := e2eGetJSON(t, router, path, token, http.StatusOK)["bookmark"].(map[string]any)
	if state["marked"] != false || state["revision"] != float64(0) {
		t.Fatalf("unexpected absent state: %#v", state)
	}
	var taskBefore string
	if err := db.QueryRow(`SELECT row_to_json(t)::text FROM review_task t WHERE id=$1::uuid`, f.task).Scan(&taskBefore); err != nil {
		t.Fatal(err)
	}
	firstBody := `{"expected_revision":0,"reason":"核对第二步"}`
	first := bookmarkPostgresRequest(t, router, http.MethodPut, path, token, "bookmark-create", firstBody, http.StatusOK)
	id := e2eString(t, first, "bookmark_id")
	if first["revision"] != float64(1) || first["marked"] != true {
		t.Fatalf("create acknowledgement: %#v", first)
	}
	bookmarkPostgresRequest(t, router, http.MethodPut, path, token, "bookmark-stale-create", firstBody, http.StatusConflict)
	bookmarkPostgresRequest(t, router, http.MethodPut, path, token, "bookmark-too-long", `{"expected_revision":1,"reason":"`+strings.Repeat("😀", 201)+`"}`, http.StatusBadRequest)
	bookmarkPostgresRequest(t, router, http.MethodPut, path, token, "bookmark-edit", `{"expected_revision":1,"reason":"`+strings.Repeat("😀", 200)+`"}`, http.StatusOK)
	bookmarkPostgresRequest(t, router, http.MethodPut, path, token, "bookmark-create", `{"expected_revision":0,"reason":"不同载荷"}`, http.StatusConflict)
	deleted := bookmarkPostgresRequest(t, router, http.MethodDelete, "/api/v1/me/review-bookmarks/"+id, token, "bookmark-delete", `{"expected_revision":2}`, http.StatusOK)
	if deleted["marked"] != false || deleted["revision"] != float64(3) {
		t.Fatalf("delete acknowledgement: %#v", deleted)
	}
	replayed := bookmarkPostgresRequest(t, router, http.MethodPut, path, token, "bookmark-create", firstBody, http.StatusOK)
	if replayed["revision"] != float64(1) {
		t.Fatalf("original receipt was not stable: %#v", replayed)
	}
	state = e2eGetJSON(t, router, path, token, http.StatusOK)["bookmark"].(map[string]any)
	if state["marked"] != false || state["revision"] != float64(3) || state["reason"] != "" {
		t.Fatalf("late receipt revived a deleted bookmark: %#v", state)
	}
	readded := bookmarkPostgresRequest(t, router, http.MethodPut, path, token, "bookmark-readd", `{"expected_revision":3,"reason":"保留"}`, http.StatusOK)
	if readded["bookmark_id"] != id || readded["revision"] != float64(4) {
		t.Fatalf("readd lost monotonic identity: %#v", readded)
	}
	var taskAfter string
	if err := db.QueryRow(`SELECT row_to_json(t)::text FROM review_task t WHERE id=$1::uuid`, f.task).Scan(&taskAfter); err != nil || taskAfter != taskBefore {
		t.Fatalf("bookmark mutated grading task: err=%v", err)
	}
	list := e2eGetJSON(t, router, "/api/v1/me/review-bookmarks", token, http.StatusOK)
	if list["total_count"] != float64(1) || len(list["items"].([]any)) != 1 {
		t.Fatalf("visible list: %#v", list)
	}
	// 完成后移出活动 scope，仍仅能看本人历史；书签不能依赖普通活动任务授权。
	bookmarkExec(t, db, `INSERT INTO human_grade(tenant_id,review_task_id,answer_segment_id,reviewer_id,score,max_score,grade_round) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,3,5,'single')`, f.tenant, f.task, f.segment, f.grader)
	bookmarkExec(t, db, `UPDATE review_task SET status='submitted' WHERE id=$1::uuid`, f.task)
	state = e2eGetJSON(t, router, path, token, http.StatusOK)["bookmark"].(map[string]any)
	if state["marked"] != true {
		t.Fatal("completed task lost personal state")
	}
	bookmarkExec(t, db, `UPDATE review_task SET assigned_to=$2::uuid WHERE id=$1::uuid`, f.task, f.other)
	e2eGetJSON(t, router, path, token, http.StatusNotFound)
	list = e2eGetJSON(t, router, "/api/v1/me/review-bookmarks", token, http.StatusOK)
	if list["total_count"] != float64(0) || len(list["items"].([]any)) != 0 {
		t.Fatalf("reassigned source leaked into personal list: %#v", list)
	}
	bookmarkPostgresRequest(t, router, http.MethodPut, path, token, "bookmark-readd", `{"expected_revision":3,"reason":"保留"}`, http.StatusNotFound)
	cleanup := e2eGetJSON(t, router, "/api/v1/me/review-bookmarks/cleanup", token, http.StatusOK)
	cleanupJSON, _ := json.Marshal(cleanup)
	if strings.Contains(string(cleanupJSON), "保留") || strings.Contains(string(cleanupJSON), f.task) || strings.Contains(string(cleanupJSON), f.exam) {
		t.Fatalf("cleanup leaked source facts: %s", cleanupJSON)
	}
	bookmarkPostgresRequest(t, router, http.MethodDelete, "/api/v1/me/review-bookmarks/"+id, token, "bookmark-cleanup", `{"expected_revision":4}`, http.StatusOK)
	// 转回本人并归档，清理仍能执行，新增或改说明被数据库和 HTTP 同时阻止。
	bookmarkExec(t, db, `UPDATE review_task SET assigned_to=$2::uuid WHERE id=$1::uuid`, f.task, f.grader)
	bookmarkPostgresRequest(t, router, http.MethodPut, path, token, "bookmark-before-archive", `{"expected_revision":5,"reason":"归档前"}`, http.StatusOK)
	bookmarkExec(t, db, `UPDATE exam SET archived_at=now() WHERE id=$1::uuid`, f.exam)
	bookmarkPostgresRequest(t, router, http.MethodPut, path, token, "bookmark-after-archive", `{"expected_revision":6,"reason":"归档后"}`, http.StatusConflict)
	if _, err := db.Exec(`UPDATE review_bookmark SET reason='bypass', revision=revision+1 WHERE id=$1::uuid`, id); err == nil {
		t.Fatal("direct SQL edited an archived bookmark")
	}
	bookmarkPostgresRequest(t, router, http.MethodDelete, "/api/v1/me/review-bookmarks/"+id, token, "bookmark-archived-cleanup", `{"expected_revision":6}`, http.StatusOK)
	bookmarkPostgresRequest(t, router, http.MethodPut, path, token, "bookmark-archived-readd", `{"expected_revision":7,"reason":""}`, http.StatusConflict)
	if _, err := db.Exec(`DELETE FROM review_bookmark WHERE id=$1::uuid`, id); err == nil {
		t.Fatal("runtime hard delete bypassed bookmark tombstone")
	}
}

type bookmarkPostgresFixture struct{ tenant, grader, other, exam, task, segment string }

func seedBookmarkPostgresFixture(t *testing.T, db *sql.DB) bookmarkPostgresFixture {
	t.Helper()
	f := bookmarkPostgresFixture{exam: uuid.NewString(), task: uuid.NewString(), segment: uuid.NewString()}
	if err := db.QueryRow(`SELECT t.id::text,u.id::text FROM tenant t JOIN app_user u ON u.tenant_id=t.id WHERE t.code='demo' AND u.username='grader'`).Scan(&f.tenant, &f.grader); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id::text FROM app_user WHERE tenant_id=$1::uuid AND username='teacher'`, f.tenant).Scan(&f.other); err != nil {
		t.Fatal(err)
	}
	school, question, submission, asset, page := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	bookmarkExec(t, db, `INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'书签合成学校',$1::text,'active')`, school, f.tenant)
	bookmarkExec(t, db, `UPDATE app_user SET school_id=$1::uuid WHERE id IN ($2::uuid,$3::uuid)`, school, f.grader, f.other)
	bookmarkExec(t, db, `INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'书签合成考试','physics','midterm',5,'draft','ai_assisted','manual_after_confirmation',$4::uuid)`, f.exam, f.tenant, school, f.grader)
	bookmarkExec(t, db, `INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,stem,sort_order,status) VALUES($1::uuid,$2::uuid,$3::uuid,'Q1','short_answer',5,'合成题干',1,'draft')`, question, f.tenant, f.exam)
	bookmarkExec(t, db, `INSERT INTO submission(id,tenant_id,exam_id,source_type,status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,'manual_import','created',$4::uuid)`, submission, f.tenant, f.exam, f.grader)
	bookmarkExec(t, db, `INSERT INTO file_asset(id,tenant_id,school_id,exam_id,submission_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'submission_page',$5::uuid,'synthetic.png','image/png',1,$6,'bookmark-tests',$1::text,'private',$7::uuid)`, asset, f.tenant, school, f.exam, submission, strings.Repeat("a", 64), f.grader)
	bookmarkExec(t, db, `INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, page, f.tenant, submission, asset)
	bookmarkExec(t, db, `INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'Q1','{"x":0.1,"y":0.1,"w":0.5,"h":0.5}','manual','accepted')`, f.segment, f.tenant, submission, page, question)
	bookmarkExec(t, db, `INSERT INTO review_task(id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,priority,assigned_to,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Q1',$5::uuid,$6::uuid,'ANON-BOOKMARK','manual_sample','assigned',0,$7::uuid,$7::uuid)`, f.task, f.tenant, f.exam, question, f.segment, submission, f.grader)
	return f
}

func bookmarkExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("bookmark fixture SQL: %v", err)
	}
}

func bookmarkPostgresRequest(t *testing.T, router http.Handler, method, path, token, command, body string, want int) map[string]any {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	if command != "" {
		request.Header.Set("Idempotency-Key", command)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(fmt.Errorf("bookmark response: %w", err))
	}
	return result
}
