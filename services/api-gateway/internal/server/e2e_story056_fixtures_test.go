package server

import (
	"database/sql"
	"fmt"
	"net/http"
	"testing"
)

func e2eCreateStory056AcceptanceFixture(t *testing.T, db *sql.DB, router http.Handler, adminToken string, suffix string, candidateCount ...int) story056AcceptanceFixture {
	return e2eCreateStory056AcceptanceFixtureWithOMRProfile(t, db, router, adminToken, suffix, nil, candidateCount...)
}

func e2eCreateStory056AcceptanceFixtureWithOMRProfile(t *testing.T, db *sql.DB, router http.Handler, adminToken string, suffix string, omrProfile map[string]any, candidateCount ...int) story056AcceptanceFixture {
	return e2eCreateStory056AcceptanceFixtureWithLifecycle(t, db, router, adminToken, suffix, omrProfile, true, candidateCount...)
}

func e2eCreateStory056MutableTemplateFixture(t *testing.T, db *sql.DB, router http.Handler, adminToken string, suffix string, omrProfile map[string]any, candidateCount ...int) story056AcceptanceFixture {
	return e2eCreateStory056AcceptanceFixtureWithLifecycle(t, db, router, adminToken, suffix, omrProfile, false, candidateCount...)
}

func e2eCreateStory056AcceptanceFixtureWithLifecycle(t *testing.T, db *sql.DB, router http.Handler, adminToken string, suffix string, omrProfile map[string]any, activateExam bool, candidateCount ...int) story056AcceptanceFixture {
	t.Helper()
	adminID := e2eLookupUserID(t, db, "demo", "tenant_admin")
	var tenantID string
	if err := db.QueryRow(`SELECT id::text FROM tenant WHERE code='demo' AND deleted_at IS NULL`).Scan(&tenantID); err != nil {
		t.Fatalf("look up demo tenant: %v", err)
	}

	school := e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", adminToken, story056JSON(t, map[string]any{
		"name": "STORY-056 验收学校", "code": "story056-" + suffix,
	}), http.StatusCreated)["school"].(map[string]any)
	schoolID := e2eString(t, school, "id")
	// 验收使用学校当前学年的高一；不以固定旧学年绕过届别与年级对应规则。
	grade := e2ePostJSON(t, router, http.MethodPost, "/api/v1/grades", adminToken, story056JSON(t, map[string]any{
		"school_id": schoolID, "name": "高一", "level_no": 10,
	}), http.StatusCreated)["grade"].(map[string]any)
	class := e2ePostJSON(t, router, http.MethodPost, "/api/v1/classes", adminToken, story056JSON(t, map[string]any{
		"school_id": schoolID, "grade_id": e2eString(t, grade, "id"), "name": "高一(1)班", "code": "story056-" + suffix,
	}), http.StatusCreated)["class"].(map[string]any)
	classID := e2eString(t, class, "id")
	count := 1
	if len(candidateCount) > 0 {
		count = candidateCount[0]
	}
	if count < 1 {
		t.Fatal("candidate count must be positive")
	}
	// 学生名单必须先于准备确认建立；确认后新增学生会使名单快照失效，不能在答卷夹具中临时补人。
	studentIDs := make([]string, 0, count)
	for index := 0; index < count; index++ {
		studentNo := "S056-READY-" + suffix
		studentName := "验收学生"
		if index > 0 {
			studentNo += fmt.Sprintf("-%03d", index+1)
			studentName = fmt.Sprintf("验收学生%03d", index+1)
		}
		student := e2ePostJSON(t, router, http.MethodPost, "/api/v1/students", adminToken, story056JSON(t, map[string]any{
			"school_id": schoolID, "class_id": classID, "student_no": studentNo, "name": studentName,
			"password": "SyntheticStudent123!", "password_confirm": "SyntheticStudent123!",
		}), http.StatusCreated)["student"].(map[string]any)
		studentIDs = append(studentIDs, e2eString(t, student, "id"))
	}

	exam := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams", adminToken, story056JSON(t, map[string]any{
		"school_id": schoolID, "name": "高一物理能力测试", "subject": "physics", "exam_type": "unit_test", "total_score": 3,
		"grading_mode": "auto_objective_only", "appeal_enabled": true, "publish_policy": "manual_after_confirmation", "class_ids": []string{classID},
	}), http.StatusCreated)["exam"].(map[string]any)
	examID := e2eString(t, exam, "id")
	paperFileID := e2eUploadSyntheticPDF(t, router, adminToken, "story056-paper-"+suffix+".pdf", "%PDF-1.4\n% story056 acceptance paper "+suffix+"\n")
	paper := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/papers", adminToken, story056JSON(t, map[string]any{
		"file_asset_id": paperFileID,
	}), http.StatusCreated)["paper"].(map[string]any)
	paperID := e2eString(t, paper, "id")

	questionIDs := map[string]string{}
	questionIDs["single_choice"] = e2eCreateStory056Question(t, router, adminToken, story056QuestionSpec{ExamID: examID, PaperID: paperID, QuestionNo: "Q1", QuestionType: "single_choice", StandardAnswer: "A", SortOrder: 1})
	questionIDs["true_false"] = e2eCreateStory056Question(t, router, adminToken, story056QuestionSpec{ExamID: examID, PaperID: paperID, QuestionNo: "Q2", QuestionType: "true_false", StandardAnswer: true, SortOrder: 2})
	questionIDs["multiple_choice"] = e2eCreateStory056Question(t, router, adminToken, story056QuestionSpec{ExamID: examID, PaperID: paperID, QuestionNo: "Q3", QuestionType: "multiple_choice", StandardAnswer: []string{"A", "C"}, SortOrder: 3})

	for questionType, questionID := range questionIDs {
		config := map[string]any{}
		if questionType == "multiple_choice" {
			config["allow_partial"] = true
			// 本场每题 1 分、两个正确选项；少选必须明确每项分值，不能依赖历史比例缺省。
			config["score_per_correct_option"] = 0.5
		}
		rule := e2ePostJSON(t, router, http.MethodPost, "/api/v1/questions/"+questionID+"/scoring-rules", adminToken, story056JSON(t, map[string]any{
			"rule_type": questionType, "config": config,
		}), http.StatusCreated)["scoring_rule"].(map[string]any)
		e2ePostJSON(t, router, http.MethodPost, "/api/v1/scoring-rules/"+e2eString(t, rule, "id")+"/publish", adminToken, `{}`, http.StatusOK)
	}

	layout := map[string]any{"pages": []any{map[string]any{
		"page_no": 1, "width": 1000, "height": 1400,
		"registration_marks": []any{}, "identity_regions": []any{},
		"question_regions": []any{
			e2eStory056QuestionRegion(questionIDs["single_choice"], 0.10, e2eStory056Options("A", "B", "C", "D")),
			e2eStory056QuestionRegion(questionIDs["true_false"], 0.35, e2eStory056Options("true", "false")),
			e2eStory056QuestionRegion(questionIDs["multiple_choice"], 0.60, e2eStory056Options("A", "B", "C", "D")),
		},
	}}}
	if omrProfile != nil {
		layout["omr_profile"] = omrProfile
	}
	template := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/answer-sheet-templates", adminToken, story056JSON(t, map[string]any{
		"exam_paper_id": paperID, "name": "高一综合能力测试答题卡", "page_count": 1, "layout": layout,
	}), http.StatusCreated)["template"].(map[string]any)
	templateID := e2eString(t, template, "id")
	locked := e2ePostJSON(t, router, http.MethodPost, "/api/v1/answer-sheet-templates/"+templateID+"/lock", adminToken, story056JSON(t, map[string]any{
		"expected_revision": template["revision"],
	}), http.StatusOK)["template"].(map[string]any)
	if activateExam {
		e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/readiness/confirm", adminToken, `{}`, http.StatusOK)
		e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/start-collection", adminToken, `{}`, http.StatusOK)
	}

	return story056AcceptanceFixture{
		TenantID: tenantID, AdminID: adminID, SchoolID: schoolID, ClassID: classID, StudentIDs: studentIDs, ExamID: examID,
		PaperFileAssetID: paperFileID, TemplateID: templateID, TemplateContentHash: e2eString(t, locked, "content_hash"), QuestionIDs: questionIDs,
	}
}

// 题目夹具把试卷归属、题型、标准答案与排序作为一份输入，避免相邻字符串参数传反。
type story056QuestionSpec struct {
	ExamID, PaperID, QuestionNo, QuestionType string
	StandardAnswer                            any
	SortOrder                                 int
}

func e2eCreateStory056Question(t *testing.T, router http.Handler, adminToken string, spec story056QuestionSpec) string {
	t.Helper()
	examID, paperID, questionNo, questionType, standardAnswer, sortOrder := spec.ExamID, spec.PaperID, spec.QuestionNo, spec.QuestionType, spec.StandardAnswer, spec.SortOrder
	question := e2ePostJSON(t, router, http.MethodPost, "/api/v1/exams/"+examID+"/questions", adminToken, story056JSON(t, map[string]any{
		"exam_paper_id": paperID, "question_no": questionNo, "question_type": questionType, "score": 1,
		"stem": "STORY-056 " + questionNo, "knowledge_points": []string{"story056"}, "answer_area": map[string]any{}, "sort_order": sortOrder,
		"answer_key": map[string]any{"standard_answer": standardAnswer, "equivalent_answers": []any{}, "tolerance": map[string]any{}},
	}), http.StatusCreated)["question"].(map[string]any)
	return e2eString(t, question, "id")
}

func e2eStory056QuestionRegion(questionID string, y float64, options []any) map[string]any {
	return map[string]any{"question_id": questionID, "x": 0.10, "y": y, "width": 0.70, "height": 0.16, "option_regions": options}
}

func e2eStory056Options(labels ...string) []any {
	options := make([]any, 0, len(labels))
	for index, label := range labels {
		options = append(options, map[string]any{
			"label": label, "x": 0.12 + float64(index)*0.14, "y": 0.02, "width": 0.08, "height": 0.08,
		})
	}
	return options
}
