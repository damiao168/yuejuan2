package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// 整场列表、编辑和归档必须在真实数据库中使用同一组子科事实。
// 此测试另建隔离数据库，覆盖 HTTP 边界和绕过 HTTP 的直接写入；不修改运行中的学校数据。
func TestWholeExamManagementE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for whole-exam PostgreSQL acceptance")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	school := e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", token, fmt.Sprintf(`{"name":"Whole Exam %s","code":"whole-%s"}`, suffix, suffix), http.StatusCreated)["school"].(map[string]any)
	schoolID := e2eString(t, school, "id")
	grade := e2ePostJSON(t, router, http.MethodPost, "/api/v1/grades", token, fmt.Sprintf(`{"school_id":"%s","name":"Whole Grade","level_no":10,"academic_year":"2026"}`, schoolID), http.StatusCreated)["grade"].(map[string]any)
	gradeID := e2eString(t, grade, "id")
	classIDs := []string{}
	for index := range 2 {
		class := e2ePostJSON(t, router, http.MethodPost, "/api/v1/classes", token, fmt.Sprintf(`{"school_id":"%s","grade_id":"%s","name":"Whole Class %d","code":"whole-%s-%d"}`, schoolID, gradeID, index, suffix, index), http.StatusCreated)["class"].(map[string]any)
		classIDs = append(classIDs, e2eString(t, class, "id"))
	}
	name := "Whole-" + suffix
	session := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exam-sessions", token, fmt.Sprintf(`{"creation_mode":"quick","school_id":"%s","grade_id":"%s","name":"%s","exam_type":"formal_exam","grading_mode":"ai_assisted","appeal_enabled":true,"publish_policy":"after_admin_approval","class_ids":["%s"],"subjects":[{"subject":"math","total_score":150,"duration_minutes":120,"candidate_rule":"all_selected_classes","class_ids":[]},{"subject":"english","total_score":100,"duration_minutes":90,"candidate_rule":"subject_selected_classes","class_ids":["%s"]}]}`, schoolID, gradeID, name, classIDs[0], classIDs[0]), http.StatusCreated)["exam_session"].(map[string]any)
	sessionID := e2eString(t, session, "id")
	path := "/api/v1/exam-management/session/" + sessionID
	item := e2eGetJSON(t, router, path, token, http.StatusOK)["exam"].(map[string]any)
	children := item["exams"].([]any)
	if len(children) != 2 || item["name"] != name || item["scope_complete"] != true {
		t.Fatalf("whole detail mismatch: %#v", item)
	}
	list := e2eGetJSON(t, router, "/api/v1/exam-management?school_id="+schoolID+"&limit=1", token, http.StatusOK)
	listed := list["exams"].([]any)
	if len(listed) != 1 || len(listed[0].(map[string]any)["exams"].([]any)) != 2 {
		t.Fatalf("group pagination split subjects: %#v", list)
	}
	mathID, englishID := "", ""
	for _, child := range children {
		exam := child.(map[string]any)
		if exam["subject"] == "math" {
			mathID = e2eString(t, exam, "id")
		} else {
			englishID = e2eString(t, exam, "id")
		}
	}
	if mathID == "" || englishID == "" {
		t.Fatal("missing fixture subjects")
	}
	scope := wholeExamManagementScope{SchoolID: schoolID, SessionID: sessionID, Path: path, MathID: mathID, EnglishID: englishID, Suffix: suffix, Name: name, ClassIDs: classIDs}
	e2eAssertWholeExamCursorPages(t, db, router, token, scope)
	item = e2eEditWholeExamSubjects(t, db, router, token, scope, item)
	item = e2eArchiveDraftWholeExam(t, db, router, token, scope, item)
	e2eRestoreWholeExamAndCheckPublication(t, db, router, token, scope, item)
	item, scope.TenantID, scope.ActorID = e2eSeedWholeExamPublishedFacts(t, db, router, token, scope)
	e2eArchivePublishedWholeExam(t, db, router, token, scope, item)
	e2eAssertStandaloneAppealArchiveBoundary(t, db, router, token, scope)
}

type wholeExamManagementScope struct {
	SchoolID, SessionID, Path, MathID, EnglishID, Suffix, Name, TenantID, ActorID string
	ClassIDs                                                                      []string
}

// 游标分页始终以整场为单位：保留两科、拒绝重复组并核对两页合计，另校夹具不混入后续归档范围。
func e2eAssertWholeExamCursorPages(t *testing.T, db *sql.DB, router http.Handler, token string, scope wholeExamManagementScope) {
	t.Helper()
	mathID, sessionID, suffix := scope.MathID, scope.SessionID, scope.Suffix
	// 另校分页夹具不干扰本场归档范围；跨页仍须保留整场全部学科，并且不能重复或遗漏。
	pageSchool := e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", token, fmt.Sprintf(`{"name":"Pagination %s","code":"page-%s"}`, suffix, suffix), http.StatusCreated)["school"].(map[string]any)
	if _, err := db.Exec(`INSERT INTO exam(tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by,created_at)
SELECT e.tenant_id,$1::uuid,'Page fixture ' || n::text,'math','formal_exam',100,'draft','ai_assisted','after_admin_approval',e.created_by,now()-interval '1 day'-n*interval '1 second'
FROM exam e CROSS JOIN generate_series(1,52) n WHERE e.id=$2::uuid`, e2eString(t, pageSchool, "id"), mathID); err != nil {
		t.Fatal(err)
	}
	listStarted := time.Now()
	globalList := e2eGetJSON(t, router, "/api/v1/exam-management?limit=50&archive=all", token, http.StatusOK)
	firstPage := globalList["exams"].([]any)
	t.Logf("whole-exam limit=50 request returned %d groups in %s", len(firstPage), time.Since(listStarted))
	if len(firstPage) != 50 || globalList["has_more"] != true {
		t.Fatalf("full page did not preserve group cursor: %#v", globalList)
	}
	nextPage := e2eGetJSON(t, router, "/api/v1/exam-management?limit=50&archive=all&cursor="+e2eString(t, globalList, "next_cursor"), token, http.StatusOK)
	seen := map[string]bool{}
	for _, raw := range append(firstPage, nextPage["exams"].([]any)...) {
		entry := raw.(map[string]any)
		key := fmt.Sprint(entry["kind"]) + ":" + e2eString(t, entry, "id")
		if seen[key] {
			t.Fatalf("group repeated across cursor pages: %s", key)
		}
		seen[key] = true
		if entry["id"] == sessionID && len(entry["exams"].([]any)) != 2 {
			t.Fatal("full page split the session into subjects")
		}
	}
	if len(seen) != 53 || nextPage["has_more"] != false {
		t.Fatalf("cursor lost whole groups: count=%d next=%#v", len(seen), nextPage)
	}
}

// 整场编辑仅同步继承配置，独立选班和满分保持原值；同一旧修订号再次提交必须整体冲突。
func e2eEditWholeExamSubjects(t *testing.T, db *sql.DB, router http.Handler, token string, scope wholeExamManagementScope, item map[string]any) map[string]any {
	t.Helper()
	name, classIDs, mathID, sessionID, path := scope.Name, scope.ClassIDs, scope.MathID, scope.SessionID, scope.Path
	update := examManagementMutationInput(t, item)
	update["name"], update["exam_type"], update["class_ids"] = name+" Updated", "midterm_exam", []string{classIDs[1]}
	item = e2eExamManagementJSON(t, router, http.MethodPatch, path, token, update, http.StatusOK)["exam"].(map[string]any)
	var editAuditCount int
	if err := db.QueryRow(`SELECT count(*) FROM audit_log WHERE action='exam_management.updated' AND target_type='exam_session' AND target_id=$1::uuid`, sessionID).Scan(&editAuditCount); err != nil || editAuditCount != 1 {
		t.Fatalf("whole edit did not preserve audit record: count=%d err=%v", editAuditCount, err)
	}
	for _, raw := range item["exams"].([]any) {
		child := raw.(map[string]any)
		wantClass := classIDs[0]
		wantScore := float64(100)
		if child["id"] == mathID {
			wantClass, wantScore = classIDs[1], 150
		}
		actualClasses := child["class_ids"].([]any)
		if len(actualClasses) != 1 || actualClasses[0] != wantClass || child["total_score"] != wantScore {
			t.Fatalf("whole edit changed independent subject configuration: %#v", child)
		}
	}
	// 已提交的旧版本不能覆盖整场新设置，也不能留下部分学科变更。
	update["command_id"] = uuid.NewString()
	e2eExamManagementJSON(t, router, http.MethodPatch, path, token, update, http.StatusConflict)
	return item
}

// 草稿归档保留历史学科状态，同时阻断 HTTP 和直接数据库写入；只读工作区和列表归档事实必须一致。
func e2eArchiveDraftWholeExam(t *testing.T, db *sql.DB, router http.Handler, token string, scope wholeExamManagementScope, item map[string]any) map[string]any {
	t.Helper()
	path, mathID, englishID, schoolID := scope.Path, scope.MathID, scope.EnglishID, scope.SchoolID
	check := e2eGetJSON(t, router, path+"/archive-check", token, http.StatusOK)["check"].(map[string]any)
	if check["allowed"] != true {
		t.Fatalf("unused whole exam should archive: %#v", check)
	}
	archiveInput := examManagementMutationInput(t, item)
	archiveInput["reason"] = "Acceptance archive"
	item = e2eExamManagementJSON(t, router, http.MethodPost, path+"/archive", token, archiveInput, http.StatusOK)["exam"].(map[string]any)
	if item["archived_at"] == nil || item["archived_at"] == "" {
		t.Fatalf("missing independent archive metadata: %#v", item)
	}
	for _, raw := range item["exams"].([]any) {
		if raw.(map[string]any)["status"] != "draft" {
			t.Fatalf("archive replaced historical subject stage: %#v", raw)
		}
	}
	e2eGetJSON(t, router, "/api/v1/exams/"+mathID, token, http.StatusOK)
	archivedWorkspace := e2eGetJSON(t, router, "/api/v1/exams/"+mathID+"/workspace", token, http.StatusOK)["workspace"].(map[string]any)
	if archivedWorkspace["read_only"] != true || archivedWorkspace["exam_status"] != "draft" {
		t.Fatalf("subject workspace lost independent archive boundary: %#v", archivedWorkspace)
	}
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+mathID+"/submissions", token, `{"candidate_no":"archived-test","source_type":"pdf_upload","expected_page_count":0}`, http.StatusConflict)
	if _, err := db.Exec(`UPDATE exam SET name='bypass archived content' WHERE id=$1::uuid`, mathID); err == nil {
		t.Fatal("direct exam write bypassed archive boundary")
	}
	if _, err := db.Exec(`DELETE FROM exam_class WHERE exam_id=$1::uuid`, englishID); err == nil {
		t.Fatal("direct roster write bypassed inherited archive boundary")
	}
	active := e2eGetJSON(t, router, "/api/v1/exam-management?school_id="+schoolID+"&archive=active", token, http.StatusOK)["exams"].([]any)
	if len(active) != 0 {
		t.Fatalf("archived whole exam remains active: %#v", active)
	}
	archived := e2eGetJSON(t, router, "/api/v1/exam-management?school_id="+schoolID+"&archive=archived", token, http.StatusOK)["exams"].([]any)
	if len(archived) != 1 {
		t.Fatalf("archive list missing whole exam: %#v", archived)
	}
	return item
}

// 恢复后才重新允许写入；仅修改 published 状态不能伪造正式发布记录或通过混合状态归档门禁。
func e2eRestoreWholeExamAndCheckPublication(t *testing.T, db *sql.DB, router http.Handler, token string, scope wholeExamManagementScope, item map[string]any) map[string]any {
	t.Helper()
	path, mathID := scope.Path, scope.MathID
	item = e2eExamManagementJSON(t, router, http.MethodPost, path+"/restore", token, examManagementMutationInput(t, item), http.StatusOK)["exam"].(map[string]any)
	if item["archived_at"] != nil && item["archived_at"] != "" {
		t.Fatalf("restore retained effective archive: %#v", item)
	}
	if _, err := db.Exec(`UPDATE exam SET status='published' WHERE id=$1::uuid`, mathID); err != nil {
		t.Fatalf("restored fixture write: %v", err)
	}
	item = e2eGetJSON(t, router, path, token, http.StatusOK)["exam"].(map[string]any)
	if item["progress"].(map[string]any)["published_count"] != float64(0) {
		t.Fatalf("status label fabricated official publication: %#v", item["progress"])
	}
	check := e2eGetJSON(t, router, path+"/archive-check", token, http.StatusOK)["check"].(map[string]any)
	if check["allowed"] == true {
		t.Fatalf("unpublished mixed exam passed archive gate: %#v", check)
	}
	return item
}

// 先建立两科不可变发布事实和当前版本指针，再核对管理投影的正式发布数量。
func e2eSeedWholeExamPublishedFacts(t *testing.T, db *sql.DB, router http.Handler, token string, scope wholeExamManagementScope) (map[string]any, string, string) {
	t.Helper()
	path, mathID, englishID := scope.Path, scope.MathID, scope.EnglishID
	// 发布事实来自正式发布记录；归档只改变管理收纳，不能移动或撤销当前成绩版本。
	var tenantID, actorID string
	if err := db.QueryRow(`SELECT e.tenant_id::text,e.created_by::text FROM exam e WHERE e.id=$1::uuid`, mathID).Scan(&tenantID, &actorID); err != nil {
		t.Fatal(err)
	}
	for _, examID := range []string{mathID, englishID} {
		releaseID := uuid.NewString()
		if _, err := db.Exec(`INSERT INTO score_release (id,tenant_id,exam_id,version,source,reason,status,idempotency_key,created_by,published_by,published_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,1,'initial','whole-exam acceptance','published',$4,$5::uuid,$5::uuid,now())`, releaseID, tenantID, examID, uuid.NewString(), actorID); err != nil {
			t.Fatalf("seed official release: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO score_release_current (tenant_id,exam_id,release_id) VALUES ($1::uuid,$2::uuid,$3::uuid)`, tenantID, examID, releaseID); err != nil {
			t.Fatalf("seed current release: %v", err)
		}
	}
	item := e2eGetJSON(t, router, path, token, http.StatusOK)["exam"].(map[string]any)
	if item["progress"].(map[string]any)["published_count"] != float64(2) {
		t.Fatalf("missing official publications: %#v", item["progress"])
	}
	return item, tenantID, actorID
}

// 后台任务未结束时两种归档查询都拒绝；取消任务再归档，前后正式成绩读取与学科历史状态保持一致。
func e2eArchivePublishedWholeExam(t *testing.T, db *sql.DB, router http.Handler, token string, scope wholeExamManagementScope, item map[string]any) map[string]any {
	t.Helper()
	path, schoolID, mathID, tenantID, actorID := scope.Path, scope.SchoolID, scope.MathID, scope.TenantID, scope.ActorID
	// 归档资格不能只看发布数量；列表批量检查与独立检查必须同时拒绝未结束后台任务。
	assertArchiveEligibility := func(want bool) {
		t.Helper()
		check := e2eGetJSON(t, router, path+"/archive-check", token, http.StatusOK)["check"].(map[string]any)
		rows := e2eGetJSON(t, router, "/api/v1/exam-management?school_id="+schoolID, token, http.StatusOK)["exams"].([]any)
		if check["allowed"] != want || len(rows) != 1 || rows[0].(map[string]any)["can_archive"] != want {
			t.Fatalf("bulk and individual archive facts differ: want=%v check=%#v list=%#v", want, check, rows)
		}
	}
	var runtimeTaskID string
	if err := db.QueryRow(`INSERT INTO agent_worker_task(tenant_id,task_type,queue_name,source_type,source_id,payload_schema_version,idempotency_key,created_by)
VALUES($1::uuid,'export','export','exam',$2::uuid,'archive-acceptance-v1',$3,$4::uuid) RETURNING id::text`, tenantID, mathID, uuid.NewString(), actorID).Scan(&runtimeTaskID); err != nil {
		t.Fatal(err)
	}
	assertArchiveEligibility(false)
	if _, err := db.Exec(`UPDATE agent_worker_task SET status='cancelled' WHERE id=$1::uuid`, runtimeTaskID); err != nil {
		t.Fatal(err)
	}
	assertArchiveEligibility(true)
	before := e2eGetJSON(t, router, "/api/v1/exams/"+mathID+"/score-releases/current-published", token, http.StatusOK)
	item = e2eExamManagementJSON(t, router, http.MethodPost, path+"/archive", token, examManagementMutationInput(t, item), http.StatusOK)["exam"].(map[string]any)
	after := e2eGetJSON(t, router, "/api/v1/exams/"+mathID+"/score-releases/current-published", token, http.StatusOK)
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatalf("archive changed public release read: before=%s after=%s", beforeJSON, afterJSON)
	}
	for _, raw := range item["exams"].([]any) {
		child := raw.(map[string]any)
		if child["id"] == mathID && child["status"] != "published" {
			t.Fatal("archive lost published subject stage")
		}
	}
	return item
}

// 独立考试保持原身份；开放申诉窗口用新发布夹具，列表和单场检查都应拒绝归档。
func e2eAssertStandaloneAppealArchiveBoundary(t *testing.T, db *sql.DB, router http.Handler, token string, scope wholeExamManagementScope) {
	t.Helper()
	schoolID, suffix, classIDs, tenantID, actorID := scope.SchoolID, scope.Suffix, scope.ClassIDs, scope.TenantID, scope.ActorID
	legacy := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams", token, fmt.Sprintf(`{"school_id":"%s","name":"Legacy Whole %s","subject":"physics","exam_type":"formal_exam","total_score":100,"grading_mode":"ai_assisted","appeal_enabled":true,"publish_policy":"after_admin_approval","class_ids":["%s"]}`, schoolID, suffix, classIDs[0]), http.StatusCreated)["exam"].(map[string]any)
	legacyPath := "/api/v1/exam-management/standalone/" + e2eString(t, legacy, "id")
	legacyItem := e2eGetJSON(t, router, legacyPath, token, http.StatusOK)["exam"].(map[string]any)
	if legacyItem["kind"] != "standalone" || len(legacyItem["exams"].([]any)) != 1 {
		t.Fatalf("legacy standalone was fabricated into another identity: %#v", legacyItem)
	}
	// 正式发布记录不可变，开放申诉窗口另建发布 fixture；不能用 UPDATE 绕过历史版本保护。
	legacyID := e2eString(t, legacy, "id")
	appealReleaseID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO score_release(id,tenant_id,exam_id,version,source,reason,status,idempotency_key,created_by,published_by,published_at,appeal_window)
VALUES($1::uuid,$2::uuid,$3::uuid,1,'initial','appeal-window acceptance','published',$4,$5::uuid,$5::uuid,now(),'{"enabled":true,"closes_at":"2099-01-01T00:00:00Z"}'::jsonb)`, appealReleaseID, tenantID, legacyID, uuid.NewString(), actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO score_release_current(tenant_id,exam_id,release_id) VALUES($1::uuid,$2::uuid,$3::uuid)`, tenantID, legacyID, appealReleaseID); err != nil {
		t.Fatal(err)
	}
	appealCheck := e2eGetJSON(t, router, legacyPath+"/archive-check", token, http.StatusOK)["check"].(map[string]any)
	if appealCheck["allowed"] != false || !strings.Contains(fmt.Sprint(appealCheck["blockers"]), "appeal_window_open") {
		t.Fatalf("open published appeal window passed archive: %#v", appealCheck)
	}
	activeList := e2eGetJSON(t, router, "/api/v1/exam-management?school_id="+schoolID, token, http.StatusOK)["exams"].([]any)
	if len(activeList) != 1 || activeList[0].(map[string]any)["can_archive"] != false {
		t.Fatalf("bulk archive check ignored published appeal window: %#v", activeList)
	}

}

func examManagementMutationInput(t *testing.T, item map[string]any) map[string]any {
	t.Helper()
	revisions := map[string]any{}
	for _, raw := range item["exams"].([]any) {
		child := raw.(map[string]any)
		revisions[e2eString(t, child, "id")] = child["revision"]
	}
	return map[string]any{"expected_revision": item["revision"], "expected_subject_revisions": revisions, "command_id": uuid.NewString()}
}

func e2eExamManagementJSON(t *testing.T, router http.Handler, method, path, token string, payload map[string]any, want int) map[string]any {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if commandID, ok := payload["command_id"].(string); ok {
		req.Header.Set("Idempotency-Key", commandID)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s expected %d, got %d %s", method, path, want, rec.Code, rec.Body.String())
	}
	out := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode management response: %v", err)
	}
	return out
}
