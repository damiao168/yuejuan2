package workspace

import (
	"context"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
)

type managementArchiveReader interface {
	GetManagementExam(context.Context, auth.AccessScope, string, string) (exam.ManagementExam, error)
}

func (s *Service) applyArchiveReadOnly(ctx context.Context, scope auth.AccessScope, item exam.Exam, result *Projection) {
	result.ReadOnly = item.Status == "archived"
	reader, available := s.deps.Exams.(managementArchiveReader)
	if !available {
		return
	}
	kind, targetID := "standalone", item.ID
	if item.SessionID != "" {
		kind, targetID = "session", item.SessionID
	}
	management, err := reader.GetManagementExam(ctx, scope, kind, targetID)
	if err != nil {
		// 归档事实未知时仍可读取历史工作区，但不能把未知范围开放成可写状态。
		result.ReadOnly = true
		result.Warnings = append(result.Warnings, unavailableNotice("archive_state_unavailable", "暂无法确认考试归档状态，当前按只读展示"))
		return
	}
	result.ReadOnly = result.ReadOnly || management.ArchivedAt != nil || management.LegacyArchived
	for _, archivedID := range management.ArchivedSubjectIDs {
		if archivedID == item.ID {
			result.ReadOnly = true
		}
	}
}
