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
	if normalized.IsDefault {
		s.preserveInheritedChatAccessLocked(tenantID, "", true)
		s.clearManagedDefault(tenantID, "")
	}
	now := time.Now().UTC()
	id := uuid.NewString()
	item := ManagedAPIConfig{
		ID: id, TenantID: tenantID, ProviderKey: normalized.ProviderKey,
		DisplayName: normalized.DisplayName, AdapterType: normalized.AdapterType,
		BaseURL: normalized.BaseURL, ModelName: normalized.ModelName, ModelVersion: normalized.ModelVersion,
		Region: normalized.Region, CredentialConfigured: true, CredentialHint: credentialHint(normalized.APIKey),
		Modalities: []string{"text"}, CapabilityProfile: "general",
		PricingPolicy: map[string]any{},
		DataPolicy:    map[string]any{"training_allowed": false, "retention_mode": "no_store"},
		HealthState:   "unverified",
		Status:        normalized.Status, IsDefault: normalized.IsDefault, LastTestStatus: "untested",
		LastTextStatus: "untested", LastCapabilityStatus: "untested",
		ConfigSource: normalized.ConfigSource, ProviderRegistryVersion: normalized.ProviderRegistryVersion,
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
	// 先核对检测所用版本再切换默认项或密钥；迟到请求失败时不改变任何已保存配置。
	if normalized.InitialProbe != nil && normalized.ExpectedUpdatedAt.IsZero() ||
		!normalized.ExpectedUpdatedAt.IsZero() && !item.UpdatedAt.Equal(normalized.ExpectedUpdatedAt) {
		return ManagedAPIConfig{}, ErrManagedProbeStale
	}
	s.preserveInheritedChatAccessLocked(tenantID, id, normalized.IsDefault)
	if normalized.IsDefault {
		s.clearManagedDefault(tenantID, id)
	}
	connectionChanged := item.AdapterType != normalized.AdapterType || item.BaseURL != normalized.BaseURL ||
		item.ModelName != normalized.ModelName || item.ModelVersion != normalized.ModelVersion ||
		item.Region != normalized.Region || normalized.APIKey != ""
	item.DisplayName = normalized.DisplayName
	item.AdapterType = normalized.AdapterType
	item.BaseURL = normalized.BaseURL
	item.ModelName = normalized.ModelName
	item.ModelVersion = normalized.ModelVersion
	item.Region = normalized.Region
	item.Status = normalized.Status
	item.IsDefault = normalized.IsDefault
	item.UpdatedAt = nextManagedConfigUpdatedAt(item.UpdatedAt)
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
	item.LastTextStatus = "untested"
	item.LastTextMessage = ""
	item.LastTextTestedAt = nil
	item.LastTextUsage = ManagedAPIProbeUsage{}
	item.LastTextDiagnostic = ManagedAPIProbeDiagnostic{}
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
	if err := s.managedConfigDeletionDependencyLocked(tenantID, id); err != nil {
		return err
	}
	if policy, ok := s.policies[tenantID]; ok && contains(policy.AllowedModelConfigIDs, id) {
		return ErrConflict
	}
	now := time.Now().UTC()
	item.DeletedAt, item.DeletedBy = &now, actorID
	item.Status, item.IsDefault, item.UpdatedAt = "disabled", false, now
	s.managedConfigs[id] = item
	delete(s.managedSecrets, id)
	return nil
}

// 调用方必须持有写锁；依赖校验和软删除不能被新增绑定或审批穿插。
func (s *MemoryStore) managedConfigDeletionDependencyLocked(tenantID, id string) error {
	if selection := s.answerSheetSelections[tenantID]; selection.Mode == "model" && selection.ModelConfigID == id {
		return ErrManagedConfigInUse
	}
	if selection := s.gradingSelections[tenantID]; selection.Mode == "model" && selection.ModelConfigID == id {
		return ErrManagedConfigInUse
	}
	if selection := s.documentSelections[tenantID]; selection.Mode == "model" && selection.ModelConfigID == id {
		return ErrManagedConfigInUse
	}
	for _, binding := range s.roleBindings {
		if binding.TenantID == tenantID && binding.ManagedModelAPIConfigID == id && binding.Status == "active" {
			return ErrManagedConfigInUse
		}
	}
	for _, approval := range s.modelApprovals {
		if approval.TenantID == tenantID && approval.ModelConfigID == id && approval.IsActive(time.Now().UTC()) {
			return ErrConflict
		}
	}
	for _, approval := range s.approvals {
		if approval.TenantID == tenantID && approval.ModelConfigID == id && approval.IsActive(time.Now().UTC()) {
			return ErrConflict
		}
	}
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

// 只接受探测开始时的更新时间，配置在请求期间被修改就丢弃结果，避免旧探测覆盖新密钥。
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

// 三种检测独立记录证据；临时故障只更新本次状态，不撤销已有文本或结构化结果。
// quick 和复用结果不产生新生成证据，响应预览也不会写入存储。
func applyManagedProbe(item *ManagedAPIConfig, result ManagedAPIProbeResult, testedAt time.Time) {
	item.LastTestStatus = "failed"
	connectionOK := managedProbeConnectionOK(result)
	transientFailure := !result.OK && transientManagedProbeFailure(result)
	if transientFailure {
		item.LastTestStatus = "temporary_unavailable"
	} else if connectionOK {
		item.LastTestStatus = "success"
		item.LastSuccessfulTestedAt = &testedAt
	}
	if connectionOK && !result.OK && !transientFailure {
		item.LastTestMessage = "连接正常，生成检测未通过：" + strings.TrimSpace(result.Message)
	} else {
		item.LastTestMessage = strings.TrimSpace(result.Message)
	}
	item.LastTestLatencyMS = result.LatencyMS
	item.LastTestedAt = &testedAt
	item.LastProbeMode = result.ProbeMode
	if item.LastProbeMode == "" {
		item.LastProbeMode = "capability"
	}
	if result.Reused || transientFailure {
		return
	}
	if result.ProbeMode == "text" || managedProbeTextResponse(result) && managedStructuredProbe(result.ProbeMode) {
		item.LastTextStatus = "failed"
		if managedProbeTextResponse(result) {
			item.LastTextStatus = "success"
		}
		item.LastTextMessage = boundedManagedProbeMessage(result.TextCheck.Message)
		if item.LastTextMessage == "" {
			item.LastTextMessage = boundedManagedProbeMessage(result.Message)
		}
		item.LastTextTestedAt = &testedAt
		item.LastTextUsage = result.Usage
		item.LastTextDiagnostic = result.Diagnostic
		item.LastTextDiagnostic.ContentPreview = ""
	}
	if managedStructuredProbe(item.LastProbeMode) &&
		(result.GeneratedRequest || result.CapabilityCheck.OK || result.CapabilityCheck.Code != "") {
		applyManagedCapabilityProbe(item, result, &testedAt)
	}
}

func applyManagedCapabilityProbe(item *ManagedAPIConfig, result ManagedAPIProbeResult, testedAt *time.Time) {
	item.LastCapabilityStatus = "failed"
	if result.CapabilityCheck.OK {
		item.LastCapabilityStatus = "success"
	}
	item.LastCapabilityMessage = strings.TrimSpace(result.CapabilityCheck.Message)
	if item.LastCapabilityMessage == "" {
		item.LastCapabilityMessage = strings.TrimSpace(result.Message)
	}
	item.LastCapabilityTestedAt = testedAt
	item.LastCapabilityVersion = ManagedCapabilityProbeVersion
	item.LastCapabilityUsage = result.Usage
	item.LastCapabilityDiagnostic = result.Diagnostic
	item.LastCapabilityDiagnostic.ContentPreview = ""
}

func transientManagedProbeFailure(result ManagedAPIProbeResult) bool {
	if result.ErrorCode == "provider_timeout" || result.ErrorCode == "provider_unavailable" || result.ErrorCode == "provider_rate_limited" {
		return true
	}
	return result.StatusCode == 408 || result.StatusCode == 425 || result.StatusCode == 429 || result.StatusCode >= 500 ||
		result.ErrorCode == "provider_error" && result.StatusCode == 0
}

func managedProbeConnectionOK(result ManagedAPIProbeResult) bool {
	return result.OK || managedProbeTextResponse(result) || result.CredentialCheck.OK && result.ModelCheck.OK &&
		result.StatusCode != 401 && result.StatusCode != 403 && result.StatusCode != 404 && !transientManagedProbeFailure(result)
}

func (s *MemoryStore) clearManagedDefault(tenantID, exceptID string) {
	for id, item := range s.managedConfigs {
		if item.TenantID == tenantID && id != exceptID && item.IsDefault {
			item.IsDefault = false
			item.UpdatedAt = nextManagedConfigUpdatedAt(item.UpdatedAt)
			s.managedConfigs[id] = item
		}
	}
}

// 更新时间承担配置版本标识；系统时钟回退也不能让新修改重复旧版本。
func nextManagedConfigUpdatedAt(previous time.Time) time.Time {
	now := time.Now().UTC()
	if !now.After(previous) {
		return previous.Add(time.Nanosecond)
	}
	return now
}

var _ ManagedAPIConfigStore = (*MemoryStore)(nil)
