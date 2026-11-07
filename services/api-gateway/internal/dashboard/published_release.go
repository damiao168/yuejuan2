package dashboard

import (
	"context"

	"edugrade-enterprise/services/api-gateway/internal/exam"
)

// publishedExamReader 读取学生端当前生效的正式成绩版本。学生端发布不修改 exam.status，
// 只有旧发布接口会把状态写成 published，所以首页必须另读发布记录。
type publishedExamReader interface {
	PublishedExamIDs(context.Context, string, []string) (map[string]bool, error)
}

// applyStudentReleaseStatus 把学生端已有正式成绩的考试就地改记为 published，使其不再计入进行中、
// 待发布和各阶段计数，也不出现在 active_exams。已是 published 或 archived 的考试不再查询。
// 仓库未实现读取接口（内存仓库）时沿用考试状态；读取失败时也沿用并告警，计数可能偏多但不会漏掉未发布考试。
func (s *Service) applyStudentReleaseStatus(ctx context.Context, tenantID string, exams []exam.Exam, result *Summary) {
	reader, ok := s.deps.Exams.(publishedExamReader)
	if !ok {
		return
	}
	examIDs := make([]string, 0, len(exams))
	for _, item := range exams {
		if item.Status != "published" && item.Status != "archived" {
			examIDs = append(examIDs, item.ID)
		}
	}
	if len(examIDs) == 0 {
		return
	}
	published, err := reader.PublishedExamIDs(ctx, tenantID, examIDs)
	if err != nil {
		result.Warnings = append(result.Warnings, "成绩发布状态暂时无法确认")
		return
	}
	for index := range exams {
		if published[exams[index].ID] {
			exams[index].Status = "published"
		}
	}
}
