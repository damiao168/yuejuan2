// 合成答案按确定性客观题和主观复核分别记录；真实 AI 输出始终保留教师确认路径。
package main

import (
	"context"
	"fmt"
	"net/http"
)

func (run *candidateImport) recordAnswers(ctx context.Context, result *importResult, aiTargets *[]aiTarget) error {
	data, candidate, segmentByQuestion := run.data, run.candidate, run.segmentByQuestion
	for _, question := range data.Questions {
		answerData, ok := candidate.Answers[question.QuestionNo]
		if !ok || !answerData.Synthetic {
			return fmt.Errorf("missing governed synthetic answer %s/%s", candidate.CandidateNo, question.QuestionNo)
		}
		segmentID := segmentByQuestion[question.QuestionNo]
		if segmentID == "" {
			return fmt.Errorf("missing segment %s/%s", candidate.CandidateNo, question.QuestionNo)
		}
		if err := run.recordAnswer(ctx, question, answerData, segmentID, result, aiTargets); err != nil {
			return err
		}
	}
	return nil
}

func (run *candidateImport) recordAnswer(ctx context.Context, question questionDef, answerData candidateAnswer, segmentID string, result *importResult, aiTargets *[]aiTarget) error {
	client, db, candidate := run.client, run.db, run.candidate
	tenantID, adminID, graderID := run.users.tenantID, run.users.adminID, run.users.graderID
	source := "ocr_text"
	extractor := "ocr"
	if questionNumber(question.QuestionNo) <= 10 {
		source = "imported_answer"
		extractor = "omr"
	}
	answerResponse, recordErr := client.json(ctx, http.MethodPut, answerSegmentRoutePrefix+segmentID+"/answer", map[string]any{
		"answer_text": displayText(answerData.Text),
		"answer_payload": map[string]any{
			"answer":          answerData.Text,
			"synthetic":       true,
			"simulation_only": true,
			"extractor":       extractor,
			"dataset_id":      datasetID,
		},
		"source":     source,
		"confidence": answerData.Confidence,
	}, http.StatusOK)
	if recordErr != nil {
		return fmt.Errorf("record %s/%s answer: %w", candidate.CandidateNo, question.QuestionNo, recordErr)
	}
	answerID := nestedStringField(answerResponse, "answer", "id")
	candidateID, candidateErr := createAnswerCandidate(ctx, db, answerCandidateCreateInput{
		tenantID: tenantID, adminID: adminID, segmentID: segmentID, answerID: answerID,
		question: question, answerData: answerData, extractor: extractor,
	})
	if candidateErr != nil {
		return candidateErr
	}

	if questionNumber(question.QuestionNo) <= 16 {
		return run.gradeObjective(ctx, objectiveAnswer{
			question: question, answerData: answerData, segmentID: segmentID,
			candidateID: candidateID, extractor: extractor,
		}, result)
	}
	if _, recordErr = createReviewTask(ctx, client, segmentID, graderID, "subjective_default_review", 60); recordErr != nil {
		return fmt.Errorf("create subjective review task %s/%s: %w", candidate.CandidateNo, question.QuestionNo, recordErr)
	}
	result.AssignedReviewTaskCount++
	if run.candidateIndex == 0 {
		*aiTargets = append(*aiTargets, aiTarget{SegmentID: segmentID, Label: candidate.CandidateNo + "/" + question.QuestionNo})
	}
	return nil
}

type objectiveAnswer struct {
	question    questionDef
	answerData  candidateAnswer
	segmentID   string
	candidateID string
	extractor   string
}

func (run *candidateImport) gradeObjective(ctx context.Context, answer objectiveAnswer, result *importResult) error {
	client, db, candidate := run.client, run.db, run.candidate
	tenantID, adminID, graderID := run.users.tenantID, run.users.adminID, run.users.graderID
	examID, submissionID, questionIDs, ruleIDs := run.examID, run.submissionID, run.questions.questionIDs, run.questions.ruleIDs
	question, answerData, segmentID, candidateID, extractor := answer.question, answer.answerData, answer.segmentID, answer.candidateID, answer.extractor
	gradeResponse, gradeErr := client.json(ctx, http.MethodPost, answerSegmentRoutePrefix+segmentID+"/rule-grade", map[string]any{}, http.StatusCreated)
	if gradeErr != nil {
		return fmt.Errorf("rule grade %s/%s: %w", candidate.CandidateNo, question.QuestionNo, gradeErr)
	}
	grade := nestedMap(gradeResponse, "grade")
	// 自动确认仍须规则同时满足通过且无需复核；低置信度路线只创建教师任务。
	if answerData.Route == "auto_confirm" {
		if !boolField(grade, "auto_pass") || boolField(grade, "needs_human_review") {
			return fmt.Errorf("auto route rejected by grading guardrail for %s/%s", candidate.CandidateNo, question.QuestionNo)
		}
		if confirmErr := confirmObjectiveGrade(ctx, db, objectiveGradeConfirmation{
			tenantID: tenantID, adminID: adminID, examID: examID, submissionID: submissionID,
			questionID: questionIDs[question.QuestionNo], segmentID: segmentID,
			candidateID: candidateID, ruleID: ruleIDs[question.QuestionNo], grade: grade,
		}); confirmErr != nil {
			return confirmErr
		}
		result.AutoConfirmedGradeCount++
	} else {
		sourceCode := "ocr_low_confidence"
		if extractor == "omr" {
			sourceCode = "manual_sample"
		}
		if _, gradeErr = createReviewTask(ctx, client, segmentID, graderID, sourceCode, 80); gradeErr != nil {
			return fmt.Errorf("create review task %s/%s: %w", candidate.CandidateNo, question.QuestionNo, gradeErr)
		}
		result.AssignedReviewTaskCount++
	}
	return nil
}
