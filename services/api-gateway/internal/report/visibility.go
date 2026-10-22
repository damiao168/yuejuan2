package report

import (
	"context"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// studentReportAudience 只把发布可见性应用于学生查看；report:read 的管理查看仍保留逐题分析。
// 没有 HTTP 身份的调用属于可信内部报表任务，权限及本人边界由 Handler 和路由负责。
func studentReportAudience(ctx context.Context, data dataset) dataset {
	user, authenticated := auth.UserFromContext(ctx)
	if !authenticated || hasPermission(user, "report:read") {
		data.ShowQuestionScores, data.ShowKnowledgeAnalysis, data.ShowFeedback = true, true, true
	}
	return data
}
