// 仅给已经导入的仿真考试补充受治理 AI 建议；不隐式创建考试或更换冻结题目。
package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
)

func addExistingAISuggestions(
	ctx context.Context,
	cfg config,
	client *apiClient,
	db *sql.DB,
	tenantID string,
	examID string,
) (importResult, error) {
	var result importResult
	if err := readJSON(cfg.resultPath, &result); err != nil {
		return result, fmt.Errorf("read existing import result: %w", err)
	}
	rows, err := db.QueryContext(ctx, `
SELECT seg.id::text, sub.candidate_no, q.question_no
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND sub.candidate_no='SIM-FJ2024-001'
  AND q.question_no IN ('Q17','Q18','Q19') AND seg.deleted_at IS NULL
ORDER BY q.sort_order
`, tenantID, examID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	type target struct {
		segmentID   string
		candidateNo string
		questionNo  string
	}
	targets := []target{}
	for rows.Next() {
		var item target
		if err = rows.Scan(&item.segmentID, &item.candidateNo, &item.questionNo); err != nil {
			return result, err
		}
		targets = append(targets, item)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	if len(targets) != 3 {
		return result, fmt.Errorf("found %d subjective AI targets, want 3", len(targets))
	}

	existingIDs := map[string]bool{}
	for _, id := range result.SubjectiveAISuggestionIDs {
		existingIDs[id] = true
	}
	created := 0
	for _, item := range targets {
		if created >= cfg.aiLimit {
			break
		}
		var priorID string
		_ = db.QueryRowContext(ctx, `
SELECT id::text FROM ai_grade
WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid
  AND grader_type='llm_subjective' AND status='succeeded' AND mock=false AND deleted_at IS NULL
ORDER BY created_at DESC LIMIT 1
`, tenantID, item.segmentID).Scan(&priorID)
		if priorID != "" {
			if !existingIDs[priorID] {
				result.SubjectiveAISuggestionIDs = append(result.SubjectiveAISuggestionIDs, priorID)
				existingIDs[priorID] = true
			}
			continue
		}
		response, gradeErr := client.json(ctx, http.MethodPost, answerSegmentRoutePrefix+item.segmentID+"/subjective-ai-grade", map[string]any{
			"model_policy": map[string]any{},
		}, http.StatusCreated)
		if gradeErr != nil {
			return result, fmt.Errorf("%s/%s: %w", item.candidateNo, item.questionNo, gradeErr)
		}
		grade := nestedMap(response, "grade")
		if stringField(grade, "status") != "succeeded" {
			return result, fmt.Errorf("%s/%s failed: %s", item.candidateNo, item.questionNo, stringField(grade, "failure_reason"))
		}
		id := stringField(grade, "id")
		result.SubjectiveAISuggestionIDs = append(result.SubjectiveAISuggestionIDs, id)
		existingIDs[id] = true
		created++
	}
	if result.AutomationDisclosure == nil {
		result.AutomationDisclosure = map[string]any{}
	}
	result.AutomationDisclosure["subjective"] = fmt.Sprintf(
		"首份答题卡已有%d条真实受治理智能体建议；所有主观题仍分配给教师确认。",
		len(result.SubjectiveAISuggestionIDs),
	)
	return result, nil
}
