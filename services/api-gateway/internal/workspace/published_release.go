package workspace

import (
	"context"

	"edugrade-enterprise/services/api-gateway/internal/exam"
)

// publishedExamReader 读取学生端当前生效的正式成绩版本。学生端发布不修改 exam.status，
// 只有旧发布接口会把状态写成 published，所以工作区必须另读发布记录。
type publishedExamReader interface {
	PublishedExamIDs(context.Context, string, []string) (map[string]bool, error)
}

// applyStudentRelease 在学生端已有正式成绩时把工作区阶段改为已发布。
// 发布门禁不检查考试阶段，复核或阅卷阶段也可能已经发布，所以除归档外的状态都要核对。
// 旧版归档状态优先展示；读取失败时保留原状态并告警，不能据此断定成绩尚未发布。
func (s *Service) applyStudentRelease(ctx context.Context, item exam.Exam, result *Projection) {
	if item.Status == "published" || item.Status == "archived" {
		return
	}
	reader, ok := s.deps.Exams.(publishedExamReader)
	if !ok {
		// 内存仓库没有学生端发布记录，只能沿用考试状态。
		return
	}
	published, err := reader.PublishedExamIDs(ctx, item.TenantID, []string{item.ID})
	if err != nil {
		result.releaseUnknown = true
		result.Warnings = append(result.Warnings, unavailableNotice("score_release_unavailable", "成绩发布状态暂不可用"))
		return
	}
	if published[item.ID] {
		result.ExamStatus = "published"
		result.Stage = stageForStatus(result.ExamStatus)
		result.Stages = buildStages(item.ID, result.ExamStatus)
	}
}
