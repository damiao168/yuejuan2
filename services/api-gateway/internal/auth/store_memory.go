package auth

import (
	"context"
	"sync"
	"time"
)

type MemoryStore struct {
	mu               sync.RWMutex
	users            map[string]UserWithPassword
	roles            map[string]map[string]AssignableRole
	tenantStatuses   map[string]string
	sessions         map[string]memorySession
	activations      map[string]memoryActivation
	recoveries       map[string]memoryRecovery
	devices          map[string]memoryDeviceBinding
	riskEvents       []RiskEvent
	wechatChallenges map[string]WechatLoginChallenge
	wechatIdentities map[string]struct{ TenantID, UserID string }
	boundaries       map[string]ResourceBoundary
	audits           []AuditRecord
	auditSeq         int
	userSeq          int
	sessionSeq       int
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users:            map[string]UserWithPassword{},
		roles:            map[string]map[string]AssignableRole{},
		tenantStatuses:   map[string]string{},
		sessions:         map[string]memorySession{},
		activations:      map[string]memoryActivation{},
		recoveries:       map[string]memoryRecovery{},
		devices:          map[string]memoryDeviceBinding{},
		riskEvents:       []RiskEvent{},
		wechatChallenges: map[string]WechatLoginChallenge{},
		wechatIdentities: map[string]struct{ TenantID, UserID string }{},
		boundaries:       map[string]ResourceBoundary{},
		audits:           []AuditRecord{},
	}
}

func (s *MemoryStore) FindUserByID(_ context.Context, tenantID, userID string) (UserWithPassword, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, user := range s.users {
		if user.TenantID == tenantID && user.ID == userID && user.Status == "active" && s.tenantStatuses[user.TenantID] == "active" {
			return user, nil
		}
	}
	return UserWithPassword{}, ErrInvalidCredentials
}

func (s *MemoryStore) AddUser(user UserWithPassword) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user.SecurityEpoch <= 0 {
		user.SecurityEpoch = 1
	}
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now().UTC()
	}
	s.users[user.TenantCode+"|"+user.Username] = user
	if _, exists := s.tenantStatuses[user.TenantID]; !exists {
		s.tenantStatuses[user.TenantID] = "active"
	}
	if s.roles[user.TenantID] == nil {
		s.roles[user.TenantID] = map[string]AssignableRole{}
	}
	for _, code := range user.Roles {
		if _, exists := s.roles[user.TenantID][code]; !exists {
			s.roles[user.TenantID][code] = AssignableRole{Code: code, Name: code, ScopeType: RolePolicy(code).CanonicalScope}
		}
	}
}

// SetTenantStatus 支持内存运行环境及测试中的租户生命周期检查。用户和会话仍保留，但停用租户不能认证或使用已有会话。
func (s *MemoryStore) SetTenantStatus(tenantID, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenantStatuses[tenantID] = status
}

func (s *MemoryStore) AddRole(tenantID string, role AssignableRole) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.roles[tenantID] == nil {
		s.roles[tenantID] = map[string]AssignableRole{}
	}
	s.roles[tenantID][role.Code] = role
}

func (s *MemoryStore) FindUserByLogin(_ context.Context, tenantCode string, username string) (UserWithPassword, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found UserWithPassword
	matches := 0
	phone, _ := NormalizePhone(username)
	for _, user := range s.users {
		if user.TenantCode != tenantCode || user.Status != "active" || s.tenantStatuses[user.TenantID] != "active" {
			continue
		}
		if user.Username == username || user.EmployeeNo == username || (phone != "" && user.PhoneNormalized == phone) {
			found = user
			matches++
		}
	}
	// 不同登录别名若命中多个人，拒绝登录，不能任意选择其中一个账户。
	if matches != 1 {
		return UserWithPassword{}, ErrInvalidCredentials
	}
	return found, nil
}

func (s *MemoryStore) FindPasswordHash(_ context.Context, tenantID string, userID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, user := range s.users {
		if user.TenantID == tenantID && user.ID == userID && user.Status == "active" && s.tenantStatuses[user.TenantID] == "active" {
			return user.PasswordHash, nil
		}
	}
	return "", ErrInvalidCredentials
}

func (s *MemoryStore) RecordSuccessfulLogin(_ context.Context, tenantID, userID, expectedPasswordHash, replacementPasswordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, user := range s.users {
		if user.TenantID != tenantID || user.ID != userID || user.Status != "active" || user.PasswordHash != expectedPasswordHash {
			continue
		}
		user.LastLoginAt = time.Now().UTC()
		if replacementPasswordHash != "" {
			user.PasswordHash = replacementPasswordHash
		}
		s.users[key] = user
		return nil
	}
	return ErrInvalidCredentials
}
