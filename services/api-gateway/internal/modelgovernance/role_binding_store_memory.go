package modelgovernance

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

func (s *MemoryStore) SaveModelRoleBinding(_ context.Context, tenantID, actorID string, input SaveModelRoleBindingInput) (ModelRoleBinding, error) {
	input, err := normalizeRoleBinding(input)
	if err != nil || tenantID == "" || actorID == "" {
		return ModelRoleBinding{}, ErrInvalidManagedConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantID + "\x00" + input.EducationStage + "\x00" + input.SubjectCode + "\x00" + input.ArchetypeCode + "\x00" + input.AgentRole
	previous, previouslyBound := s.roleBindings[key]
	config, ok := s.managedConfigs[input.ManagedModelAPIConfigID]
	if (!ok && !(input.Status == "disabled" && previouslyBound && previous.ManagedModelAPIConfigID == input.ManagedModelAPIConfigID)) ||
		(ok && config.TenantID != tenantID) ||
		(input.Status == "active" && (config.Status != "active" || config.LastCapabilityStatus != "success" || config.LastCapabilityVersion != ManagedCapabilityProbeVersion)) {
		return ModelRoleBinding{}, ErrManagedCapabilityRequired
	}
	now := time.Now().UTC()
	item, exists := s.roleBindings[key]
	if !exists {
		item.ID, item.CreatedAt, item.CreatedBy = uuid.NewString(), now, actorID
	}
	item.TenantID, item.EducationStage, item.SubjectCode, item.ArchetypeCode = tenantID, input.EducationStage, input.SubjectCode, input.ArchetypeCode
	item.AgentRole, item.ManagedModelAPIConfigID, item.PromptVersion = input.AgentRole, input.ManagedModelAPIConfigID, input.PromptVersion
	item.StrengthRank, item.Status, item.UpdatedAt = input.StrengthRank, input.Status, now
	s.roleBindings[key] = item
	return item, nil
}

func (s *MemoryStore) ListConfiguredModelRoleBindings(_ context.Context, tenantID, stage, subject, archetype string) ([]ModelRoleBinding, error) {
	stage, subject, archetype, err := normalizeRoleScope(stage, subject, archetype)
	if err != nil || tenantID == "" {
		return nil, ErrInvalidManagedConfig
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []ModelRoleBinding{}
	for _, item := range s.roleBindings {
		if item.TenantID == tenantID && item.EducationStage == stage && item.SubjectCode == subject && item.ArchetypeCode == archetype {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].AgentRole < items[j].AgentRole })
	return items, nil
}

func (s *MemoryStore) ListModelRoleBindings(_ context.Context, tenantID, stage, subject, archetype string) ([]ModelRoleBinding, error) {
	stage, subject, archetype, err := normalizeRoleScope(stage, subject, archetype)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []ModelRoleBinding{}
	for _, item := range s.roleBindings {
		config := s.managedConfigs[item.ManagedModelAPIConfigID]
		if item.TenantID == tenantID && item.EducationStage == stage && item.SubjectCode == subject &&
			(item.ArchetypeCode == archetype || item.ArchetypeCode == "*") && config.Status == "active" &&
			config.LastCapabilityStatus == "success" && config.LastCapabilityVersion == ManagedCapabilityProbeVersion {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if (items[i].ArchetypeCode == archetype) != (items[j].ArchetypeCode == archetype) {
			return items[i].ArchetypeCode == archetype
		}
		return items[i].AgentRole < items[j].AgentRole
	})
	return items, nil
}
