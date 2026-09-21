package subjective

import (
	"context"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
)

func (s *MemoryStore) CreatePanelPolicy(_ context.Context, tenantID, actorID string, input CreatePanelPolicyInput) (PanelPolicy, error) {
	input, err := normalizeCreatePanelPolicy(input)
	if err != nil || tenantID == "" || actorID == "" {
		return PanelPolicy{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.panelPolicies {
		if existing.TenantID == tenantID && existing.EducationStage == assessment.EducationStage(input.EducationStage) &&
			existing.SubjectCode == assessment.SubjectCode(input.SubjectCode) && existing.ArchetypeCode == input.ArchetypeCode &&
			existing.PolicyVersion == input.PolicyVersion {
			return PanelPolicy{}, ErrIdempotencyConflict
		}
	}
	item := PanelPolicy{
		ID: s.id("panel-policy"), TenantID: tenantID, PolicyVersion: input.PolicyVersion,
		EducationStage: assessment.EducationStage(input.EducationStage), SubjectCode: assessment.SubjectCode(input.SubjectCode),
		ArchetypeCode: input.ArchetypeCode, DecisionConfig: clonePanelConfig(input.DecisionConfig),
		ReadinessPolicy: input.ReadinessPolicy, Status: PanelPolicyShadow, CreatedBy: actorID, CreatedAt: time.Now().UTC(),
	}
	s.panelPolicies[key(tenantID, item.ID)] = item
	return clonePanelPolicy(item), nil
}

func (s *MemoryStore) GetPanelPolicy(_ context.Context, tenantID, policyID string) (PanelPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.panelPolicies[key(tenantID, policyID)]
	if !ok {
		return PanelPolicy{}, ErrNotFound
	}
	return clonePanelPolicy(item), nil
}

func (s *MemoryStore) FindApprovedPanelPolicy(_ context.Context, tenantID string, stage assessment.EducationStage, subject assessment.SubjectCode, archetype string) (PanelPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.panelPolicies {
		if item.TenantID == tenantID && item.Status == PanelPolicyApproved && item.EducationStage == stage && item.SubjectCode == subject && item.ArchetypeCode == archetype {
			return clonePanelPolicy(item), nil
		}
	}
	return PanelPolicy{}, ErrNotFound
}

func (s *MemoryStore) ApprovePanelPolicy(_ context.Context, tenantID, policyID, actorID, evaluationRunID string, report gradingevaluation.PanelSliceReadiness, at time.Time) (PanelPolicy, error) {
	if tenantID == "" || actorID == "" || evaluationRunID == "" || !report.Ready {
		return PanelPolicy{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	policyKey := key(tenantID, policyID)
	item, ok := s.panelPolicies[policyKey]
	if !ok {
		return PanelPolicy{}, ErrNotFound
	}
	if item.Status != PanelPolicyShadow || report.PolicyVersion != item.PolicyVersion || report.EducationStage != string(item.EducationStage) ||
		report.Subject != string(item.SubjectCode) || report.Archetype != item.ArchetypeCode {
		return PanelPolicy{}, ErrIdempotencyConflict
	}
	for _, existing := range s.panelPolicies {
		if existing.Status == PanelPolicyApproved && existing.TenantID == tenantID && existing.EducationStage == item.EducationStage &&
			existing.SubjectCode == item.SubjectCode && existing.ArchetypeCode == item.ArchetypeCode {
			return PanelPolicy{}, ErrIdempotencyConflict
		}
	}
	approvedAt := at.UTC()
	item.Status, item.EvaluationRunID, item.ApprovedBy, item.ApprovedAt = PanelPolicyApproved, evaluationRunID, actorID, &approvedAt
	copyReport := report
	copyReport.Reasons = append([]string(nil), report.Reasons...)
	item.ReadinessReport = &copyReport
	s.panelPolicies[policyKey] = item
	return clonePanelPolicy(item), nil
}

func (s *MemoryStore) InvalidatePanelPolicy(_ context.Context, tenantID, policyID, actorID, reason string, at time.Time) (PanelPolicy, error) {
	if tenantID == "" || actorID == "" || reason == "" {
		return PanelPolicy{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	policyKey := key(tenantID, policyID)
	item, ok := s.panelPolicies[policyKey]
	if !ok {
		return PanelPolicy{}, ErrNotFound
	}
	if item.Status == PanelPolicyInvalidated {
		return PanelPolicy{}, ErrIdempotencyConflict
	}
	invalidatedAt := at.UTC()
	item.Status, item.InvalidatedBy, item.InvalidatedAt, item.InvalidationReason = PanelPolicyInvalidated, actorID, &invalidatedAt, reason
	s.panelPolicies[policyKey] = item
	return clonePanelPolicy(item), nil
}

func clonePanelPolicy(item PanelPolicy) PanelPolicy {
	item.DecisionConfig = clonePanelConfig(item.DecisionConfig)
	if item.ReadinessReport != nil {
		copyReport := *item.ReadinessReport
		copyReport.Reasons = append([]string(nil), item.ReadinessReport.Reasons...)
		item.ReadinessReport = &copyReport
	}
	return item
}
