package review

import (
	"context"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

// ErrQualificationRequired 表示新提交未通过当前冻结题目的 R3 校准资格检查。
var ErrQualificationRequired = errors.New("current grader qualification is required")

type submissionQualificationKey struct{}

type submissionQualification struct {
	tenantID, taskID, reviewerID string
	examID, questionID           string
	snapshotID                   string
	riskTier                     assessment.RiskTier
	err                          error
}

// 资格服务还要读取校准和 Gold 版本，因此先在业务事务外检查，避免单连接池在持锁时再次借连接。
// 检查结果只带入新写入路径：已成功的命令及请求指纹冲突仍由存储先读取回执后裁决。
func prepareSubmissionQualification(ctx context.Context, store Store, gate GraderQualificationGate, tenantID, taskID, reviewerID string) context.Context {
	if gate == nil {
		return ctx
	}
	verdict := submissionQualification{tenantID: tenantID, taskID: taskID, reviewerID: reviewerID, err: ErrQualificationRequired}
	if contexts, ok := store.(TaskContextStore); ok {
		if current, err := contexts.GetTaskContext(ctx, tenantID, taskID); err == nil {
			verdict.examID, verdict.questionID = current.Task.ExamID, current.Task.QuestionID
			verdict.snapshotID, verdict.riskTier = current.QuestionSnapshot.ID, current.QuestionSnapshot.RiskTier
			verdict.err = nil
			if verdict.riskTier == assessment.RiskR3 {
				verdict.err = gate.RequireQualification(ctx, tenantID, verdict.examID, verdict.questionID, reviewerID)
			}
		}
	}
	return context.WithValue(ctx, submissionQualificationKey{}, verdict)
}

// 新成绩使用的冻结题目必须与请求开始时校验的身份和快照一致；读取失败或快照变化时关闭 R3 写入。
// 这是接收请求后、写分前的当前资格检查，不承诺资格失效与评分事务之间具有跨模块原子性。
func requireSubmissionQualification(ctx context.Context, tenantID, reviewerID string, task ReviewTask, taskContext Context) error {
	verdict, checked := ctx.Value(submissionQualificationKey{}).(submissionQualification)
	if !checked || taskContext.AssessmentSnapshot.RiskTier != assessment.RiskR3 {
		return nil
	}
	if verdict.err != nil || verdict.tenantID != tenantID || verdict.taskID != task.ID || verdict.reviewerID != reviewerID ||
		verdict.examID != task.ExamID || verdict.questionID != task.QuestionID ||
		verdict.snapshotID != taskContext.AssessmentSnapshot.ID || verdict.riskTier != taskContext.AssessmentSnapshot.RiskTier {
		return ErrQualificationRequired
	}
	return nil
}
