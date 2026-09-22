package modelgovernance

import (
	"context"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

const (
	ModelRolePrimaryA = "primary_a"
	ModelRolePrimaryB = "primary_b"
	ModelRoleArbiter  = "arbiter"
)

type ModelRoleBinding struct {
	ID                      string    `json:"id"`
	TenantID                string    `json:"tenant_id"`
	EducationStage          string    `json:"education_stage"`
	SubjectCode             string    `json:"subject_code"`
	ArchetypeCode           string    `json:"archetype_code"`
	AgentRole               string    `json:"agent_role"`
	ManagedModelAPIConfigID string    `json:"managed_model_api_config_id"`
	PromptVersion           string    `json:"prompt_version"`
	StrengthRank            int       `json:"strength_rank"`
	Status                  string    `json:"status"`
	CreatedBy               string    `json:"created_by"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type SaveModelRoleBindingInput struct {
	EducationStage          string `json:"education_stage"`
	SubjectCode             string `json:"subject_code"`
	ArchetypeCode           string `json:"archetype_code"`
	AgentRole               string `json:"agent_role"`
	ManagedModelAPIConfigID string `json:"managed_model_api_config_id"`
	PromptVersion           string `json:"prompt_version"`
	StrengthRank            int    `json:"strength_rank"`
	Status                  string `json:"status"`
}

type PanelRoleBindings struct {
	PrimaryA ModelRoleBinding `json:"primary_a"`
	PrimaryB ModelRoleBinding `json:"primary_b"`
	Arbiter  ModelRoleBinding `json:"arbiter"`
}

type ModelRoleBindingStore interface {
	SaveModelRoleBinding(context.Context, string, string, SaveModelRoleBindingInput) (ModelRoleBinding, error)
	ListModelRoleBindings(context.Context, string, string, string, string) ([]ModelRoleBinding, error)
}

// The admin view includes disabled or temporarily unverified bindings. Runtime
// resolution must continue using ListModelRoleBindings, which filters them out.
type ModelRoleBindingAdminStore interface {
	ModelRoleBindingStore
	ListConfiguredModelRoleBindings(context.Context, string, string, string, string) ([]ModelRoleBinding, error)
}

func ResolvePanelRoleBindings(ctx context.Context, store ModelRoleBindingStore, tenantID, stage, subject, archetype string) (PanelRoleBindings, error) {
	if store == nil || strings.TrimSpace(tenantID) == "" {
		return PanelRoleBindings{}, ErrInvalidManagedConfig
	}
	stage, subject, archetype, err := normalizeRoleScope(stage, subject, archetype)
	if err != nil {
		return PanelRoleBindings{}, err
	}
	items, err := store.ListModelRoleBindings(ctx, tenantID, stage, subject, archetype)
	if err != nil {
		return PanelRoleBindings{}, err
	}
	var out PanelRoleBindings
	for _, item := range items {
		if item.Status != "active" {
			continue
		}
		switch item.AgentRole {
		case ModelRolePrimaryA:
			if out.PrimaryA.ID == "" {
				out.PrimaryA = item
			}
		case ModelRolePrimaryB:
			if out.PrimaryB.ID == "" {
				out.PrimaryB = item
			}
		case ModelRoleArbiter:
			if out.Arbiter.ID == "" {
				out.Arbiter = item
			}
		}
	}
	if out.PrimaryA.ID == "" || out.PrimaryB.ID == "" || out.Arbiter.ID == "" ||
		out.Arbiter.ManagedModelAPIConfigID == out.PrimaryA.ManagedModelAPIConfigID ||
		out.Arbiter.ManagedModelAPIConfigID == out.PrimaryB.ManagedModelAPIConfigID ||
		out.Arbiter.StrengthRank <= out.PrimaryA.StrengthRank || out.Arbiter.StrengthRank <= out.PrimaryB.StrengthRank {
		return PanelRoleBindings{}, ErrManagedCapabilityRequired
	}
	return out, nil
}

func normalizeRoleBinding(input SaveModelRoleBindingInput) (SaveModelRoleBindingInput, error) {
	stage, subject, archetype, err := normalizeRoleScope(input.EducationStage, input.SubjectCode, input.ArchetypeCode)
	if err != nil {
		return SaveModelRoleBindingInput{}, err
	}
	input.EducationStage, input.SubjectCode, input.ArchetypeCode = stage, subject, archetype
	input.AgentRole = strings.TrimSpace(input.AgentRole)
	input.ManagedModelAPIConfigID = strings.TrimSpace(input.ManagedModelAPIConfigID)
	input.PromptVersion = strings.TrimSpace(input.PromptVersion)
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "active"
	}
	if (input.AgentRole != ModelRolePrimaryA && input.AgentRole != ModelRolePrimaryB && input.AgentRole != ModelRoleArbiter) ||
		input.ManagedModelAPIConfigID == "" || input.PromptVersion == "" || input.StrengthRank <= 0 ||
		(input.Status != "active" && input.Status != "disabled") {
		return SaveModelRoleBindingInput{}, ErrInvalidManagedConfig
	}
	return input, nil
}

func normalizeRoleScope(stage, subject, archetype string) (string, string, string, error) {
	stage = strings.ToLower(strings.TrimSpace(stage))
	if !assessment.EducationStage(stage).Valid() {
		return "", "", "", ErrInvalidManagedConfig
	}
	canonical, ok := assessment.NormalizeSubjectCode(subject)
	if !ok {
		return "", "", "", ErrInvalidManagedConfig
	}
	archetype = strings.TrimSpace(archetype)
	if archetype == "" {
		archetype = "*"
	}
	if len(archetype) > 128 {
		return "", "", "", ErrInvalidManagedConfig
	}
	return stage, string(canonical), archetype, nil
}
