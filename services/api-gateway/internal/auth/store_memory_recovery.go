package auth

import (
	"context"
	"time"
)

type memoryRecovery struct {
	TenantID      string
	UserID        string
	ExpiresAt     time.Time
	UsedAt        time.Time
	SecurityEpoch int64
}

func (s *MemoryStore) CreateRecovery(_ context.Context, actor User, actorScope AccessScope, userID, tokenHash string, expiresAt time.Time) (RecoveryPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.users {
		if user.TenantID != actor.TenantID || user.ID != userID || user.Status != "active" {
			continue
		}
		if _, err := managedUserAuthority(actor, actorScope, user); err != nil {
			return RecoveryPreview{}, err
		}
		for hash, recovery := range s.recoveries {
			if recovery.TenantID == user.TenantID && recovery.UserID == user.ID && recovery.UsedAt.IsZero() {
				recovery.UsedAt = time.Now().UTC()
				s.recoveries[hash] = recovery
			}
		}
		s.recoveries[tokenHash] = memoryRecovery{TenantID: user.TenantID, UserID: user.ID, ExpiresAt: expiresAt, SecurityEpoch: user.SecurityEpoch}
		return RecoveryPreview{PasswordMinLength: PasswordMinimum(user.Roles), DisplayName: user.DisplayName, TenantCode: user.TenantCode, PhoneMasked: MaskPhone(user.PhoneNormalized), ExpiresAt: expiresAt}, nil
	}
	return RecoveryPreview{}, ErrManagedUserNotFound
}

func (s *MemoryStore) FindRecovery(_ context.Context, tokenHash string, now time.Time) (RecoveryPreview, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	recovery, ok := s.recoveries[tokenHash]
	if !ok || !recovery.UsedAt.IsZero() || !recovery.ExpiresAt.After(now) {
		return RecoveryPreview{}, ErrRecoveryInvalid
	}
	for _, user := range s.users {
		if user.TenantID == recovery.TenantID && user.ID == recovery.UserID && user.Status == "active" && user.SecurityEpoch == recovery.SecurityEpoch && s.tenantStatuses[user.TenantID] == "active" {
			return RecoveryPreview{PasswordMinLength: PasswordMinimum(user.Roles), DisplayName: user.DisplayName, TenantCode: user.TenantCode, PhoneMasked: MaskPhone(user.PhoneNormalized), ExpiresAt: recovery.ExpiresAt}, nil
		}
	}
	return RecoveryPreview{}, ErrRecoveryInvalid
}

func (s *MemoryStore) CompleteRecovery(_ context.Context, tokenHash string, passwordHash string, now time.Time) (RecoveryResult, error) {
	// 恢复凭据绑定发放时的安全版本，期间改密或禁用后不能再使用旧恢复链接。
	s.mu.Lock()
	defer s.mu.Unlock()
	recovery, ok := s.recoveries[tokenHash]
	if !ok || !recovery.UsedAt.IsZero() || !recovery.ExpiresAt.After(now) {
		return RecoveryResult{}, ErrRecoveryInvalid
	}
	for key, user := range s.users {
		if user.TenantID != recovery.TenantID || user.ID != recovery.UserID || user.Status != "active" || user.SecurityEpoch != recovery.SecurityEpoch || s.tenantStatuses[user.TenantID] != "active" {
			continue
		}
		user.PasswordHash = passwordHash
		user.MustChangePassword = false
		user.SecurityEpoch++
		s.users[key] = user
		recovery.UsedAt = now
		s.recoveries[tokenHash] = recovery
		s.revokeUserCredentials(user.TenantID, user.ID)
		return RecoveryResult{TenantID: user.TenantID, UserID: user.ID}, nil
	}
	return RecoveryResult{}, ErrRecoveryInvalid
}
