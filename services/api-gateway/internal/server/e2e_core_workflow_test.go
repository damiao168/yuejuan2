package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appealpkg "edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/evidence"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/segment"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"edugrade-enterprise/services/api-gateway/internal/submission"
)

const (
	e2eTenantID       = "tenant-1"
	e2eTenantCode     = "e2e"
	e2ePassword       = "ChangeMe123!"
	e2eAdminID        = "00000000-0000-0000-0000-000000000a01"
	e2eTeacherID      = "00000000-0000-0000-0000-000000000a02"
	e2eStudentUserID  = "00000000-0000-0000-0000-000000000a03"
	e2eOtherUserID    = "00000000-0000-0000-0000-000000000a04"
	e2eLimitedUserID  = "00000000-0000-0000-0000-000000000a05"
	e2eStudentID      = "11111111-1111-1111-1111-111111111111"
	e2eOtherStudentID = "22222222-2222-2222-2222-222222222222"
)

type e2eMemoryFixture struct {
	router          http.Handler
	authStore       *auth.MemoryStore
	paperStore      *paper.MemoryStore
	submissionStore *submission.MemoryStore
	gradingStore    *grading.MemoryStore
	subjectiveStore *subjective.MemoryStore
	evidenceStore   *evidence.MemoryStore
	reviewStore     *review.MemoryStore
	scoreStore      *score.MemoryStore
	appealStore     *appealpkg.MemoryStore
}

func TestCoreWorkflowE2EWithSyntheticMemoryStores(t *testing.T) {
	fixture := newE2EMemoryFixture(t)
	adminToken := e2eLogin(t, fixture.router, "e2e_admin")
	teacherToken := e2eLogin(t, fixture.router, "e2e_teacher")
	studentToken := e2eLogin(t, fixture.router, "e2e_student")
	otherStudentToken := e2eLogin(t, fixture.router, "e2e_other_student")
	limitedToken := e2eLogin(t, fixture.router, "e2e_limited")

	e2eExpectStatus(t, fixture.router, http.MethodPost, "/api/v1/exams", limitedToken, `{"name":"blocked"}`, http.StatusForbidden)

	e2eExpectStatus(t, fixture.router, http.MethodPost, "/api/v1/tenants", adminToken, `{"name":"Synthetic E2E Tenant","code":"synthetic-e2e"}`, http.StatusForbidden)

	actors := memoryWorkflowActors{Admin: adminToken, Teacher: teacherToken, Student: studentToken, OtherStudent: otherStudentToken}
	exam := e2ePrepareMemoryWorkflowExam(t, fixture, adminToken)
	answer := e2eCollectMemoryWorkflowAnswer(t, fixture, adminToken, exam.ExamID)
	aiGradeID := e2eReviewMemoryWorkflowEvidence(t, fixture, actors, exam, answer)
	e2ePublishAndAppealMemoryWorkflow(t, fixture, actors, exam, answer, aiGradeID)
}

type memoryWorkflowActors struct {
	Admin, Teacher, Student, OtherStudent string
}

type memoryWorkflowExam struct {
	ExamID, PaperID, QuestionID, RubricID string
}

type memoryWorkflowAnswer struct {
	SubmissionID, PageID, AnswerFileID, AnswerText, SegmentID string
}

// 内存夹具需要显式设置试卷总分，再保存题目与量规；纸卷配置校验仍走原 API。
func e2ePrepareMemoryWorkflowExam(t *testing.T, fixture *e2eMemoryFixture, adminToken string) memoryWorkflowExam {
	t.Helper()
	school := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/schools", adminToken, `{"name":"Synthetic Academy","code":"SYN-E2E"}`, http.StatusCreated)["school"].(map[string]any)
	schoolID := e2eString(t, school, "id")
	grade := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/grades", adminToken, `{"school_id":"`+schoolID+`","name":"Synthetic Grade 10","level_no":10,"academic_year":"2026"}`, http.StatusCreated)["grade"].(map[string]any)
	gradeID := e2eString(t, grade, "id")
	class := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/classes", adminToken, `{"school_id":"`+schoolID+`","grade_id":"`+gradeID+`","name":"Synthetic Class A","code":"SYN-A"}`, http.StatusCreated)["class"].(map[string]any)
	classID := e2eString(t, class, "id")
	student := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/students", adminToken, `{"id":"`+e2eStudentID+`","school_id":"`+schoolID+`","class_id":"`+classID+`","student_no":"SYN-001","name":"Synthetic Student One"}`, http.StatusCreated)["student"].(map[string]any)
	if got := e2eString(t, student, "id"); got != e2eStudentID {
		t.Fatalf("synthetic student id should be preserved for UUID-based submission tests, got %s", got)
	}

	examBody := `{"school_id":"` + schoolID + `","name":"Synthetic Midterm","subject":"physics","exam_type":"midterm","total_score":5,"grading_mode":"ai_assisted","appeal_enabled":true,"publish_policy":"manual_after_confirmation","class_ids":["` + classID + `"]}`
	examResp := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/exams", adminToken, examBody, http.StatusCreated)["exam"].(map[string]any)
	examID := e2eString(t, examResp, "id")
	if examResp["status"] != "draft" {
		t.Fatalf("exam should start as draft: %#v", examResp)
	}
	fixture.paperStore.SetExamTotal(examID, 5)

	paperFileID := e2eUploadSyntheticPDF(t, fixture.router, adminToken, "synthetic-paper.pdf", "%PDF-1.4\n% synthetic exam paper\n")
	paperResp := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/exams/"+examID+"/papers", adminToken, `{"file_asset_id":"`+paperFileID+`"}`, http.StatusCreated)["paper"].(map[string]any)
	paperID := e2eString(t, paperResp, "id")
	if paperResp["status"] != "uploaded" {
		t.Fatalf("paper should be uploaded: %#v", paperResp)
	}

	questionBody := `{"exam_paper_id":"` + paperID + `","question_no":"Q1","question_type":"short_answer","score":5,"stem":"Synthetic prompt: explain one photosynthesis benefit.","knowledge_points":["synthetic-photosynthesis"],"answer_area":{"page":1,"x":0.1,"y":0.2,"w":0.6,"h":0.2},"sort_order":1}`
	questionResp := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/exams/"+examID+"/questions", adminToken, questionBody, http.StatusCreated)["question"].(map[string]any)
	questionID := e2eString(t, questionResp, "id")
	rubricResp := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/questions/"+questionID+"/rubric", adminToken, `{"status":"approved","max_score":5,"points":[{"id":"p1","description":"synthetic point: mentions sunlight and plant energy","score":5,"required":true}],"deductions":[],"examples":[]}`, http.StatusCreated)["rubric"].(map[string]any)
	rubricID := e2eString(t, rubricResp, "id")
	validateResp := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/exams/"+examID+"/validate-paper-config", adminToken, `{}`, http.StatusOK)["result"].(map[string]any)
	if validateResp["valid"] != true {
		t.Fatalf("paper config should be valid: %#v", validateResp)
	}

	return memoryWorkflowExam{ExamID: examID, PaperID: paperID, QuestionID: questionID, RubricID: rubricID}
}

// 采集质量先通过，才领取 OCR Runtime 租约、回传低置信结果并生成答题片段。
func e2eCollectMemoryWorkflowAnswer(t *testing.T, fixture *e2eMemoryFixture, adminToken, examID string) memoryWorkflowAnswer {
	t.Helper()
	answerFileID := e2eUploadSyntheticPDF(t, fixture.router, adminToken, "synthetic-answer.pdf", "%PDF-1.4\n% synthetic answer sheet\n")
	submissionResp := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/exams/"+examID+"/submissions", adminToken, `{"student_id":"`+e2eStudentID+`","candidate_no":"SYN-001","source_type":"pdf_upload","expected_page_count":1}`, http.StatusCreated)["submission"].(map[string]any)
	submissionID := e2eString(t, submissionResp, "id")
	if submissionResp["status"] != "created" {
		t.Fatalf("submission should be created: %#v", submissionResp)
	}
	pageResp := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/submissions/"+submissionID+"/pages", adminToken, `{"file_asset_id":"`+answerFileID+`","page_no":1}`, http.StatusCreated)["page"].(map[string]any)
	pageID := e2eString(t, pageResp, "id")
	qualityResp := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/submissions/"+submissionID+"/quality-check", adminToken, `{}`, http.StatusOK)["result"].(map[string]any)
	if qualityResp["valid"] != true {
		t.Fatalf("submission collection integrity should pass: %#v", qualityResp)
	}
	if _, err := fixture.submissionStore.ApplyPageQualityResult(context.Background(), e2eTenantID, submission.ApplyPageQualityInput{
		SubmissionID:          submissionID,
		PageID:                pageID,
		LatestQualityRunID:    "quality-run-1",
		NormalizedFileAssetID: answerFileID,
		QualityStatus:         "passed",
	}); err != nil {
		t.Fatalf("apply synthetic page image quality result: %v", err)
	}
	readyResp := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/submissions/"+submissionID+"/status", adminToken, `{"status":"ready_for_ocr","expected_revision":1}`, http.StatusOK)["submission"].(map[string]any)
	if readyResp["status"] != "ready_for_ocr" {
		t.Fatalf("submission should be ready_for_ocr: %#v", readyResp)
	}

	ocrResp := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/submissions/"+submissionID+"/ocr-tasks", adminToken, `{"engine":"mock_ocr","engine_version":"synthetic-v1","min_confidence":0.8}`, http.StatusCreated)["task"].(map[string]any)
	ocrTaskID := e2eString(t, ocrResp, "id")
	if ocrResp["status"] != "queued" {
		t.Fatalf("ocr task should be queued: %#v", ocrResp)
	}
	runtimeClaim := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/internal/worker/tasks/claim", adminToken, `{"queue_name":"ocr","worker_service":"story041-memory-e2e","worker_instance_id":"story041-memory-e2e-1","limit":1,"lease_seconds":300}`, http.StatusOK)
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
	startedOCR := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/ocr-tasks/"+ocrTaskID+"/start", adminToken, `{}`, http.StatusOK)["task"].(map[string]any)
	if startedOCR["status"] != "processing" {
		t.Fatalf("ocr task should be processing: %#v", startedOCR)
	}
	answerText := "Synthetic answer: photosynthesis uses sunlight to help plants make energy."
	completedOCR := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/ocr-tasks/"+ocrTaskID+"/results", adminToken, `{"worker_id":"story041-memory-e2e-1","model_version":"mock-ocr-story041","config_hash":"story041-config","input_hash":"story041-input","duration_ms":12,"preprocess_profile":"story041-synthetic","runtime_task_id":"`+runtimeTaskID+`","runtime_lease_token":"`+runtimeLeaseToken+`","results":[{"submission_page_id":"`+pageID+`","text":"`+answerText+`","bbox":[0.1,0.2,0.6,0.2],"confidence":0.76,"source_image_file_id":"`+answerFileID+`"}]}`, http.StatusOK)["task"].(map[string]any)
	if completedOCR["status"] != "completed" || completedOCR["requires_human_review"] != true {
		t.Fatalf("ocr task should complete with low-confidence review requirement: %#v", completedOCR)
	}

	segmentResult := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/submissions/"+submissionID+"/segment-answers", adminToken, `{}`, http.StatusOK)["result"].(map[string]any)
	if segmentResult["valid"] != true {
		t.Fatalf("answer segmentation should be valid: %#v", segmentResult)
	}
	segments := segmentResult["segments"].([]any)
	if len(segments) != 1 {
		t.Fatalf("expected one segment, got %#v", segments)
	}
	segmentResp := segments[0].(map[string]any)
	segmentID := e2eString(t, segmentResp, "id")
	if segmentResp["status"] != "generated" {
		t.Fatalf("segment should be generated: %#v", segmentResp)
	}

	return memoryWorkflowAnswer{SubmissionID: submissionID, PageID: pageID, AnswerFileID: answerFileID, AnswerText: answerText, SegmentID: segmentID}
}

// 模拟模型与低置信证据不能自动发布；先保留未完成复核门禁，再由指定教师提交合法分数。
func e2eReviewMemoryWorkflowEvidence(t *testing.T, fixture *e2eMemoryFixture, actors memoryWorkflowActors, exam memoryWorkflowExam, answer memoryWorkflowAnswer) string {
	t.Helper()
	adminToken, teacherToken := actors.Admin, actors.Teacher
	examID, paperID, questionID, rubricID := exam.ExamID, exam.PaperID, exam.QuestionID, exam.RubricID
	submissionID, pageID, answerText, segmentID := answer.SubmissionID, answer.PageID, answer.AnswerText, answer.SegmentID
	question := paper.Question{
		ID:           questionID,
		TenantID:     e2eTenantID,
		ExamID:       examID,
		ExamPaperID:  paperID,
		QuestionNo:   "Q1",
		QuestionType: "short_answer",
		Score:        5,
		Stem:         "Synthetic prompt: explain one photosynthesis benefit.",
		AnswerArea:   map[string]any{"page": float64(1), "x": 0.1, "y": 0.2, "w": 0.6, "h": 0.2},
		Status:       "active",
	}
	rubric := paper.Rubric{
		ID:         rubricID,
		QuestionID: questionID,
		Version:    "v1",
		Status:     "approved",
		MaxScore:   5,
		Points: []paper.RubricPoint{
			{ID: "p1", Description: "synthetic point: mentions sunlight and plant energy", Score: 5, Required: true},
		},
	}
	confidence := 0.76
	fixture.gradingStore.AddContext(e2eTenantID, segmentID, question)
	answerResp := e2ePostJSON(t, fixture.router, http.MethodPut, "/api/v1/answer-segments/"+segmentID+"/answer", adminToken, `{"answer_text":"`+answerText+`","answer_payload":{"answer":"`+answerText+`"},"source":"ocr_text","confidence":0.76}`, http.StatusOK)["answer"].(map[string]any)
	if answerResp["source"] != "ocr_text" {
		t.Fatalf("answer should be recorded from ocr_text: %#v", answerResp)
	}

	fixture.subjectiveStore.AddContext(e2eTenantID, segmentID, subjective.Context{
		Question:        question,
		Rubric:          rubric,
		AnswerText:      answerText,
		AnswerImageRef:  map[string]any{"submission_page_id": pageID, "answer_segment_id": segmentID},
		OCRConfidence:   &confidence,
		AnswerCreatedAt: time.Now().UTC(),
	})
	aiGradeResp := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/answer-segments/"+segmentID+"/subjective-ai-grade", adminToken, `{"model_policy":{"model_version":"mock-llm-synthetic-v1","prompt_version":"story-041-synthetic","min_confidence":0.8}}`, http.StatusCreated)["grade"].(map[string]any)
	aiGradeID := e2eString(t, aiGradeResp, "id")
	if aiGradeResp["mock"] != true || aiGradeResp["needs_human_review"] != true || aiGradeResp["status"] != "succeeded" {
		t.Fatalf("subjective AI grade should be explicit mock and require review: %#v", aiGradeResp)
	}

	fixture.evidenceStore.AddContext(e2eTenantID, aiGradeID, evidence.Context{
		Grade: evidence.Grade{
			ID:               aiGradeID,
			TenantID:         e2eTenantID,
			AnswerSegmentID:  segmentID,
			QuestionID:       questionID,
			QuestionNo:       "Q1",
			QuestionType:     "short_answer",
			SuggestedScore:   e2eFloat(t, aiGradeResp, "suggested_score"),
			MaxScore:         e2eFloat(t, aiGradeResp, "max_score"),
			MatchedPoints:    []grading.PointResult{},
			MissingPoints:    []grading.PointResult{{Code: "p1", Label: "synthetic point: mentions sunlight and plant energy", Score: 5}},
			Evidence:         []grading.Evidence{{Type: "mock", Rule: "mock output; not real model evidence"}},
			RiskFlags:        []string{"mock_llm_output", "low_model_confidence"},
			NeedsHumanReview: true,
			Status:           "succeeded",
		},
		AnswerText:        answerText,
		OCRConfidence:     &confidence,
		AnswerSegmentBBox: []float64{0.1, 0.2, 0.6, 0.2},
		Rubric:            rubric,
	})
	evidenceJob := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/ai-grades/"+aiGradeID+"/verify-evidence", adminToken, `{}`, http.StatusCreated)["job"].(map[string]any)
	if evidenceJob["needs_human_review"] != true {
		t.Fatalf("evidence verification should require human review for mock/low-confidence grade: %#v", evidenceJob)
	}

	fixture.reviewStore.AddContext(e2eTenantID, segmentID, review.Context{
		ExamID:          examID,
		SubmissionID:    submissionID,
		AnswerSegmentID: segmentID,
		AnonymousCode:   "SYN-001",
		Question:        question,
		Rubric:          rubric,
		RawAnswer:       answerText,
		OCRText:         answerText,
		AISuggestion: map[string]any{
			"ai_grade_id":     aiGradeID,
			"suggested_score": 0,
			"mock":            true,
			"synthetic":       true,
		},
	})
	reviewTask := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/review-tasks", adminToken, `{"answer_segment_id":"`+segmentID+`","source":"evidence_verification_failed","priority":5}`, http.StatusCreated)["task"].(map[string]any)
	reviewTaskID := e2eString(t, reviewTask, "id")
	if reviewTask["status"] != "pending" {
		t.Fatalf("review task should be pending: %#v", reviewTask)
	}
	fixture.assertUnfinishedReviewCannotPublish(t, adminToken)

	assignedTask := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/review-tasks/"+reviewTaskID+"/assign", adminToken, `{"assigned_to":"`+e2eTeacherID+`","expected_revision":1}`, http.StatusOK)["task"].(map[string]any)
	if assignedTask["status"] != "assigned" || assignedTask["assigned_to"] != e2eTeacherID {
		t.Fatalf("review task should be assigned to teacher: %#v", assignedTask)
	}
	e2eExpectStatus(t, fixture.router, http.MethodPost, "/api/v1/review-tasks/"+reviewTaskID+"/submit", teacherToken, `{"expected_revision":2,"score":6,"rubric_selections":[{"point_id":"p1","score":6}],"comments":"synthetic over max"}`, http.StatusBadRequest)
	submittedReview := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/review-tasks/"+reviewTaskID+"/submit", teacherToken, `{"expected_revision":2,"score":4,"rubric_selections":[{"point_id":"p1","score":4}],"comments":"synthetic human review","reason":"manual review after mock AI"}`, http.StatusCreated)
	humanGrade := submittedReview["human_grade"].(map[string]any)
	if humanGrade["score"] != float64(4) || submittedReview["task"].(map[string]any)["status"] != "submitted" {
		t.Fatalf("human grade should be submitted with score 4: %#v", submittedReview)
	}

	return aiGradeID
}

// 人工成绩确认后才发布；随后验证学生自有成绩、同伴隔离、申诉调整及整条审计链。
func e2ePublishAndAppealMemoryWorkflow(t *testing.T, fixture *e2eMemoryFixture, actors memoryWorkflowActors, exam memoryWorkflowExam, answer memoryWorkflowAnswer, aiGradeID string) {
	t.Helper()
	adminToken, teacherToken, studentToken, otherStudentToken := actors.Admin, actors.Teacher, actors.Student, actors.OtherStudent
	examID, questionID := exam.ExamID, exam.QuestionID
	submissionID, answerText, segmentID := answer.SubmissionID, answer.AnswerText, answer.SegmentID
	fixture.scoreStore.AddSegment(score.SegmentSeed{ExamID: examID, SubmissionID: submissionID, StudentID: e2eStudentID, AnonymousCode: "SYN-001", AnswerSegmentID: segmentID, QuestionID: questionID, QuestionNo: "Q1", MaxScore: 5})
	fixture.scoreStore.AddHumanGrade(score.GradeSeed{AnswerSegmentID: segmentID, Score: 4, MaxScore: 5})
	finalized := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/exams/"+examID+"/finalize", adminToken, `{}`, http.StatusCreated)
	if finalized["status"] != "pending_confirmation" || e2eFloat(t, finalized, "created_finals") != 1 {
		t.Fatalf("finalize should create one pending final grade: %#v", finalized)
	}
	e2eExpectStatus(t, fixture.router, http.MethodPost, "/api/v1/exams/"+examID+"/publish", adminToken, `{"reason":"too early"}`, http.StatusConflict)

	confirmed := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/exams/"+examID+"/confirm-grades", adminToken, `{"reason":"synthetic checked by lead"}`, http.StatusOK)["grades"].([]any)
	if len(confirmed) != 1 || confirmed[0].(map[string]any)["status"] != "confirmed" {
		t.Fatalf("grades should be confirmed: %#v", confirmed)
	}
	published := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/exams/"+examID+"/publish", adminToken, `{"reason":"synthetic approved for release"}`, http.StatusOK)
	if published["status"] != "published" {
		t.Fatalf("publish should succeed after confirmation: %#v", published)
	}

	studentGrade := e2eGetJSON(t, fixture.router, "/api/v1/students/"+e2eStudentID+"/exams/"+examID+"/grade", studentToken, http.StatusOK)["grade"].(map[string]any)
	if studentGrade["total_score"] != float64(4) || studentGrade["status"] != "published" {
		t.Fatalf("student should see published grade: %#v", studentGrade)
	}
	e2eExpectStatus(t, fixture.router, http.MethodGet, "/api/v1/students/"+e2eStudentID+"/exams/"+examID+"/grade", otherStudentToken, "", http.StatusForbidden)

	items := studentGrade["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("published grade should include one final grade item: %#v", studentGrade)
	}
	finalGrade := items[0].(map[string]any)
	finalGradeID := e2eString(t, finalGrade, "id")
	fixture.appealStore.AddSubmissionGrade(appealpkg.SubmissionGradeSeed{
		ID:            "submission-grade-synthetic-1",
		ExamID:        examID,
		SubmissionID:  submissionID,
		StudentID:     e2eStudentID,
		AnonymousCode: "SYN-001",
		TotalScore:    4,
		MaxScore:      5,
		Status:        "published",
		Locked:        true,
	})
	fixture.appealStore.AddFinalGrade(appealpkg.FinalGradeSeed{
		ID:              finalGradeID,
		ExamID:          examID,
		SubmissionID:    submissionID,
		AnswerSegmentID: segmentID,
		QuestionID:      questionID,
		QuestionNo:      "Q1",
		Score:           4,
		MaxScore:        5,
		Status:          "locked",
		Locked:          true,
		RawAnswer:       answerText,
		OCRText:         answerText,
		AIGrades:        []map[string]any{{"id": aiGradeID, "mock": true, "synthetic": true, "suggested_score": 0}},
		HumanGrades:     []map[string]any{{"score": 4, "reviewer_id": e2eTeacherID}},
		Rubric:          map[string]any{"points": []any{"p1"}},
	})
	appealResp := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/appeals", studentToken, `{"exam_id":"`+examID+`","student_id":"`+e2eStudentID+`","target_type":"question","final_grade_id":"`+finalGradeID+`","reason":"Synthetic appeal: answer evidence should receive full credit"}`, http.StatusCreated)["appeal"].(map[string]any)
	appealID := e2eString(t, appealResp, "id")
	if appealResp["status"] != "submitted" {
		t.Fatalf("appeal should be submitted: %#v", appealResp)
	}
	reviewedAppeal := e2ePostJSON(t, fixture.router, http.MethodPost, "/api/v1/appeals/"+appealID+"/review", teacherToken, `{"status":"score_adjusted","reason":"Synthetic teacher adjustment after appeal","adjusted_score":5,"expected_revision":1}`, http.StatusOK)
	if reviewedAppeal["appeal"].(map[string]any)["status"] != "score_adjusted" || reviewedAppeal["score_adjustment"] == nil {
		t.Fatalf("teacher should process appeal with score adjustment: %#v", reviewedAppeal)
	}

	audits := e2eGetJSON(t, fixture.router, "/api/v1/audit-logs?limit=200", adminToken, http.StatusOK)["audit_logs"].([]any)
	e2eAssertAuditActions(t, audits, []string{
		"auth.login_succeeded",
		"org.student_created",
		"file.uploaded",
		"ocr.task_completed",
		"subjective.ai_grade_created",
		"evidence.checked",
		"review.human_grade_submitted",
		"score.published",
		"appeal.reviewed",
	})
}

func newE2EMemoryFixture(t *testing.T) *e2eMemoryFixture {
	t.Helper()
	authStore := e2eAuthStore(t)
	orgStore := org.NewMemoryStore()
	examStore := exam.NewMemoryStore()
	paperStore := paper.NewMemoryStore()
	fileStore := files.NewMemoryStore()
	objectStore := files.NewMemoryObjectStorage()
	submissionStore := submission.NewMemoryStore()
	ocrStore := ocrpkg.NewMemoryStore()
	segmentStore := segment.NewMemoryStore()
	gradingStore := grading.NewMemoryStore()
	subjectiveStore := subjective.NewMemoryStore()
	evidenceStore := evidence.NewMemoryStore()
	reviewStore := review.NewMemoryStore()
	scoreStore := score.NewMemoryStore()
	appealStore := appealpkg.NewMemoryStore()
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "api-gateway-e2e-test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
		Files: config.FileConfig{
			Bucket:            "edugrade-e2e-synthetic",
			MaxUploadBytes:    2 * 1024 * 1024,
			AllowedExtensions: []string{".pdf", ".png", ".jpg", ".jpeg", ".csv", ".docx"},
		},
		Security: config.SecurityConfig{MaxRequestBodyBytes: 2 * 1024 * 1024},
		Observability: config.ObservabilityConfig{
			SlowRequestThreshold: time.Second,
		},
	}
	router := NewMemoryRouter(cfg, logger.New(io.Discard, "error"), nil, objectStore, func(stores *ApplicationStores) {
		stores.Identity = IdentityStores{Auth: authStore, Org: orgStore}
		stores.Exam.Exam = examStore
		stores.Exam.Paper = paperStore
		stores.Exam.Files = fileStore
		stores.Exam.Submissions = submissionStore
		stores.Exam.Segments = segmentStore
		stores.Capture.OCR = ocrStore
		stores.Capture.OCRQueue = ocrpkg.NewMemoryQueue()
		stores.Grading.Grading = gradingStore
		stores.Grading.Subjective = subjectiveStore
		stores.Grading.Evidence = evidenceStore
		stores.Grading.Review = reviewStore
		stores.Release.Score = scoreStore
		stores.Release.Appeal = appealStore
	})
	return &e2eMemoryFixture{
		router:          router,
		authStore:       authStore,
		paperStore:      paperStore,
		submissionStore: submissionStore,
		gradingStore:    gradingStore,
		subjectiveStore: subjectiveStore,
		evidenceStore:   evidenceStore,
		reviewStore:     reviewStore,
		scoreStore:      scoreStore,
		appealStore:     appealStore,
	}
}

func (f *e2eMemoryFixture) assertUnfinishedReviewCannotPublish(t *testing.T, adminToken string) {
	t.Helper()
	incompleteExamID := "synthetic-incomplete-review-exam"
	f.scoreStore.AddReviewTask(score.TaskSeed{ExamID: incompleteExamID, Status: "assigned"})
	rec := e2eExpectStatus(t, f.router, http.MethodPost, "/api/v1/exams/"+incompleteExamID+"/publish", adminToken, `{"reason":"should fail"}`, http.StatusConflict)
	if !strings.Contains(rec.Body.String(), "unfinished_review_tasks") {
		t.Fatalf("publish with unfinished review should expose quality issue, got %s", rec.Body.String())
	}
}

func e2eAuthStore(t *testing.T) *auth.MemoryStore {
	t.Helper()
	hash, err := auth.HashPassword(e2ePassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	allPermissions := []string{
		"system:read", "tenant:manage", "org:manage", "student:import", "exam:manage", "file:manage",
		"submission:manage", "ocr:manage", "segment:manage", "orchestrator:manage", "grading:manage",
		"evidence:manage", "review:manage", "arbitration:manage", "score:manage", "student:grade:read",
		"appeal:create", "appeal:read", "appeal:manage", "audit:read", "audit:export", "report:read", "report:export",
	}
	store := auth.NewMemoryStore()
	for _, user := range []auth.User{
		{
			ID:          e2eAdminID,
			TenantID:    e2eTenantID,
			TenantCode:  e2eTenantCode,
			Username:    "e2e_admin",
			DisplayName: "Synthetic E2E Admin",
			Status:      "active",
			Roles:       []string{"tenant_admin"},
			Permissions: allPermissions,
			DataScope:   map[string]any{"scope": "tenant", "synthetic": true},
		},
		{
			ID:          e2eTeacherID,
			TenantID:    e2eTenantID,
			TenantCode:  e2eTenantCode,
			Username:    "e2e_teacher",
			DisplayName: "Synthetic E2E Teacher",
			Status:      "active",
			Roles:       []string{"teacher", "grader"},
			Permissions: []string{"review:manage", "review:work", "appeal:read", "appeal:manage"},
			DataScope:   map[string]any{"scope": "school", "synthetic": true},
		},
		{
			ID:          e2eStudentUserID,
			TenantID:    e2eTenantID,
			TenantCode:  e2eTenantCode,
			Username:    "e2e_student",
			DisplayName: "Synthetic E2E Student",
			Status:      "active",
			Roles:       []string{"student"},
			Permissions: []string{"student:grade:read", "appeal:create", "appeal:read"},
			DataScope:   map[string]any{"scope": "self", "student_id": e2eStudentID, "synthetic": true},
		},
		{
			ID:          e2eOtherUserID,
			TenantID:    e2eTenantID,
			TenantCode:  e2eTenantCode,
			Username:    "e2e_other_student",
			DisplayName: "Synthetic E2E Other Student",
			Status:      "active",
			Roles:       []string{"student"},
			Permissions: []string{"student:grade:read", "appeal:create", "appeal:read"},
			DataScope:   map[string]any{"scope": "self", "student_id": e2eOtherStudentID, "synthetic": true},
		},
		{
			ID:          e2eLimitedUserID,
			TenantID:    e2eTenantID,
			TenantCode:  e2eTenantCode,
			Username:    "e2e_limited",
			DisplayName: "Synthetic E2E Limited",
			Status:      "active",
			Roles:       []string{"observer"},
			Permissions: []string{"system:read"},
			DataScope:   map[string]any{"scope": "none", "synthetic": true},
		},
	} {
		store.AddUser(auth.UserWithPassword{User: user, PasswordHash: hash})
	}
	return store
}

func e2eLogin(t *testing.T, router http.Handler, username string) string {
	t.Helper()
	return e2eLoginWithTenant(t, router, e2eTenantCode, username, e2ePassword)
}

func e2eLoginWithTenant(t *testing.T, router http.Handler, tenantCode string, username string, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"tenant_code": tenantCode, "username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s expected 200, got %d %s", username, rec.Code, rec.Body.String())
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if response.AccessToken == "" {
		t.Fatal("login response missing access token")
	}
	return response.AccessToken
}

func e2ePostJSON(t *testing.T, router http.Handler, method string, path string, token string, body string, want int) map[string]any {
	t.Helper()
	rec := e2eExpectStatus(t, router, method, path, token, body, want)
	if rec.Body.Len() == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s %s response: %v; raw=%s", method, path, err, rec.Body.String())
	}
	return out
}

func e2eGetJSON(t *testing.T, router http.Handler, path string, token string, want int) map[string]any {
	t.Helper()
	return e2ePostJSON(t, router, http.MethodGet, path, token, "", want)
}

func e2eExpectStatus(t *testing.T, router http.Handler, method string, path string, token string, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s expected %d, got %d %s", method, path, want, rec.Code, rec.Body.String())
	}
	return rec
}

func e2eUploadSyntheticPDF(t *testing.T, router http.Handler, token string, filename string, content string) string {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := io.Copy(part, strings.NewReader(content)); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.WriteField("owner_type", "generic"); err != nil {
		t.Fatalf("write owner type: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload synthetic file expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	return e2eString(t, response["file"].(map[string]any), "id")
}

func e2eString(t *testing.T, item map[string]any, key string) string {
	t.Helper()
	value, ok := item[key].(string)
	if !ok || value == "" {
		t.Fatalf("missing string field %s in %#v", key, item)
	}
	return value
}

func e2eFloat(t *testing.T, item map[string]any, key string) float64 {
	t.Helper()
	value, ok := item[key].(float64)
	if !ok {
		t.Fatalf("missing numeric field %s in %#v", key, item)
	}
	return value
}

func e2eAssertAuditActions(t *testing.T, audits []any, actions []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, item := range audits {
		record, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("audit record has unexpected shape: %#v", item)
		}
		if action, ok := record["action"].(string); ok {
			seen[action] = true
		}
	}
	for _, action := range actions {
		if !seen[action] {
			t.Fatalf("missing audit action %s in %#v", action, seen)
		}
	}
}
