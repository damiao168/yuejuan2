package modelgovernance

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

func (s *MemoryStore) GetDocumentModelSelection(_ context.Context, tenantID string) (DocumentModelSelection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if selection, ok := s.documentSelections[tenantID]; ok {
		return selection, nil
	}
	return initialDocumentSelection(), nil
}

func (s *MemoryStore) SaveDocumentModelSelection(_ context.Context, tenantID, _ string, input DocumentModelSelectionInput) (DocumentModelSelection, error) {
	input, err := normalizeDocumentSelection(input)
	if err != nil {
		return DocumentModelSelection{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.documentSelections[tenantID]
	if !ok {
		current = initialDocumentSelection()
	}
	if current.Revision != input.ExpectedRevision {
		return DocumentModelSelection{}, ErrDocumentSelectionChanged
	}
	if input.Mode == "model" {
		config, ok := s.managedConfigs[input.ModelConfigID]
		if !ok || config.TenantID != tenantID || config.DeletedAt != nil || config.Status != "active" || !config.CredentialConfigured || s.managedSecrets[config.ID] == "" || !managedCapabilityVerified(config) {
			return DocumentModelSelection{}, ErrManagedCapabilityRequired
		}
	}
	now := time.Now().UTC()
	selection := DocumentModelSelection{Revision: uuid.NewString(), Mode: input.Mode, ModelConfigID: input.ModelConfigID, UpdatedAt: &now}
	s.documentSelections[tenantID] = selection
	return selection, nil
}

func (s *MemoryStore) ListGradingModelUses(_ context.Context, tenantID string) ([]GradingModelUse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := []GradingModelUse{}
	for _, binding := range s.roleBindings {
		if binding.TenantID == tenantID && binding.Status == "active" {
			result = append(result, GradingModelUse{ModelConfigID: binding.ManagedModelAPIConfigID, EducationStage: binding.EducationStage, SubjectCode: binding.SubjectCode, ArchetypeCode: binding.ArchetypeCode, AgentRole: binding.AgentRole})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		return a.ModelConfigID+a.EducationStage+a.SubjectCode+a.ArchetypeCode+a.AgentRole < b.ModelConfigID+b.EducationStage+b.SubjectCode+b.ArchetypeCode+b.AgentRole
	})
	return result, nil
}
