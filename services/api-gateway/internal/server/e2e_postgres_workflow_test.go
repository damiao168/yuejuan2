package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCoreWorkflowE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping PostgreSQL E2E test database workflow")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eAssertSchoolAdminRBACBackfill(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin", "school_admin", "teacher", "grader"})
	e2eActivatePostgresUsers(t, db, "platform", []string{"platform_admin"})
	router := e2ePostgresRouter(db)
	suffix := time.Now().UTC().Format("20060102150405.000000000")

	platformToken := e2eLoginWithTenant(t, router, "platform", "platform_admin", "ChangeMe123!")
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	teacherToken := e2eLoginWithTenant(t, router, "demo", "teacher", "ChangeMe123!")
	teacherID := e2eLookupUserID(t, db, "demo", "teacher")
	graderID := e2eLookupUserID(t, db, "demo", "grader")
	var demoTenantID string
	if err := db.QueryRow(`SELECT id::text FROM tenant WHERE code='demo' AND deleted_at IS NULL`).Scan(&demoTenantID); err != nil {
		t.Fatalf("lookup demo tenant: %v", err)
	}
	graderToken := e2eLoginWithTenant(t, router, "demo", "grader", "ChangeMe123!")
	appealReviewerID := teacherID
	appealReviewerToken := teacherToken

	provisionedAdminUsername := "story041_admin_" + strings.ReplaceAll(suffix, ".", "_")
	tenant := e2ePostJSON(t, router, http.MethodPost, "/api/v1/tenants", platformToken, `{"name":"Story 041 Synthetic Tenant `+suffix+`","code":"story041-`+suffix+`","admin_username":"`+provisionedAdminUsername+`","admin_display_name":"Story 041 School Admin","admin_password":"Story041AdminPassphrase!"}`, http.StatusCreated)["tenant"].(map[string]any)
	if tenant["status"] != "active" {
		t.Fatalf("test database tenant should be active: %#v", tenant)
	}
	// 登录会拒绝同时指向其他租户的学校代码。这里让学校和刚创建的测试租户使用不同代码。
	schoolCode := "story041-school-" + suffix
	school := e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", adminToken, `{"name":"Story 041 Synthetic School","code":"`+schoolCode+`"}`, http.StatusCreated)["school"].(map[string]any)
	schoolID := e2eString(t, school, "id")
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", adminToken, `{"name":"Duplicate School","code":"`+schoolCode+`"}`, http.StatusConflict)
	e2eBindPostgresSchoolAdmin(t, db, "school_admin", schoolID)
	schoolAdminToken := e2eLoginWithTenant(t, router, "demo", "school_admin", "ChangeMe123!")
	evaluationKey := "pipeline-" + strings.ReplaceAll(suffix, ".", "-")
	evaluation := e2ePostJSON(t, router, http.MethodPost, "/api/v1/grading-evaluations", adminToken, `{"key":"`+evaluationKey+`","display_name":"Pipeline attribution E2E","model_reference":"local-shadow-v1","prompt_version":"prompt-v1","rubric_version":"rubric-v1","dataset_reference":"authorized-pipeline-e2e","dataset_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`, http.StatusCreated)["evaluation_run"].(map[string]any)
	evaluationID := e2eString(t, evaluation, "id")
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/grading-evaluations/"+evaluationID+"/observations", adminToken, `{"response_key":"aligned-1","response_fingerprint":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","reference_kind":"gold","subject":"mathematics","archetype":"structured_steps","ocr_quality":"high","answer_length":"medium","rubric_complexity":"medium","reference_score":4,"model_score":4,"max_score":4,"page_match_correct":true,"crop_iou":0.97,"transcription_cer":0.01,"formula_exact":true,"rubric_criterion_agreement":1,"error_source":"none","needs_human_review":false,"reference_reviewer_count":1,"reference_adjudicated":false}`, http.StatusCreated)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/grading-evaluations/"+evaluationID+"/observations", adminToken, `{"response_key":"aligned-2","response_fingerprint":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","reference_kind":"human_adjudicated","subject":"mathematics","archetype":"structured_steps","ocr_quality":"low","answer_length":"long","rubric_complexity":"high","reference_score":3,"model_score":1,"max_score":4,"page_match_correct":true,"crop_iou":0.91,"transcription_cer":0.35,"formula_exact":false,"rubric_criterion_agreement":0.5,"error_source":"formula_recognition","needs_human_review":true,"reference_reviewer_count":2,"reference_adjudicated":true}`, http.StatusCreated)
	qualitySummary := e2eGetJSON(t, router, "/api/v1/grading-evaluations/"+evaluationID+"/quality-summary", adminToken, http.StatusOK)["quality_summary"].(map[string]any)
	if qualitySummary["sample_count"] != float64(2) || qualitySummary["risky_error_routing_recall"] != float64(1) || len(qualitySummary["error_attribution"].([]any)) != 1 {
		t.Fatalf("pipeline evaluation must report aligned stage evidence and routed errors: %#v", qualitySummary)
	}
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/grading-evaluations/"+evaluationID+"/complete", adminToken, `{}`, http.StatusOK)
	grade := e2ePostJSON(t, router, http.MethodPost, "/api/v1/grades", adminToken, `{"school_id":"`+schoolID+`","name":"Story 041 Grade","level_no":10}`, http.StatusCreated)["grade"].(map[string]any)
	gradeID := e2eString(t, grade, "id")
	var schoolHasSeniorStage bool
	if err := db.QueryRow(`SELECT education_stages @> '["senior"]'::jsonb FROM school WHERE tenant_id=$1::uuid AND id=$2::uuid`, demoTenantID, schoolID).Scan(&schoolHasSeniorStage); err != nil || !schoolHasSeniorStage {
		t.Fatalf("creating a senior grade must update the school's education stages, present=%t err=%v", schoolHasSeniorStage, err)
	}
	class := e2ePostJSON(t, router, http.MethodPost, "/api/v1/classes", adminToken, `{"school_id":"`+schoolID+`","grade_id":"`+gradeID+`","name":"Story 041 Class","code":"story041-`+suffix+`"}`, http.StatusCreated)["class"].(map[string]any)
	classID := e2eString(t, class, "id")
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/classes", adminToken, `{"school_id":"`+schoolID+`","grade_id":"`+gradeID+`","name":"Duplicate Class","code":"story041-`+suffix+`"}`, http.StatusConflict)
	// 阅卷资格来自学校归属和任教学科；班级只用于学生名册，不能再为教师提供考试访问范围。
	if _, err := db.Exec(`UPDATE app_user SET school_id=$2::uuid,teaching_subjects=ARRAY['physics']::text[] WHERE tenant_id=$1::uuid AND id IN ($3::uuid,$4::uuid)`, demoTenantID, schoolID, teacherID, graderID); err != nil {
		t.Fatalf("set synthetic reviewers' school and subject: %v", err)
	}
	student := e2ePostJSON(t, router, http.MethodPost, "/api/v1/students", adminToken, `{"school_id":"`+schoolID+`","class_id":"`+classID+`","student_no":"SYN-`+suffix+`","name":"Story 041 Synthetic Student","password":"SyntheticStudent123!","password_confirm":"SyntheticStudent123!"}`, http.StatusCreated)["student"].(map[string]any)
	studentID := e2eString(t, student, "id")
	e2eAssertHighRiskTenantRLS(t, db, dsn, demoTenantID, studentID)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/students", adminToken, `{"school_id":"`+schoolID+`","class_id":"`+classID+`","student_no":"SYN-`+suffix+`","name":"Duplicate Student","password":"SyntheticStudent123!","password_confirm":"SyntheticStudent123!"}`, http.StatusConflict)
	otherStudent := e2ePostJSON(t, router, http.MethodPost, "/api/v1/students", adminToken, `{"school_id":"`+schoolID+`","class_id":"`+classID+`","student_no":"SYN-OTHER-`+suffix+`","name":"Story 041 Other Synthetic Student","password":"SyntheticStudent123!","password_confirm":"SyntheticStudent123!"}`, http.StatusCreated)["student"].(map[string]any)
	otherStudentID := e2eString(t, otherStudent, "id")
	studentToken := e2eLoginProvisionedStudent(t, router, e2eString(t, school, "code"), e2eString(t, student, "student_no"), "SyntheticStudent123!")
	otherStudentToken := e2eLoginProvisionedStudent(t, router, e2eString(t, school, "code"), e2eString(t, otherStudent, "student_no"), "SyntheticStudent123!")

	scope := postgresWorkflowScope{DB: db, Router: router, TenantID: demoTenantID, SchoolID: schoolID, GradeID: gradeID, ClassID: classID, StudentID: studentID, OtherStudentID: otherStudentID, Suffix: suffix, AdminToken: adminToken, TeacherToken: teacherToken, GraderToken: graderToken, StudentToken: studentToken, OtherStudentToken: otherStudentToken, SchoolAdminToken: schoolAdminToken, TeacherID: teacherID, GraderID: graderID, AppealReviewerID: appealReviewerID, AppealReviewerToken: appealReviewerToken}
	examID := e2ePreparePostgresExamAndFrozenPaper(t, scope)
	segmentID := e2eCollectPostgresAnswerAndAbstainFromAI(t, scope, examID)
	e2eCompletePostgresReviewWithDraftIsolation(t, scope, examID, segmentID)
	e2ePublishPostgresGradeAndReviewAppeal(t, scope, examID, segmentID)
}

type postgresWorkflowScope struct {
	DB                                                                                       *sql.DB
	Router                                                                                   http.Handler
	TenantID, SchoolID, GradeID, ClassID, StudentID, OtherStudentID, Suffix                  string
	AdminToken, TeacherToken, GraderToken, StudentToken, OtherStudentToken, SchoolAdminToken string
	TeacherID, GraderID, AppealReviewerID, AppealReviewerToken                               string
}

// 先冻结考试名单和评估规则再接收答卷；跨学科策略拒绝与正确学科快照分别保留。
func e2ePreparePostgresExamAndFrozenPaper(t *testing.T, scope postgresWorkflowScope) string {
	t.Helper()
	db, router, schoolID, classID, suffix, adminToken := scope.DB, scope.Router, scope.SchoolID, scope.ClassID, scope.Suffix, scope.AdminToken
	examResp := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams", adminToken, `{"school_id":"`+schoolID+`","name":"Story 041 Synthetic Exam","subject":"physics","exam_type":"midterm","total_score":5,"grading_mode":"ai_assisted","appeal_enabled":true,"publish_policy":"manual_after_confirmation","class_ids":["`+classID+`"]}`, http.StatusCreated)["exam"].(map[string]any)
	examID := e2eString(t, examResp, "id")
	e2eAssertPostgresExamRosterSnapshot(t, scope, examID)
	paperFileID := e2eUploadSyntheticPDF(t, router, adminToken, "story041-paper-"+suffix+".pdf", "%PDF-1.4\n% story 041 synthetic paper "+suffix+"\n")
	paperResp := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/papers", adminToken, `{"file_asset_id":"`+paperFileID+`"}`, http.StatusCreated)["paper"].(map[string]any)
	paperID := e2eString(t, paperResp, "id")
	question := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/questions", adminToken, `{"exam_paper_id":"`+paperID+`","question_no":"Q1","question_type":"short_answer","score":5,"stem":"Story 041 synthetic short answer","knowledge_points":["story041"],"answer_area":{"page":1,"x":0.1,"y":0.2,"w":0.6,"h":0.2},"sort_order":1}`, http.StatusCreated)["question"].(map[string]any)
	questionID := e2eString(t, question, "id")
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/questions/"+questionID+"/rubric", adminToken, `{"status":"approved","max_score":5,"points":[{"id":"p1","description":"story041 synthetic rubric point","score":5,"required":true}],"deductions":[],"examples":[]}`, http.StatusCreated)
	biologyProfiles := e2eGetJSON(t, router, "/api/v1/assessment/subject-profiles?stage=senior&subject=biology", adminToken, http.StatusOK)["subject_profiles"].([]any)
	if len(biologyProfiles) != 1 {
		t.Fatalf("expected one senior biology assessment profile for the cross-subject rejection check, got %#v", biologyProfiles)
	}
	biologyProfileID := e2eString(t, biologyProfiles[0].(map[string]any), "id")
	e2ePostJSON(t, router, http.MethodPut, "/api/v1/exams/"+examID+"/questions/"+questionID+"/assessment-profile", adminToken, `{"subject_profile_id":"`+biologyProfileID+`","archetype_code":"short_constructed","allowed_evidence_types":["text_span","concept"],"risk_tier":"R2","scoring_policy":{"mode":"AI_ASSIST","require_evidence":true,"human_review_below_confidence":true},"expected_revision":0}`, http.StatusBadRequest)

	profiles := e2eGetJSON(t, router, "/api/v1/assessment/subject-profiles?stage=senior&subject=physics", adminToken, http.StatusOK)["subject_profiles"].([]any)
	if len(profiles) != 1 {
		t.Fatalf("expected one senior physics assessment profile, got %#v", profiles)
	}
	profileID := e2eString(t, profiles[0].(map[string]any), "id")
	e2ePostJSON(t, router, http.MethodPut, "/api/v1/exams/"+examID+"/questions/"+questionID+"/assessment-profile", adminToken, `{"subject_profile_id":"`+profileID+`","archetype_code":"short_constructed","allowed_evidence_types":["text_span"],"risk_tier":"R2","scoring_policy":{"mode":"AI_ASSIST","require_evidence":true,"human_review_below_confidence":true},"expected_revision":0}`, http.StatusOK)
	if _, err := db.Exec(`UPDATE exam SET status='ready', updated_at=now() WHERE id=$1::uuid`, examID); err != nil {
		t.Fatalf("freeze assessment snapshot for PostgreSQL workflow: %v", err)
	}
	snapshot := e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/questions/"+questionID+"/assessment-snapshot", adminToken, http.StatusOK)["assessment_snapshot"].(map[string]any)
	if e2eString(t, snapshot, "subject_code") != "physics" || e2eString(t, snapshot, "archetype_code") != "short_constructed" {
		t.Fatalf("grading must use the frozen assessment snapshot: %#v", snapshot)
	}
	validation := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/validate-paper-config", adminToken, `{}`, http.StatusOK)["result"].(map[string]any)
	if validation["valid"] != true {
		t.Fatalf("test database paper config should be valid: %#v", validation)
	}

	return examID
}

// 考试创建后改名和转班只更新当前学籍，名单仍使用考试时的姓名和班级，历史学籍顺序也必须保留。
func e2eAssertPostgresExamRosterSnapshot(t *testing.T, scope postgresWorkflowScope, examID string) {
	t.Helper()
	db, router, demoTenantID, schoolID, gradeID, classID, studentID, suffix, adminToken := scope.DB, scope.Router, scope.TenantID, scope.SchoolID, scope.GradeID, scope.ClassID, scope.StudentID, scope.Suffix, scope.AdminToken
	var candidateCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM exam_candidate_snapshot WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, demoTenantID, examID).Scan(&candidateCount); err != nil || candidateCount != 2 {
		t.Fatalf("exam creation must freeze two candidate identities, count=%d err=%v", candidateCount, err)
	}
	if _, err := db.Exec(`UPDATE student SET name='Story 041 Current Renamed Student' WHERE tenant_id=$1::uuid AND id=$2::uuid`, demoTenantID, studentID); err != nil {
		t.Fatalf("rename current student after snapshot: %v", err)
	}
	transferClass := e2ePostJSON(t, router, http.MethodPost, "/api/v1/classes", adminToken, `{"school_id":"`+schoolID+`","grade_id":"`+gradeID+`","name":"Story 041 Transfer Class","code":"story041-transfer-`+suffix+`"}`, http.StatusCreated)["class"].(map[string]any)
	transferClassID := e2eString(t, transferClass, "id")
	e2ePostJSON(t, router, http.MethodPut, "/api/v1/students/"+studentID+"/enrollment", adminToken, `{"class_id":"`+transferClassID+`"}`, http.StatusOK)
	enrollments := e2eGetJSON(t, router, "/api/v1/students/"+studentID+"/enrollments", adminToken, http.StatusOK)["enrollments"].([]any)
	if len(enrollments) != 2 || enrollments[0].(map[string]any)["class_id"] != transferClassID || enrollments[1].(map[string]any)["class_id"] != classID {
		t.Fatalf("student transfer must preserve enrollment history: %#v", enrollments)
	}
	roster := e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/roster", adminToken, http.StatusOK)["roster"].(map[string]any)
	foundFrozenIdentity := false
	for _, rawEntry := range roster["entries"].([]any) {
		entry := rawEntry.(map[string]any)
		if entry["student_id"] == studentID {
			foundFrozenIdentity = entry["student_name"] == "Story 041 Synthetic Student" && entry["class_id"] == classID
		}
	}
	if !foundFrozenIdentity {
		t.Fatalf("exam roster must retain the student name captured at exam creation: %#v", roster)
	}
}

// 按原顺序完成图像质量、OCR 租约和切题；生产组合没有 AI 资格时必须弃权并要求人工复核。
func e2eCollectPostgresAnswerAndAbstainFromAI(t *testing.T, scope postgresWorkflowScope, examID string) string {
	t.Helper()
	db, router, demoTenantID, studentID, suffix, adminToken := scope.DB, scope.Router, scope.TenantID, scope.StudentID, scope.Suffix, scope.AdminToken
	answerFileID := e2eUploadSyntheticPDF(t, router, adminToken, "story041-answer-"+suffix+".pdf", "%PDF-1.4\n% story 041 synthetic answer "+suffix+"\n")
	submissionResp := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/submissions", adminToken, `{"student_id":"`+studentID+`","candidate_no":"SYN-`+suffix+`","source_type":"pdf_upload","expected_page_count":1}`, http.StatusCreated)["submission"].(map[string]any)
	submissionID := e2eString(t, submissionResp, "id")
	pageResp := e2ePostJSON(t, router, http.MethodPost, "/api/v1/submissions/"+submissionID+"/pages", adminToken, `{"file_asset_id":"`+answerFileID+`","page_no":1}`, http.StatusCreated)["page"].(map[string]any)
	pageID := e2eString(t, pageResp, "id")
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/submissions/"+submissionID+"/quality-check", adminToken, `{}`, http.StatusOK)
	if _, err := submission.NewPostgresStore(db).ApplyPageQualityResult(context.Background(), demoTenantID, submission.ApplyPageQualityInput{
		SubmissionID:  submissionID,
		PageID:        pageID,
		QualityStatus: "passed",
	}); err != nil {
		t.Fatalf("apply synthetic page image quality result: %v", err)
	}
	// 添加页面和质量检查会推进答卷版本，后续状态命令必须基于最新读到的版本。
	currentSubmission := e2eGetJSON(t, router, "/api/v1/submissions/"+submissionID, adminToken, http.StatusOK)["submission"].(map[string]any)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/submissions/"+submissionID+"/status", adminToken, story056JSON(t, map[string]any{
		"status": "ready_for_ocr", "expected_revision": currentSubmission["revision"],
	}), http.StatusOK)

	ocrTask := e2ePostJSON(t, router, http.MethodPost, "/api/v1/submissions/"+submissionID+"/ocr-tasks", adminToken, `{"engine":"mock_ocr","engine_version":"story041-synthetic","min_confidence":0.8}`, http.StatusCreated)["task"].(map[string]any)
	ocrTaskID := e2eString(t, ocrTask, "id")
	runtimeClaim := e2ePostWorkerJSON(t, router, http.MethodPost, "/api/v1/internal/worker/tasks/claim", adminToken, `{"queue_name":"ocr","worker_service":"story041-e2e","worker_instance_id":"story041-e2e-1","limit":1,"lease_seconds":300}`, http.StatusOK)
	runtimeTasks := runtimeClaim["tasks"].([]any)
	if len(runtimeTasks) != 1 {
		t.Fatalf("expected one OCR runtime task, got %#v", runtimeTasks)
	}
	runtimeTask := runtimeTasks[0].(map[string]any)
	if e2eString(t, runtimeTask, "source_id") != ocrTaskID {
		t.Fatalf("runtime task does not reference OCR source: %#v", runtimeTask)
	}
	runtimeTaskID := e2eString(t, runtimeTask, "id")
	runtimeLeaseToken := e2eString(t, runtimeTask, "lease_token")
	e2ePostWorkerJSON(t, router, http.MethodPost, "/api/v1/ocr-tasks/"+ocrTaskID+"/start", adminToken, `{}`, http.StatusOK)
	answerText := "Story 041 synthetic answer mentions sunlight and plant energy."
	ocrDone := e2ePostWorkerJSON(t, router, http.MethodPost, "/api/v1/ocr-tasks/"+ocrTaskID+"/results", adminToken, `{"worker_id":"story041-e2e-1","model_version":"mock-ocr-story041","config_hash":"story041-config","input_hash":"story041-input","duration_ms":12,"preprocess_profile":"story041-synthetic","runtime_task_id":"`+runtimeTaskID+`","runtime_lease_token":"`+runtimeLeaseToken+`","results":[{"submission_page_id":"`+pageID+`","text":"`+answerText+`","bbox":[0.1,0.2,0.6,0.2],"confidence":0.76,"source_image_file_id":"`+answerFileID+`"}]}`, http.StatusOK)["task"].(map[string]any)
	if ocrDone["status"] != "completed" {
		t.Fatalf("ocr task should complete in test database workflow: %#v", ocrDone)
	}
	e2eAssertPostgresProcessingProjectionIsReadOnly(t, scope, examID, ocrTaskID)
	segmentResult := e2ePostJSON(t, router, http.MethodPost, "/api/v1/submissions/"+submissionID+"/segment-answers", adminToken, `{}`, http.StatusOK)["result"].(map[string]any)
	segments := segmentResult["segments"].([]any)
	if len(segments) != 1 {
		t.Fatalf("test database workflow expected one answer segment, got %#v", segments)
	}
	segmentID := e2eString(t, segments[0].(map[string]any), "id")
	e2ePostJSON(t, router, http.MethodPut, "/api/v1/answer-segments/"+segmentID+"/answer", adminToken, `{"answer_text":"`+answerText+`","answer_payload":{"answer":"`+answerText+`"},"source":"ocr_text","confidence":0.76}`, http.StatusOK)
	aiGrade := e2ePostJSON(t, router, http.MethodPost, "/api/v1/answer-segments/"+segmentID+"/subjective-ai-grade", adminToken, `{"model_policy":{"model_version":"mock-llm-story041","prompt_version":"story041-synthetic","min_confidence":0.8}}`, http.StatusCreated)["grade"].(map[string]any)
	if aiGrade["mock"] != false || aiGrade["needs_human_review"] != true || aiGrade["failure_reason"] != "ai_eligibility_abstained" {
		t.Fatalf("production PostgreSQL composition must enforce AI eligibility and route missing policy to review: %#v", aiGrade)
	}
	aiGradeID := e2eString(t, aiGrade, "id")
	evidenceJob := e2ePostJSON(t, router, http.MethodPost, "/api/v1/ai-grades/"+aiGradeID+"/verify-evidence", adminToken, `{}`, http.StatusCreated)["job"].(map[string]any)
	if evidenceJob["needs_human_review"] != true {
		t.Fatalf("PostgreSQL evidence job should require review for an eligibility abstention: %#v", evidenceJob)
	}

	return segmentID
}

// 只读 HTTP 查询不能移动投影游标；随后重新投影也不能覆盖采集处理人已认领的异常归属。
func e2eAssertPostgresProcessingProjectionIsReadOnly(t *testing.T, scope postgresWorkflowScope, examID, ocrTaskID string) {
	t.Helper()
	db, router, demoTenantID, adminToken := scope.DB, scope.Router, scope.TenantID, scope.AdminToken
	processingStore := processing.NewPostgresStore(db)
	e2eProjectProcessingUntilCurrent(t, db, processingStore, demoTenantID, examID)
	var requestedBefore, projectedBefore int64
	if err := db.QueryRow(`SELECT requested_version,projected_version FROM processing_projection_cursor WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, demoTenantID, examID).Scan(&requestedBefore, &projectedBefore); err != nil {
		t.Fatalf("read processing cursor before API queries: %v", err)
	}
	summaryResponse := e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/processing/summary", adminToken, http.StatusOK)["summary"].(map[string]any)
	if summaryResponse["generated_at"] == "1970-01-01T00:00:00Z" {
		t.Fatalf("processing summary must expose the completed projection time: %#v", summaryResponse)
	}
	exceptionResponse := e2eGetJSON(t, router, "/api/v1/processing/exceptions?exam_id="+examID, adminToken, http.StatusOK)
	if exceptionResponse["projected_at"] == nil {
		t.Fatalf("processing exception list must expose the completed projection time: %#v", exceptionResponse)
	}
	var requestedAfter, projectedAfter int64
	if err := db.QueryRow(`SELECT requested_version,projected_version FROM processing_projection_cursor WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, demoTenantID, examID).Scan(&requestedAfter, &projectedAfter); err != nil {
		t.Fatalf("read processing cursor after API queries: %v", err)
	}
	if requestedAfter != requestedBefore || projectedAfter != projectedBefore {
		t.Fatalf("GET processing endpoints must be read-only: before=(%d,%d) after=(%d,%d)", requestedBefore, projectedBefore, requestedAfter, projectedAfter)
	}
	exceptions, err := processingStore.ListExceptions(context.Background(), demoTenantID, processing.ExceptionFilter{ExamID: examID, Limit: 25})
	if err != nil || len(exceptions.Exceptions) != 1 {
		t.Fatalf("list projected OCR exception: %#v, %v", exceptions, err)
	}
	captureManagerID := e2eAssertPostgresProcessingAssigneeIsolation(t, scope, processingStore, exceptions.Exceptions[0].ID)
	assigned, err := processingStore.AssignException(context.Background(), demoTenantID, exceptions.Exceptions[0].ID, captureManagerID, processing.AssignInput{AssigneeID: captureManagerID})
	if err != nil || assigned.Status != processing.ExceptionAssigned {
		t.Fatalf("assign projected OCR exception: %#v, %v", assigned, err)
	}
	if _, err := db.Exec(`UPDATE ocr_task SET updated_at=clock_timestamp() WHERE tenant_id=$1::uuid AND id=$2::uuid`, demoTenantID, ocrTaskID); err != nil {
		t.Fatalf("request processing projection after assignment: %v", err)
	}
	e2eProjectProcessingUntilCurrent(t, db, processingStore, demoTenantID, examID)
	preserved, err := processingStore.GetException(context.Background(), demoTenantID, assigned.ID)
	if err != nil || preserved.Status != processing.ExceptionAssigned || preserved.AssignedTo != captureManagerID {
		t.Fatalf("query refresh must preserve manual assignment: %#v, %v", preserved, err)
	}
}

// 阅卷资格不等于采集处理权限；同校阅卷员、外校管理员和停用管理员都不能受派。
func e2eAssertPostgresProcessingAssigneeIsolation(t *testing.T, scope postgresWorkflowScope, processingStore *processing.PostgresStore, exceptionID string) string {
	t.Helper()
	db, demoTenantID, graderID := scope.DB, scope.TenantID, scope.GraderID
	if _, err := db.Exec(`UPDATE app_user SET school_id=$3::uuid WHERE tenant_id=$1::uuid AND id=$2::uuid`, demoTenantID, graderID, scope.SchoolID); err != nil {
		t.Fatalf("bind grader to workflow school: %v", err)
	}
	if _, err := processingStore.AssignException(context.Background(), demoTenantID, exceptionID, graderID, processing.AssignInput{AssigneeID: graderID}); !errors.Is(err, processing.ErrNotFound) {
		t.Fatalf("grader without capture permission must not receive processing exception: %v", err)
	}
	captureManagerID := e2eLookupUserID(t, db, "demo", "school_admin")
	otherSchoolID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'Assignee isolation school',$3,'active')`, otherSchoolID, demoTenantID, "assignee-isolation-"+otherSchoolID); err != nil {
		t.Fatalf("seed different assignee school: %v", err)
	}
	for _, blocked := range []struct {
		name, status string
		schoolID     string
	}{
		{name: "different school", status: "active", schoolID: otherSchoolID},
		{name: "disabled", status: "disabled", schoolID: scope.SchoolID},
	} {
		if _, err := db.Exec(`UPDATE app_user SET school_id=$3::uuid,status=$4 WHERE tenant_id=$1::uuid AND id=$2::uuid`, demoTenantID, captureManagerID, blocked.schoolID, blocked.status); err != nil {
			t.Fatalf("prepare %s assignee: %v", blocked.name, err)
		}
		if _, err := processingStore.AssignException(context.Background(), demoTenantID, exceptionID, graderID, processing.AssignInput{AssigneeID: captureManagerID}); !errors.Is(err, processing.ErrNotFound) {
			t.Fatalf("%s assignee must remain rejected: %v", blocked.name, err)
		}
	}
	if _, err := db.Exec(`UPDATE app_user SET school_id=$3::uuid,status='active' WHERE tenant_id=$1::uuid AND id=$2::uuid`, demoTenantID, captureManagerID, scope.SchoolID); err != nil {
		t.Fatalf("restore authorized assignee: %v", err)
	}
	unchanged, err := processingStore.GetException(context.Background(), demoTenantID, exceptionID)
	if err != nil || unchanged.Status != processing.ExceptionOpen || unchanged.AssignedTo != "" {
		t.Fatalf("rejected assignees changed projected exception: %#v, %v", unchanged, err)
	}
	return captureManagerID
}

// 管理员派发后才可人工评分；临时改派立即阻断原阅卷员读写草稿，再恢复原派发并核对超分拒绝与命令重放。
func e2eCompletePostgresReviewWithDraftIsolation(t *testing.T, scope postgresWorkflowScope, examID, segmentID string) {
	t.Helper()
	db, router, demoTenantID, adminToken, teacherToken, graderToken, teacherID, graderID := scope.DB, scope.Router, scope.TenantID, scope.AdminToken, scope.TeacherToken, scope.GraderToken, scope.TeacherID, scope.GraderID
	reviewTask := e2ePostJSON(t, router, http.MethodPost, "/api/v1/review-tasks", adminToken, `{"answer_segment_id":"`+segmentID+`","source":"evidence_verification_failed","priority":5}`, http.StatusCreated)["task"].(map[string]any)
	reviewTaskID := e2eString(t, reviewTask, "id")
	e2eExpectStatus(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/publish", adminToken, `{"reason":"too early"}`, http.StatusConflict)
	e2eExpectStatus(t, router, http.MethodPost, "/api/v1/review-tasks/"+reviewTaskID+"/assign", teacherToken, `{"assigned_to":"`+graderID+`","expected_revision":1}`, http.StatusForbidden)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/review-tasks/"+reviewTaskID+"/assign", adminToken, `{"assigned_to":"`+graderID+`","expected_revision":1}`, http.StatusOK)
	e2eExpectStatus(t, router, http.MethodGet, "/api/v1/review-tasks/"+reviewTaskID+"/original-image", teacherToken, "", http.StatusForbidden)
	draftStore := review.NewPostgresStore(db)
	draft, err := draftStore.SaveDraft(context.Background(), demoTenantID, reviewTaskID, graderID, review.SaveDraftInput{
		Comments:         "grader-owned draft",
		RubricSelections: []review.RubricSelection{},
		ViewerState: map[string]any{
			"mode": "segment",
		},
	})
	if err != nil {
		t.Fatalf("save PostgreSQL review draft: %v", err)
	}
	if _, err := db.Exec(`UPDATE review_task SET assigned_to=$3::uuid,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, demoTenantID, reviewTaskID, teacherID); err != nil {
		t.Fatalf("simulate review task reassignment: %v", err)
	}
	if _, err := draftStore.GetDraft(context.Background(), demoTenantID, reviewTaskID, graderID); !errors.Is(err, review.ErrNotFound) {
		t.Fatalf("former reviewer must not read draft after reassignment, got %v", err)
	}
	if _, err := draftStore.SaveDraft(context.Background(), demoTenantID, reviewTaskID, graderID, review.SaveDraftInput{
		Comments:         "stale reviewer overwrite",
		ExpectedRevision: draft.Revision,
	}); !errors.Is(err, review.ErrForbidden) {
		t.Fatalf("former reviewer must not write draft after reassignment, got %v", err)
	}
	if _, err := db.Exec(`UPDATE review_task SET assigned_to=$3::uuid,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, demoTenantID, reviewTaskID, graderID); err != nil {
		t.Fatalf("restore review task assignment: %v", err)
	}
	e2eExpectStatus(t, router, http.MethodPost, "/api/v1/review-tasks/"+reviewTaskID+"/submit", graderToken, `{"expected_revision":2,"score":6,"rubric_selections":[{"point_id":"p1","score":6}],"comments":"over max"}`, http.StatusBadRequest)
	e2eReviewCommandRecoveryAfterAssignmentEnds(t, router, "/api/v1/review-tasks/"+reviewTaskID+"/submit", graderToken, `{"expected_revision":2,"score":4,"rubric_selections":[{"point_id":"p1","score":4}],"comments":"story041 synthetic human grade","reason":"manual review after mock AI"}`)

}

// 未收到答卷的冻结名册学生自动缺考，不再发送人工出勤写请求；成绩确认、学生隔离和申诉仍依原顺序执行。
func e2ePublishPostgresGradeAndReviewAppeal(t *testing.T, scope postgresWorkflowScope, examID, segmentID string) {
	t.Helper()
	db, router, demoTenantID, studentID, otherStudentID, adminToken, studentToken, otherStudentToken, schoolAdminToken, graderID, appealReviewerID, appealReviewerToken := scope.DB, scope.Router, scope.TenantID, scope.StudentID, scope.OtherStudentID, scope.AdminToken, scope.StudentToken, scope.OtherStudentToken, scope.SchoolAdminToken, scope.GraderID, scope.AppealReviewerID, scope.AppealReviewerToken
	// 此流程用旧答卷导入接口，补齐收卷完成事实后再验证自动缺考和成绩发布。
	e2eSeedCompletedCollection(t, db, demoTenantID, examID, e2eLookupUserID(t, db, "demo", "tenant_admin"))
	finalized := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/finalize", adminToken, `{}`, http.StatusCreated)
	if finalized["status"] != "pending_confirmation" || e2eFloat(t, finalized, "created_finals") != 1 {
		t.Fatalf("PostgreSQL finalize should create one final grade: %#v", finalized)
	}
	e2eExpectStatus(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/publish", adminToken, `{"reason":"before confirmation"}`, http.StatusConflict)
	// 退役接口不能继续改写旧标记；无卷学生由名册事实自动排除，不影响已收答卷的成绩门禁。
	e2eExpectStatus(t, router, http.MethodPut, "/api/v1/exams/"+examID+"/roster/"+otherStudentID+"/attendance", adminToken, `{"status":"absent","reason":"retired manual attendance"}`, http.StatusNotFound)
	confirmationQuality := e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/grades/quality?stage=confirmation", adminToken, http.StatusOK)["quality"].(map[string]any)
	if confirmationQuality["passed"] != true {
		// 合成整场流程失败时保留实际阻塞原因，避免把成绩门禁误诊为学生账号或登录失败。
		t.Logf("confirmation quality before publish: %#v", confirmationQuality)
	}
	e2eBusinessCommandReplay(t, router, "/api/v1/exams/"+examID+"/confirm-grades", adminToken, `{"reason":"story041 synthetic confirmation"}`, "score", http.StatusOK)
	e2eBusinessCommandReplay(t, router, "/api/v1/exams/"+examID+"/publish", adminToken, `{"reason":"story041 synthetic publish"}`, "score", http.StatusOK)
	publishedExam := e2eGetJSON(t, router, "/api/v1/exams/"+examID, adminToken, http.StatusOK)["exam"].(map[string]any)
	if publishedExam["status"] != "published" {
		t.Fatalf("publishing grades must advance the exam to published: %#v", publishedExam)
	}
	publishedQuality := e2eGetJSON(t, router, "/api/v1/exams/"+examID+"/grades/quality?stage=publish", adminToken, http.StatusOK)
	if publishedQuality["can_publish"] != true {
		t.Fatalf("published grades must remain quality-passed: %#v", publishedQuality)
	}
	studentGrade := e2eGetJSON(t, router, "/api/v1/students/"+studentID+"/exams/"+examID+"/grade", studentToken, http.StatusOK)["grade"].(map[string]any)
	if studentGrade["total_score"] != float64(4) || studentGrade["status"] != "published" {
		t.Fatalf("student should see own published grade in test database workflow: %#v", studentGrade)
	}
	e2eExpectStatus(t, router, http.MethodGet, "/api/v1/students/"+studentID+"/exams/"+examID+"/grade", otherStudentToken, "", http.StatusForbidden)
	finalID := e2eString(t, studentGrade["items"].([]any)[0].(map[string]any), "id")
	appealResp := e2ePostJSON(t, router, http.MethodPost, "/api/v1/appeals", studentToken, `{"exam_id":"`+examID+`","student_id":"`+studentID+`","target_type":"question","final_grade_id":"`+finalID+`","reason":"Story 041 synthetic appeal"}`, http.StatusCreated)["appeal"].(map[string]any)
	appealID := e2eString(t, appealResp, "id")
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/appeals/"+appealID+"/assign", schoolAdminToken, `{"assigned_to":"`+appealReviewerID+`","expected_revision":1}`, http.StatusOK)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/appeals/"+appealID+"/recommendation", appealReviewerToken, `{"recommendation":"adjust_score","reason":"Story 041 synthetic independent appeal review","recommended_score":5,"expected_revision":2}`, http.StatusOK)
	appealReviewed := e2ePostJSON(t, router, http.MethodPost, "/api/v1/appeals/"+appealID+"/review", schoolAdminToken, `{"status":"score_adjusted","reason":"Story 041 synthetic appeal adjustment","adjusted_score":5,"expected_revision":3}`, http.StatusOK)
	if appealReviewed["score_adjustment"] == nil {
		t.Fatalf("PostgreSQL appeal review should create score adjustment: %#v", appealReviewed)
	}
	audits := e2eGetJSON(t, router, "/api/v1/audit-logs?limit=200", adminToken, http.StatusOK)["audit_logs"].([]any)
	e2eAssertAuditActions(t, audits, []string{"score.published", "appeal.assigned", "appeal.teacher_recommendation_submitted", "appeal.reviewed", "review.human_grade_submitted"})
	var manualAttendanceAuditCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE tenant_id=$1::uuid AND action='score.roster_attendance_updated' AND target_id=$2`, demoTenantID, otherStudentID).Scan(&manualAttendanceAuditCount); err != nil || manualAttendanceAuditCount != 0 {
		t.Fatalf("retired manual attendance must not append an attendance audit, count=%d err=%v", manualAttendanceAuditCount, err)
	}
	secondReviewer, arbitrator := e2eSeedPostgresCommandReviewers(t, scope)
	e2eArbitrationAndExportCommands(t, db, arbitrationCommandFixture{Tenant: demoTenantID, Exam: examID, Segment: segmentID, FirstActor: graderID, SecondActor: secondReviewer, Arbitrator: arbitrator})

}
