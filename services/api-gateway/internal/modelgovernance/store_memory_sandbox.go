package modelgovernance

import (
	"context"
	"github.com/google/uuid"
	"sort"
	"strings"
	"time"
)

func (s *MemoryStore) ListSandboxApprovals(_ context.Context, tenantID string) ([]SandboxApproval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []SandboxApproval{}
	for _, item := range s.approvals {
		if item.TenantID == tenantID {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// CreateSandboxApproval 在同一把锁内检查库存、重复审批并写入；过期但未撤销的审批仍阻止再次创建。
func (s *MemoryStore) CreateSandboxApproval(
	_ context.Context,
	tenantID string,
	_ string,
	input SandboxApprovalInput,
) (SandboxApproval, error) {
	now := time.Now().UTC()
	if ValidateSandboxApprovalInput(input, now) != nil {
		return SandboxApproval{}, ErrInvalidApproval
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.ModelConfigID != "" {
		return s.createManagedSandboxApprovalLocked(tenantID, input, now)
	}
	return s.createLegacySandboxApprovalLocked(tenantID, input, now)
}

// 调用方须持有写锁，库存检查、未撤销审批检查和新审批写入不能分开解锁。
func (s *MemoryStore) createManagedSandboxApprovalLocked(tenantID string, input SandboxApprovalInput, now time.Time) (SandboxApproval, error) {
	config, ok := s.managedConfigs[input.ModelConfigID]
	if !ok || config.TenantID != tenantID || config.Status != "active" ||
		config.ProviderKey != "aliyun" || config.Region != input.ApprovedRegion {
		return SandboxApproval{}, ErrNotFound
	}
	for _, item := range s.approvals {
		if item.TenantID == tenantID && item.ModelConfigID == config.ID && item.RevokedAt == nil {
			return SandboxApproval{}, ErrConflict
		}
	}
	item := sandboxApprovalFromInput(tenantID, input, now)
	item.ModelConfigID = config.ID
	item.ProviderKey = config.ProviderKey
	item.ModelName = config.ModelName
	item.ModelVersion = config.ModelVersion
	s.approvals[item.ID] = item
	return item, nil
}

// 调用方须持有写锁；旧版部署还要关联同租户的原供应商，并匹配两者的审批区域。
func (s *MemoryStore) createLegacySandboxApprovalLocked(tenantID string, input SandboxApprovalInput, now time.Time) (SandboxApproval, error) {
	provider, ok := s.providers[input.ProviderID]
	if !ok || provider.TenantID != tenantID ||
		provider.Kind != ProviderExternal ||
		provider.AdapterType != SandboxProtocolDashScopeNative {
		return SandboxApproval{}, ErrNotFound
	}
	deployment, ok := s.deployments[input.DeploymentID]
	if !ok || deployment.TenantID != tenantID ||
		deployment.ProviderID != provider.ID ||
		deployment.Region != input.ApprovedRegion ||
		provider.Region != input.ApprovedRegion {
		return SandboxApproval{}, ErrNotFound
	}
	for _, item := range s.approvals {
		if item.TenantID == tenantID &&
			item.DeploymentID == deployment.ID &&
			item.RevokedAt == nil {
			return SandboxApproval{}, ErrConflict
		}
	}
	item := sandboxApprovalFromInput(tenantID, input, now)
	item.ProviderID = provider.ID
	item.DeploymentID = deployment.ID
	item.ProviderKey = provider.Key
	item.DeploymentKey = deployment.Key
	s.approvals[item.ID] = item
	return item, nil
}

func sandboxApprovalFromInput(tenantID string, input SandboxApprovalInput, now time.Time) SandboxApproval {
	return SandboxApproval{
		ID:                    uuid.NewString(),
		TenantID:              tenantID,
		Protocol:              input.Protocol,
		ApprovalReference:     strings.TrimSpace(input.ApprovalReference),
		ApprovedRegion:        strings.TrimSpace(input.ApprovedRegion),
		SandboxAccount:        input.SandboxAccount,
		ContractReviewed:      input.ContractReviewed,
		RetentionReviewed:     input.RetentionReviewed,
		DataResidencyReviewed: input.DataResidencyReviewed,
		PricingReviewed:       input.PricingReviewed,
		SyntheticDataOnly:     input.SyntheticDataOnly,
		ImageExportReviewed:   input.ImageExportReviewed,
		ExpiresAt:             input.ExpiresAt.UTC(),
		CreatedAt:             now,
	}
}

func (s *MemoryStore) RevokeSandboxApproval(
	_ context.Context,
	tenantID string,
	_ string,
	id string,
	reason string,
) (SandboxApproval, error) {
	if strings.TrimSpace(reason) == "" {
		return SandboxApproval{}, ErrInvalidApproval
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.approvals[id]
	if !ok || item.TenantID != tenantID {
		return SandboxApproval{}, ErrNotFound
	}
	if item.RevokedAt != nil {
		return SandboxApproval{}, ErrConflict
	}
	now := time.Now().UTC()
	item.RevokedAt = &now
	s.approvals[id] = item
	return item, nil
}
