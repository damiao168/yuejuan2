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

// RiskEvaluator 负责可选风险存储的边界。风险上下文不可用时，实现必须保留中性回退。
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
	command, tenantCode, identifier, err := normalizedLoginCommand(command)
	if err != nil {
		return LoginResult{}, err
	}
	user, attempt, passwordNeedsRehash, err := s.authenticatePassword(ctx, command, tenantCode, identifier)
	if err != nil {
		return LoginResult{}, err
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
	deviceTokenHash := ""
	if !command.TokenResponse && !command.PublicDevice {
		if command.DeviceToken != "" {
			deviceTokenHash = HashToken(command.DeviceToken)
		}
	}
	riskContext := WithUser(ctx, user.User)
	riskRequest := LoginRiskContextRequest{
		TenantID: user.TenantID, UserID: user.ID, DeviceTokenHash: deviceTokenHash,
		UserAgentHash: userAgentHash, IPPrefix: ipPrefix, Now: riskNow,
	}
	riskEvaluation, riskErr := s.risk.EvaluateLogin(riskContext, riskRequest, sessionType)
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

	// 旧密码哈希确认必须先于会话创建，避免并发改密后仍用旧凭据建立会话。
	replacementHash, err := s.confirmSuccessfulCredentials(ctx, user, command.Password, passwordNeedsRehash)
	if err != nil {
		return LoginResult{}, err
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
	s.persistSessionRisk(ctx, riskContext, command, riskEvaluation, riskRequest, createdSession.ID, &result)

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

func normalizedLoginCommand(command LoginCommand) (LoginCommand, string, string, error) {
	// 只规范化账号和设备字段；密码中的空白仍是凭据的一部分。
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
		return command, "", "", detailedLoginServiceError(LoginFailureInvalidRequest, "missing_required")
	}
	if command.RememberDevice && command.PublicDevice {
		return command, "", "", detailedLoginServiceError(LoginFailureInvalidDeviceMode, "public_remembered")
	}
	if command.TokenResponse && command.PublicDevice {
		return command, "", "", detailedLoginServiceError(LoginFailureInvalidDeviceMode, "public_token")
	}
	if !loginFieldsWithinLimits(tenantCode, identifier, command.Password) {
		return command, "", "", detailedLoginServiceError(LoginFailureInvalidRequest, "fields_too_large")
	}
	return command, tenantCode, identifier, nil
}

func (s *LoginService) authenticatePassword(ctx context.Context, command LoginCommand, tenantCode, identifier string) (UserWithPassword, LoginAttempt, bool, error) {
	attempt := LoginAttempt{TenantCode: tenantCode, Identifier: identifier, IPAddress: command.IPAddress}
	// 来源限流先于账号查询；找到账号后才检查账号桶，保留原来的失败优先级。
	if err := s.checkLoginAttempt(ctx, attempt, PlatformTenantID, "", command); err != nil {
		return UserWithPassword{}, attempt, false, err
	}

	user, findErr := s.credentials.FindUserByLogin(ctx, tenantCode, identifier)
	// 只有明确的凭据不匹配才算认证失败。依赖错误不应污染限流器状态，也不应冒充密码错误。
	if findErr != nil && !errors.Is(findErr, ErrInvalidCredentials) {
		return UserWithPassword{}, attempt, false, loginServiceError(LoginFailureServiceUnavailable)
	}
	if findErr == nil {
		attempt.AccountID = user.ID
		if err := s.checkLoginAttempt(ctx, attempt, user.TenantID, user.ID, command); err != nil {
			return UserWithPassword{}, attempt, false, err
		}
	}

	passwordHash := user.PasswordHash
	// 账号不存在时也进行密码校验，避免直接跳过哈希计算形成明显的账号枚举时序差异。
	if errors.Is(findErr, ErrInvalidCredentials) {
		passwordHash = dummyPasswordHash
	}
	passwordValid, passwordNeedsRehash := VerifyPassword(passwordHash, command.Password)
	if findErr != nil || !passwordValid {
		return UserWithPassword{}, attempt, false, s.recordCredentialFailure(ctx, user.User, attempt, command)
	}
	return user, attempt, passwordNeedsRehash, nil
}

func (s *LoginService) checkLoginAttempt(ctx context.Context, attempt LoginAttempt, tenantID, userID string, command LoginCommand) error {
	limit, blocked := s.guard.Check(ctx, attempt, s.now())
	if !s.limiterAvailable() {
		return loginServiceError(LoginFailureLimiterUnavailable)
	}
	if blocked {
		s.recordRateLimited(ctx, tenantID, userID, limit, command)
		return rateLimitedLoginError(limit)
	}
	return nil
}

func (s *LoginService) recordCredentialFailure(ctx context.Context, user User, attempt LoginAttempt, command LoginCommand) error {
	limit, blocked := s.guard.RegisterFailure(ctx, attempt, s.now())
	tenantID := user.TenantID
	if tenantID == "" {
		tenantID = PlatformTenantID
	}
	// 失败记录和审计先落下，再判断依赖降级或限流；不能因提前返回丢失失败事实。
	RecordAudit(ctx, s.audit, AuditEvent{
		TenantID: tenantID, ActorID: user.ID, Action: "auth.login_failed",
		TargetType: "user", TargetID: user.ID, Reason: "invalid credentials",
		IPAddress: command.IPAddress, UserAgent: command.UserAgent, RequestID: command.RequestID,
	})
	if !s.limiterAvailable() {
		return loginServiceError(LoginFailureLimiterUnavailable)
	}
	if blocked {
		s.recordRateLimited(ctx, tenantID, user.ID, limit, command)
		return rateLimitedLoginError(limit)
	}
	return loginServiceError(LoginFailureInvalidCredentials)
}

func (s *LoginService) confirmSuccessfulCredentials(ctx context.Context, user UserWithPassword, password string, needsRehash bool) (string, error) {
	replacementHash := ""
	if needsRehash {
		var err error
		replacementHash, err = HashPassword(password)
		if err != nil {
			return "", loginServiceError(LoginFailureServiceUnavailable)
		}
	}
	// 传入校验时读取的旧哈希，让存储层拒绝在并发改密后继续按旧凭据确认登录。
	if err := s.credentials.RecordSuccessfulLogin(ctx, user.TenantID, user.ID, user.PasswordHash, replacementHash); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return "", loginServiceError(LoginFailureInvalidCredentials)
		}
		return "", loginServiceError(LoginFailureServiceUnavailable)
	}
	return replacementHash, nil
}

func (s *LoginService) persistSessionRisk(ctx, riskContext context.Context, command LoginCommand, evaluation LoginRiskEvaluation, request LoginRiskContextRequest, sessionID string, result *LoginResult) {
	if !evaluation.Persistent {
		return
	}
	// 会话已创建；风险事件和设备存储失败只记降级审计，不撤销成功会话。
	user := result.User
	if err := s.risk.RecordLogin(riskContext, RiskEvent{
		TenantID: user.TenantID, UserID: user.ID, SessionID: sessionID,
		Purpose: evaluation.Decision.Purpose, Level: evaluation.Decision.Level,
		Action: evaluation.Decision.Action, Score: evaluation.Decision.Score,
		ReasonCodes: evaluation.Decision.ReasonCodes, FamilyScores: evaluation.Decision.FamilyScores,
		EvidenceQuality: evaluation.Decision.EvidenceQuality, PolicyVersion: evaluation.Decision.PolicyVersion,
		UserAgentHash: request.UserAgentHash, IPPrefix: request.IPPrefix,
		DeviceRecognized: evaluation.Context.KnownDevice,
		DeviceTrusted:    evaluation.Context.TrustedDevice, OccurredAt: request.Now,
	}); err != nil {
		RecordAudit(ctx, s.audit, AuditEvent{
			TenantID: user.TenantID, ActorID: user.ID, Action: "auth.risk_event_persist_failed",
			TargetType: "user", TargetID: user.ID, Reason: "risk event persistence failed",
			IPAddress: command.IPAddress, UserAgent: command.UserAgent, RequestID: command.RequestID,
		})
	}
	if command.TokenResponse || command.PublicDevice {
		return
	}
	deviceToken, deviceTokenHash := command.DeviceToken, request.DeviceTokenHash
	newDeviceToken := false
	if !evaluation.Context.KnownDevice {
		var err error
		deviceToken, deviceTokenHash, err = s.newToken()
		newDeviceToken = err == nil
	}
	if deviceTokenHash == "" {
		return
	}
	deviceExpiresAt := request.Now.Add(s.options.DeviceBindingTTL)
	if err := s.risk.ObserveDevice(riskContext, ObservedDeviceInput{
		TenantID: user.TenantID, UserID: user.ID, TokenHash: deviceTokenHash,
		UserAgentHash: request.UserAgentHash, IPPrefix: request.IPPrefix, AssuranceLevel: 1,
		TrustBasis: "password_observed", Now: request.Now, ExpiresAt: deviceExpiresAt,
	}); err != nil {
		RecordAudit(ctx, s.audit, AuditEvent{
			TenantID: user.TenantID, ActorID: user.ID, Action: "auth.device_binding_persist_failed",
			TargetType: "user", TargetID: user.ID, Reason: "device binding persistence failed",
			IPAddress: command.IPAddress, UserAgent: command.UserAgent, RequestID: command.RequestID,
		})
	} else if newDeviceToken {
		// 只有新绑定成功才返回设备 cookie；生成或保存失败不能把临时令牌发给浏览器。
		result.DeviceToken = deviceToken
		result.DeviceTokenExpiresAt = deviceExpiresAt
	}
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
		return tokenSessionPolicy(user, command, sessionTTL)
	}
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
	return sessionType, sessionTTL, nil
}

func tokenSessionPolicy(user User, command *LoginCommand, sessionTTL time.Duration) (string, time.Duration, error) {
	sessionType := SessionTypeStandard
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
