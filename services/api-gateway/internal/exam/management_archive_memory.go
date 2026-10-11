package exam

import (
	"context"
	"encoding/json"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func (s *MemoryStore) memoryExamArchivedLocked(item Exam) bool {
	return item.Status == "archived" || s.archiveState["standalone:"+item.ID].ArchivedAt != nil ||
		(item.SessionID != "" && (s.archiveState["session:"+item.SessionID].ArchivedAt != nil || s.managementSessions[item.SessionID].Status == "archived"))
}

func (s *MemoryStore) CheckManagementArchive(_ context.Context, scope auth.AccessScope, kind, id string) (ManagementArchiveCheck, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, err := s.managementExamLocked(scope, kind, id)
	if err != nil {
		return ManagementArchiveCheck{}, err
	}
	return s.checkManagementArchiveLocked(item), nil
}

func (s *MemoryStore) checkManagementArchiveLocked(item ManagementExam) ManagementArchiveCheck {
	check := baseManagementArchiveCheck(item)
	if len(check.Blockers) > 0 {
		return check
	}
	// 内存仓库没有任务、答卷和申诉事实，只模拟未开展的准备阶段；不能凭 published 状态放行生产条件。
	for _, child := range item.Exams {
		if child.Status != "draft" && child.Status != "configured" && child.Status != "ready" {
			check.Blockers = append(check.Blockers, archiveSubjectBlocker(child, "archive_facts_unavailable", "内存仓库无法核实已开展考试的任务、正式发布及申诉情况"))
		}
	}
	check.Allowed = len(check.Blockers) == 0 && len(item.Exams) > 0
	return check
}

func (s *MemoryStore) ArchiveManagementExam(ctx context.Context, scope auth.AccessScope, actor, kind, id string, input ManagementArchiveInput) (ManagementExam, error) {
	return s.mutateManagementArchiveMemory(ctx, scope, actor, kind, id, "archive", input)
}

func (s *MemoryStore) RestoreManagementExam(ctx context.Context, scope auth.AccessScope, actor, kind, id string, input ManagementArchiveInput) (ManagementExam, error) {
	return s.mutateManagementArchiveMemory(ctx, scope, actor, kind, id, "restore", input)
}

func (s *MemoryStore) RecoverManagementArchiveCommand(_ context.Context, scope auth.AccessScope, actor, commandID string) (ManagementArchiveCommandResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := ManagementArchiveCommandResult{CommandID: commandID, Status: "not_accepted"}
	prior, ok := s.archiveCommands[scope.TenantID+"\x00"+actor+"\x00"+commandID]
	if !ok {
		return result, nil
	}
	current, err := s.managementExamLocked(scope, prior.Result.Kind, prior.Result.ID)
	if err != nil {
		return result, err
	}
	if !current.CanManage {
		return result, ErrScopeForbidden
	}
	item := cloneManagementArchiveResult(prior.Result)
	result.Status, result.Exam = "succeeded", &item
	return result, nil
}

func (s *MemoryStore) mutateManagementArchiveMemory(_ context.Context, scope auth.AccessScope, actor, kind, id, action string, input ManagementArchiveInput) (ManagementExam, error) {
	if err := validateManagementArchiveInput(input); err != nil {
		return ManagementExam{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.managementExamLocked(scope, kind, id)
	if err != nil {
		return ManagementExam{}, err
	}
	if !current.CanManage {
		return ManagementExam{}, ErrScopeForbidden
	}
	commandKey := scope.TenantID + "\x00" + actor + "\x00" + input.CommandID
	hash := managementArchiveHash(kind, id, action, input)
	if prior, ok := s.archiveCommands[commandKey]; ok {
		if prior.Hash != hash {
			return ManagementExam{}, ErrCommandConflict
		}
		return cloneManagementArchiveResult(prior.Result), nil
	}
	if err := requireManagementArchiveRevision(current, input); err != nil {
		return ManagementExam{}, err
	}
	if action == "archive" && current.ArchivedAt == nil && !s.checkManagementArchiveLocked(current).Allowed {
		return ManagementExam{}, ErrArchiveBlocked
	}
	if action == "restore" && !current.CanRestore {
		return ManagementExam{}, ErrLocked
	}
	if action == "restore" || current.ArchivedAt == nil {
		now := time.Now().UTC()
		state := ManagementArchiveMetadata{}
		if action == "archive" {
			state = ManagementArchiveMetadata{ArchivedAt: &now, ArchivedBy: actor, ArchiveReason: input.Reason}
		}
		s.archiveState[kind+":"+id] = state
		if kind == "session" {
			parent := s.managementSessions[id]
			parent.Revision++
			parent.UpdatedAt = now
			s.managementSessions[id] = parent
		} else {
			child := s.items[id]
			child.Revision++
			child.UpdatedAt = now
			s.items[id] = child
		}
	}
	result, err := s.managementExamLocked(scope, kind, id)
	if err != nil {
		return ManagementExam{}, err
	}
	applyManagementArchiveCheck(&result, s.checkManagementArchiveLocked(result), true)
	s.archiveCommands[commandKey] = managementArchiveCommandRecord{Hash: hash, Result: cloneManagementArchiveResult(result)}
	return result, nil
}

func cloneManagementArchiveResult(item ManagementExam) ManagementExam {
	// 回执必须独立于调用者持有的切片、指针和 map，避免展示层修改污染后续重试结果。
	raw, _ := json.Marshal(item)
	var result ManagementExam
	_ = json.Unmarshal(raw, &result)
	return result
}
