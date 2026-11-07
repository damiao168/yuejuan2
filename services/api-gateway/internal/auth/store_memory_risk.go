package auth

import (
	"context"
	"time"
)

type memoryDeviceBinding struct {
	TenantID       string
	UserID         string
	UserAgentHash  string
	IPPrefix       string
	AssuranceLevel int
	TrustBasis     string
	LastSeenAt     time.Time
	ExpiresAt      time.Time
}

func (s *MemoryStore) LoadLoginRiskContext(_ context.Context, request LoginRiskContextRequest) (LoginRiskContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := request.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	history := s.loginRiskHistory(request, now)
	result := LoginRiskContext{PriorSuccessfulLogins: len(history)}
	for _, event := range history {
		if request.UserAgentHash != "" && event.UserAgentHash == request.UserAgentHash {
			result.KnownUserAgent = true
		}
		if request.IPPrefix != "" && event.IPPrefix == request.IPPrefix {
			result.KnownNetwork = true
		}
	}
	if device, ok := s.loginRiskDevice(request, now); ok {
		result.KnownDevice = true
		result.TrustedDevice = device.AssuranceLevel >= 2
	}
	result.RecentFailures = s.recentLoginFailures(request, now)
	result.RecentRecovery = s.hasRecentRecovery(request, now)
	return result, nil
}

// 风险历史和设备均在调用方的读锁内读取，只统计当前租户、账户的事实。
func (s *MemoryStore) loginRiskHistory(request LoginRiskContextRequest, now time.Time) []RiskEvent {
	cutoff := now.Add(-RiskHistoryRetention)
	history := make([]RiskEvent, 0, RiskHistoryLimit)
	for index := len(s.riskEvents) - 1; index >= 0 && len(history) < RiskHistoryLimit; index-- {
		event := s.riskEvents[index]
		if event.TenantID != request.TenantID || event.UserID != request.UserID || event.Purpose != RiskPurposeLogin || event.OccurredAt.Before(cutoff) {
			continue
		}
		history = append(history, event)
	}
	return history
}

func (s *MemoryStore) loginRiskDevice(request LoginRiskContextRequest, now time.Time) (memoryDeviceBinding, bool) {
	device, ok := s.devices[request.DeviceTokenHash]
	if request.DeviceTokenHash == "" || !ok || device.TenantID != request.TenantID || device.UserID != request.UserID || !device.ExpiresAt.After(now) {
		return memoryDeviceBinding{}, false
	}
	return device, true
}

func (s *MemoryStore) recentLoginFailures(request LoginRiskContextRequest, now time.Time) int {
	failures := 0
	failureCutoff := now.Add(-RiskFailureWindow)
	for _, record := range s.audits {
		if record.TenantID == request.TenantID && record.ActorID == request.UserID && !record.CreatedAt.Before(failureCutoff) &&
			(record.Action == "auth.login_failed" || record.Action == "auth.login_rate_limited") {
			failures++
		}
	}
	return failures
}

func (s *MemoryStore) hasRecentRecovery(request LoginRiskContextRequest, now time.Time) bool {
	recoveryCutoff := now.Add(-RiskRecoveryWindow)
	for _, recovery := range s.recoveries {
		if recovery.TenantID == request.TenantID && recovery.UserID == request.UserID && !recovery.UsedAt.IsZero() && !recovery.UsedAt.Before(recoveryCutoff) {
			return true
		}
	}
	return false
}

func (s *MemoryStore) RecordRiskEvent(_ context.Context, event RiskEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	event.ReasonCodes = append([]string(nil), event.ReasonCodes...)
	event.FamilyScores = cloneIntMap(event.FamilyScores)
	s.riskEvents = append(s.riskEvents, event)
	cutoff := event.OccurredAt.Add(-RiskHistoryRetention)
	kept := make([]RiskEvent, 0, len(s.riskEvents))
	loginCount := 0
	for index := len(s.riskEvents) - 1; index >= 0; index-- {
		candidate := s.riskEvents[index]
		if candidate.OccurredAt.Before(cutoff) {
			continue
		}
		if candidate.TenantID == event.TenantID && candidate.UserID == event.UserID && candidate.Purpose == RiskPurposeLogin {
			loginCount++
			if loginCount > RiskHistoryLimit {
				continue
			}
		}
		kept = append(kept, candidate)
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	s.riskEvents = kept
	return nil
}

func (s *MemoryStore) StoreObservedDevice(_ context.Context, input ObservedDeviceInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.devices[input.TokenHash]; ok && (existing.TenantID != input.TenantID || existing.UserID != input.UserID) {
		return ErrForbidden
	}
	assuranceLevel := input.AssuranceLevel
	if assuranceLevel < 1 {
		assuranceLevel = 1
	}
	if assuranceLevel > 3 {
		assuranceLevel = 3
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.devices[input.TokenHash] = memoryDeviceBinding{
		TenantID: input.TenantID, UserID: input.UserID, UserAgentHash: input.UserAgentHash, IPPrefix: input.IPPrefix,
		AssuranceLevel: assuranceLevel, TrustBasis: input.TrustBasis, LastSeenAt: now, ExpiresAt: input.ExpiresAt,
	}
	return nil
}

func (s *MemoryStore) RiskEvents() []RiskEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RiskEvent, len(s.riskEvents))
	for index, event := range s.riskEvents {
		event.ReasonCodes = append([]string(nil), event.ReasonCodes...)
		event.FamilyScores = cloneIntMap(event.FamilyScores)
		out[index] = event
	}
	return out
}

func cloneIntMap(value map[string]int) map[string]int {
	if len(value) == 0 {
		return map[string]int{}
	}
	out := make(map[string]int, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}
