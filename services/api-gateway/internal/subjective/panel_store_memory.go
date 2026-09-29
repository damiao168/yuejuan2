package subjective

import (
	"context"
	"time"
)

// 面板按答案段、答案版本和量规版本复用；关键题目字段不一致时返回幂等冲突，避免混用不同评分上下文。
func (s *MemoryStore) GetOrCreatePanel(_ context.Context, tenantID, actorID string, input CreatePanelInput) (GradingPanel, error) {
	input.DecisionConfig = NormalizePanelDecisionConfig(input.DecisionConfig)
	if tenantID == "" || actorID == "" || validateCreatePanel(input) != nil {
		return GradingPanel{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, panel := range s.panels {
		if panel.TenantID == tenantID && panel.AnswerSegmentID == input.AnswerSegmentID && panel.AnswerVersion == input.AnswerVersion &&
			panel.RubricVersion == input.RubricVersion {
			if panel.QuestionID != input.QuestionID || panel.MaxScore != input.MaxScore {
				return GradingPanel{}, ErrIdempotencyConflict
			}
			return clonePanel(panel), nil
		}
	}
	now := time.Now().UTC()
	panel := GradingPanel{
		ID: s.id("subjective-panel"), TenantID: tenantID, AnswerSegmentID: input.AnswerSegmentID, AnswerVersion: input.AnswerVersion,
		QuestionID: input.QuestionID, RubricVersion: input.RubricVersion,
		PolicyVersion: input.DecisionConfig.PolicyVersion, MaxScore: input.MaxScore,
		DecisionConfig: clonePanelConfig(input.DecisionConfig), TriggerCodes: []string{},
		Status: PanelPrimaryPending, CreatedBy: actorID, CreatedAt: now, UpdatedAt: now,
	}
	s.panels[key(tenantID, panel.ID)] = panel
	return clonePanel(panel), nil
}

func (s *MemoryStore) GetPanel(_ context.Context, tenantID, panelID string) (GradingPanel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	panel, ok := s.panels[key(tenantID, panelID)]
	if !ok {
		return GradingPanel{}, ErrNotFound
	}
	return clonePanel(panel), nil
}

// 更新同时检查状态迁移、分数范围和角色运行是否已成功，保证终态面板具备可追溯的运行证据。
func (s *MemoryStore) UpdatePanel(_ context.Context, tenantID, panelID string, input UpdatePanelInput) (GradingPanel, error) {
	if !validPanelStatus(input.Status) {
		return GradingPanel{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	panelKey := key(tenantID, panelID)
	panel, ok := s.panels[panelKey]
	if !ok {
		return GradingPanel{}, ErrNotFound
	}
	if !validPanelTransition(panel.Status, input.Status) {
		return GradingPanel{}, ErrIdempotencyConflict
	}
	applyPanelUpdate(&panel, input)
	panel.UpdatedAt = time.Now().UTC()
	if panel.Status == PanelResolved {
		panel.CompletedAt = &panel.UpdatedAt
	}
	if validatePanelState(panel) != nil {
		return GradingPanel{}, ErrInvalidInput
	}
	if !s.panelCompletedRolesValid(panel) {
		return GradingPanel{}, ErrIdempotencyConflict
	}
	s.panels[panelKey] = panel
	return clonePanel(panel), nil
}

func (s *MemoryStore) panelCompletedRolesValid(panel GradingPanel) bool {
	requiresPrimaries := panel.Status == PanelComparing || panel.Status == PanelArbitrationPending ||
		(panel.Status == PanelResolved && (panel.ResolutionSource == ResolutionPrimaryConsensus || panel.ResolutionSource == ResolutionArbiter))
	validRole := func(runID, role string) bool {
		if runID == "" {
			return false
		}
		for _, run := range s.runs {
			if run.TenantID == panel.TenantID && run.ID == runID && run.PanelID == panel.ID &&
				run.AgentRole == role && run.Status == RunSucceeded && run.GradeID != "" {
				return true
			}
		}
		return false
	}
	if requiresPrimaries && (!validRole(panel.PrimaryARunID, AgentRolePrimaryA) || !validRole(panel.PrimaryBRunID, AgentRolePrimaryB)) {
		return false
	}
	return panel.Status != PanelResolved || panel.ResolutionSource != ResolutionArbiter || validRole(panel.ArbiterRunID, AgentRoleArbiter)
}

func (s *MemoryStore) RoutePanelHumanReview(_ context.Context, tenantID, actorID string, panel GradingPanel) (string, error) {
	if panel.TenantID != tenantID || actorID == "" || panel.AnswerSegmentID == "" {
		return "", ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	panelKey := key(tenantID, panel.ID)
	if existing := s.panelReviews[panelKey]; existing != "" {
		return existing, nil
	}
	id := s.id("review-task")
	s.panelReviews[panelKey] = id
	return id, nil
}

func clonePanel(panel GradingPanel) GradingPanel {
	panel.TriggerCodes = cloneStrings(panel.TriggerCodes)
	panel.DecisionConfig = clonePanelConfig(panel.DecisionConfig)
	return panel
}

func clonePanelConfig(config PanelDecisionConfig) PanelDecisionConfig {
	config.HardRiskCodes = cloneStrings(config.HardRiskCodes)
	return config
}
