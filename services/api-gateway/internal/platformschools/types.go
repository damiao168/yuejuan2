package platformschools

import "time"

const (
	ModelHealthy      = "healthy"
	ModelWarning      = "warning"
	ModelUnconfigured = "unconfigured"
)

type ListFilter struct {
	Query       string
	Status      string
	Activity    string
	ModelHealth string
	UsageDays   int
	Sort        string
	Order       string
	Limit       int
	Cursor      string
}

type AdministratorSummary struct {
	ID          string     `json:"id,omitempty"`
	DisplayName string     `json:"display_name,omitempty"`
	Username    string     `json:"username,omitempty"`
	AdminCount  int64      `json:"admin_count"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

type MemberCounts struct {
	Accounts       int64 `json:"accounts"`
	ActiveAccounts int64 `json:"active_accounts"`
	Administrators int64 `json:"administrators"`
	Teachers       int64 `json:"teachers"`
	Graders        int64 `json:"graders"`
	Students       int64 `json:"students"`
	Classes        int64 `json:"classes"`
}

type UsageSummary struct {
	InputTokens           int64 `json:"input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	ReasoningTokens       int64 `json:"reasoning_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
	Requests              int64 `json:"requests"`
	ArbitrationRequests   int64 `json:"arbitration_requests"`
	EstimatedCostMicroUSD int64 `json:"estimated_cost_microusd"`
	WindowDays            int   `json:"window_days"`
}

type ModelHealth struct {
	Status                 string     `json:"status"`
	ConfigStatus           string     `json:"config_status,omitempty"`
	ConfigID               string     `json:"config_id,omitempty"`
	DisplayName            string     `json:"display_name,omitempty"`
	ProviderKey            string     `json:"provider_key,omitempty"`
	ModelName              string     `json:"model_name,omitempty"`
	CredentialHint         string     `json:"credential_hint,omitempty"`
	ConnectionStatus       string     `json:"connection_status,omitempty"`
	ConnectionMessage      string     `json:"connection_message,omitempty"`
	CapabilityStatus       string     `json:"capability_status,omitempty"`
	CapabilityVersion      string     `json:"capability_version,omitempty"`
	CapabilityMessage      string     `json:"capability_message,omitempty"`
	LatencyMS              int64      `json:"latency_ms,omitempty"`
	LastTestedAt           *time.Time `json:"last_tested_at,omitempty"`
	LastCapabilityTestedAt *time.Time `json:"last_capability_tested_at,omitempty"`
}

type PlatformSchoolSummary struct {
	TenantID              string               `json:"tenant_id"`
	SchoolID              string               `json:"school_id"`
	Name                  string               `json:"name"`
	Code                  string               `json:"code"`
	Status                string               `json:"status"`
	CreatedAt             time.Time            `json:"created_at"`
	LastActivityAt        *time.Time           `json:"last_activity_at,omitempty"`
	Administrator         AdministratorSummary `json:"administrator"`
	Members               MemberCounts         `json:"members"`
	Usage                 UsageSummary         `json:"usage"`
	ModelHealth           ModelHealth          `json:"model_health"`
	ExamCount             int64                `json:"exam_count"`
	AttentionReasons      []string             `json:"attention_reasons"`
	ConsecutiveAIFailures int64                `json:"-"`
}

type FleetSummary struct {
	Total    int `json:"total"`
	Active   int `json:"active"`
	Disabled int `json:"disabled"`
}

type ListResult struct {
	Schools    []PlatformSchoolSummary `json:"schools"`
	Summary    FleetSummary            `json:"summary"`
	NextCursor string                  `json:"next_cursor"`
	HasMore    bool                    `json:"has_more"`
}

type Member struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	PhoneMasked string     `json:"phone_masked,omitempty"`
	EmployeeNo  string     `json:"employee_no,omitempty"`
	Status      string     `json:"status"`
	Roles       []string   `json:"roles"`
	SchoolID    string     `json:"school_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ActivatedAt *time.Time `json:"activated_at,omitempty"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

type MemberSummary struct {
	Total    int64 `json:"total"`
	Active   int64 `json:"active"`
	Disabled int64 `json:"disabled"`
	Admins   int64 `json:"admins"`
	Teachers int64 `json:"teachers"`
	Graders  int64 `json:"graders"`
}

type MembersResult struct {
	Members []Member      `json:"members"`
	Summary MemberSummary `json:"summary"`
}

type UsageRange struct {
	Start time.Time
	End   time.Time
	Days  int
}

type UsageTrendPoint struct {
	Date         string `json:"date"`
	TotalTokens  int64  `json:"total_tokens"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	Requests     int64  `json:"requests"`
}

type UsageBreakdown struct {
	Key                   string  `json:"key"`
	Label                 string  `json:"label"`
	TotalTokens           int64   `json:"total_tokens"`
	Requests              int64   `json:"requests"`
	Share                 float64 `json:"share"`
	EstimatedCostMicroUSD int64   `json:"estimated_cost_microusd"`
}

type UsageResult struct {
	Range     UsageRange        `json:"-"`
	Summary   UsageSummary      `json:"summary"`
	Trend     []UsageTrendPoint `json:"trend"`
	ByFeature []UsageBreakdown  `json:"by_feature"`
	ByModel   []UsageBreakdown  `json:"by_model"`
	StartDate string            `json:"start_date"`
	EndDate   string            `json:"end_date"`
}

type ModelRole struct {
	AgentRole   string `json:"agent_role"`
	ModelName   string `json:"model_name"`
	ProviderKey string `json:"provider_key"`
	Status      string `json:"status"`
}

type ModelHealthResult struct {
	DefaultModel ModelHealth `json:"default_model"`
	Roles        []ModelRole `json:"roles"`
}

type ActivityItem struct {
	ID         string    `json:"id"`
	EventType  string    `json:"event_type"`
	Severity   string    `json:"severity"`
	Title      string    `json:"title"`
	Summary    string    `json:"summary"`
	ActorName  string    `json:"actor_name,omitempty"`
	HappenedAt time.Time `json:"happened_at"`
}

type SecuritySummary struct {
	ActiveAdmins   int64      `json:"active_admins"`
	MFAEnabled     int64      `json:"mfa_enabled"`
	ActiveSessions int64      `json:"active_sessions"`
	LastLoginAt    *time.Time `json:"last_login_at,omitempty"`
}

type ActivityResult struct {
	Activities []ActivityItem  `json:"activities"`
	Security   SecuritySummary `json:"security"`
}
