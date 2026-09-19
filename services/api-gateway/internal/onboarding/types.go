package onboarding

type CheckState string

const (
	CheckReady       CheckState = "ready"
	CheckAction      CheckState = "action_required"
	CheckWarning     CheckState = "warning"
	CheckOptional    CheckState = "optional"
	CheckUnavailable CheckState = "unavailable"
)

type Severity string

const (
	SeverityBlocking    Severity = "blocking"
	SeverityRecommended Severity = "recommended"
	SeverityOptional    Severity = "optional"
)

type ReadinessCheck struct {
	Key         string         `json:"key"`
	State       CheckState     `json:"state"`
	Severity    Severity       `json:"severity"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	ActionCode  string         `json:"action_code,omitempty"`
	ActionPath  string         `json:"action_path,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type NextAction struct {
	Code string `json:"code"`
	Path string `json:"path"`
}

type OnboardingReadiness struct {
	Scope          string           `json:"scope"`
	ReadyForUse    bool             `json:"ready_for_use"`
	CompletedCount int              `json:"completed_count"`
	TotalRequired  int              `json:"total_required"`
	Checks         []ReadinessCheck `json:"checks"`
	NextAction     *NextAction      `json:"next_action,omitempty"`
}

type ResourceScope struct {
	TenantWide bool
	SchoolIDs  []string
	GradeIDs   []string
	ClassIDs   []string
	ExamIDs    []string
}

type OrganizationSummary struct {
	HasSchool            bool
	HasTeachingStructure bool
	HasStudents          bool
}

type Actor struct {
	ID       string
	TenantID string
	Status   string
	Roles    []string
	Scope    ResourceScope
}

type SystemReadinessSummary struct {
	CoreReady    bool
	AIMode       string
	AIConfigured bool
	AIAvailable  bool
	AIModel      string
}

type DataPolicySummary struct {
	ExternalEnabled    bool
	TextExportEnabled  bool
	ImageExportEnabled bool
	FallbackMode       string
}
