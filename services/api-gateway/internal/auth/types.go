package auth

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidCredentials         = errors.New("invalid credentials")
	ErrUnauthenticated            = errors.New("unauthenticated")
	ErrForbidden                  = errors.New("forbidden")
	ErrUsernameExists             = errors.New("username already exists")
	ErrIdentityExists             = errors.New("login identity already exists")
	ErrRoleNotFound               = errors.New("role not found")
	ErrRoleAssignment             = errors.New("role assignment is not allowed")
	ErrOrganizationScope          = errors.New("organization scope is not allowed")
	ErrInvalidRoleBinding         = errors.New("role organization binding is invalid")
	ErrManagedUserNotFound        = errors.New("managed user not found")
	ErrUserStatusForbidden        = errors.New("managed user status change is not allowed")
	ErrLastSchoolAdmin            = errors.New("last active school administrator cannot be disabled")
	ErrManagedUserAccessConflict  = errors.New("managed user access changed")
	ErrManagedUserProfileConflict = errors.New("managed user profile changed")
	ErrActivationInvalid          = errors.New("activation token is invalid or expired")
	ErrRecoveryInvalid            = errors.New("recovery token is invalid or expired")
)

const PlatformTenantID = "00000000-0000-0000-0000-000000000001"

type User struct {
	ID          string         `json:"id"`
	TenantID    string         `json:"tenant_id"`
	TenantCode  string         `json:"tenant_code"`
	Username    string         `json:"username"`
	DisplayName string         `json:"display_name"`
	Status      string         `json:"status"`
	Roles       []string       `json:"roles"`
	Permissions []string       `json:"permissions"`
	DataScope   map[string]any `json:"data_scope"`
	// OrganizationScope 是已解析、由服务端信任的组织边界。DataScope 仍是持久化 RBAC 声明；
	// 客户端判断哪些学校、年级和班级可选时，应使用此投影。
	OrganizationScope  *OrganizationScope `json:"organization_scope,omitempty"`
	CurrentSessionType string             `json:"current_session_type,omitempty"`
	CurrentAuthLevel   int                `json:"-"`
	ReauthenticatedAt  *time.Time         `json:"-"`
	CurrentRiskLevel   RiskLevel          `json:"-"`
	CurrentRiskAction  RiskAction         `json:"-"`
	RiskPolicyVersion  string             `json:"-"`
}

type OrganizationScope struct {
	TenantWide bool     `json:"tenant_wide"`
	SchoolIDs  []string `json:"school_ids"`
	GradeIDs   []string `json:"grade_ids"`
	ClassIDs   []string `json:"class_ids"`
}

type UserWithPassword struct {
	User
	PasswordHash    string
	SchoolID        string
	PhoneNormalized string
	EmployeeNo      string
	SecurityEpoch   int64
	ActivatedAt     time.Time
	LastLoginAt     time.Time
	CreatedAt       time.Time
	ManagedClassIDs []string
}

type ManagedUser struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	PhoneMasked string     `json:"phone_masked,omitempty"`
	EmployeeNo  string     `json:"employee_no,omitempty"`
	Status      string     `json:"status"`
	Roles       []string   `json:"roles"`
	ClassIDs    []string   `json:"class_ids"`
	SchoolID    string     `json:"school_id,omitempty"`
	ActivatedAt *time.Time `json:"activated_at,omitempty"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at,omitempty"`
}

type ManagedUserFilter struct {
	Query           string
	Role            string
	Status          string
	UserID          string
	RestrictSchools bool
	SchoolIDs       []string
	Limit           int
	CursorCreatedAt time.Time
	CursorID        string
	ScopeMode       string
}

type AssignableRole struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	ScopeType   string `json:"scope_type"`
	Description string `json:"description,omitempty"`
}

type CreateManagedUserInput struct {
	Username            string    `json:"username"`
	Phone               string    `json:"phone,omitempty"`
	EmployeeNo          string    `json:"employee_no,omitempty"`
	DisplayName         string    `json:"display_name"`
	Password            string    `json:"password"`
	RoleCode            string    `json:"role_code"`
	RoleCodes           []string  `json:"role_codes,omitempty"`
	SchoolID            string    `json:"school_id,omitempty"`
	ClassIDs            []string  `json:"class_ids,omitempty"`
	ActivationTokenHash string    `json:"-"`
	ActivationExpiresAt time.Time `json:"-"`
}

type ActivationPreview struct {
	PasswordMinLength int       `json:"password_min_length,omitempty"`
	DisplayName       string    `json:"display_name"`
	TenantCode        string    `json:"tenant_code"`
	SchoolID          string    `json:"school_id,omitempty"`
	PhoneMasked       string    `json:"phone_masked,omitempty"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type ActivationResult struct {
	TenantID string
	UserID   string
}

type RecoveryPreview struct {
	PasswordMinLength int       `json:"password_min_length,omitempty"`
	DisplayName       string    `json:"display_name"`
	TenantCode        string    `json:"tenant_code"`
	PhoneMasked       string    `json:"phone_masked,omitempty"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type RecoveryResult struct {
	TenantID string
	UserID   string
}

type UpdateManagedUserStatusInput struct {
	Status string `json:"status"`
}

// UpdateManagedUserAccessInput 只调整同校职责和教学班级；旧值用于拒绝覆盖另一位管理员的修改。
type UpdateManagedUserAccessInput struct {
	RoleCodes        []string `json:"role_codes"`
	ClassIDs         []string `json:"class_ids"`
	ExpectedRoles    []string `json:"expected_roles"`
	ExpectedClassIDs []string `json:"expected_class_ids"`
	Reason           string   `json:"reason"`
}

// UpdateManagedUserProfileInput 只修改基本资料；手机号省略时保留现值，工号允许显式清空。
type UpdateManagedUserProfileInput struct {
	DisplayName         string  `json:"display_name"`
	EmployeeNo          string  `json:"employee_no"`
	Phone               *string `json:"phone,omitempty"`
	ExpectedDisplayName string  `json:"expected_display_name"`
	ExpectedEmployeeNo  string  `json:"expected_employee_no"`
	ExpectedPhoneMasked string  `json:"expected_phone_masked"`
}

type Session struct {
	Token     string    `json:"access_token"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	User      User      `json:"user"`
}

const (
	SessionTypeStandard         = "standard"
	SessionTypeRememberedDevice = "remembered_device"
	SessionTypePublicDevice     = "public_device"
	SessionTypeDesktopDevice    = "desktop_device"
	SessionTypeService          = "service"
)

type CreateSessionInput struct {
	TenantID            string
	UserID              string
	TokenHash           string
	SessionType         string
	DeviceID            string
	DeviceName          string
	UserAgentHash       string
	IPPrefix            string
	ExpiresAt           time.Time
	RiskLevel           RiskLevel
	RiskAction          RiskAction
	RiskScore           int
	RiskEvaluatedAt     time.Time
	RiskPolicyVersion   string
	RiskEvidenceQuality RiskEvidenceQuality
	AuthMethod          string
	// SecurityEpoch 是登录时验证的凭据快照；创建会话时，正值仍须匹配。
	SecurityEpoch int64
}

var ErrWechatIdentityUnbound = errors.New("wechat identity is not bound")

type WechatLoginChallenge struct {
	ID             string
	StateHash      string
	PollTokenHash  string
	TenantCode     string
	Status         string
	ErrorCode      string
	TenantID       string
	UserID         string
	RememberDevice bool
	PublicDevice   bool
	ExpiresAt      time.Time
	AuthorizedAt   time.Time
	ConsumedAt     time.Time
}

type CreateWechatLoginChallengeInput struct {
	ID             string
	StateHash      string
	PollTokenHash  string
	TenantCode     string
	RememberDevice bool
	PublicDevice   bool
	ExpiresAt      time.Time
}

// WechatLoginRepository 持久保存一次性浏览器挑战和租户范围内的开放平台绑定，使回调可落到任意 API 实例。
type WechatLoginRepository interface {
	CreateWechatLoginChallenge(context.Context, CreateWechatLoginChallengeInput) error
	FindWechatLoginChallengeByState(context.Context, string, time.Time) (WechatLoginChallenge, error)
	FindWechatLoginChallenge(context.Context, string, string, time.Time) (WechatLoginChallenge, error)
	FindUserByWechatIdentity(context.Context, string, string, string, string) (UserWithPassword, error)
	FindUserByID(context.Context, string, string) (UserWithPassword, error)
	AuthorizeWechatLoginChallenge(context.Context, string, string, string, time.Time) error
	FailWechatLoginChallenge(context.Context, string, string, time.Time) error
	ConsumeWechatLoginChallenge(context.Context, string, string, time.Time) (WechatLoginChallenge, error)
}

type DeviceSession struct {
	ID          string    `json:"id"`
	SessionType string    `json:"session_type"`
	DeviceName  string    `json:"device_name"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Current     bool      `json:"current"`
}

type AuditEvent struct {
	TenantID    string
	ActorID     string
	Action      string
	TargetType  string
	TargetID    string
	BeforeValue map[string]any
	AfterValue  map[string]any
	Reason      string
	IPAddress   string
	UserAgent   string
	RequestID   string
}

type AuditRecord struct {
	ID          string         `json:"id"`
	TenantID    string         `json:"tenant_id"`
	ActorID     string         `json:"actor_id,omitempty"`
	Action      string         `json:"action"`
	TargetType  string         `json:"target_type"`
	TargetID    string         `json:"target_id,omitempty"`
	BeforeValue map[string]any `json:"before_value,omitempty"`
	AfterValue  map[string]any `json:"after_value,omitempty"`
	Reason      string         `json:"reason,omitempty"`
	IPAddress   string         `json:"ip_address,omitempty"`
	UserAgent   string         `json:"user_agent,omitempty"`
	RequestID   string         `json:"request_id,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

type AuditFilter struct {
	Action          string
	ActorID         string
	TargetType      string
	TargetID        string
	ExamID          string
	IPAddress       string
	CreatedFrom     time.Time
	CreatedTo       time.Time
	Limit           int
	CursorCreatedAt time.Time
	CursorID        string
	// ScopeMode 从已认证主体派生。在审计记录包含可信资源投影前，非租户级作用域拒绝放行。
	ScopeMode string
}

type CredentialRepository interface {
	FindUserByLogin(ctx context.Context, tenantCode string, username string) (UserWithPassword, error)
	FindPasswordHash(ctx context.Context, tenantID string, userID string) (string, error)
	RecordSuccessfulLogin(ctx context.Context, tenantID string, userID string, expectedPasswordHash string, replacementPasswordHash string) error
	UpdatePasswordAndRevokeSessions(ctx context.Context, tenantID string, userID string, expectedPasswordHash string, newPasswordHash string) (bool, int, error)
}

type SessionRepository interface {
	CreateSession(ctx context.Context, input CreateSessionInput) (DeviceSession, error)
	FindUserBySession(ctx context.Context, tokenHash string, now time.Time) (User, error)
	FindUserBySessionForReauthentication(ctx context.Context, tokenHash string, now time.Time) (User, error)
	LockSession(ctx context.Context, tenantID string, userID string, tokenHash string, now time.Time) (bool, error)
	MarkSessionReauthenticated(ctx context.Context, tenantID string, userID string, tokenHash string, startedAt time.Time, now time.Time) (bool, error)
	DeleteSession(ctx context.Context, tokenHash string, reason string) error
	ListSessions(ctx context.Context, tenantID string, userID string, currentTokenHash string, now time.Time) ([]DeviceSession, error)
	RevokeSession(ctx context.Context, tenantID string, userID string, sessionID string, reason string) (bool, error)
	RevokeAllSessions(ctx context.Context, tenantID string, userID string, reason string) (int, error)
}

type AccessScopeResolver interface {
	ResolveAccessScope(ctx context.Context, user User) (AccessScope, error)
}

type AuditRecorder interface {
	Audit(ctx context.Context, event AuditEvent) error
}

type AuditRepository interface {
	AuditRecorder
	ListAudits(ctx context.Context, tenantID string, filter AuditFilter) ([]AuditRecord, error)
}

type UserAdministrationRepository interface {
	ListManagedUsers(ctx context.Context, tenantID string, filter ManagedUserFilter) ([]ManagedUser, error)
	ListAssignableRoles(ctx context.Context, actor User) ([]AssignableRole, error)
	CreateManagedUser(ctx context.Context, actor User, actorScope AccessScope, input CreateManagedUserInput, passwordHash string) (ManagedUser, error)
	UpdateManagedUserStatus(ctx context.Context, actor User, actorScope AccessScope, userID string, status string) (ManagedUser, string, error)
	UpdateManagedUserAccess(ctx context.Context, actor User, actorScope AccessScope, userID string, input UpdateManagedUserAccessInput) (ManagedUser, error)
	UpdateManagedUserProfile(ctx context.Context, actor User, actorScope AccessScope, userID string, input UpdateManagedUserProfileInput) (ManagedUser, bool, error)
}

type ActivationRepository interface {
	CreateActivation(ctx context.Context, actor User, actorScope AccessScope, userID string, tokenHash string, expiresAt time.Time) (ActivationPreview, error)
	FindActivation(ctx context.Context, tokenHash string, now time.Time) (ActivationPreview, error)
	ActivateUser(ctx context.Context, tokenHash string, passwordHash string, now time.Time) (ActivationResult, error)
}

type RecoveryRepository interface {
	CreateRecovery(ctx context.Context, actor User, actorScope AccessScope, userID string, tokenHash string, expiresAt time.Time) (RecoveryPreview, error)
	FindRecovery(ctx context.Context, tokenHash string, now time.Time) (RecoveryPreview, error)
	CompleteRecovery(ctx context.Context, tokenHash string, passwordHash string, now time.Time) (RecoveryResult, error)
}

// Store 保留为现有组装入口使用的兼容聚合接口；新的应用服务应只依赖自己使用的能力。
type Store interface {
	CredentialRepository
	SessionRepository
	AccessScopeResolver
	AuditRepository
	UserAdministrationRepository
	ActivationRepository
	RecoveryRepository
}
