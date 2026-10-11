package main

import (
	"context"
	"database/sql"
)

// 旧源码覆盖验证只替换此文件中的签名适配；公共测试正文和断言保持同一份字节。
func runBatch9Dataset(ctx context.Context, cfg config, data dataset, client *apiClient, db *sql.DB) (importResult, error) {
	return importDataset(ctx, cfg, data, client, db, importUsers{
		tenantID: "tenant", adminID: "admin", graderID: "grader",
	})
}

func createBatch9Registration(ctx context.Context, db *sql.DB, page pageRecord) (string, error) {
	return createRegistration(ctx, db, registrationCreateInput{
		tenantID: "tenant", adminID: "admin", captureBatchID: "batch",
		submissionID: "submission", page: page, templateID: "template", templateHash: "template-hash", candidateNo: "C001",
	})
}

func completeBatch9Registration(ctx context.Context, db *sql.DB, asset map[string]any) error {
	return completeRegistration(ctx, db, registrationCompleteInput{
		tenantID: "tenant", submissionID: "submission", submissionPageID: "page",
		registrationID: "registration", templateID: "template", templateHash: "template-hash", registered: asset,
	})
}

func createBatch9AnswerCandidate(ctx context.Context, db *sql.DB, answer candidateAnswer, extractor string) (string, error) {
	return createAnswerCandidate(ctx, db, answerCandidateCreateInput{
		tenantID: "tenant", adminID: "admin", segmentID: "segment", answerID: "answer",
		question: questionDef{QuestionNo: "Q11"}, answerData: answer, extractor: extractor,
	})
}

func confirmBatch9ObjectiveGrade(ctx context.Context, db *sql.DB, grade map[string]any) error {
	return confirmObjectiveGrade(ctx, db, objectiveGradeConfirmation{
		tenantID: "tenant", adminID: "admin", examID: "exam", submissionID: "submission",
		questionID: "question", segmentID: "segment", candidateID: "candidate", ruleID: "rule", grade: grade,
	})
}
