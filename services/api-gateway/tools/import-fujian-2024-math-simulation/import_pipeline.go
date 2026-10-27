// 首次导入的顺序固定为名册、试卷题目、模板锁定、准备度、采集、逐考生处理和 AI 建议。
// 合成 OCR 真值只验证流程分流，结果说明不能把它称为真实 OCR 精度。
package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"
)

type importResult struct {
	DatasetID                 string            `json:"dataset_id"`
	ExamID                    string            `json:"exam_id"`
	ExamName                  string            `json:"exam_name"`
	ExamStatus                string            `json:"exam_status"`
	TeacherUsername           string            `json:"teacher_username"`
	TeacherUserID             string            `json:"teacher_user_id"`
	CandidateCount            int               `json:"candidate_count"`
	PageCount                 int               `json:"page_count"`
	QuestionCount             int               `json:"question_count"`
	AnswerSegmentCount        int               `json:"answer_segment_count"`
	CompletedOCRTaskCount     int               `json:"completed_ocr_task_count"`
	SeededOCRResultCount      int               `json:"seeded_ocr_result_count"`
	AutoConfirmedGradeCount   int               `json:"auto_confirmed_grade_count"`
	AssignedReviewTaskCount   int               `json:"assigned_review_task_count"`
	SubjectiveAISuggestionIDs []string          `json:"subjective_ai_suggestion_ids"`
	SourceAssetIDs            map[string]string `json:"source_asset_ids"`
	Synthetic                 bool              `json:"synthetic"`
	SimulationOnly            bool              `json:"simulation_only"`
	TrainingAllowed           bool              `json:"training_allowed"`
	ContainsRealStudentID     bool              `json:"contains_real_student_identity"`
	AutomationDisclosure      map[string]any    `json:"automation_disclosure"`
	CreatedAt                 string            `json:"created_at"`
}

type importUsers struct {
	tenantID string
	adminID  string
	graderID string
}

type importRoster struct {
	schoolID   string
	classID    string
	studentIDs map[string]string
}

type importQuestionBindings struct {
	questionIDs map[string]string
	ruleIDs     map[string]string
	rubricIDs   map[string]string
}

type aiTarget struct {
	SegmentID string
	Label     string
}

func importDataset(
	ctx context.Context,
	cfg config,
	data dataset,
	client *apiClient,
	db *sql.DB,
	users importUsers,
) (importResult, error) {
	tenantID, adminID, graderID := users.tenantID, users.adminID, users.graderID
	roster, err := createImportRoster(ctx, client, data.Candidates)
	if err != nil {
		return importResult{}, err
	}
	schoolID, classID := roster.schoolID, roster.classID
	examResponse, err := client.json(ctx, http.MethodPost, "/api/v1/exams", map[string]any{
		"school_id":      schoolID,
		"name":           examName,
		"subject":        "math",
		"exam_type":      "formal_exam",
		"total_score":    88,
		"grading_mode":   "ai_assisted",
		"appeal_enabled": true,
		"publish_policy": "after_admin_approval",
		"class_ids":      []string{classID},
	}, http.StatusCreated)
	if err != nil {
		return importResult{}, fmt.Errorf("create exam: %w", err)
	}
	examID := nestedStringField(examResponse, "exam", "id")
	paperID, sourceAssets, err := createImportPaperAssets(ctx, cfg, data, client, examID, schoolID)
	if err != nil {
		return importResult{}, err
	}
	baseRegions := data.Regions[data.Candidates[0].CandidateNo]
	questions, err := createImportQuestions(ctx, client, examID, paperID, data.Questions, baseRegions)
	if err != nil {
		return importResult{}, err
	}
	questionIDs := questions.questionIDs

	layout := buildTemplateLayout(questionIDs, baseRegions)
	templateResponse, err := client.json(ctx, http.MethodPost, examRoutePrefix+examID+"/answer-sheet-templates", map[string]any{
		"exam_paper_id": paperID,
		"name":          "福建中考数学匿名仿真双页答题卡 v1",
		"page_count":    2,
		"layout":        layout,
	}, http.StatusCreated)
	if err != nil {
		return importResult{}, fmt.Errorf("create answer sheet template: %w", err)
	}
	templateID := nestedStringField(templateResponse, "template", "id")
	lockedResponse, err := client.json(ctx, http.MethodPost, "/api/v1/answer-sheet-templates/"+templateID+"/lock", map[string]any{}, http.StatusOK)
	if err != nil {
		return importResult{}, fmt.Errorf("lock answer sheet template: %w", err)
	}
	templateHash := nestedStringField(lockedResponse, "template", "content_hash")
	if err = addAnswerAreaCompatibilityAliases(ctx, db, tenantID, examID); err != nil {
		return importResult{}, err
	}
	if _, err = client.json(ctx, http.MethodPost, examRoutePrefix+examID+"/readiness/confirm", map[string]any{}, http.StatusOK); err != nil {
		return importResult{}, fmt.Errorf("confirm exam readiness: %w", err)
	}
	if _, err = client.json(ctx, http.MethodPost, examRoutePrefix+examID+"/start-collection", map[string]any{}, http.StatusOK); err != nil {
		return importResult{}, fmt.Errorf("start collection: %w", err)
	}

	captureBatchID, err := createCaptureBatch(ctx, db, tenantID, examID, adminID)
	if err != nil {
		return importResult{}, err
	}

	result := importResult{
		DatasetID:                 datasetID,
		ExamID:                    examID,
		ExamName:                  examName,
		ExamStatus:                "collecting",
		TeacherUsername:           cfg.graderUser,
		TeacherUserID:             graderID,
		CandidateCount:            len(data.Candidates),
		PageCount:                 len(data.Candidates) * 2,
		QuestionCount:             len(data.Questions),
		SourceAssetIDs:            sourceAssets,
		Synthetic:                 true,
		SimulationOnly:            true,
		TrainingAllowed:           false,
		ContainsRealStudentID:     false,
		SubjectiveAISuggestionIDs: []string{},
		CreatedAt:                 time.Now().Format(time.RFC3339),
	}
	aiTargets := []aiTarget{}
	ocrStore := ocrpkg.NewPostgresStore(db)
	for candidateIndex, candidate := range data.Candidates {
		candidateRun := candidateImport{
			cfg: cfg, data: data, client: client, db: db, users: users, roster: roster,
			questions: questions, examID: examID, captureBatchID: captureBatchID,
			templateID: templateID, templateHash: templateHash, ocrStore: ocrStore,
			candidate: candidate, candidateIndex: candidateIndex,
		}
		if err = candidateRun.run(ctx, &result, &aiTargets); err != nil {
			return importResult{}, err
		}
	}
	if err = createImportAISuggestions(ctx, cfg, client, aiTargets, &result); err != nil {
		return importResult{}, err
	}
	result.AutomationDisclosure = map[string]any{
		"objective":  "高置信度选择题/填空题已通过确定性规则形成 confirmed question_grade。",
		"ocr":        "4个双页OCR任务已完成，但文本由 simulation-importer 按标注真值写入；这验证流程分流，不等同于验证真实OCR模型精度。",
		"subjective": "首份答题卡的解答题调用真实受治理智能体生成建议；所有主观题仍分配给教师确认。",
		"privacy":    "候选人、字迹、条码与扫描噪声均为匿名仿真，不含真实未成年人身份。",
	}
	return result, nil
}

func createImportRoster(ctx context.Context, client *apiClient, candidates []candidateDef) (importRoster, error) {
	schoolResponse, err := client.json(ctx, http.MethodPost, "/api/v1/schools", map[string]any{
		"name": "福建中考数学匿名仿真验证学校",
		"code": "sim-fj2024-math-v1",
	}, http.StatusCreated)
	if err != nil {
		return importRoster{}, fmt.Errorf("create school: %w", err)
	}
	schoolID := nestedStringField(schoolResponse, "school", "id")
	gradeResponse, err := client.json(ctx, http.MethodPost, "/api/v1/grades", map[string]any{
		"school_id":     schoolID,
		"name":          "九年级（匿名仿真）",
		"level_no":      9,
		"academic_year": "2024",
	}, http.StatusCreated)
	if err != nil {
		return importRoster{}, fmt.Errorf("create grade: %w", err)
	}
	classResponse, err := client.json(ctx, http.MethodPost, "/api/v1/classes", map[string]any{
		"school_id": schoolID,
		"grade_id":  nestedStringField(gradeResponse, "grade", "id"),
		"name":      "中考数学流程验证班",
		"code":      "sim-fj2024-g9",
	}, http.StatusCreated)
	if err != nil {
		return importRoster{}, fmt.Errorf("create class: %w", err)
	}
	classID := nestedStringField(classResponse, "class", "id")
	studentIDs := map[string]string{}
	for index, candidate := range candidates {
		studentResponse, studentErr := client.json(ctx, http.MethodPost, "/api/v1/students", map[string]any{
			"school_id":  schoolID,
			"class_id":   classID,
			"student_no": candidate.CandidateNo,
			"name":       fmt.Sprintf("匿名仿真考生%03d", index+1),
		}, http.StatusCreated)
		if studentErr != nil {
			return importRoster{}, fmt.Errorf("create synthetic student %s: %w", candidate.CandidateNo, studentErr)
		}
		studentIDs[candidate.CandidateNo] = nestedStringField(studentResponse, "student", "id")
	}
	return importRoster{schoolID: schoolID, classID: classID, studentIDs: studentIDs}, nil
}

func createImportPaperAssets(ctx context.Context, cfg config, data dataset, client *apiClient, examID, schoolID string) (string, map[string]string, error) {
	blankPDF := fileFromManifest(cfg.datasetDir, data.Manifest.Files, "blank_answer_sheet")
	paperAsset, err := client.upload(ctx, blankPDF, map[string]string{
		"owner_type": "exam", "owner_id": examID, "exam_id": examID, "school_id": schoolID,
	})
	if err != nil {
		return "", nil, fmt.Errorf("upload blank answer sheet: %w", err)
	}
	paperResponse, err := client.json(ctx, http.MethodPost, examRoutePrefix+examID+"/papers", map[string]any{
		"file_asset_id": stringField(paperAsset, "id"),
	}, http.StatusCreated)
	if err != nil {
		return "", nil, fmt.Errorf("create exam paper: %w", err)
	}
	paperID := nestedStringField(paperResponse, "paper", "id")

	sourceAssets := map[string]string{"blank_answer_sheet": stringField(paperAsset, "id")}
	for _, item := range []struct {
		key string
		out string
	}{
		{key: "official_questions", out: "official_questions"},
		{key: "official_answers", out: "official_answers"},
	} {
		asset, uploadErr := client.upload(ctx, fileFromManifest(cfg.datasetDir, data.Manifest.Files, item.key), map[string]string{
			"owner_type": "import", "owner_id": examID, "exam_id": examID, "school_id": schoolID,
		})
		if uploadErr != nil {
			return "", nil, fmt.Errorf("upload %s: %w", item.key, uploadErr)
		}
		sourceAssets[item.out] = stringField(asset, "id")
	}

	return paperID, sourceAssets, nil
}

func createImportQuestions(ctx context.Context, client *apiClient, examID, paperID string, definitions []questionDef, baseRegions map[string]region) (importQuestionBindings, error) {
	bindings := importQuestionBindings{questionIDs: map[string]string{}, ruleIDs: map[string]string{}, rubricIDs: map[string]string{}}
	for index, question := range definitions {
		itemRegion, ok := baseRegions[question.QuestionNo]
		if !ok {
			return importQuestionBindings{}, fmt.Errorf("missing region for %s", question.QuestionNo)
		}
		pageNo := 1
		if questionNumber(question.QuestionNo) >= 17 {
			pageNo = 2
		}
		response, createErr := client.json(ctx, http.MethodPost, examRoutePrefix+examID+"/questions", map[string]any{
			"exam_paper_id":    paperID,
			"question_no":      question.QuestionNo,
			"question_type":    question.QuestionType,
			"score":            question.Score,
			"stem":             question.Stem,
			"knowledge_points": []string{"2024福建中考数学", "真实流程仿真"},
			"answer_area": map[string]any{
				"page": pageNo,
				"x":    itemRegion.X, "y": itemRegion.Y, "w": itemRegion.Width, "h": itemRegion.Height,
			},
			"sort_order": index + 1,
			"answer_key": map[string]any{
				"standard_answer":    question.StandardAnswer,
				"equivalent_answers": []any{},
				"tolerance":          map[string]any{},
			},
		}, http.StatusCreated)
		if createErr != nil {
			return importQuestionBindings{}, fmt.Errorf("create %s: %w", question.QuestionNo, createErr)
		}
		questionID := nestedStringField(response, "question", "id")
		bindings.questionIDs[question.QuestionNo] = questionID
		ruleID, rubricID, err := createImportScoringDefinition(ctx, client, question, index, questionID)
		if err != nil {
			return importQuestionBindings{}, err
		}
		if index < 16 {
			bindings.ruleIDs[question.QuestionNo] = ruleID
		} else {
			bindings.rubricIDs[question.QuestionNo] = rubricID
		}
	}
	return bindings, nil
}

func createImportScoringDefinition(ctx context.Context, client *apiClient, question questionDef, index int, questionID string) (string, string, error) {
	// 评分定义按数据集顺序划分前 16 题；不能改为按题号判断。
	if index < 16 {
		ruleResponse, ruleErr := client.json(ctx, http.MethodPost, "/api/v1/questions/"+questionID+"/scoring-rules", map[string]any{
			"rule_type": question.QuestionType,
			"config":    map[string]any{},
		}, http.StatusCreated)
		if ruleErr != nil {
			return "", "", fmt.Errorf("create rule for %s: %w", question.QuestionNo, ruleErr)
		}
		ruleID := nestedStringField(ruleResponse, "scoring_rule", "id")
		if _, ruleErr = client.json(ctx, http.MethodPost, "/api/v1/scoring-rules/"+ruleID+"/publish", map[string]any{}, http.StatusOK); ruleErr != nil {
			return "", "", fmt.Errorf("publish rule for %s: %w", question.QuestionNo, ruleErr)
		}
		return ruleID, "", nil
	}
	rubric := rubricFor(question.QuestionNo)
	rubricResponse, rubricErr := client.json(ctx, http.MethodPost, "/api/v1/questions/"+questionID+"/rubric", rubric, http.StatusCreated)
	if rubricErr != nil {
		return "", "", fmt.Errorf("create rubric for %s: %w", question.QuestionNo, rubricErr)
	}
	return "", nestedStringField(rubricResponse, "rubric", "id"), nil
}

func createImportAISuggestions(ctx context.Context, cfg config, client *apiClient, aiTargets []aiTarget, result *importResult) error {
	for index, target := range aiTargets {
		if index >= cfg.aiLimit {
			break
		}
		response, aiErr := client.json(ctx, http.MethodPost, answerSegmentRoutePrefix+target.SegmentID+"/subjective-ai-grade", map[string]any{
			"model_policy": map[string]any{},
		}, http.StatusCreated)
		if aiErr != nil {
			return fmt.Errorf("create governed AI suggestion for %s: %w", target.Label, aiErr)
		}
		grade := nestedMap(response, "grade")
		if stringField(grade, "status") != "succeeded" {
			return fmt.Errorf("governed AI suggestion failed for %s: %s", target.Label, stringField(grade, "failure_reason"))
		}
		result.SubjectiveAISuggestionIDs = append(result.SubjectiveAISuggestionIDs, stringField(grade, "id"))
	}

	return nil
}
