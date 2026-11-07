package modelgovernance

import (
	"context"
	"github.com/google/uuid"
	"strings"
	"time"
)

func (s *MemoryStore) GetPolicy(_ context.Context, tenantID string) (TenantPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if item, ok := s.policies[tenantID]; ok {
		return item, nil
	}
	item := DefaultTenantPolicy()
	item.ID = uuid.NewString()
	item.TenantID = tenantID
	item.PolicyKey = "default"
	item.DisplayName = "Default local-only policy"
	item.Status = "active"
	item.Version = 1
	item.UpdatedAt = time.Now().UTC()
	s.policies[tenantID] = item
	return item, nil
}

// 策略版本采用乐观锁；同时写入旧版部署白名单和托管配置白名单会被拒绝，避免授权来源混淆。
func (s *MemoryStore) UpdatePolicy(_ context.Context, tenantID string, _ string, input PolicyUpdateInput) (TenantPolicy, error) {
	policy := PolicyFromUpdate(input)
	if ValidateTenantPolicy(policy) != nil || input.ExpectedVersion < 1 || strings.TrimSpace(input.Reason) == "" {
		return TenantPolicy{}, ErrInvalidPolicy
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(input.AllowedDeployments) > 0 && len(input.AllowedModelConfigIDs) > 0 {
		return TenantPolicy{}, ErrInvalidPolicy
	}
	for _, id := range input.AllowedModelConfigIDs {
		config, exists := s.managedConfigs[id]
		if !exists || config.TenantID != tenantID || config.Status != "active" || !managedCapabilityVerified(config) {
			return TenantPolicy{}, ErrInvalidPolicy
		}
	}
	current, ok := s.policies[tenantID]
	if !ok {
		current = DefaultTenantPolicy()
		current.ID = uuid.NewString()
		current.Version = 1
	}
	if current.Version != input.ExpectedVersion {
		return TenantPolicy{}, ErrConflict
	}
	policy.ID = current.ID
	policy.TenantID = tenantID
	policy.PolicyKey = "default"
	policy.DisplayName = strings.TrimSpace(input.DisplayName)
	if policy.DisplayName == "" {
		policy.DisplayName = current.DisplayName
	}
	policy.Status = "active"
	policy.Version = current.Version + 1
	policy.UpdatedAt = time.Now().UTC()
	s.policies[tenantID] = policy
	return policy, nil
}
