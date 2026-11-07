package modelgovernance

import (
	"context"
	"github.com/google/uuid"
	"net/url"
	"sort"
	"strings"
	"time"
)

func (s *MemoryStore) ListProviders(_ context.Context, tenantID string) ([]Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Provider{}
	for _, item := range s.providers {
		if item.TenantID == tenantID {
			out = append(out, sanitizeProvider(item))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *MemoryStore) CreateProvider(_ context.Context, tenantID string, _ string, input ProviderInput) (Provider, error) {
	provider := ProviderFromInput(input)
	provider.TenantID = tenantID
	provider.DisplayName = strings.TrimSpace(provider.DisplayName)
	if provider.Status == "" {
		provider.Status = "unverified"
	}
	if provider.DisplayName == "" || ValidateProvider(provider) != nil {
		return Provider{}, ErrInvalidProvider
	}
	if provider.Kind == ProviderExternal && provider.Status != "unverified" && provider.Status != "disabled" {
		return Provider{}, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.providers {
		if item.TenantID == tenantID && item.Key == provider.Key {
			return Provider{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	provider.ID = uuid.NewString()
	provider.CreatedAt = now
	provider.UpdatedAt = now
	s.providers[provider.ID] = provider
	return sanitizeProvider(provider), nil
}

func (s *MemoryStore) UpdateProviderStatus(_ context.Context, tenantID string, id string, input ProviderStatusInput) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.providers[id]
	if !ok || item.TenantID != tenantID {
		return Provider{}, ErrNotFound
	}
	item.Status = input.Status
	if ValidateProvider(item) != nil {
		return Provider{}, ErrInvalidProvider
	}
	if item.Kind == ProviderExternal && item.Status != "unverified" && item.Status != "disabled" {
		return Provider{}, ErrConflict
	}
	item.UpdatedAt = time.Now().UTC()
	s.providers[id] = item
	return sanitizeProvider(item), nil
}

func (s *MemoryStore) ListDeployments(_ context.Context, tenantID string) ([]Deployment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Deployment{}
	for _, item := range s.deployments {
		if item.TenantID == tenantID {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *MemoryStore) CreateDeployment(_ context.Context, tenantID string, _ string, input DeploymentInput) (Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	provider, ok := s.providers[input.ProviderID]
	if !ok || provider.TenantID != tenantID {
		return Deployment{}, ErrNotFound
	}
	deployment := DeploymentFromInput(input, provider.Key)
	deployment.TenantID = tenantID
	if deployment.Status == "" {
		deployment.Status = "unverified"
	}
	if deployment.HealthState == "" {
		deployment.HealthState = "unverified"
	}
	if strings.TrimSpace(deployment.ModelName) == "" || ValidateDeployment(deployment, provider) != nil {
		return Deployment{}, ErrInvalidDeployment
	}
	if provider.Kind == ProviderExternal &&
		(deployment.Status != "unverified" || deployment.HealthState != "unverified") {
		return Deployment{}, ErrConflict
	}
	for _, item := range s.deployments {
		if item.TenantID == tenantID && item.Key == deployment.Key {
			return Deployment{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	deployment.ID = uuid.NewString()
	deployment.CreatedAt = now
	deployment.UpdatedAt = now
	s.deployments[deployment.ID] = deployment
	return deployment, nil
}

func (s *MemoryStore) UpdateDeploymentState(_ context.Context, tenantID string, id string, input DeploymentStateInput) (Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.deployments[id]
	if !ok || item.TenantID != tenantID {
		return Deployment{}, ErrNotFound
	}
	provider, ok := s.providers[item.ProviderID]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	item.Status = input.Status
	item.HealthState = input.HealthState
	if ValidateDeployment(item, provider) != nil {
		return Deployment{}, ErrInvalidDeployment
	}
	externalStateAllowed := item.Status == "unverified" && item.HealthState == "unverified" ||
		item.Status == "disabled" &&
			(item.HealthState == "unverified" || item.HealthState == "unavailable" || item.HealthState == "disabled")
	if provider.Kind == ProviderExternal && !externalStateAllowed {
		return Deployment{}, ErrConflict
	}
	item.UpdatedAt = time.Now().UTC()
	s.deployments[id] = item
	return item, nil
}

// 对外返回供应商时只保留密钥是否配置和 scheme，绝不把 credential_ref 原文带出存储边界。
func sanitizeProvider(provider Provider) Provider {
	provider.CredentialConfigured = provider.CredentialRef != ""
	if parsed, err := url.Parse(provider.CredentialRef); err == nil {
		provider.CredentialScheme = parsed.Scheme
	}
	provider.CredentialRef = ""
	return provider
}
