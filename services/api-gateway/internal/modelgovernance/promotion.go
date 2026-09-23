package modelgovernance

import (
	"strings"
	"time"
)

const maxModelApprovalLifetime = 365 * 24 * time.Hour

type ModelApproval struct {
	ID                    string     `json:"id"`
	TenantID              string     `json:"tenant_id,omitempty"`
	EvaluationRunID       string     `json:"evaluation_run_id"`
	EvaluationCandidateID string     `json:"evaluation_candidate_id"`
	DeploymentID          string     `json:"deployment_id"`
	ModelConfigID         string     `json:"model_config_id,omitempty"`
	ProviderKey           string     `json:"provider_key"`
	DeploymentKey         string     `json:"deployment_key"`
	ModelName             string     `json:"model_name"`
	ModelVersion          string     `json:"model_version"`
	PromptVersion         string     `json:"prompt_version"`
	RubricVersion         string     `json:"rubric_version"`
	DatasetReference      string     `json:"dataset_reference"`
	DatasetSHA256         string     `json:"dataset_sha256"`
	AuthorizationRef      string     `json:"authorization_reference"`
	Subject               string     `json:"subject"`
	Grade                 string     `json:"grade"`
	QuestionType          string     `json:"question_type"`
	Modality              string     `json:"modality"`
	ManualReviewRate      float64    `json:"manual_review_rate"`
	DecisionReference     string     `json:"decision_reference"`
	ExpiresAt             time.Time  `json:"expires_at"`
	RevokedAt             *time.Time `json:"revoked_at,omitempty"`
	Revision              int64      `json:"revision"`
	CreatedAt             time.Time  `json:"created_at"`
}

type ModelApprovalInput struct {
	TenantID          string    `json:"tenant_id,omitempty"`
	EvaluationRunID   string    `json:"evaluation_run_id"`
	DeploymentID      string    `json:"deployment_id"`
	ModelConfigID     string    `json:"model_config_id"`
	ManualReviewRate  float64   `json:"manual_review_rate"`
	DecisionReference string    `json:"decision_reference"`
	ExpiresAt         time.Time `json:"expires_at"`
	Reason            string    `json:"reason"`
}

type ModelApprovalRevokeInput struct {
	Reason           string `json:"reason"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type ModelApprovalScope struct {
	ModelConfigID string
	DeploymentID  string
	ModelVersion  string
	PromptVersion string
	RubricVersion string
	Subject       string
	Grade         string
	QuestionType  string
	Modality      string
}

func ValidateModelApprovalInput(input ModelApprovalInput, now time.Time) error {
	if strings.TrimSpace(input.EvaluationRunID) == "" ||
		(strings.TrimSpace(input.DeploymentID) == "") == (strings.TrimSpace(input.ModelConfigID) == "") ||
		!governanceKey.MatchString(strings.TrimSpace(input.DecisionReference)) ||
		input.ManualReviewRate < 0 ||
		input.ManualReviewRate > 1 ||
		now.IsZero() ||
		input.ExpiresAt.IsZero() ||
		!input.ExpiresAt.After(now) ||
		input.ExpiresAt.Sub(now) > maxModelApprovalLifetime ||
		strings.TrimSpace(input.Reason) == "" {
		return ErrInvalidPromotion
	}
	return nil
}

func (approval ModelApproval) IsActive(now time.Time) bool {
	return approval.RevokedAt == nil && !now.IsZero() && approval.ExpiresAt.After(now)
}

func (approval ModelApproval) Matches(scope ModelApprovalScope, now time.Time) bool {
	return approval.IsActive(now) &&
		approval.ModelConfigID == scope.ModelConfigID &&
		approval.DeploymentID == scope.DeploymentID &&
		approval.ModelVersion == scope.ModelVersion &&
		approval.PromptVersion == scope.PromptVersion &&
		approval.RubricVersion == scope.RubricVersion &&
		approval.Subject == scope.Subject &&
		approval.Grade == scope.Grade &&
		approval.QuestionType == scope.QuestionType &&
		approval.Modality == scope.Modality
}

func RequireActiveModelApproval(
	approvals []ModelApproval,
	scope ModelApprovalScope,
	now time.Time,
) (ModelApproval, error) {
	for _, approval := range approvals {
		if approval.Matches(scope, now) {
			return approval, nil
		}
	}
	return ModelApproval{}, ErrModelNotApproved
}
