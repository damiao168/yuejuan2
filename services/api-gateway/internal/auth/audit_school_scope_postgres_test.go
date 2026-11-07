package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresSchoolAuditProjectionPreservesSealsAndFiltersBeforePaginationAndExport(t *testing.T) {
	ctx, db := authScopePostgresFixture(t)
	for _, table := range []string{"file_asset", "capture_batch", "capture_file", "answer_segment", "human_grade"} {
		execAuthScopeFixture(t, ctx, db, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES)")
	}
	installHistoricalAuditTrigger(t, ctx, db)
	applyAuditAppendOrderMigration(t, ctx, db)
	// 克隆表没有触发器；恢复只追加保护，迁移必须在不绕过它的情况下回填旁表。
	execAuthScopeFixture(t, ctx, db, `CREATE TRIGGER synthetic_audit_append_only BEFORE UPDATE OR DELETE ON audit_log FOR EACH ROW EXECUTE FUNCTION reject_audit_log_mutation()`)
	tenant, schoolA, schoolB, actorID, teacherB, examA, examB := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, row := range []struct{ id, school string }{{actorID, schoolA}, {teacherB, schoolB}} {
		execAuthScopeFixture(t, ctx, db, `INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status) VALUES($1::uuid,$2,$3,$1::text,'Synthetic actor','unused','active')`, row.id, tenant, row.school)
	}
	for _, row := range []struct{ id, school string }{{examA, schoolA}, {examB, schoolB}} {
		execAuthScopeFixture(t, ctx, db, `INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2,$3,'Synthetic scope','math','quiz',10,'grading','ai_assisted','manual_after_confirmation',$4)`, row.id, tenant, row.school, actorID)
	}
	store := NewPostgresStore(db)
	events := []AuditEvent{
		{TenantID: tenant, ActorID: actorID, Action: "synthetic.local.login", TargetType: "user", TargetID: actorID},
		{TenantID: tenant, ActorID: actorID, Action: "synthetic.local.exam", TargetType: "exam", TargetID: examA, BeforeValue: map[string]any{"password": "audit-test-secret"}},
		{TenantID: tenant, ActorID: actorID, Action: "synthetic.foreign.exam", TargetType: "exam", TargetID: examB},
		{TenantID: tenant, ActorID: actorID, Action: "synthetic.foreign.user", TargetType: "user", TargetID: teacherB},
		{TenantID: tenant, ActorID: actorID, Action: "synthetic.unknown", TargetType: "unknown", TargetID: uuid.NewString(), AfterValue: map[string]any{"school_id": schoolA}},
	}
	for _, event := range events {
		if err := store.Audit(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	var oldEvidence string
	if err := db.QueryRowContext(ctx, `SELECT jsonb_object_agg(id,jsonb_build_array(record_hash,previous_hash,before_value,after_value))::text FROM audit_log`).Scan(&oldEvidence); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000223_school_audit_scope.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var isolatedSearchPath string
	if err := db.QueryRowContext(ctx, `SELECT format('SET search_path = %I, public, pg_temp',current_schema())`).Scan(&isolatedSearchPath); err != nil {
		t.Fatal(err)
	}
	migrationSQL := strings.ReplaceAll(string(raw), "SET search_path = public, pg_temp", isolatedSearchPath)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, migrationSQL); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var preservedEvidence string
	if err := db.QueryRowContext(ctx, `SELECT jsonb_object_agg(id,jsonb_build_array(record_hash,previous_hash,before_value,after_value))::text FROM audit_log`).Scan(&preservedEvidence); err != nil || preservedEvidence != oldEvidence {
		t.Fatalf("scope migration changed immutable audit evidence: error=%v", err)
	}
	if err := store.Audit(ctx, AuditEvent{TenantID: tenant, ActorID: actorID, Action: "synthetic.local.new", TargetType: "exam", TargetID: examA}); err != nil {
		t.Fatal(err)
	}
	assertAuditAppendOrderLinear(t, ctx, db, tenant, 6)
	// 学校归属从目标快照读取；之后目标改校不会把旧审计跟着挪到另一校。
	execAuthScopeFixture(t, ctx, db, `UPDATE app_user SET school_id=$2 WHERE id=$1`, teacherB, schoolA)
	actor := User{ID: actorID, TenantID: tenant, Roles: []string{"school_admin"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": schoolA}}}
	scope, err := ResolveDeclaredAccessScope(actor)
	if err != nil {
		t.Fatal(err)
	}
	// 外校导航投影不能进入审计管理学校集合。
	scope.SchoolIDs = append(scope.SchoolIDs, schoolB)
	handler := NewHandler(store, time.Hour)
	seen, cursor := []string{}, ""
	for page := 0; page < 4; page++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs?limit=1&cursor="+cursor, nil)
		req = req.WithContext(WithAccessScope(WithUser(ctx, actor), scope))
		rec := httptest.NewRecorder()
		handler.ListAudits(rec, req)
		var response struct {
			Records []AuditRecord `json:"audit_logs"`
			Next    string        `json:"next_cursor"`
			HasMore bool          `json:"has_more"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil || len(response.Records) != 1 || strings.Contains(rec.Body.String(), "audit-test-secret") {
			t.Fatalf("scoped audit page failed: status=%d page=%d", rec.Code, page)
		}
		seen = append(seen, response.Records[0].Action)
		if !response.HasMore {
			break
		}
		cursor = response.Next
	}
	slices.Sort(seen)
	if !slices.Equal(seen, []string{"synthetic.local.exam", "synthetic.local.login", "synthetic.local.new"}) {
		t.Fatalf("school audit actions=%v", seen)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs/export?q=synthetic", nil)
	req = req.WithContext(WithAccessScope(WithUser(ctx, actor), scope))
	rec := httptest.NewRecorder()
	handler.ExportAudits(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "synthetic.foreign") || strings.Contains(rec.Body.String(), "synthetic.unknown") || strings.Contains(rec.Body.String(), "audit-test-secret") || !strings.Contains(rec.Body.String(), "synthetic.local.exam") {
		t.Fatalf("school audit export did not share list boundary: status=%d", rec.Code)
	}
	items, err := store.ListAudits(ctx, tenant, AuditFilter{ScopeMode: "school", SchoolIDs: []string{schoolA}, ExamID: examA, Limit: 200})
	if err != nil || len(items) != 2 {
		t.Fatalf("school exam audit filter count=%d error=%v", len(items), err)
	}
	// 每个白名单分支都执行真实查询，字段或别名错误不能在生产 AFTER INSERT 时才暴露。
	for _, kind := range strings.Fields(`user app_user class school_class student grade question_bank exam_session answer_sheet_template_library school file_asset exam question submission paper_import_job answer_sheet_template exam_paper review_task arbitration_task scoring_rule scoring_run capture_batch final_grade submission_grade appeal question_appeal operational_exception page_template_match_run double_mark_session score_release answer_group backmark_batch grader_calibration_session grading_gold_paper grading_quality_incident regrade_job release_gate_evidence release_gate_waiver_request capture_upload_session paper_import processing_exception gold_paper release_gate_waiver capture_upload answer_segment submission_page ocr_task ai_human_disagreement regrade_item ai_grade subjective_grading_run omr_run math_understanding_artifact human_grade review_annotation backmark_item scoring_run_item capture_file capture_page page_registration_run page_registration_correction auth_session user_role unknown`) {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM resolve_audit_target_scope($1,$2,$3)`, tenant, kind, uuid.NewString()).Scan(&count); err != nil || count != 0 {
			t.Fatalf("audit scope branch %s failed: count=%d error=%v", kind, count, err)
		}
	}
	submissionID, segmentID, taskID, gradeID, assetID, batchID, fileID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	execAuthScopeFixture(t, ctx, db, `INSERT INTO submission(id,tenant_id,exam_id,source_type,status,collected_by) VALUES($1,$2,$3,'manual_import','created',$4)`, submissionID, tenant, examA, actorID)
	execAuthScopeFixture(t, ctx, db, `INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1,$2,$3,$4,$5,'1','{}','manual','accepted')`, segmentID, tenant, submissionID, uuid.NewString(), uuid.NewString())
	execAuthScopeFixture(t, ctx, db, `INSERT INTO review_task(id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,created_by) VALUES($1,$2,$3,$4,'1',$5,$6,'synthetic','manual_sample','pending',$7)`, taskID, tenant, examA, uuid.NewString(), segmentID, submissionID, actorID)
	execAuthScopeFixture(t, ctx, db, `INSERT INTO human_grade(id,tenant_id,review_task_id,answer_segment_id,reviewer_id,score,max_score,grade_round) VALUES($1,$2,$3,$4,$5,1,10,'single')`, gradeID, tenant, taskID, segmentID, actorID)
	execAuthScopeFixture(t, ctx, db, `INSERT INTO file_asset(id,tenant_id,submission_id,owner_type,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1,$2,$3,'generic','synthetic.png','image/png',1,'synthetic','synthetic','synthetic','private',$4)`, assetID, tenant, submissionID, actorID)
	execAuthScopeFixture(t, ctx, db, `INSERT INTO capture_batch(id,tenant_id,exam_id,name,source_type,operator_id) VALUES($1,$2,$3,'Synthetic capture','web_upload',$4)`, batchID, tenant, examA, actorID)
	execAuthScopeFixture(t, ctx, db, `INSERT INTO capture_file(id,tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,idempotency_key,uploaded_by) VALUES($1,$2,$3,$4,'synthetic.png','image/png','synthetic',1,'synthetic',$5)`, fileID, tenant, batchID, assetID, actorID)
	for _, target := range []struct{ kind, id string }{{"answer_segment", segmentID}, {"human_grade", gradeID}, {"file_asset", assetID}, {"capture_file", fileID}} {
		if err := store.Audit(ctx, AuditEvent{TenantID: tenant, ActorID: actorID, Action: "synthetic.nested", TargetType: target.kind, TargetID: target.id}); err != nil {
			t.Fatalf("append nested audit %s: %v", target.kind, err)
		}
		var projectedSchool, projectedExam string
		if err := db.QueryRowContext(ctx, `SELECT p.school_id::text,p.exam_id::text FROM audit_log_scope p JOIN audit_log a ON a.id=p.audit_id AND a.tenant_id=p.tenant_id WHERE a.tenant_id=$1 AND a.target_id=$2 AND a.action='synthetic.nested'`, tenant, target.id).Scan(&projectedSchool, &projectedExam); err != nil || projectedSchool != schoolA || projectedExam != examA {
			t.Fatalf("nested audit %s school=%s exam=%s error=%v", target.kind, projectedSchool, projectedExam, err)
		}
	}
	// 模拟登录前无租户上下文的运行连接：旁表只能由触发器写，且触发器必须恢复调用方上下文。
	execAuthScopeFixture(t, ctx, db, `DO $$ BEGIN EXECUTE format('GRANT USAGE ON SCHEMA %I TO edugrade_tenant_runtime',current_schema()); END $$`)
	execAuthScopeFixture(t, ctx, db, `GRANT SELECT,INSERT ON audit_log TO edugrade_tenant_runtime`)
	execAuthScopeFixture(t, ctx, db, `GRANT USAGE ON SEQUENCE audit_log_append_sequence_seq TO edugrade_tenant_runtime`)
	runtimeTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeTx.Rollback()
	if _, err := runtimeTx.ExecContext(ctx, `SET LOCAL ROLE edugrade_tenant_runtime; SELECT set_config('edugrade.tenant_id','',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeTx.ExecContext(ctx, `INSERT INTO audit_log(tenant_id,actor_id,action,target_type,target_id) VALUES($1,$2,'synthetic.runtime.login','user',$2)`, tenant, actorID); err != nil {
		t.Fatalf("pre-login runtime audit append: %v", err)
	}
	var restoredTenant string
	var readable, insertAllowed int
	if err := runtimeTx.QueryRowContext(ctx, `SELECT current_setting('edugrade.tenant_id',true),(SELECT count(*) FROM audit_log_scope),has_table_privilege(current_user,'audit_log_scope','INSERT')::int`).Scan(&restoredTenant, &readable, &insertAllowed); err != nil || restoredTenant != "" || readable != 0 || insertAllowed != 0 {
		t.Fatalf("runtime audit projection escaped boundary: tenant=%q readable=%d insert=%d error=%v", restoredTenant, readable, insertAllowed, err)
	}
	if _, err := runtimeTx.ExecContext(ctx, `SELECT set_config('edugrade.tenant_id',$1,true)`, tenant); err != nil {
		t.Fatal(err)
	}
	if err := runtimeTx.QueryRowContext(ctx, `SELECT count(*) FROM audit_log_scope p JOIN audit_log a ON a.id=p.audit_id AND a.tenant_id=p.tenant_id WHERE a.action='synthetic.runtime.login'`).Scan(&readable); err != nil || readable != 1 {
		t.Fatalf("runtime login audit did not get trusted school scope: count=%d error=%v", readable, err)
	}
	if err := runtimeTx.Commit(); err != nil {
		t.Fatal(err)
	}
}
