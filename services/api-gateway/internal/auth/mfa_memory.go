package auth

import (
	"context"
	"time"
)

func mfaOwnerKey(tenantID, userID string) string { return tenantID + "|" + userID }

// Caller holds s.mu. All mutations use the same live session/epoch check.
func (s *MemoryStore) liveMFAPrincipal(tenantID, userID, sessionHash string, now time.Time) (UserWithPassword, error) {
	session, ok := s.sessions[sessionHash]
	if !ok || session.TenantID != tenantID || session.UserID != userID || !session.ExpiresAt.After(now) || !session.LockedAt.IsZero() {
		return UserWithPassword{}, ErrMFAInvalid
	}
	for _, user := range s.users {
		if user.ID == userID && user.TenantID == tenantID && user.Status == "active" && user.SecurityEpoch == session.SecurityEpoch && s.tenantStatuses[tenantID] == "active" {
			return user, nil
		}
	}
	return UserWithPassword{}, ErrMFAInvalid
}

func (s *MemoryStore) FindTOTP(_ context.Context, tenantID, userID string) (TOTPRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.totpRecords[mfaOwnerKey(tenantID, userID)]
	if !ok {
		return TOTPRecord{}, ErrMFAInvalid
	}
	record.Ciphertext = append([]byte(nil), record.Ciphertext...)
	record.Nonce = append([]byte(nil), record.Nonce...)
	for _, used := range s.mfaRecovery[record.ID] {
		if !used {
			record.RecoveryCodesRemaining++
		}
	}
	return record, nil
}

func (s *MemoryStore) SavePendingTOTP(_ context.Context, record TOTPRecord, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, err := s.liveMFAPrincipal(record.TenantID, record.UserID, record.EnrollmentSessionHash, now)
	if err != nil || user.PasswordHash != record.ExpectedPasswordHash {
		return ErrMFAInvalid
	}
	key := mfaOwnerKey(record.TenantID, record.UserID)
	if existing, ok := s.totpRecords[key]; ok && existing.EnabledAt != nil {
		return ErrMFAConflict
	}
	record.SecurityEpoch = user.SecurityEpoch
	record.ExpectedPasswordHash = ""
	record.Ciphertext = append([]byte(nil), record.Ciphertext...)
	record.Nonce = append([]byte(nil), record.Nonce...)
	s.totpRecords[key] = record
	return nil
}

func (s *MemoryStore) ConfirmTOTP(ctx context.Context, input TOTPRecord, sessionHash string, step int64, recoveryHashes []string, now time.Time) error {
	if len(recoveryHashes) != 10 {
		return ErrMFAInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, err := s.liveMFAPrincipal(input.TenantID, input.UserID, sessionHash, now)
	record, ok := s.totpRecords[mfaOwnerKey(input.TenantID, input.UserID)]
	if err != nil || !ok || record.ID != input.ID || record.EnabledAt != nil || record.EnrollmentSessionHash != sessionHash || record.SecurityEpoch != user.SecurityEpoch || !record.PendingExpiresAt.After(now) || record.FailedAttempts >= 5 || step <= record.LastUsedStep {
		return ErrMFAInvalid
	}
	change, err := newMFASecurityChange(input.TenantID, input.UserID, "auth.mfa_enabled", "totp", now)
	if err != nil {
		return err
	}
	record.EnabledAt = timePointer(now)
	record.LastUsedStep = step
	s.totpRecords[mfaOwnerKey(input.TenantID, input.UserID)] = record
	s.replaceMFARecovery(record.ID, recoveryHashes)
	s.recordMFASecurityChange(ctx, change)
	return nil
}

func (s *MemoryStore) replaceMFARecovery(credentialID string, hashes []string) {
	s.mfaRecovery[credentialID] = map[string]bool{}
	for _, hash := range hashes {
		s.mfaRecovery[credentialID][hash] = false
	}
}

func (s *MemoryStore) RecordTOTPFailure(_ context.Context, tenantID, userID, credentialID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := mfaOwnerKey(tenantID, userID)
	record, ok := s.totpRecords[key]
	if ok && record.ID == credentialID && record.EnabledAt == nil {
		record.FailedAttempts = min(record.FailedAttempts+1, 5)
		s.totpRecords[key] = record
	}
	return nil
}

func (s *MemoryStore) CreateMFAChallenge(_ context.Context, challenge MFAChallenge, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, err := s.liveMFAPrincipal(challenge.TenantID, challenge.UserID, challenge.SessionHash, now)
	record, ok := s.totpRecords[mfaOwnerKey(challenge.TenantID, challenge.UserID)]
	if err != nil || user.PasswordHash != challenge.ExpectedPasswordHash || !ok || record.EnabledAt == nil || record.ID != challenge.CredentialID || !validMFAOperation(challenge.Operation) {
		return ErrMFAInvalid
	}
	challenge.SecurityEpoch = user.SecurityEpoch
	challenge.ExpectedPasswordHash = ""
	for hash, old := range s.mfaChallenges {
		if !old.ExpiresAt.After(now) || (old.TenantID == challenge.TenantID && old.UserID == challenge.UserID && old.SessionHash == challenge.SessionHash && old.Operation == challenge.Operation) {
			delete(s.mfaChallenges, hash)
		}
	}
	s.mfaChallenges[challenge.TokenHash] = challenge
	return nil
}

func (s *MemoryStore) FindMFAChallenge(_ context.Context, tenantID, userID, sessionHash, challengeHash string, now time.Time) (MFAChallenge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.findMFAChallenge(tenantID, userID, sessionHash, challengeHash, now)
}

func (s *MemoryStore) findMFAChallenge(tenantID, userID, sessionHash, challengeHash string, now time.Time) (MFAChallenge, error) {
	user, err := s.liveMFAPrincipal(tenantID, userID, sessionHash, now)
	challenge, ok := s.mfaChallenges[challengeHash]
	record := s.totpRecords[mfaOwnerKey(tenantID, userID)]
	if err != nil || !ok || challenge.TenantID != tenantID || challenge.UserID != userID || challenge.SessionHash != sessionHash || challenge.SecurityEpoch != user.SecurityEpoch || record.ID != challenge.CredentialID || record.EnabledAt == nil || !challenge.ExpiresAt.After(now) || challenge.ConsumedAt != nil || challenge.Attempts >= 5 {
		return MFAChallenge{}, ErrMFAInvalid
	}
	return challenge, nil
}

func (s *MemoryStore) RecordMFAChallengeFailure(_ context.Context, tenantID, userID, sessionHash, challengeHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, ok := s.mfaChallenges[challengeHash]
	if ok && challenge.TenantID == tenantID && challenge.UserID == userID && challenge.SessionHash == sessionHash && challenge.VerifiedAt == nil {
		challenge.Attempts = min(challenge.Attempts+1, 5)
		s.mfaChallenges[challengeHash] = challenge
	}
	return nil
}

func (s *MemoryStore) VerifyMFAChallenge(ctx context.Context, proof MFAProof) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, err := s.findMFAChallenge(proof.TenantID, proof.UserID, proof.SessionHash, proof.ChallengeHash, proof.Now)
	if err != nil || challenge.VerifiedAt != nil || challenge.CredentialID != proof.CredentialID {
		return ErrMFAInvalid
	}
	key := mfaOwnerKey(proof.TenantID, proof.UserID)
	record := s.totpRecords[key]
	method := "totp"
	var change MFASecurityChange
	if proof.RecoveryHash != "" {
		if challenge.Operation != MFAOperationDisable {
			return ErrMFAInvalid
		}
		used, exists := s.mfaRecovery[record.ID][proof.RecoveryHash]
		if !exists || used {
			return ErrMFAInvalid
		}
		change, err = newMFASecurityChange(proof.TenantID, proof.UserID, "auth.mfa_recovery_used", "recovery_code", proof.Now)
		if err != nil {
			return err
		}
		s.mfaRecovery[record.ID][proof.RecoveryHash] = true
		method = "recovery_code"
	} else {
		if proof.Step <= record.LastUsedStep {
			return ErrMFAInvalid
		}
		// 时间步在整份凭据上消费，不能换一个挑战重复使用同一时段验证码。
		record.LastUsedStep = proof.Step
		s.totpRecords[key] = record
	}
	challenge.VerifiedAt = timePointer(proof.Now)
	challenge.VerifiedMethod = method
	s.mfaChallenges[proof.ChallengeHash] = challenge
	if method == "recovery_code" {
		s.recordMFASecurityChange(ctx, change)
	}
	return nil
}

func (s *MemoryStore) FinishMFACommand(ctx context.Context, proof MFAProof, operation string, recoveryHashes []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, err := s.findMFAChallenge(proof.TenantID, proof.UserID, proof.SessionHash, proof.ChallengeHash, proof.Now)
	if err != nil || challenge.VerifiedAt == nil || challenge.Operation != operation {
		return ErrMFAInvalid
	}
	eventType := "auth.mfa_disabled"
	if operation == MFAOperationRotateRecovery {
		eventType = "auth.mfa_recovery_rotated"
	}
	change, err := newMFASecurityChange(proof.TenantID, proof.UserID, eventType, challenge.VerifiedMethod, proof.Now)
	if err != nil {
		return err
	}
	key := mfaOwnerKey(proof.TenantID, proof.UserID)
	switch operation {
	case MFAOperationRotateRecovery:
		if challenge.VerifiedMethod != "totp" || len(recoveryHashes) != 10 {
			return ErrMFAInvalid
		}
		s.replaceMFARecovery(challenge.CredentialID, recoveryHashes)
		challenge.ConsumedAt = timePointer(proof.Now)
		s.mfaChallenges[proof.ChallengeHash] = challenge
	case MFAOperationDisable:
		// 关闭 MFA 同时撤销会话、设备并推进安全版本，已有登录状态不能继续沿用。
		delete(s.totpRecords, key)
		delete(s.mfaRecovery, challenge.CredentialID)
		for hash, old := range s.mfaChallenges {
			if old.TenantID == proof.TenantID && old.UserID == proof.UserID {
				delete(s.mfaChallenges, hash)
			}
		}
		for hash, session := range s.sessions {
			if session.TenantID == proof.TenantID && session.UserID == proof.UserID {
				delete(s.sessions, hash)
			}
		}
		for hash, device := range s.devices {
			if device.TenantID == proof.TenantID && device.UserID == proof.UserID {
				delete(s.devices, hash)
			}
		}
		for loginKey, user := range s.users {
			if user.ID == proof.UserID && user.TenantID == proof.TenantID {
				user.SecurityEpoch++
				s.users[loginKey] = user
			}
		}
	default:
		return ErrMFAInvalid
	}
	s.recordMFASecurityChange(ctx, change)
	return nil
}
