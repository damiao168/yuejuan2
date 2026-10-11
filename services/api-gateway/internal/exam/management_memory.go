package exam

import (
	"context"
	"fmt"
	"sort"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func (s *MemoryStore) GetManagementExam(_ context.Context, scope auth.AccessScope, kind, id string) (ManagementExam, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.managementExamLocked(scope, kind, id)
}

// managementExamLocked 由读、编辑和归档共用；调用方持有 mu，避免在整场写入中再次加锁。
func (s *MemoryStore) managementExamLocked(scope auth.AccessScope, kind, id string) (ManagementExam, error) {
	if kind != "session" && kind != "standalone" {
		return ManagementExam{}, ErrInvalidInput
	}
	out := ManagementExam{ID: id, Kind: kind, Exams: []Exam{}, ManagementArchiveMetadata: s.archiveState[kind+":"+id]}
	total := s.appendManagementExamChildrenLocked(scope, kind, id, &out)
	if len(out.Exams) == 0 {
		return ManagementExam{}, ErrNotFound
	}
	first := out.Exams[0]
	out.SchoolID, out.Name, out.ExamType = first.SchoolID, first.Name, first.ExamType
	out.Revision, out.CreatedBy, out.CreatedAt, out.UpdatedAt = first.Revision, first.CreatedBy, first.CreatedAt, first.UpdatedAt
	if kind == "session" {
		parent, ok := s.managementSessions[id]
		if !ok {
			return ManagementExam{}, ErrNotFound
		}
		out.Name, out.GradeID, out.ExamType, out.Revision = parent.Name, parent.GradeID, parent.ExamType, parent.Revision
		out.CreatedBy, out.CreatedAt, out.UpdatedAt = parent.CreatedBy, parent.CreatedAt, parent.UpdatedAt
		out.LegacyArchived = parent.Status == "archived"
	}
	if kind == "standalone" {
		out.LegacyArchived = first.Status == "archived"
	}
	completeManagementExam(&out, scope, total, s.managementRules, s.managementFacts)
	return out, nil
}

func (s *MemoryStore) appendManagementExamChildrenLocked(scope auth.AccessScope, kind, id string, out *ManagementExam) int {
	total := 0
	for _, item := range s.items {
		if item.TenantID != scope.TenantID || (kind == "session" && item.SessionID != id) || (kind == "standalone" && (item.ID != id || item.SessionID != "")) {
			continue
		}
		// 隐藏科目也计入整场总数，不能因只收集授权科目而误授予整场写权限。
		total++
		if managementScopeAllowsExam(scope, item) {
			child := item
			child.ClassIDs = cloneStrings(item.ClassIDs)
			out.Exams = append(out.Exams, child)
			if kind == "session" && s.archiveState["standalone:"+item.ID].ArchivedAt != nil {
				out.ArchivedSubjectIDs = append(out.ArchivedSubjectIDs, item.ID)
			}
		}
	}
	return total
}

func (s *MemoryStore) ListManagementExams(_ context.Context, scope auth.AccessScope, filter ManagementListFilter) ([]ManagementExam, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out, err := s.listManagementExamsLocked(scope, filter)
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	for i := range out {
		applyManagementArchiveCheck(&out[i], s.checkManagementArchiveLocked(out[i]), true)
	}
	return out, nil
}

func (s *MemoryStore) listManagementExamsLocked(scope auth.AccessScope, filter ManagementListFilter) ([]ManagementExam, error) {
	seen := map[string]bool{}
	out := []ManagementExam{}
	for _, child := range s.items {
		if child.TenantID != scope.TenantID || !managementScopeAllowsExam(scope, child) {
			continue
		}
		kind, id := "standalone", child.ID
		if child.SessionID != "" {
			kind, id = "session", child.SessionID
		}
		if seen[kind+":"+id] {
			continue
		}
		seen[kind+":"+id] = true
		item, err := s.managementExamLocked(scope, kind, id)
		if err != nil {
			return nil, err
		}
		if managementCursorSkips(item, filter) {
			continue
		}
		if managementMatchesFilter(item, filter) {
			out = append(out, item)
		}
	}
	return out, nil
}

func managementCursorSkips(item ManagementExam, filter ManagementListFilter) bool {
	return !filter.CursorAt.IsZero() && filter.CursorID != "" && (item.CreatedAt.After(filter.CursorAt) || item.CreatedAt.Equal(filter.CursorAt) && item.ID >= filter.CursorID)
}

func (s *MemoryStore) UpdateManagementExam(_ context.Context, scope auth.AccessScope, kind, id string, input ManagementUpdateInput) (ManagementExam, error) {
	if err := validateManagementUpdate(input); err != nil {
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
	if err := requireManagementRevisions(current, input); err != nil {
		return ManagementExam{}, err
	}
	if !current.CanEdit {
		return ManagementExam{}, ErrLocked
	}
	if input.ClassIDs != nil && len(current.InheritedSubjectIDs) == 0 {
		return ManagementExam{}, ErrInvalidInput
	}
	// 先校验全部受影响学科，再一起写入；最后一科冻结时不能先改掉前面的班级。
	if err := s.validateManagementChildrenLocked(scope, current.Exams, input); err != nil {
		return ManagementExam{}, err
	}
	now := time.Now().UTC()
	s.updateManagementChildrenLocked(kind, current.Exams, input, now)
	if kind == "session" {
		parent := s.managementSessions[id]
		if input.Name != nil {
			parent.Name = *input.Name
		}
		if input.ExamType != nil {
			parent.ExamType = *input.ExamType
		}
		parent.Revision++
		parent.UpdatedAt = now
		s.managementSessions[id] = parent
	}
	return s.managementExamLocked(scope, kind, id)
}

func (s *MemoryStore) validateManagementChildrenLocked(scope auth.AccessScope, children []Exam, input ManagementUpdateInput) error {
	for _, child := range children {
		if input.ExamType != nil && *input.ExamType != child.ExamType && !CanChangeCandidateRoster(child.Status) {
			return ErrLocked
		}
		if input.ClassIDs != nil && s.managementRules[child.ID] != "subject_selected_classes" && !classIDSetsEqual(child.ClassIDs, *input.ClassIDs) {
			if !CanChangeCandidateRoster(child.Status) {
				return ErrCandidatesFrozen
			}
			if !managementScopeAllowsRequestedClasses(scope, child.SchoolID, *input.ClassIDs) {
				return ErrScopeForbidden
			}
		}
	}
	return nil
}

func (s *MemoryStore) updateManagementChildrenLocked(kind string, children []Exam, input ManagementUpdateInput, now time.Time) {
	for _, child := range children {
		if input.Name != nil {
			if kind == "session" {
				child.Name, child.SessionName = fmt.Sprintf("%s · %s", *input.Name, subjectDisplayName(child.Subject)), *input.Name
			} else {
				child.Name = *input.Name
			}
		}
		if input.ExamType != nil {
			child.ExamType = *input.ExamType
		}
		if input.ClassIDs != nil && s.managementRules[child.ID] != "subject_selected_classes" {
			child.ClassIDs = normalizedClassIDs(*input.ClassIDs)
		}
		child.Revision++
		child.UpdatedAt = now
		s.items[child.ID] = child
	}
}
