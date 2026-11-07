package dashboard

import (
	"context"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// AppealStatistics 只统计持有 appeal:manage 的管理者要动手的成绩复核申请（已发布成绩的题目申诉）。
type AppealStatistics struct {
	// PendingAssignmentCount 为 status=submitted，等待分派复核教师。
	PendingAssignmentCount int `json:"pending_assignment_count"`
	// PendingDecisionCount 为 status=under_review 且复核教师已提交意见，等待管理员裁决。
	PendingDecisionCount int `json:"pending_decision_count"`
}

// AppealStatisticsStore 的统计范围必须与教职工申诉管理列表一致，首页数量不能多于 /appeals 能看到的记录。
type AppealStatisticsStore interface {
	DashboardAppealStatistics(ctx context.Context, tenantID string, scope auth.AccessScope) (AppealStatistics, error)
}

// applyAppealStatistics 只为持有 appeal:manage 的用户读取；没有权限时省略字段而不是写 0。
// 申诉发生在成绩发布之后，所以这里不按“未发布考试”收窄。
func (s *Service) applyAppealStatistics(ctx context.Context, user auth.User, scope auth.AccessScope, result *Summary) {
	if !auth.HasPermission(user, "appeal:manage") {
		return
	}
	store, ok := s.deps.Organizations.(AppealStatisticsStore)
	if !ok {
		result.Warnings = append(result.Warnings, "成绩复核申请暂时无法统计")
		return
	}
	statistics, err := store.DashboardAppealStatistics(ctx, user.TenantID, scope)
	if err != nil {
		result.Warnings = append(result.Warnings, "成绩复核申请暂时无法统计")
		return
	}
	result.AppealStatistics = &statistics
}
