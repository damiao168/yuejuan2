package auth

import (
	"context"
	"time"
)

// BindWechatIdentity 支持本地部署和测试。生产绑定由管理员验证账户所有者后，在 wechat_identity 中配置。
func (s *MemoryStore) BindWechatIdentity(appID, tenantCode, unionID, openID, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.users {
		if user.TenantCode != tenantCode || user.ID != userID {
			continue
		}
		binding := struct{ TenantID, UserID string }{user.TenantID, user.ID}
		if unionID != "" {
			s.wechatIdentities[appID+"|"+tenantCode+"|union|"+unionID] = binding
		}
		if openID != "" {
			s.wechatIdentities[appID+"|"+tenantCode+"|open|"+openID] = binding
		}
	}
}

func (s *MemoryStore) CreateWechatLoginChallenge(_ context.Context, input CreateWechatLoginChallengeInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wechatChallenges[input.ID] = WechatLoginChallenge{
		ID: input.ID, StateHash: input.StateHash, PollTokenHash: input.PollTokenHash,
		TenantCode: input.TenantCode, Status: "pending", RememberDevice: input.RememberDevice,
		PublicDevice: input.PublicDevice, ExpiresAt: input.ExpiresAt,
	}
	return nil
}

func (s *MemoryStore) FindWechatLoginChallengeByState(_ context.Context, stateHash string, now time.Time) (WechatLoginChallenge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, challenge := range s.wechatChallenges {
		if challenge.StateHash == stateHash && challenge.ExpiresAt.After(now) && challenge.ConsumedAt.IsZero() {
			return challenge, nil
		}
	}
	return WechatLoginChallenge{}, ErrInvalidCredentials
}

func (s *MemoryStore) FindWechatLoginChallenge(_ context.Context, id, pollTokenHash string, now time.Time) (WechatLoginChallenge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	challenge, ok := s.wechatChallenges[id]
	if !ok || challenge.PollTokenHash != pollTokenHash {
		return WechatLoginChallenge{}, ErrInvalidCredentials
	}
	if !challenge.ExpiresAt.After(now) && challenge.Status == "pending" {
		challenge.Status = "expired"
	}
	return challenge, nil
}

func (s *MemoryStore) FindUserByWechatIdentity(_ context.Context, tenantCode, appID, unionID, openID string) (UserWithPassword, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var binding struct{ TenantID, UserID string }
	var ok bool
	if unionID != "" {
		binding, ok = s.wechatIdentities[appID+"|"+tenantCode+"|union|"+unionID]
	}
	if !ok && openID != "" {
		binding, ok = s.wechatIdentities[appID+"|"+tenantCode+"|open|"+openID]
	}
	if !ok {
		return UserWithPassword{}, ErrWechatIdentityUnbound
	}
	for _, user := range s.users {
		if user.TenantID == binding.TenantID && user.ID == binding.UserID && user.Status == "active" {
			return user, nil
		}
	}
	return UserWithPassword{}, ErrWechatIdentityUnbound
}

func (s *MemoryStore) AuthorizeWechatLoginChallenge(_ context.Context, id, tenantID, userID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, ok := s.wechatChallenges[id]
	if !ok || challenge.Status != "pending" || !challenge.ExpiresAt.After(now) {
		return ErrInvalidCredentials
	}
	challenge.Status, challenge.TenantID, challenge.UserID, challenge.AuthorizedAt = "authorized", tenantID, userID, now
	s.wechatChallenges[id] = challenge
	return nil
}

func (s *MemoryStore) FailWechatLoginChallenge(_ context.Context, id, code string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, ok := s.wechatChallenges[id]
	if !ok || challenge.Status != "pending" || !challenge.ExpiresAt.After(now) {
		return ErrInvalidCredentials
	}
	challenge.Status, challenge.ErrorCode = "failed", code
	s.wechatChallenges[id] = challenge
	return nil
}

func (s *MemoryStore) ConsumeWechatLoginChallenge(_ context.Context, id, pollTokenHash string, now time.Time) (WechatLoginChallenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, ok := s.wechatChallenges[id]
	if !ok || challenge.PollTokenHash != pollTokenHash || challenge.Status != "authorized" || !challenge.ExpiresAt.After(now) {
		return WechatLoginChallenge{}, ErrInvalidCredentials
	}
	challenge.Status, challenge.ConsumedAt = "consumed", now
	s.wechatChallenges[id] = challenge
	return challenge, nil
}
