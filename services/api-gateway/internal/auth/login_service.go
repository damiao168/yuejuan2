package auth

import (
	"context"
	"errors"
	"strings"
	"time"
)

type LoginFailure string

const (
	LoginFailureInvalidRequest      LoginFailure = "invalid_request"
	LoginFailureInvalidDeviceMode   LoginFailure = "invalid_device_mode"
	LoginFailureRateLimited         LoginFailure = "rate_limited"
	LoginFailureLimiterUnavailable  LoginFailure = "limiter_unavailable"
	LoginFailureServiceUnavailable  LoginFailure = "service_unavailable"
	LoginFailureInvalidCredentials  LoginFailure = "invalid_credentials"
	LoginFailureClientTypeForbidden LoginFailure = "client_type_forbidden"
	LoginFailureClientTypeRequired  LoginFailure = "client_type_required"
	LoginFailureTokenGeneration     LoginFailure = "token_generation_failed"
	LoginFailureSessionCreate       LoginFailure = "session_create_failed"
)

type LoginServiceError struct {
	Kind       LoginFailure
	Detail     string
	RetryAfter time.Duration
}

func (e *LoginServiceError) Error() string { return string(e.Kind) }

type LoginCommand struct {
	TenantCode     string
	TenantHint     string
	Username       string
	Identifier     string
	Password       string
	RememberDevice bool
	PublicDevice   bool
	DeviceName     string
	ClientType     string
	TokenResponse  bool
	IPAddress      string
	UserAgent      string
	RequestID      string
	DeviceToken    string
}

type LoginResult struct {
	Token                string
	ExpiresAt            time.Time
	User                 User
	PersistentCookie     bool
	DeviceToken          string
	DeviceTokenExpiresAt time.Time
	ClearDeviceCookie    bool
}

type LoginRiskEvaluation struct {
	Decision   RiskDecision
	Context    LoginRiskContext
	Persistent bool
}

// RiskEvaluator owns the optional risk-store boundary. Implementations must
// preserve the neutral fallback when risk context is unavailable.
type RiskEvaluator interface {
	EvaluateLogin(context.Context, LoginRiskContextRequest, string) (LoginRiskEvaluation, error)
	RecordLogin(context.Context, RiskEvent) error
	ObserveDevice(context.Context, ObservedDeviceInput) error
}

type storeRiskEvaluator struct {
	mode  string
	store RiskStore
}

func NewStoreRiskEvaluator(store RiskStore, mode string) RiskEvaluator {
	return &storeRiskEvaluator{mode: normalizeRiskMode(mode), store: store}
}

func (e *storeRiskEvaluator) EvaluateLogin(ctx context.Context, request LoginRiskContextRequest, sessionType string) (LoginRiskEvaluation, error) {
	decision := DefaultLoginRiskDecision(request.Now)
	if e == nil || e.mode == "off" || sessionType == SessionTypeService {
		decision.ReasonCodes = []string{"risk_evaluation_disabled"}
		decision.PolicyVersion = "off"
		return LoginRiskEvaluation{Decision: decision}, nil
	}
	if e.store == nil {
		return LoginRiskEvaluation{Decision: decision}, nil
	}
	riskContext, err := e.store.LoadLoginRiskContext(ctx, request)
	if err != nil {
		return LoginRiskEvaluation{Decision: decision, Persistent: true}, err
	}
	return LoginRiskEvaluation{
		Decision:   EvaluateLoginRisk(riskContext, request.Now),
		Context:    riskContext,
		Persistent: true,
	}, nil
}

func (e *storeRiskEvaluator) RecordLogin(ctx context.Context, event RiskEvent) error {
	if e == nil || e.store == nil {
		return nil
	}
	return e.store.RecordRiskEvent(ctx, event)
}

func (e *storeRiskEvaluator) ObserveDevice(ctx context.Context, input ObservedDeviceInput) error {
	if e == nil || e.store == nil {
		return nil
	}
	return e.store.StoreObservedDevice(ctx, input)
}

type LoginServiceOptions struct {
	SessionTTL             time.Duration
	RememberedSessionTTL   time.Duration
	PublicSessionTTL       time.Duration
	DeviceBindingTTL       time.Duration
	RiskMode               string
	LoginLimiterFailClosed bool
}

type LoginService struct {
	credentials CredentialRepository
	sessions    SessionRepository
	scopes      AccessScopeResolver
	audit       AuditRecorder
	guard       LoginAttemptGuard
	risk        RiskEvaluator
	options     LoginServiceOptions
	now         func() time.Time
	newToken    func() (string, string, error)
}

func NewLoginService(
	credentials CredentialRepository,
	sessions SessionRepository,
	scopes AccessScopeResolver,
	audit AuditRecorder,
	guard LoginAttemptGuard,
	risk RiskEvaluator,
	options LoginServiceOptions,
) *LoginService {
	return &LoginService{
		credentials: credentials,
		sessions:    sessions,
		scopes:      scopes,
		audit:       audit,
		guard:       guard,
		risk:        risk,
		options:     options,
		now:         func() time.Time { return time.Now().UTC() },
		newToken:    NewToken,
	}
}

func (s *LoginService) Login(ctx context.Context, command LoginCommand) (LoginResult, error) {
	command.TenantCode = strings.TrimSpace(command.TenantCode)
	command.TenantHint = strings.TrimSpace(command.TenantHint)
	command.Username = strings.TrimSpace(command.Username)
	command.Identifier = strings.TrimSpace(command.Identifier)
	command.DeviceName = strings.TrimSpace(command.DeviceName)
	command.ClientType = strings.ToLower(strings.TrimSpace(command.ClientType))
	tenantCode := command.TenantCode
	if tenantCode == "" {
		tenantCode = command.TenantHint
	}
	identifier := command.Identifier
	if identifier == "" {
		identifier = command.Username
	}
	if tenantCode == "" || identifier == "" || command.Password == "" {
		return LoginResult{}, detailedLoginServiceError(LoginFailureInvalidRequest, "missing_required")
	}
	if command.RememberDevice && command.PublicDevice {
		return LoginResult{}, detailedLoginServiceError(LoginFailureInvalidDeviceMode, "public_remembered")
	}
	if command.TokenResponse && command.PublicDevice {
		return LoginResult{}, detailedLoginServiceError(LoginFailureInvalidDeviceMode, "public_token")
	}
	if !loginFieldsWithinLimits(tenantCode, identifier, command.Password) {
		return LoginResult{}, detailedLoginServiceError(LoginFailureInvalidRequest, "fields_too_large")
	}

	attempt := LoginAttempt{TenantCode: tenantCode, Identifier: identifier, IPAddress: command.IPAddress}
	limit, blocked := s.guard.Check(ctx, attempt, s.now())
	if !s.limiterAvailable() {
		return LoginResult{}, loginServiceError(LoginFailureLimiterUnavailable)
	}
	if blocked {
		s.recordRateLimited(ctx, PlatformTenantID, "", limit, command)
		return LoginResult{}, rateLimitedLoginError(limit)
	}

	user, findErr := s.credentials.FindUserByLogin(ctx, tenantCode, identifier)
	// Only an explicit credential miss is an authentication failure. Dependency
	// errors do not poison the limiter and do not masquerade as bad passwords.
	if findErr != nil && !errors.Is(findErr, ErrInvalidCredentials) {
		return LoginResult{}, loginServiceError(LoginFailureServiceUnavailable)
	}
	if findErr == nil {
		attempt.AccountID = user.ID
		limit, blocked = s.guard.Check(ctx, attempt, s.now())
		if !s.limiterAvailable() {
			return LoginResult{}, loginServiceError(LoginFailureLimiterUnavailable)
		}
		if blocked {
			s.recordRateLimited(ctx, user.TenantID, user.ID, limit, command)
			return LoginResult{}, rateLimitedLoginError(limit)
		}
	}

	passwordHash := user.PasswordHash
	// 账号不存在时也进行密码校验，避免直接跳过哈希计算形成明显的账号枚举时序差异。
	if errors.Is(findErr, ErrInvalidCredentials) {
		passwordHash = dummyPasswordHash
	}
	passwordValid, passwordNeedsRehash := VerifyPassword(passwordHash, command.Password)
	if findErr != nil || !passwordValid {
		limit, blocked = s.guard.RegisterFailure(ctx, attempt, s.now())
		tenantID := user.TenantID
		if tenantID == "" {
			tenantID = PlatformTenantID
		}
		RecordAudit(ctx, s.audit, AuditEvent{
			TenantID: tenantID, ActorID: user.ID, Action: "auth.login_failed",
			TargetType: "user", TargetID: user.ID, Reason: "invalid credentials",
			IPAddress: command.IPAddress, UserAgent: command.UserAgent, RequestID: command.RequestID,
		})
		if !s.limiterAvailable() {
			return LoginResult{}, loginServiceError(LoginFailureLimiterUnavailable)
		}
		if blocked {
			s.recordRateLimited(ctx, tenantID, user.ID, limit, command)
			return LoginResult{}, rateLimitedLoginError(limit)
		}
		return LoginResult{}, loginServiceError(LoginFailureInvalidCredentials)
	}

	if resolvedScope, err := s.scopes.ResolveAccessScope(ctx, user.User); err == nil {
		organizationScope := resolvedScope.OrganizationScope()
		user.OrganizationScope = &organizationScope
	}

	sessionType, sessionTTL, clientTypeErr := s.sessionPolicy(user.User, &command)
	if clientTypeErr != nil {
		return LoginResult{}, clientTypeErr
	}

	riskNow := s.now()
	userAgentHash := hashUserAgent(command.UserAgent)
	ipPrefix := networkPrefix(command.IPAddress)
	deviceToken := ""
	deviceTokenHash := ""
	if !command.TokenResponse && !command.PublicDevice {
		deviceToken = command.DeviceToken
		if deviceToken != "" {
			deviceTokenHash = HashToken(deviceToken)
		}
	}
	riskContext := WithUser(ctx, user.User)
	riskEvaluation, riskErr := s.risk.EvaluateLogin(riskContext, LoginRiskContextRequest{
		TenantID: user.TenantID, UserID: user.ID, DeviceTokenHash: deviceTokenHash,
		UserAgentHash: userAgentHash, IPPrefix: ipPrefix, Now: riskNow,
	}, sessionType)
	if riskErr != nil {
		RecordAudit(ctx, s.audit, AuditEvent{
			TenantID: user.TenantID, ActorID: user.ID, Action: "auth.risk_evaluation_degraded",
			TargetType: "user", TargetID: user.ID, Reason: "risk context unavailable; neutral decision used",
			AfterValue: map[string]any{
				"risk_level": riskEvaluation.Decision.Level, "risk_action": riskEvaluation.Decision.Action,
				"risk_policy_version": riskEvaluation.Decision.PolicyVersion,
			},
			IPAddress: command.IPAddress, UserAgent: command.UserAgent, RequestID: command.RequestID,
		})
	}

	replacementHash := ""
	if passwordNeedsRehash {
		var err error
		replacementHash, err = HashPassword(command.Password)
		if err != nil {
			return LoginResult{}, loginServiceError(LoginFailureServiceUnavailable)
		}
	}
	// 传入校验时读取的旧哈希，让存储层拒绝在并发改密后继续按旧凭据确认登录。
	if err := s.credentials.RecordSuccessfulLogin(ctx, user.TenantID, user.ID, user.PasswordHash, replacementHash); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return LoginResult{}, loginServiceError(LoginFailureInvalidCredentials)
		}
		return LoginResult{}, loginServiceError(LoginFailureServiceUnavailable)
	}

	token, tokenHash, err := s.newToken()
	if err != nil {
		return LoginResult{}, loginServiceError(LoginFailureTokenGeneration)
	}
	_, deviceID, err := s.newToken()
	if err != nil {
		return LoginResult{}, loginServiceError(LoginFailureTokenGeneration)
	}
	expiresAt := s.now().Add(sessionTTL)
	createdSession, err := s.sessions.CreateSession(ctx, CreateSessionInput{
		TenantID: user.TenantID, UserID: user.ID, TokenHash: tokenHash,
		SessionType: sessionType, DeviceID: deviceID,
		DeviceName:    normalizedDeviceName(command.DeviceName, sessionType),
		UserAgentHash: userAgentHash, IPPrefix: ipPrefix,
		ExpiresAt: expiresAt, SecurityEpoch: user.SecurityEpoch,
		RiskLevel: riskEvaluation.Decision.Level, RiskAction: riskEvaluation.Decision.Action,
		RiskScore: riskEvaluation.Decision.Score, RiskEvaluatedAt: riskNow,
		RiskPolicyVersion:   riskEvaluation.Decision.PolicyVersion,
		RiskEvidenceQuality: riskEvaluation.Decision.EvidenceQuality,
	})
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return LoginResult{}, loginServiceError(LoginFailureInvalidCredentials)
		}
		return LoginResult{}, loginServiceError(LoginFailureSessionCreate)
	}

	result := LoginResult{
		Token: token, ExpiresAt: expiresAt, User: user.User,
		PersistentCookie:  command.RememberDevice,
		ClearDeviceCookie: !command.TokenResponse && command.PublicDevice,
	}
	if riskEvaluation.Persistent {
		if err := s.risk.RecordLogin(riskContext, RiskEvent{
			TenantID: user.TenantID, UserID: user.ID, SessionID: createdSession.ID,
			Purpose: riskEvaluation.Decision.Purpose, Level: riskEvaluation.Decision.Level,
			Action: riskEvaluation.Decision.Action, Score: riskEvaluation.Decision.Score,
			ReasonCodes: riskEvaluation.Decision.ReasonCodes, FamilyScores: riskEvaluation.Decision.FamilyScores,
			EvidenceQuality: riskEvaluation.Decision.EvidenceQuality, PolicyVersion: riskEvaluation.Decision.PolicyVersion,
			UserAgentHash: userAgentHash, IPPrefix: ipPrefix,
			DeviceRecognized: riskEvaluation.Context.KnownDevice,
			DeviceTrusted:    riskEvaluation.Context.TrustedDevice, OccurredAt: riskNow,
		}); err != nil {
			RecordAudit(ctx, s.audit, AuditEvent{
				TenantID: user.TenantID, ActorID: user.ID, Action: "auth.risk_event_persist_failed",
				TargetType: "user", TargetID: user.ID, Reason: "risk event persistence failed",
				IPAddress: command.IPAddress, UserAgent: command.UserAgent, RequestID: command.RequestID,
			})
		}
		if !command.TokenResponse && !command.PublicDevice {
			newDeviceToken := false
			if !riskEvaluation.Context.KnownDevice {
				deviceToken, deviceTokenHash, err = s.newToken()
				newDeviceToken = err == nil
			}
			if deviceTokenHash != "" {
				deviceExpiresAt := riskNow.Add(s.options.DeviceBindingTTL)
				if err := s.risk.ObserveDevice(riskContext, ObservedDeviceInput{
					TenantID: user.TenantID, UserID: user.ID, TokenHash: deviceTokenHash,
					UserAgentHash: userAgentHash, IPPrefix: ipPrefix, AssuranceLevel: 1,
					TrustBasis: "password_observed", Now: riskNow, ExpiresAt: deviceExpiresAt,
				}); err != nil {
					RecordAudit(ctx, s.audit, AuditEvent{
						TenantID: user.TenantID, ActorID: user.ID, Action: "auth.device_binding_persist_failed",
						TargetType: "user", TargetID: user.ID, Reason: "device binding persistence failed",
						IPAddress: command.IPAddress, UserAgent: command.UserAgent, RequestID: command.RequestID,
					})
				} else if newDeviceToken {
					result.DeviceToken = deviceToken
					result.DeviceTokenExpiresAt = deviceExpiresAt
				}
			}
		}
	}

	s.guard.RegisterSuccess(ctx, attempt)
	user.CurrentSessionType = sessionType
	user.CurrentRiskLevel = riskEvaluation.Decision.Level
	user.CurrentRiskAction = riskEvaluation.Decision.Action
	user.RiskPolicyVersion = riskEvaluation.Decision.PolicyVersion
	result.User = user.User
	RecordAudit(ctx, s.audit, AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: "auth.login_succeeded",
		TargetType: "user", TargetID: user.ID,
		AfterValue: map[string]any{
			"auth_method": "password", "risk_level": riskEvaluation.Decision.Level,
			"risk_action": riskEvaluation.Decision.Action, "risk_score": riskEvaluation.Decision.Score,
			"risk_policy_version":   riskEvaluation.Decision.PolicyVersion,
			"risk_evidence_quality": riskEvaluation.Decision.EvidenceQuality,
			"risk_reason_codes":     riskEvaluation.Decision.ReasonCodes,
			"risk_mode":             normalizeRiskMode(s.options.RiskMode), "session_type": sessionType,
			"credential_rehashed": replacementHash != "",
		},
		IPAddress: command.IPAddress, UserAgent: command.UserAgent, RequestID: command.RequestID,
	})
	return result, nil
}

func (s *LoginService) limiterAvailable() bool {
	status, ok := s.guard.(LoginLimiterStatus)
	return !s.options.LoginLimiterFailClosed || !ok || !status.Degraded()
}

func (s *LoginService) recordRateLimited(ctx context.Context, tenantID, userID string, limit LoginLimit, command LoginCommand) {
	RecordAudit(ctx, s.audit, AuditEvent{
		TenantID: tenantID, ActorID: userID, Action: "auth.login_rate_limited",
		TargetType: "user", TargetID: userID, AfterValue: map[string]any{"bucket": limit.Bucket},
		Reason: "too many failed login attempts", IPAddress: command.IPAddress,
		UserAgent: command.UserAgent, RequestID: command.RequestID,
	})
}

func (s *LoginService) sessionPolicy(user User, command *LoginCommand) (string, time.Duration, error) {
	sessionType := SessionTypeStandard
	sessionTTL := s.options.SessionTTL
	if command.TokenResponse {
		if command.ClientType == "" {
			command.ClientType = "desktop"
		}
		switch command.ClientType {
		case "desktop":
			if IsServiceUser(user) {
				return "", 0, detailedLoginServiceError(LoginFailureClientTypeForbidden, "account_client_type")
			}
			sessionType = SessionTypeDesktopDevice
		case "service":
			if !IsServiceUser(user) {
				return "", 0, detailedLoginServiceError(LoginFailureClientTypeForbidden, "account_client_type")
			}
			sessionType = SessionTypeService
		default:
			return "", 0, loginServiceError(LoginFailureClientTypeRequired)
		}
	} else {
		if IsServiceUser(user) {
			return "", 0, detailedLoginServiceError(LoginFailureClientTypeForbidden, "browser_service_account")
		}
		if command.PublicDevice {
			sessionType = SessionTypePublicDevice
			sessionTTL = s.options.PublicSessionTTL
		} else if command.RememberDevice {
			sessionType = SessionTypeRememberedDevice
			sessionTTL = s.options.RememberedSessionTTL
		}
	}
	return sessionType, sessionTTL, nil
}

func loginServiceError(kind LoginFailure) *LoginServiceError {
	return &LoginServiceError{Kind: kind}
}

func detailedLoginServiceError(kind LoginFailure, detail string) *LoginServiceError {
	return &LoginServiceError{Kind: kind, Detail: detail}
}

func rateLimitedLoginError(limit LoginLimit) *LoginServiceError {
	return &LoginServiceError{Kind: LoginFailureRateLimited, RetryAfter: limit.RetryAfter}
}
