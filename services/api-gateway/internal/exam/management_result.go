package exam

func applyManagementArchiveCheck(item *ManagementExam, check ManagementArchiveCheck, available bool) {
	item.archiveCheckResolved = true
	if !available {
		// 可读结果仍可展示；归档资格未查明时只能禁用动作，不能把查询故障解释为条件已满足。
		item.CanArchive = false
		item.ArchiveBlockers = []ManagementArchiveBlocker{{Code: "archive_check_unavailable", Message: "暂无法检查归档条件，请刷新后重试"}}
		return
	}
	item.CanArchive = check.Allowed && item.CanManage && item.ArchivedAt == nil && !item.LegacyArchived && len(item.ArchivedSubjectIDs) == 0
	item.ArchiveBlockers = check.Blockers
}

func applyManagementArchiveChecks(items []ManagementExam, checks map[string]ManagementArchiveCheck, available bool) {
	for i := range items {
		check, found := checks[items[i].Kind+":"+items[i].ID]
		applyManagementArchiveCheck(&items[i], check, available && found)
	}
}
