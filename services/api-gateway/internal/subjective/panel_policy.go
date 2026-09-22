package subjective

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
)

type PanelPolicyStatus string

const (
	PanelPolicyShadow      PanelPolicyStatus = "shadow"
	PanelPolicyApproved    PanelPolicyStatus = "approved"
	PanelPolicyInvalidated PanelPolicyStatus = "invalidated"
)

var ErrPanelPolicyNotReady = errors.New("subjective panel policy evaluation is not ready")

// PanelPolicy freezes both runtime disagreement thresholds and the empirical
// gates used to approve them. An approved policy remains configuration only:
// A/B/C still return rubric/evidence judgments and never own the final score.
type PanelPolicy struct {
	ID                 string                                 `json:"id"`
	TenantID           string                                 `json:"tenant_id"`
	PolicyVersion      string                                 `json:"policy_version"`
	ModelSetReference  string                                 `json:"model_set_reference"`
	EducationStage     assessment.EducationStage              `json:"education_stage"`
	SubjectCode        assessment.SubjectCode                 `json:"subject_code"`
	ArchetypeCode      string                                 `json:"archetype_code"`
	DecisionConfig     PanelDecisionConfig                    `json:"decision_config"`
	ReadinessPolicy    gradingevaluation.PanelReadinessPolicy `json:"readiness_policy"`
	EvaluationRunID    string                                 `json:"evaluation_run_id,omitempty"`
	ReadinessReport    *gradingevaluation.PanelSliceReadiness `json:"readiness_report,omitempty"`
	Status             PanelPolicyStatus                      `json:"status"`
	CreatedBy          string                                 `json:"created_by"`
	CreatedAt          time.Time                              `json:"created_at"`
	ApprovedBy         string                                 `json:"approved_by,omitempty"`
	ApprovedAt         *time.Time                             `json:"approved_at,omitempty"`
	InvalidatedBy      string                                 `json:"invalidated_by,omitempty"`
	InvalidatedAt      *time.Time                             `json:"invalidated_at,omitempty"`
	InvalidationReason string                                 `json:"invalidation_reason,omitempty"`
}

type CreatePanelPolicyInput struct {
	PolicyVersion     string                                 `json:"policy_version"`
	ModelSetReference string                                 `json:"model_set_reference"`
	EducationStage    string                                 `json:"education_stage"`
	SubjectCode       string                                 `json:"subject_code"`
	ArchetypeCode     string                                 `json:"archetype_code"`
	DecisionConfig    PanelDecisionConfig                    `json:"decision_config"`
	ReadinessPolicy   gradingevaluation.PanelReadinessPolicy `json:"readiness_policy"`
}

type PanelPolicyStore interface {
	CreatePanelPolicy(context.Context, string, string, CreatePanelPolicyInput) (PanelPolicy, error)
	GetPanelPolicy(context.Context, string, string) (PanelPolicy, error)
	FindApprovedPanelPolicy(context.Context, string, assessment.EducationStage, assessment.SubjectCode, string) (PanelPolicy, error)
	ApprovePanelPolicy(context.Context, string, string, string, string, gradingevaluation.PanelSliceReadiness, time.Time) (PanelPolicy, error)
	InvalidatePanelPolicy(context.Context, string, string, string, string, time.Time) (PanelPolicy, error)
}

type PanelPolicyEvaluationReader interface {
	GetRun(context.Context, string, string) (gradingevaluation.Run, error)
	ListPanelObservations(context.Context, string, string) ([]gradingevaluation.PanelObservation, error)
}

type PanelPolicyService struct {
	store       PanelPolicyStore
	evaluations PanelPolicyEvaluationReader
	now         func() time.Time
}

func NewPanelPolicyService(store PanelPolicyStore, evaluations PanelPolicyEvaluationReader) *PanelPolicyService {
	return &PanelPolicyService{store: store, evaluations: evaluations, now: func() time.Time { return time.Now().UTC() }}
}

func (s *PanelPolicyService) Create(ctx context.Context, tenantID, actorID string, input CreatePanelPolicyInput) (PanelPolicy, error) {
	if s == nil || s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(actorID) == "" {
		return PanelPolicy{}, ErrInvalidInput
	}
	input, err := normalizeCreatePanelPolicy(input)
	if err != nil {
		return PanelPolicy{}, err
	}
	return s.store.CreatePanelPolicy(ctx, tenantID, actorID, input)
}

// EvaluateAndApprove recomputes readiness from the immutable, de-identified
// panel observations. The caller cannot submit a pre-computed "ready" flag.
func (s *PanelPolicyService) EvaluateAndApprove(ctx context.Context, tenantID, actorID, policyID, evaluationRunID string) (PanelPolicy, gradingevaluation.PanelSliceReadiness, error) {
	if s == nil || s.store == nil || s.evaluations == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(actorID) == "" ||
		strings.TrimSpace(policyID) == "" || strings.TrimSpace(evaluationRunID) == "" {
		return PanelPolicy{}, gradingevaluation.PanelSliceReadiness{}, ErrInvalidInput
	}
	policy, err := s.store.GetPanelPolicy(ctx, tenantID, policyID)
	if err != nil {
		return PanelPolicy{}, gradingevaluation.PanelSliceReadiness{}, err
	}
	if policy.Status != PanelPolicyShadow {
		return PanelPolicy{}, gradingevaluation.PanelSliceReadiness{}, ErrIdempotencyConflict
	}
	run, err := s.evaluations.GetRun(ctx, tenantID, evaluationRunID)
	if err != nil {
		return PanelPolicy{}, gradingevaluation.PanelSliceReadiness{}, err
	}
	if run.Status != gradingevaluation.RunCompleted {
		return policy, gradingevaluation.PanelSliceReadiness{}, ErrPanelPolicyNotReady
	}
	if policy.ModelSetReference == "" || run.ModelReference != policy.ModelSetReference {
		return policy, gradingevaluation.PanelSliceReadiness{}, ErrPanelPolicyNotReady
	}
	items, err := s.evaluations.ListPanelObservations(ctx, tenantID, evaluationRunID)
	if err != nil {
		return PanelPolicy{}, gradingevaluation.PanelSliceReadiness{}, err
	}
	aligned := make([]gradingevaluation.PanelObservation, 0, len(items))
	for _, item := range items {
		if item.EducationStage == string(policy.EducationStage) && item.Subject == string(policy.SubjectCode) && item.Archetype == policy.ArchetypeCode {
			aligned = append(aligned, item)
		}
	}
	report, err := gradingevaluation.AssessPanelSliceShadowReadiness(
		string(policy.EducationStage), string(policy.SubjectCode), policy.ArchetypeCode, aligned, policy.ReadinessPolicy,
	)
	if err != nil {
		if errors.Is(err, gradingevaluation.ErrInvalidInput) {
			return policy, report, ErrPanelPolicyNotReady
		}
		return PanelPolicy{}, report, err
	}
	if !report.Ready {
		return policy, report, ErrPanelPolicyNotReady
	}
	approved, err := s.store.ApprovePanelPolicy(ctx, tenantID, policyID, actorID, evaluationRunID, report, s.now().UTC())
	return approved, report, err
}

func (s *PanelPolicyService) FindApproved(ctx context.Context, tenantID, stage, subject, archetype string) (PanelPolicy, error) {
	if s == nil || s.store == nil || strings.TrimSpace(tenantID) == "" {
		return PanelPolicy{}, ErrInvalidInput
	}
	canonicalStage, canonicalSubject, canonicalArchetype, err := normalizePanelPolicyScope(stage, subject, archetype)
	if err != nil {
		return PanelPolicy{}, err
	}
	return s.store.FindApprovedPanelPolicy(ctx, tenantID, canonicalStage, canonicalSubject, canonicalArchetype)
}

func (s *PanelPolicyService) Invalidate(ctx context.Context, tenantID, actorID, policyID, reason string) (PanelPolicy, error) {
	if s == nil || s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(actorID) == "" || strings.TrimSpace(policyID) == "" || strings.TrimSpace(reason) == "" {
		return PanelPolicy{}, ErrInvalidInput
	}
	return s.store.InvalidatePanelPolicy(ctx, tenantID, policyID, actorID, strings.TrimSpace(reason), s.now().UTC())
}

func normalizeCreatePanelPolicy(input CreatePanelPolicyInput) (CreatePanelPolicyInput, error) {
	input.PolicyVersion = strings.TrimSpace(input.PolicyVersion)
	input.ModelSetReference = strings.TrimSpace(input.ModelSetReference)
	stage, subject, archetype, err := normalizePanelPolicyScope(input.EducationStage, input.SubjectCode, input.ArchetypeCode)
	if err != nil || !validPanelPolicyVersion(input.PolicyVersion) || !validPanelModelSetReference(input.ModelSetReference) {
		return CreatePanelPolicyInput{}, ErrInvalidInput
	}
	input.EducationStage, input.SubjectCode, input.ArchetypeCode = string(stage), string(subject), archetype
	input.DecisionConfig = NormalizePanelDecisionConfig(input.DecisionConfig)
	input.DecisionConfig.HardRiskCodes = normalizedRiskCodes(input.DecisionConfig.HardRiskCodes)
	if input.DecisionConfig.PolicyVersion != input.PolicyVersion || input.ReadinessPolicy.PolicyVersion != input.PolicyVersion ||
		ValidatePanelDecisionConfig(input.DecisionConfig) != nil || gradingevaluation.ValidatePanelReadinessPolicy(input.ReadinessPolicy) != nil {
		return CreatePanelPolicyInput{}, ErrInvalidInput
	}
	return input, nil
}

func validPanelModelSetReference(value string) bool {
	if len(value) != len("panel:")+sha256.Size*2 || !strings.HasPrefix(value, "panel:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "panel:"))
	return err == nil && value == strings.ToLower(value)
}

func normalizePanelPolicyScope(stage, subject, archetype string) (assessment.EducationStage, assessment.SubjectCode, string, error) {
	canonicalStage := assessment.EducationStage(strings.ToLower(strings.TrimSpace(stage)))
	canonicalSubject, ok := assessment.NormalizeSubjectCode(subject)
	archetype = strings.TrimSpace(archetype)
	if !canonicalStage.Valid() || !ok || archetype == "" || len(archetype) > 128 {
		return "", "", "", ErrInvalidInput
	}
	return canonicalStage, canonicalSubject, archetype, nil
}

func normalizedRiskCodes(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func validPanelPolicyVersion(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index, character := range value {
		valid := character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == ':' || character == '-'
		if !valid || index == 0 && !(character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}
