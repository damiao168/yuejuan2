package auth

import (
	"context"
	"time"
)

type memoryActivation struct {
	TenantID  string
	UserID    string
	ExpiresAt time.Time
	UsedAt    time.Time
}

func (s *MemoryStore) FindActivation(_ context.Context, tokenHash string, now time.Time) (ActivationPreview, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	activation, ok := s.activations[tokenHash]
	if !ok || !activation.UsedAt.IsZero() || !activation.ExpiresAt.After(now) {
		return ActivationPreview{}, ErrActivationInvalid
	}
	for _, user := range s.users {
		if user.TenantID == activation.TenantID && user.ID == activation.UserID && user.Status == "invited" && s.tenantStatuses[user.TenantID] == "active" {
			return ActivationPreview{PasswordMinLength: PasswordMinimum(user.Roles), DisplayName: user.DisplayName, TenantCode: user.TenantCode, SchoolID: managedUserSchoolID(user), PhoneMasked: MaskPhone(user.PhoneNormalized), ExpiresAt: activation.ExpiresAt}, nil
		}
	}
	return ActivationPreview{}, ErrActivationInvalid
}

func (s *MemoryStore) CreateActivation(_ context.Context, actor User, actorScope AccessScope, userID, tokenHash string, expiresAt time.Time) (ActivationPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.users {
		if user.TenantID != actor.TenantID || user.ID != userID || user.Status != "invited" {
			continue
		}
		schoolID, err := managedUserAuthority(actor, actorScope, user)
		if err != nil {
			return ActivationPreview{}, err
		}
		now := time.Now().UTC()
		for hash, activation := range s.activations {
			if activation.TenantID == user.TenantID && activation.UserID == user.ID && activation.UsedAt.IsZero() {
				activation.UsedAt = now
				s.activations[hash] = activation
			}
		}
		s.activations[tokenHash] = memoryActivation{TenantID: user.TenantID, UserID: user.ID, ExpiresAt: expiresAt}
		return ActivationPreview{PasswordMinLength: PasswordMinimum(user.Roles), DisplayName: user.DisplayName, TenantCode: user.TenantCode, SchoolID: schoolID, PhoneMasked: MaskPhone(user.PhoneNormalized), ExpiresAt: expiresAt}, nil
	}
	return ActivationPreview{}, ErrManagedUserNotFound
}

func (s *MemoryStore) ActivateUser(_ context.Context, tokenHash string, passwordHash string, now time.Time) (ActivationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	activation, ok := s.activations[tokenHash]
	if !ok || !activation.UsedAt.IsZero() || !activation.ExpiresAt.After(now) {
		return ActivationResult{}, ErrActivationInvalid
	}
	for key, user := range s.users {
		if user.TenantID != activation.TenantID || user.ID != activation.UserID || user.Status != "invited" || s.tenantStatuses[user.TenantID] != "active" {
			continue
		}
		user.Status = "active"
		user.PasswordHash = passwordHash
		user.MustChangePassword = false
		user.ActivatedAt = now
		user.SecurityEpoch++
		s.users[key] = user
		activation.UsedAt = now
		s.activations[tokenHash] = activation
		for sessionToken, session := range s.sessions {
			if session.TenantID == user.TenantID && session.UserID == user.ID {
				delete(s.sessions, sessionToken)
			}
		}
		return ActivationResult{TenantID: user.TenantID, UserID: user.ID}, nil
	}
	return ActivationResult{}, ErrActivationInvalid
}
