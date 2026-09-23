package modelgovernance

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *MemoryStore) ListManagedAPIConfigs(_ context.Context, tenantID string) ([]ManagedAPIConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []ManagedAPIConfig{}
	for _, item := range s.managedConfigs {
		if item.TenantID == tenantID && item.DeletedAt == nil {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDefault != items[j].IsDefault {
			return items[i].IsDefault
		}
		return items[i].ProviderKey < items[j].ProviderKey
	})
	return items, nil
}

func (s *MemoryStore) CreateManagedAPIConfig(_ context.Context, tenantID, _ string, input ManagedAPIConfigInput) (ManagedAPIConfig, error) {
	input.TenantID = tenantID
	normalized, err := normalizeManagedAPIInput(input, true)
	if err != nil {
		return ManagedAPIConfig{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.managedConfigs {
		if item.TenantID == tenantID && item.DeletedAt == nil && item.ProviderKey == normalized.ProviderKey && item.ModelName == normalized.ModelName {
			return ManagedAPIConfig{}, ErrConflict
		}
	}
	if normalized.IsDefault {
		s.clearManagedDefault(tenantID, "")
	}
	now := time.Now().UTC()
	id := uuid.NewString()
	item := ManagedAPIConfig{
		ID: id, TenantID: tenantID, ProviderKey: normalized.ProviderKey,
		DisplayName: normalized.DisplayName, AdapterType: normalized.AdapterType,
		BaseURL: normalized.BaseURL, ModelName: normalized.ModelName, ModelVersion: normalized.ModelVersion,
		Region: normalized.Region, CredentialConfigured: true, CredentialHint: credentialHint(normalized.APIKey),
		Status: normalized.Status, IsDefault: normalized.IsDefault, LastTestStatus: "untested",
		LastCapabilityStatus: "untested",
		ConfigSource:         normalized.ConfigSource, ProviderRegistryVersion: normalized.ProviderRegistryVersion,
		CreatedAt: now, UpdatedAt: now,
	}
	if normalized.InitialProbe != nil {
		applyManagedProbe(&item, *normalized.InitialProbe, now)
	}
	s.managedConfigs[id] = item
	s.managedSecrets[id] = normalized.APIKey
	return item, nil
}

func (s *MemoryStore) UpdateManagedAPIConfig(_ context.Context, tenantID, id string, input ManagedAPIConfigUpdateInput) (ManagedAPIConfig, error) {
	normalized, err := normalizeManagedAPIUpdate(input)
	if err != nil {
		return ManagedAPIConfig{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.managedConfigs[id]
	if !ok || item.TenantID != tenantID || item.DeletedAt != nil {
		return ManagedAPIConfig{}, ErrNotFound
	}
	for otherID, other := range s.managedConfigs {
		if otherID != id && other.TenantID == tenantID && other.ProviderKey == item.ProviderKey && other.ModelName == normalized.ModelName {
			return ManagedAPIConfig{}, ErrConflict
		}
	}
	if normalized.IsDefault {
		s.clearManagedDefault(tenantID, id)
	}
	connectionChanged := item.AdapterType != normalized.AdapterType || item.BaseURL != normalized.BaseURL ||
		item.ModelName != normalized.ModelName || normalized.APIKey != ""
	item.DisplayName = normalized.DisplayName
	item.AdapterType = normalized.AdapterType
	item.BaseURL = normalized.BaseURL
	item.ModelName = normalized.ModelName
	item.ModelVersion = normalized.ModelVersion
	item.Region = normalized.Region
	item.Status = normalized.Status
	item.IsDefault = normalized.IsDefault
	item.UpdatedAt = time.Now().UTC()
	if normalized.APIKey != "" {
		s.managedSecrets[id] = normalized.APIKey
		item.CredentialHint = credentialHint(normalized.APIKey)
	}
	if connectionChanged {
		resetManagedProbeEvidence(&item)
	}
	if normalized.InitialProbe != nil {
		applyManagedProbe(&item, *normalized.InitialProbe, item.UpdatedAt)
	}
	s.managedConfigs[id] = item
	return item, nil
}

func resetManagedProbeEvidence(item *ManagedAPIConfig) {
	item.LastTestStatus = "untested"
	item.LastTestMessage = ""
	item.LastTestLatencyMS = 0
	item.LastTestedAt = nil
	item.LastSuccessfulTestedAt = nil
	item.LastProbeMode = ""
	item.LastCapabilityStatus = "untested"
	item.LastCapabilityMessage = ""
	item.LastCapabilityTestedAt = nil
	item.LastCapabilityVersion = ""
	item.LastCapabilityUsage = ManagedAPIProbeUsage{}
	item.LastCapabilityDiagnostic = ManagedAPIProbeDiagnostic{}
}

func (s *MemoryStore) DeleteManagedAPIConfig(_ context.Context, tenantID, actorID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.managedConfigs[id]
	if !ok || item.TenantID != tenantID || item.DeletedAt != nil {
		return ErrNotFound
	}
	if item.IsDefault {
		return ErrManagedDefaultMutation
	}
	for _, binding := range s.roleBindings {
		if binding.TenantID == tenantID && binding.ManagedModelAPIConfigID == id && binding.Status == "active" {
			return ErrManagedConfigInUse
		}
	}
	now := time.Now().UTC()
	item.DeletedAt, item.DeletedBy = &now, actorID
	item.Status, item.IsDefault, item.UpdatedAt = "disabled", false, now
	s.managedConfigs[id] = item
	delete(s.managedSecrets, id)
	return nil
}

func (s *MemoryStore) GetManagedAPIConnection(_ context.Context, tenantID, id string) (ManagedAPIConnection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.managedConfigs[id]
	if !ok || item.TenantID != tenantID || item.DeletedAt != nil {
		return ManagedAPIConnection{}, ErrNotFound
	}
	secret := s.managedSecrets[id]
	if strings.TrimSpace(secret) == "" {
		return ManagedAPIConnection{}, ErrManagedConfigUnavailable
	}
	return ManagedAPIConnection{Config: item, APIKey: secret}, nil
}

func (s *MemoryStore) RecordManagedAPIProbe(_ context.Context, tenantID, id string, expectedUpdatedAt time.Time, result ManagedAPIProbeResult) (ManagedAPIConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.managedConfigs[id]
	if !ok || item.TenantID != tenantID || item.DeletedAt != nil {
		return ManagedAPIConfig{}, ErrNotFound
	}
	if !item.UpdatedAt.Equal(expectedUpdatedAt) {
		return ManagedAPIConfig{}, ErrManagedProbeStale
	}
	now := time.Now().UTC()
	applyManagedProbe(&item, result, now)
	s.managedConfigs[id] = item
	return item, nil
}

func applyManagedProbe(item *ManagedAPIConfig, result ManagedAPIProbeResult, testedAt time.Time) {
	item.LastTestStatus = "failed"
	connectionOK := result.OK || (result.ProbeMode == "capability" && result.CredentialCheck.OK && result.ModelCheck.OK)
	transientFailure := !result.OK && transientManagedProbeFailure(result)
	if transientFailure {
		item.LastTestStatus = "temporary_unavailable"
	} else if connectionOK {
		item.LastTestStatus = "success"
		item.LastSuccessfulTestedAt = &testedAt
	}
	if connectionOK && !result.OK && !transientFailure {
		item.LastTestMessage = "连接正常，结构化能力检测未通过"
	} else {
		item.LastTestMessage = strings.TrimSpace(result.Message)
	}
	item.LastTestLatencyMS = result.LatencyMS
	item.LastTestedAt = &testedAt
	item.LastProbeMode = result.ProbeMode
	if item.LastProbeMode == "" {
		item.LastProbeMode = "capability"
	}
	if item.LastProbeMode == "capability" && !result.Reused && !transientFailure &&
		(result.GeneratedRequest || result.CapabilityCheck.OK || result.CapabilityCheck.Code != "") {
		item.LastCapabilityStatus = "failed"
		if result.CapabilityCheck.OK {
			item.LastCapabilityStatus = "success"
		}
		item.LastCapabilityMessage = strings.TrimSpace(result.CapabilityCheck.Message)
		if item.LastCapabilityMessage == "" {
			item.LastCapabilityMessage = strings.TrimSpace(result.Message)
		}
		item.LastCapabilityTestedAt = &testedAt
		item.LastCapabilityVersion = ManagedCapabilityProbeVersion
		item.LastCapabilityUsage = result.Usage
		item.LastCapabilityDiagnostic = result.Diagnostic
		item.LastCapabilityDiagnostic.ContentPreview = ""
	}
}

func transientManagedProbeFailure(result ManagedAPIProbeResult) bool {
	return result.ErrorCode == "provider_timeout" || result.ErrorCode == "provider_unavailable"
}

func (s *MemoryStore) clearManagedDefault(tenantID, exceptID string) {
	for id, item := range s.managedConfigs {
		if item.TenantID == tenantID && id != exceptID && item.IsDefault {
			item.IsDefault = false
			item.UpdatedAt = time.Now().UTC()
			s.managedConfigs[id] = item
		}
	}
}

var _ ManagedAPIConfigStore = (*MemoryStore)(nil)
