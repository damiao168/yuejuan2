package modelgovernance

import (
	"context"
	"github.com/google/uuid"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	mu                    sync.RWMutex
	providers             map[string]Provider
	deployments           map[string]Deployment
	policies              map[string]TenantPolicy
	approvals             map[string]SandboxApproval
	evaluations           map[string]EvaluationRun
	candidates            map[string]EvaluationCandidate
	modelApprovals        map[string]ModelApproval
	managedConfigs        map[string]ManagedAPIConfig
	managedSecrets        map[string]string
	roleBindings          map[string]ModelRoleBinding
	documentSelections    map[string]DocumentModelSelection
	answerSheetSelections map[string]DocumentModelSelection
	gradingSelections     map[string]DocumentModelSelection
	chatAccess            map[string]ChatModelAccess
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		providers:             map[string]Provider{},
		deployments:           map[string]Deployment{},
		policies:              map[string]TenantPolicy{},
		approvals:             map[string]SandboxApproval{},
		evaluations:           map[string]EvaluationRun{},
		candidates:            map[string]EvaluationCandidate{},
		modelApprovals:        map[string]ModelApproval{},
		managedConfigs:        map[string]ManagedAPIConfig{},
		managedSecrets:        map[string]string{},
		roleBindings:          map[string]ModelRoleBinding{},
		documentSelections:    map[string]DocumentModelSelection{},
		answerSheetSelections: map[string]DocumentModelSelection{},
		gradingSelections:     map[string]DocumentModelSelection{},
	}
}

func (s *MemoryStore) ValidateProductionReadiness(_ context.Context, secrets SecretReferenceResolver) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	providers := make([]Provider, 0, len(s.providers))
	for _, item := range s.providers {
		providers = append(providers, item)
	}
	deployments := make([]Deployment, 0, len(s.deployments))
	for _, item := range s.deployments {
		deployments = append(deployments, item)
	}
	return ValidateProductionInventory(providers, deployments, secrets)
}

func (s *MemoryStore) EnsureLocalBaseline(_ context.Context, tenantID string, baseline LocalBaseline) error {
	if strings.TrimSpace(tenantID) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	providerID := ""
	for id, item := range s.providers {
		if item.TenantID == tenantID && item.Key == baseline.ProviderKey {
			providerID = id
			item.DisplayName = baseline.ProviderName
			item.AdapterType = baseline.AdapterType
			item.Region = baseline.Region
			item.UpdatedAt = now
			s.providers[id] = item
			break
		}
	}
	if providerID == "" {
		providerID = uuid.NewString()
		s.providers[providerID] = Provider{
			ID:          providerID,
			TenantID:    tenantID,
			Key:         baseline.ProviderKey,
			DisplayName: baseline.ProviderName,
			Kind:        ProviderLocal,
			AdapterType: baseline.AdapterType,
			Region:      baseline.Region,
			DataPolicy:  DataPolicy{RetentionMode: "no_store"},
			Status:      "active",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	}
	for id, item := range s.deployments {
		if item.TenantID == tenantID && item.Key == baseline.DeploymentKey {
			item.ProviderID = providerID
			item.ProviderKey = baseline.ProviderKey
			item.ModelName = baseline.ModelName
			item.ModelVersion = baseline.ModelVersion
			item.Region = baseline.Region
			item.CapabilityProfile = baseline.CapabilityProfile
			item.UpdatedAt = now
			s.deployments[id] = item
			return nil
		}
	}
	id := uuid.NewString()
	s.deployments[id] = Deployment{
		ID:                id,
		TenantID:          tenantID,
		ProviderID:        providerID,
		ProviderKey:       baseline.ProviderKey,
		Key:               baseline.DeploymentKey,
		ModelName:         baseline.ModelName,
		ModelVersion:      baseline.ModelVersion,
		Region:            baseline.Region,
		CapabilityProfile: baseline.CapabilityProfile,
		Modalities:        []string{"text"},
		CapabilityPolicy:  map[string]any{},
		PricingPolicy:     map[string]any{"meter": "local_compute"},
		Status:            "shadow_only",
		HealthState:       "unverified",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	return nil
}
