package auth

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	mu                     sync.RWMutex
	users                  map[string]UserWithPassword
	roles                  map[string]map[string]AssignableRole
	tenantStatuses         map[string]string
	sessions               map[string]memorySession
	activations            map[string]memoryActivation
	recoveries             map[string]memoryRecovery
	devices                map[string]memoryDeviceBinding
	riskEvents             []RiskEvent
	totpRecords            map[string]TOTPRecord
	mfaChallenges          map[string]MFAChallenge
	mfaRecovery            map[string]map[string]bool
	mfaNotificationIntents []memorySecurityNotificationIntent
	wechatChallenges       map[string]WechatLoginChallenge
	wechatIdentities       map[string]struct{ TenantID, UserID string }
	boundaries             map[string]ResourceBoundary
	audits                 []AuditRecord
	auditSeq               int
	userSeq                int
	sessionSeq             int
}

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

type memoryActivation struct {
	TenantID  string
	UserID    string
	ExpiresAt time.Time
	UsedAt    time.Time
}

type memoryRecovery struct {
	TenantID      string
	UserID        string
	ExpiresAt     time.Time
	UsedAt        time.Time
	SecurityEpoch int64
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
		totpRecords:      map[string]TOTPRecord{},
		mfaChallenges:    map[string]MFAChallenge{},
		mfaRecovery:      map[string]map[string]bool{},
		wechatChallenges: map[string]WechatLoginChallenge{},
		wechatIdentities: map[string]struct{ TenantID, UserID string }{},
		boundaries:       map[string]ResourceBoundary{},
		audits:           []AuditRecord{},
	}
}

// BindWechatIdentity supports local deployments and tests. Production
// bindings are provisioned in wechat_identity after the account owner has
// been verified by an administrator.
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

func (s *MemoryStore) AddResourceBoundary(resourceType, resourceID string, boundary ResourceBoundary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	boundary.ResourceType = resourceType
	boundary.ResourceID = resourceID
	s.boundaries[resourceType+"|"+resourceID] = boundary
}

func (s *MemoryStore) ResolveResourceBoundary(_ context.Context, scope AccessScope, resourceType, resourceID string) (ResourceBoundary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if boundary, ok := s.boundaries[resourceType+"|"+resourceID]; ok {
		return boundary, nil
	}
	boundary := ResourceBoundary{ResourceType: resourceType, ResourceID: resourceID, TenantID: scope.TenantID}
	if scope.syntheticUnbounded {
		return boundary, nil
	}
	switch resourceType {
	case "exam":
		if scope.AllowsExam(resourceID) || scope.TenantWide {
			boundary.ExamID = resourceID
			return boundary, nil
		}
	case "submission":
		if scope.AllowsSubmission(resourceID) || scope.TenantWide {
			boundary.SubmissionID = resourceID
			return boundary, nil
		}
	case "file_asset":
		if scope.AllowsFile(resourceID) || scope.TenantWide {
			return boundary, nil
		}
	case "review_task":
		if scope.AllowsReviewTask(resourceID) || scope.TenantWide {
			boundary.AssignedTo = scope.ActorID
			return boundary, nil
		}
	case "arbitration_task":
		if scope.AllowsArbitrationTask(resourceID) || scope.TenantWide {
			boundary.AssignedTo = scope.ActorID
			return boundary, nil
		}
	case "student":
		if scope.AllowsStudent(resourceID) || scope.TenantWide {
			boundary.StudentID = resourceID
			return boundary, nil
		}
	default:
		if scope.TenantWide {
			return boundary, nil
		}
	}
	return ResourceBoundary{}, ErrResourceBoundaryNotFound
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

// SetTenantStatus supports tenant lifecycle checks in the in-memory runtime
// and tests. Users and sessions remain stored, but inactive tenants cannot
// authenticate or use an existing session.
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

func (s *MemoryStore) ResolveAccessScope(_ context.Context, user User) (AccessScope, error) {
	scope, err := ResolveDeclaredAccessScope(user)
	if err != nil {
		return AccessScope{}, err
	}
	// Synthetic unit fixtures may explicitly request a tenant-wide in-memory
	// boundary. Production scopes never receive this test-only expansion.
	if scopeDeclaresSynthetic(user.DataScope) {
		scope.syntheticUnbounded = true
	}
	return scope, nil
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

func (s *MemoryStore) LoadLoginRiskContext(_ context.Context, request LoginRiskContextRequest) (LoginRiskContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := request.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	cutoff := now.Add(-RiskHistoryRetention)
	history := make([]RiskEvent, 0, RiskHistoryLimit)
	for index := len(s.riskEvents) - 1; index >= 0 && len(history) < RiskHistoryLimit; index-- {
		event := s.riskEvents[index]
		if event.TenantID != request.TenantID || event.UserID != request.UserID || event.Purpose != RiskPurposeLogin || event.OccurredAt.Before(cutoff) {
			continue
		}
		history = append(history, event)
	}
	result := LoginRiskContext{PriorSuccessfulLogins: len(history)}
	for _, event := range history {
		if request.UserAgentHash != "" && event.UserAgentHash == request.UserAgentHash {
			result.KnownUserAgent = true
		}
		if request.IPPrefix != "" && event.IPPrefix == request.IPPrefix {
			result.KnownNetwork = true
		}
	}
	if request.DeviceTokenHash != "" {
		if device, ok := s.devices[request.DeviceTokenHash]; ok && device.TenantID == request.TenantID && device.UserID == request.UserID && device.ExpiresAt.After(now) {
			result.KnownDevice = true
			result.TrustedDevice = device.AssuranceLevel >= 2
		}
	}
	failureCutoff := now.Add(-RiskFailureWindow)
	for _, record := range s.audits {
		if record.TenantID == request.TenantID && record.ActorID == request.UserID && !record.CreatedAt.Before(failureCutoff) &&
			(record.Action == "auth.login_failed" || record.Action == "auth.login_rate_limited") {
			result.RecentFailures++
		}
	}
	recoveryCutoff := now.Add(-RiskRecoveryWindow)
	for _, recovery := range s.recoveries {
		if recovery.TenantID == request.TenantID && recovery.UserID == request.UserID && !recovery.UsedAt.IsZero() && !recovery.UsedAt.Before(recoveryCutoff) {
			result.RecentRecovery = true
			break
		}
	}
	return result, nil
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
	return revoked, nil
}

func (s *MemoryStore) UpdatePasswordAndRevokeSessions(_ context.Context, tenantID, userID, expectedPasswordHash, newPasswordHash string) (bool, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for key, user := range s.users {
		if user.TenantID == tenantID && user.ID == userID && user.Status == "active" && user.PasswordHash == expectedPasswordHash {
			user.PasswordHash = newPasswordHash
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

func (s *MemoryStore) Audit(_ context.Context, event AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendAudit(event, time.Now().UTC())
	return nil
}

// Caller holds s.mu, including callers that record security facts atomically.
func (s *MemoryStore) appendAudit(event AuditEvent, occurredAt time.Time) {
	s.auditSeq++
	s.audits = append(s.audits, AuditRecord{
		ID:          fmt.Sprintf("memory-audit-%d", s.auditSeq),
		TenantID:    event.TenantID,
		ActorID:     event.ActorID,
		Action:      event.Action,
		TargetType:  event.TargetType,
		TargetID:    event.TargetID,
		BeforeValue: cloneAuditMap(event.BeforeValue),
		AfterValue:  cloneAuditMap(event.AfterValue),
		Reason:      event.Reason,
		IPAddress:   event.IPAddress,
		UserAgent:   event.UserAgent,
		RequestID:   event.RequestID,
		CreatedAt:   occurredAt,
	})
}

func (s *MemoryStore) Audits() []AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AuditEvent, len(s.audits))
	for i, record := range s.audits {
		out[i] = AuditEvent{
			TenantID:   record.TenantID,
			ActorID:    record.ActorID,
			Action:     record.Action,
			TargetType: record.TargetType,
			TargetID:   record.TargetID,
			Reason:     record.Reason,
			IPAddress:  record.IPAddress,
			UserAgent:  record.UserAgent,
			RequestID:  record.RequestID,
		}
	}
	return out
}

func (s *MemoryStore) ListAudits(_ context.Context, tenantID string, filter AuditFilter) ([]AuditRecord, error) {
	if filter.ScopeMode != "" && filter.ScopeMode != "tenant" && filter.ScopeMode != "platform" {
		return []AuditRecord{}, nil
	}
	if filter.ScopeMode != "" && filter.ScopeMode != "tenant" && filter.ScopeMode != "platform" {
		return []AuditRecord{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []AuditRecord{}
	for _, record := range s.audits {
		if record.TenantID != tenantID {
			continue
		}
		if filter.Action != "" && record.Action != filter.Action {
			continue
		}
		if filter.ActorID != "" && record.ActorID != filter.ActorID {
			continue
		}
		if filter.TargetType != "" && record.TargetType != filter.TargetType {
			continue
		}
		if filter.TargetID != "" && record.TargetID != filter.TargetID {
			continue
		}
		if filter.ExamID != "" && record.TargetID != filter.ExamID {
			continue
		}
		if filter.IPAddress != "" && record.IPAddress != filter.IPAddress {
			continue
		}
		if !filter.CreatedFrom.IsZero() && record.CreatedAt.Before(filter.CreatedFrom) {
			continue
		}
		if !filter.CreatedTo.IsZero() && record.CreatedAt.After(filter.CreatedTo) {
			continue
		}
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if filter.CursorID != "" {
		start := 0
		for start < len(out) {
			item := out[start]
			if item.CreatedAt.Before(filter.CursorCreatedAt) ||
				(item.CreatedAt.Equal(filter.CursorCreatedAt) && item.ID < filter.CursorID) {
				break
			}
			start++
		}
		out = out[start:]
	}
	if limit := normalizedAuditLimit(filter.Limit); len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) ListManagedUsers(_ context.Context, tenantID string, filter ManagedUserFilter) ([]ManagedUser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ManagedUser{}
	for _, user := range s.users {
		if user.TenantID != tenantID || (filter.UserID != "" && user.ID != filter.UserID) {
			continue
		}
		if filter.RestrictSchools && !containsID(filter.SchoolIDs, managedUserSchoolID(user)) {
			continue
		}
		query := strings.ToLower(filter.Query)
		if query != "" && !strings.Contains(strings.ToLower(user.Username), query) && !strings.Contains(strings.ToLower(user.DisplayName), query) && !strings.Contains(strings.ToLower(user.PhoneNormalized), query) && !strings.Contains(strings.ToLower(user.EmployeeNo), query) {
			continue
		}
		if filter.Role != "" {
			matched := false
			for _, role := range user.Roles {
				if role == filter.Role {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		out = append(out, ManagedUser{
			ID: user.ID, Username: user.Username, DisplayName: user.DisplayName,
			PhoneMasked: MaskPhone(user.PhoneNormalized), EmployeeNo: user.EmployeeNo,
			Status: user.Status, Roles: append([]string(nil), user.Roles...), SchoolID: managedUserSchoolID(user),
			ActivatedAt: timePointer(user.ActivatedAt), LastLoginAt: timePointer(user.LastLoginAt), CreatedAt: user.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if filter.CursorID != "" {
		start := 0
		for start < len(out) {
			item := out[start]
			if item.CreatedAt.Before(filter.CursorCreatedAt) || (item.CreatedAt.Equal(filter.CursorCreatedAt) && item.ID < filter.CursorID) {
				break
			}
			start++
		}
		out = out[start:]
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *MemoryStore) ListAssignableRoles(_ context.Context, actor User) ([]AssignableRole, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []AssignableRole{}
	for _, role := range s.roles[actor.TenantID] {
		if policy := RolePolicy(role.Code); CanAssignManagedRole(actor, role.Code) && role.ScopeType == policy.CanonicalScope {
			out = append(out, role)
		}
	}
	return out, nil
}

func (s *MemoryStore) CreateManagedUser(_ context.Context, actor User, actorScope AccessScope, input CreateManagedUserInput, passwordHash string) (ManagedUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := actor.TenantCode + "|" + input.Username
	if existing, exists := s.users[key]; exists && existing.User.Status != "deleted" {
		return ManagedUser{}, ErrUsernameExists
	}
	for _, existing := range s.users {
		if existing.TenantID != actor.TenantID || existing.Status == "deleted" {
			continue
		}
		if (input.Phone != "" && existing.PhoneNormalized == input.Phone) || (input.EmployeeNo != "" && existing.EmployeeNo == input.EmployeeNo) {
			return ManagedUser{}, ErrIdentityExists
		}
	}
	role, exists := s.roles[actor.TenantID][input.RoleCode]
	if !exists {
		return ManagedUser{}, ErrRoleNotFound
	}
	policy := RolePolicy(role.Code)
	if !CanAssignManagedRole(actor, role.Code) || role.ScopeType != policy.CanonicalScope {
		return ManagedUser{}, ErrRoleAssignment
	}
	if len(input.ClassIDs) > 0 && !policy.ClassBinding {
		return ManagedUser{}, ErrInvalidRoleBinding
	}
	schoolID := strings.TrimSpace(input.SchoolID)
	if schoolID == "" && HasRole(actor, "school_admin") && len(actorScope.SchoolIDs) == 1 {
		schoolID = actorScope.SchoolIDs[0]
	}
	if policy.SchoolRequired && schoolID == "" {
		return ManagedUser{}, ErrInvalidRoleBinding
	}
	if schoolID != "" && !actorScope.AllowsSchool(schoolID) {
		return ManagedUser{}, ErrOrganizationScope
	}
	s.userSeq++
	status := "active"
	now := time.Now().UTC()
	activatedAt := now
	if input.ActivationTokenHash != "" {
		status = "invited"
		activatedAt = time.Time{}
	}
	user := UserWithPassword{
		User: User{
			ID: fmt.Sprintf("managed-user-%d", s.userSeq), TenantID: actor.TenantID, TenantCode: actor.TenantCode,
			Username: input.Username, DisplayName: input.DisplayName, Status: status,
			Roles: []string{input.RoleCode}, Permissions: []string{}, DataScope: map[string]any{input.RoleCode: canonicalRoleDataScope(input.RoleCode, schoolID)},
		},
		PasswordHash: passwordHash, SchoolID: schoolID, PhoneNormalized: input.Phone, EmployeeNo: input.EmployeeNo, SecurityEpoch: 1, ActivatedAt: activatedAt, CreatedAt: now,
	}
	s.users[key] = user
	if input.ActivationTokenHash != "" {
		s.activations[input.ActivationTokenHash] = memoryActivation{TenantID: actor.TenantID, UserID: user.ID, ExpiresAt: input.ActivationExpiresAt}
	}
	return managedUserFromMemory(user, schoolID), nil
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
			return ActivationPreview{DisplayName: user.DisplayName, TenantCode: user.TenantCode, SchoolID: managedUserSchoolID(user), PhoneMasked: MaskPhone(user.PhoneNormalized), ExpiresAt: activation.ExpiresAt}, nil
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
		if user.ID == actor.ID || !CanManageUserRoles(actor, user.Roles) {
			return ActivationPreview{}, ErrUserStatusForbidden
		}
		schoolID := managedUserSchoolID(user)
		if !actorScope.TenantWide && (schoolID == "" || !actorScope.AllowsSchool(schoolID)) {
			return ActivationPreview{}, ErrOrganizationScope
		}
		now := time.Now().UTC()
		for hash, activation := range s.activations {
			if activation.TenantID == user.TenantID && activation.UserID == user.ID && activation.UsedAt.IsZero() {
				activation.UsedAt = now
				s.activations[hash] = activation
			}
		}
		s.activations[tokenHash] = memoryActivation{TenantID: user.TenantID, UserID: user.ID, ExpiresAt: expiresAt}
		return ActivationPreview{DisplayName: user.DisplayName, TenantCode: user.TenantCode, SchoolID: schoolID, PhoneMasked: MaskPhone(user.PhoneNormalized), ExpiresAt: expiresAt}, nil
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

func (s *MemoryStore) CreateRecovery(_ context.Context, actor User, actorScope AccessScope, userID, tokenHash string, expiresAt time.Time) (RecoveryPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.users {
		if user.TenantID != actor.TenantID || user.ID != userID || user.Status != "active" {
			continue
		}
		if user.ID == actor.ID || !CanManageUserRoles(actor, user.Roles) {
			return RecoveryPreview{}, ErrUserStatusForbidden
		}
		schoolID := managedUserSchoolID(user)
		if !actorScope.TenantWide && (schoolID == "" || !actorScope.AllowsSchool(schoolID)) {
			return RecoveryPreview{}, ErrOrganizationScope
		}
		for hash, recovery := range s.recoveries {
			if recovery.TenantID == user.TenantID && recovery.UserID == user.ID && recovery.UsedAt.IsZero() {
				recovery.UsedAt = time.Now().UTC()
				s.recoveries[hash] = recovery
			}
		}
		s.recoveries[tokenHash] = memoryRecovery{TenantID: user.TenantID, UserID: user.ID, ExpiresAt: expiresAt, SecurityEpoch: user.SecurityEpoch}
		return RecoveryPreview{DisplayName: user.DisplayName, TenantCode: user.TenantCode, PhoneMasked: MaskPhone(user.PhoneNormalized), ExpiresAt: expiresAt}, nil
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
			return RecoveryPreview{DisplayName: user.DisplayName, TenantCode: user.TenantCode, PhoneMasked: MaskPhone(user.PhoneNormalized), ExpiresAt: recovery.ExpiresAt}, nil
		}
	}
	return RecoveryPreview{}, ErrRecoveryInvalid
}

func (s *MemoryStore) CompleteRecovery(_ context.Context, tokenHash string, passwordHash string, now time.Time) (RecoveryResult, error) {
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
		user.SecurityEpoch++
		s.users[key] = user
		recovery.UsedAt = now
		s.recoveries[tokenHash] = recovery
		for sessionToken, session := range s.sessions {
			if session.TenantID == user.TenantID && session.UserID == user.ID {
				delete(s.sessions, sessionToken)
			}
		}
		for deviceToken, device := range s.devices {
			if device.TenantID == user.TenantID && device.UserID == user.ID {
				delete(s.devices, deviceToken)
			}
		}
		return RecoveryResult{TenantID: user.TenantID, UserID: user.ID}, nil
	}
	return RecoveryResult{}, ErrRecoveryInvalid
}

func (s *MemoryStore) UpdateManagedUserStatus(_ context.Context, actor User, actorScope AccessScope, userID string, status string) (ManagedUser, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var targetKey string
	var target UserWithPassword
	for key, user := range s.users {
		if user.TenantID == actor.TenantID && user.ID == userID && user.Status != "deleted" {
			targetKey = key
			target = user
			break
		}
	}
	if targetKey == "" {
		return ManagedUser{}, "", ErrManagedUserNotFound
	}
	if target.ID == actor.ID || !CanManageUserRoles(actor, target.Roles) {
		return ManagedUser{}, "", ErrUserStatusForbidden
	}
	schoolID := managedUserSchoolID(target)
	if !actorScope.TenantWide && (schoolID == "" || !actorScope.AllowsSchool(schoolID)) {
		return ManagedUser{}, "", ErrOrganizationScope
	}
	previousStatus := target.Status
	if previousStatus == "invited" {
		return ManagedUser{}, "", ErrUserStatusForbidden
	}
	if previousStatus == status {
		return managedUserFromMemory(target, schoolID), previousStatus, nil
	}
	if status == "active" && previousStatus != "disabled" {
		return ManagedUser{}, "", ErrUserStatusForbidden
	}
	if status == "disabled" && HasRole(target.User, "school_admin") {
		activeAdmins := 0
		for _, user := range s.users {
			if user.TenantID == actor.TenantID && user.Status == "active" && HasRole(user.User, "school_admin") && managedUserSchoolID(user) == schoolID {
				activeAdmins++
			}
		}
		if activeAdmins <= 1 {
			return ManagedUser{}, "", ErrLastSchoolAdmin
		}
	}
	target.Status = status
	if status == "disabled" {
		target.SecurityEpoch++
		for tokenHash, record := range s.sessions {
			if record.TenantID == target.TenantID && record.UserID == target.ID {
				delete(s.sessions, tokenHash)
			}
		}
		for tokenHash, device := range s.devices {
			if device.TenantID == target.TenantID && device.UserID == target.ID {
				delete(s.devices, tokenHash)
			}
		}
	}
	s.users[targetKey] = target
	return managedUserFromMemory(target, schoolID), previousStatus, nil
}

func managedUserFromMemory(user UserWithPassword, schoolID string) ManagedUser {
	return ManagedUser{
		ID: user.ID, Username: user.Username, DisplayName: user.DisplayName,
		PhoneMasked: MaskPhone(user.PhoneNormalized), EmployeeNo: user.EmployeeNo,
		Status: user.Status, Roles: append([]string(nil), user.Roles...), SchoolID: schoolID,
		ActivatedAt: timePointer(user.ActivatedAt), LastLoginAt: timePointer(user.LastLoginAt), CreatedAt: user.CreatedAt,
	}
}

func managedUserSchoolID(user UserWithPassword) string {
	if user.SchoolID != "" {
		return user.SchoolID
	}
	return firstScopeID(user.DataScope, "school_id")
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func scopeDeclaresSynthetic(scope map[string]any) bool {
	for key, raw := range scope {
		if key == "synthetic" {
			value, _ := raw.(bool)
			if value {
				return true
			}
		}
		if nested, ok := raw.(map[string]any); ok && scopeDeclaresSynthetic(nested) {
			return true
		}
	}
	return false
}

func firstScopeID(scope map[string]any, key string) string {
	if value, ok := scope[key].(string); ok {
		return value
	}
	for _, raw := range scope {
		if nested, ok := raw.(map[string]any); ok {
			if value := firstScopeID(nested, key); value != "" {
				return value
			}
		}
	}
	return ""
}

func cloneAuditMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}
