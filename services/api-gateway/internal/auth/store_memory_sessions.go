package auth

import (
	"context"
	"fmt"
	"time"
)

type memorySession struct {
	ID                  string
	TenantID            string
	UserID              string
	SessionType         string
	DeviceName          string
	CreatedAt           time.Time
	LastSeenAt          time.Time
	ExpiresAt           time.Time
	ReauthenticatedAt   time.Time
	LockedAt            time.Time
	SecurityEpoch       int64
	RiskLevel           RiskLevel
	RiskAction          RiskAction
	RiskPolicyVersion   string
	RiskEvidenceQuality RiskEvidenceQuality
}

func (s *MemoryStore) CreateSession(_ context.Context, input CreateSessionInput) (DeviceSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionSeq++
	now := time.Now().UTC()
	securityEpoch := int64(0)
	for _, user := range s.users {
		if user.TenantID == input.TenantID && user.ID == input.UserID && user.Status == "active" && (input.SecurityEpoch <= 0 || input.SecurityEpoch == user.SecurityEpoch) {
			securityEpoch = user.SecurityEpoch
			break
		}
	}
	if securityEpoch <= 0 {
		return DeviceSession{}, ErrInvalidCredentials
	}
	record := memorySession{
		ID: fmt.Sprintf("memory-session-%d", s.sessionSeq), TenantID: input.TenantID, UserID: input.UserID,
		SessionType: input.SessionType, DeviceName: input.DeviceName,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: input.ExpiresAt, ReauthenticatedAt: now, SecurityEpoch: securityEpoch,
		RiskLevel: normalizeRiskLevel(input.RiskLevel), RiskAction: normalizeRiskAction(input.RiskAction),
		RiskPolicyVersion: normalizeRiskPolicyVersion(input.RiskPolicyVersion), RiskEvidenceQuality: normalizeRiskEvidenceQuality(input.RiskEvidenceQuality),
	}
	s.sessions[input.TokenHash] = record
	return deviceSessionFromMemory(record, true), nil
}

func (s *MemoryStore) FindUserBySession(_ context.Context, tokenHash string, now time.Time) (User, error) {
	return s.findUserBySession(tokenHash, now, false)
}

func (s *MemoryStore) FindUserBySessionForReauthentication(_ context.Context, tokenHash string, now time.Time) (User, error) {
	return s.findUserBySession(tokenHash, now, true)
}

func (s *MemoryStore) findUserBySession(tokenHash string, now time.Time, allowLocked bool) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[tokenHash]
	if !ok || !session.ExpiresAt.After(now) || (!allowLocked && !session.LockedAt.IsZero()) {
		return User{}, ErrUnauthenticated
	}
	if now.Sub(session.LastSeenAt) >= 5*time.Minute {
		session.LastSeenAt = now
		s.sessions[tokenHash] = session
	}
	for _, user := range s.users {
		if user.ID == session.UserID && user.TenantID == session.TenantID && user.Status == "active" && user.SecurityEpoch == session.SecurityEpoch && s.tenantStatuses[user.TenantID] == "active" {
			result := user.User
			result.CurrentSessionType = session.SessionType
			result.CurrentAuthLevel = 1
			result.ReauthenticatedAt = timePointer(session.ReauthenticatedAt)
			result.CurrentRiskLevel = normalizeRiskLevel(session.RiskLevel)
			result.CurrentRiskAction = normalizeRiskAction(session.RiskAction)
			result.RiskPolicyVersion = session.RiskPolicyVersion
			return result, nil
		}
	}
	return User{}, ErrUnauthenticated
}

func (s *MemoryStore) LockSession(_ context.Context, tenantID string, userID string, tokenHash string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[tokenHash]
	if !ok || session.TenantID != tenantID || session.UserID != userID || session.SessionType != SessionTypePublicDevice || !session.ExpiresAt.After(now) {
		return false, nil
	}
	active := false
	for _, user := range s.users {
		if user.TenantID == tenantID && user.ID == userID && user.Status == "active" && user.SecurityEpoch == session.SecurityEpoch && s.tenantStatuses[user.TenantID] == "active" {
			active = true
			break
		}
	}
	if !active {
		return false, nil
	}
	if session.LockedAt.IsZero() {
		session.LockedAt = now
		s.sessions[tokenHash] = session
	}
	return true, nil
}

func (s *MemoryStore) MarkSessionReauthenticated(_ context.Context, tenantID string, userID string, tokenHash string, startedAt time.Time, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[tokenHash]
	// 验证发起后新增的锁屏优先，较早开始的验证不能把更新的锁屏解除。
	if !ok || session.TenantID != tenantID || session.UserID != userID || !session.ExpiresAt.After(now) || session.LockedAt.After(startedAt) {
		return false, nil
	}
	for _, user := range s.users {
		if user.TenantID == tenantID && user.ID == userID && user.Status == "active" && user.SecurityEpoch == session.SecurityEpoch && s.tenantStatuses[user.TenantID] == "active" {
			session.ReauthenticatedAt = now
			session.LockedAt = time.Time{}
			session.LastSeenAt = now
			s.sessions[tokenHash] = session
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStore) DeleteSession(_ context.Context, tokenHash string, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, tokenHash)
	return nil
}

func (s *MemoryStore) ListSessions(_ context.Context, tenantID string, userID string, currentTokenHash string, now time.Time) ([]DeviceSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []DeviceSession{}
	for tokenHash, record := range s.sessions {
		if record.TenantID != tenantID || record.UserID != userID || !record.ExpiresAt.After(now) {
			continue
		}
		out = append(out, deviceSessionFromMemory(record, tokenHash == currentTokenHash))
	}
	return out, nil
}

func (s *MemoryStore) RevokeSession(_ context.Context, tenantID string, userID string, sessionID string, _ string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tokenHash, record := range s.sessions {
		if record.ID == sessionID && record.TenantID == tenantID && record.UserID == userID {
			delete(s.sessions, tokenHash)
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStore) RevokeAllSessions(_ context.Context, tenantID string, userID string, _ string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revokeAllSessionsLocked(tenantID, userID), nil
}

// 调用方持有账户变更写锁；会话与受信设备必须一起撤销。
func (s *MemoryStore) revokeAllSessionsLocked(tenantID, userID string) int {
	revoked := 0
	for tokenHash, record := range s.sessions {
		if record.TenantID == tenantID && record.UserID == userID {
			delete(s.sessions, tokenHash)
			revoked++
		}
	}
	for tokenHash, device := range s.devices {
		if device.TenantID == tenantID && device.UserID == userID {
			delete(s.devices, tokenHash)
		}
	}
	return revoked
}

func (s *MemoryStore) UpdatePasswordAndRevokeSessions(_ context.Context, tenantID, userID, expectedPasswordHash, newPasswordHash string) (bool, int, error) {
	// 改密、推进安全版本及撤销已存会话同锁完成；旧版本在途登录也会被拒绝。
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for key, user := range s.users {
		if user.TenantID == tenantID && user.ID == userID && user.Status == "active" && user.PasswordHash == expectedPasswordHash {
			user.PasswordHash = newPasswordHash
			user.MustChangePassword = false
			user.SecurityEpoch++
			s.users[key] = user
			found = true
			break
		}
	}
	if !found {
		return false, 0, nil
	}
	revoked := 0
	for tokenHash, record := range s.sessions {
		if record.TenantID == tenantID && record.UserID == userID {
			delete(s.sessions, tokenHash)
			revoked++
		}
	}
	for tokenHash, device := range s.devices {
		if device.TenantID == tenantID && device.UserID == userID {
			delete(s.devices, tokenHash)
		}
	}
	return true, revoked, nil
}

func deviceSessionFromMemory(record memorySession, current bool) DeviceSession {
	return DeviceSession{
		ID: record.ID, SessionType: record.SessionType, DeviceName: record.DeviceName,
		CreatedAt: record.CreatedAt, LastSeenAt: record.LastSeenAt, ExpiresAt: record.ExpiresAt, Current: current,
	}
}

// 调用方持有写锁；推进安全版本和清除会话、设备凭据必须处于同一次账户变更中。
func (s *MemoryStore) revokeUserCredentials(tenantID, userID string) {
	for tokenHash, session := range s.sessions {
		if session.TenantID == tenantID && session.UserID == userID {
			delete(s.sessions, tokenHash)
		}
	}
	for tokenHash, device := range s.devices {
		if device.TenantID == tenantID && device.UserID == userID {
			delete(s.devices, tokenHash)
		}
	}
}
