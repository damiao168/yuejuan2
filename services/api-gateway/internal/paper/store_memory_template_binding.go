package paper

import (
	"context"
	"strings"
	"time"
)

func memoryBindingKey(tenantID, examID string) string { return tenantID + ":" + examID }

func (s *MemoryStore) GetExamTemplateBinding(ctx context.Context, tenantID, examID string) (ExamTemplateBinding, error) {
	state, err := s.GetExamTemplateBindingState(ctx, tenantID, examID)
	if err != nil {
		return ExamTemplateBinding{}, err
	}
	if state.Binding == nil {
		return ExamTemplateBinding{}, ErrNotFound
	}
	return *state.Binding, nil
}

func (s *MemoryStore) GetExamTemplateBindingState(_ context.Context, tenantID, examID string) (ExamTemplateBindingState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := memoryBindingKey(tenantID, examID)
	state := ExamTemplateBindingState{Revision: s.bindingRevisions[key]}
	if item, ok := s.bindings[key]; ok {
		state.Binding = &item
		if state.Revision < item.Revision {
			state.Revision = item.Revision
		}
	}
	return state, nil
}

func (s *MemoryStore) BindExamTemplate(_ context.Context, tenantID, examID, userID string, input BindExamTemplateInput) (ExamTemplateBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mode, ok := normalizeBindingMode(input.Mode)
	if !ok || input.TemplateID == "" || input.ExpectedRevision < 0 {
		return ExamTemplateBinding{}, ErrInvalidInput
	}
	template, ok := s.templates[input.TemplateID]
	if !ok || template.TenantID != tenantID || template.ExamID != examID {
		return ExamTemplateBinding{}, ErrNotFound
	}
	if template.Status != "locked" {
		return ExamTemplateBinding{}, ErrTemplateNotLocked
	}
	if err := validateTemplateReviewResolved(template.Layout); err != nil {
		return ExamTemplateBinding{}, err
	}
	key := memoryBindingKey(tenantID, examID)
	current, exists := s.bindings[key]
	lastRevision := s.bindingRevisions[key]
	if exists && lastRevision < current.Revision {
		lastRevision = current.Revision
	}
	if lastRevision != input.ExpectedRevision {
		return ExamTemplateBinding{}, ErrConflict
	}
	now := time.Now().UTC()
	if !exists {
		current = ExamTemplateBinding{ID: s.id("template-binding"), TenantID: tenantID, ExamID: examID}
	}
	current.Revision = lastRevision + 1
	current.TemplateID = template.ID
	current.TemplateContentHash = template.ContentHash
	current.Mode = mode
	current.Source = "manual"
	current.BoundBy = userID
	current.BoundAt = now
	current.UpdatedAt = now
	s.bindings[key] = current
	s.bindingRevisions[key] = current.Revision
	return current, nil
}

func (s *MemoryStore) UnbindExamTemplate(_ context.Context, tenantID, examID string, input UnbindExamTemplateInput) (ExamTemplateBinding, error) {
	if strings.TrimSpace(input.Reason) == "" {
		return ExamTemplateBinding{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryBindingKey(tenantID, examID)
	current, ok := s.bindings[key]
	if !ok {
		return ExamTemplateBinding{}, ErrNotFound
	}
	lastRevision := s.bindingRevisions[key]
	if lastRevision < current.Revision {
		lastRevision = current.Revision
	}
	if input.ExpectedRevision < 1 || lastRevision != input.ExpectedRevision {
		return ExamTemplateBinding{}, ErrConflict
	}
	delete(s.bindings, key)
	// 删除当前绑定仍消耗修订号；否则旧页面的 expected_revision 可在重建绑定后再次命中。
	s.bindingRevisions[key] = lastRevision + 1
	return current, nil
}
