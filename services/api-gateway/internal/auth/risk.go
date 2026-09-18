package auth

import (
	"context"
	"slices"
	"strings"
	"time"
)

const (
	RiskPolicyVersionV1     = "login-rba-v1"
	RiskHistoryLimit        = 20
	RiskHistoryRetention    = 180 * 24 * time.Hour
	RiskFailureWindow       = 15 * time.Minute
	RiskRecoveryWindow      = 24 * time.Hour
	DefaultDeviceBindingTTL = 180 * 24 * time.Hour
)

type RiskPurpose string

const (
	RiskPurposeLogin           RiskPurpose = "login"
	RiskPurposeSession         RiskPurpose = "session"
	RiskPurposeRecovery        RiskPurpose = "recovery"
	RiskPurposeSensitiveAction RiskPurpose = "sensitive_action"
)

type RiskLevel string

const (
	RiskLevelLow    RiskLevel = "low"
	RiskLevelMedium RiskLevel = "medium"
	RiskLevelHigh   RiskLevel = "high"
)

type RiskAction string

const (
	RiskActionAllow            RiskAction = "allow"
	RiskActionAllowRestricted  RiskAction = "allow_restricted"
	RiskActionStepUp           RiskAction = "step_up"
	RiskActionDenyTemporarily  RiskAction = "deny_temporarily"
	RiskActionRecoveryRequired RiskAction = "recovery_required"
)

type RiskEvidenceQuality string

const (
	RiskEvidenceColdStart  RiskEvidenceQuality = "cold_start"
	RiskEvidenceLimited    RiskEvidenceQuality = "limited"
	RiskEvidenceSufficient RiskEvidenceQuality = "sufficient"
)

type RiskDecision struct {
	Purpose           RiskPurpose
	Level             RiskLevel
	Action            RiskAction
	RequiredAuthLevel int
	RequiredMethods   []string
	MaxAuthAge        time.Duration
	ReasonCodes       []string
	PolicyVersion     string
	EvidenceQuality   RiskEvidenceQuality
	Score             int
	FamilyScores      map[string]int
	ExpiresAt         time.Time
}

type LoginRiskContextRequest struct {
	TenantID        string
	UserID          string
	DeviceTokenHash string
	UserAgentHash   string
	IPPrefix        string
	Now             time.Time
}

type LoginRiskContext struct {
	PriorSuccessfulLogins int
	KnownDevice           bool
	TrustedDevice         bool
	KnownUserAgent        bool
	KnownNetwork          bool
	RecentFailures        int
	RecentRecovery        bool
}

type RiskEvent struct {
	TenantID         string
	UserID           string
	SessionID        string
	Purpose          RiskPurpose
	Level            RiskLevel
	Action           RiskAction
	Score            int
	ReasonCodes      []string
	FamilyScores     map[string]int
	EvidenceQuality  RiskEvidenceQuality
	PolicyVersion    string
	UserAgentHash    string
	IPPrefix         string
	DeviceRecognized bool
	DeviceTrusted    bool
	OccurredAt       time.Time
}

type ObservedDeviceInput struct {
	TenantID       string
	UserID         string
	TokenHash      string
	UserAgentHash  string
	IPPrefix       string
	AssuranceLevel int
	TrustBasis     string
	Now            time.Time
	ExpiresAt      time.Time
}

// RiskStore is deliberately separate from Store so authentication remains
// available when an older or specialized store has not implemented RBA yet.
// In that case the handler fails neutral and records only a limited decision.
type RiskStore interface {
	LoadLoginRiskContext(ctx context.Context, request LoginRiskContextRequest) (LoginRiskContext, error)
	RecordRiskEvent(ctx context.Context, event RiskEvent) error
	StoreObservedDevice(ctx context.Context, input ObservedDeviceInput) error
}

func DefaultLoginRiskDecision(now time.Time) RiskDecision {
	return RiskDecision{
		Purpose:           RiskPurposeLogin,
		Level:             RiskLevelLow,
		Action:            RiskActionAllow,
		RequiredAuthLevel: 1,
		ReasonCodes:       []string{"risk_context_unavailable"},
		PolicyVersion:     RiskPolicyVersionV1,
		EvidenceQuality:   RiskEvidenceLimited,
		FamilyScores:      map[string]int{},
		ExpiresAt:         now.Add(5 * time.Minute),
	}
}

// EvaluateLoginRisk uses capped, independent signal families. Network and
// browser novelty cannot by themselves block a teacher behind campus NAT, and
// cold-start accounts are never promoted to high risk merely for being new.
func EvaluateLoginRisk(input LoginRiskContext, now time.Time) RiskDecision {
	decision := RiskDecision{
		Purpose:           RiskPurposeLogin,
		Level:             RiskLevelLow,
		Action:            RiskActionAllow,
		RequiredAuthLevel: 1,
		PolicyVersion:     RiskPolicyVersionV1,
		ReasonCodes:       []string{},
		FamilyScores:      map[string]int{},
		ExpiresAt:         now.Add(5 * time.Minute),
	}

	switch {
	case input.PriorSuccessfulLogins == 0:
		decision.EvidenceQuality = RiskEvidenceColdStart
		decision.ReasonCodes = append(decision.ReasonCodes, "cold_start")
	case input.PriorSuccessfulLogins < 3:
		decision.EvidenceQuality = RiskEvidenceLimited
	default:
		decision.EvidenceQuality = RiskEvidenceSufficient
	}

	accountScore := min(input.RecentFailures*8, 40)
	if accountScore > 0 {
		decision.FamilyScores["account_abuse"] = accountScore
		decision.ReasonCodes = append(decision.ReasonCodes, "recent_account_failures")
	}

	if input.RecentRecovery {
		decision.FamilyScores["credential_lifecycle"] = 40
		decision.ReasonCodes = append(decision.ReasonCodes, "recent_credential_recovery")
	}

	if input.PriorSuccessfulLogins > 0 {
		deviceScore := 0
		if !input.KnownDevice {
			deviceScore += 10
			decision.ReasonCodes = append(decision.ReasonCodes, "new_device_binding")
		}
		if !input.KnownUserAgent {
			deviceScore += 10
			decision.ReasonCodes = append(decision.ReasonCodes, "new_client_family")
		}
		decision.FamilyScores["device_context"] = min(deviceScore, 25)

		if !input.KnownNetwork {
			decision.FamilyScores["network_context"] = 10
			decision.ReasonCodes = append(decision.ReasonCodes, "new_network_prefix")
		}
	}

	for _, score := range decision.FamilyScores {
		decision.Score += score
	}
	decision.Score = min(decision.Score, 100)

	switch {
	case decision.Score >= 60 && decision.EvidenceQuality == RiskEvidenceSufficient:
		decision.Level = RiskLevelHigh
		decision.Action = RiskActionStepUp
		decision.RequiredAuthLevel = 2
		decision.RequiredMethods = []string{"passkey", "enterprise_idp", "totp"}
		decision.MaxAuthAge = 10 * time.Minute
	case decision.Score >= 25:
		decision.Level = RiskLevelMedium
		decision.Action = RiskActionAllowRestricted
	default:
		decision.Level = RiskLevelLow
		decision.Action = RiskActionAllow
	}

	slices.Sort(decision.ReasonCodes)
	decision.ReasonCodes = slices.Compact(decision.ReasonCodes)
	return decision
}

func normalizeRiskLevel(value RiskLevel) RiskLevel {
	switch value {
	case RiskLevelMedium, RiskLevelHigh:
		return value
	default:
		return RiskLevelLow
	}
}

func normalizeRiskAction(value RiskAction) RiskAction {
	switch value {
	case RiskActionAllowRestricted, RiskActionStepUp, RiskActionDenyTemporarily, RiskActionRecoveryRequired:
		return value
	default:
		return RiskActionAllow
	}
}

func normalizeRiskEvidenceQuality(value RiskEvidenceQuality) RiskEvidenceQuality {
	switch value {
	case RiskEvidenceColdStart, RiskEvidenceSufficient:
		return value
	default:
		return RiskEvidenceLimited
	}
}

func normalizeRiskPolicyVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return "legacy"
	}
	return value
}

func normalizeRiskMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off":
		return "off"
	default:
		return "shadow"
	}
}
