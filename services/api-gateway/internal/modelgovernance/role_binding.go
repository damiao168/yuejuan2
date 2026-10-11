package modelgovernance

import (
	"context"
	"errors"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"github.com/google/uuid"
)

const (
	ModelRolePrimaryA = "primary_a"
	ModelRolePrimaryB = "primary_b"
	ModelRoleArbiter  = "arbiter"
	// ModelRoleBindingAbsent 要求该范围的角色尚不存在，不能把缺少版本当成覆盖授权。
	ModelRoleBindingAbsent = "absent"
)

var ErrModelRoleBindingChanged = errors.New("model role binding changed")

type ModelRoleBinding struct {
	// Revision 每次成功保存都更换，内容改回原值也不会复用旧修订。
	Revision                string    `json:"revision"`
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
	// ExpectedRevision 必须来自读取结果；首次创建显式传 absent。
	ExpectedRevision        string `json:"expected_revision"`
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

// ModelRoleBindingStore 保存必须带读取修订或显式 absent。
// ErrModelRoleBindingChanged 不会写入草稿，调用方须重新读取并让管理员核对后再保存。
type ModelRoleBindingStore interface {
	SaveModelRoleBinding(context.Context, string, string, SaveModelRoleBindingInput) (ModelRoleBinding, error)
	ListModelRoleBindings(context.Context, string, string, string, string) ([]ModelRoleBinding, error)
}

// 管理员视图包含停用或暂时未验证的绑定；运行时解析必须继续使用会过滤这些绑定的 ListModelRoleBindings。
type ModelRoleBindingAdminStore interface {
	ModelRoleBindingStore
	ListConfiguredModelRoleBindings(context.Context, string, string, string, string) ([]ModelRoleBinding, error)
}

// ResolvePanelRoleBindings 按存储返回顺序选取各角色首个启用绑定；模型验证由运行时存储查询负责。
// 两个主模型和仲裁模型必须完整，且仲裁模型必须独立并强于两个主模型。
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
		setFirstPanelRoleBinding(&out, item)
	}
	if out.PrimaryA.ID == "" || out.PrimaryB.ID == "" || out.Arbiter.ID == "" ||
		out.Arbiter.ManagedModelAPIConfigID == out.PrimaryA.ManagedModelAPIConfigID ||
		out.Arbiter.ManagedModelAPIConfigID == out.PrimaryB.ManagedModelAPIConfigID ||
		out.Arbiter.StrengthRank <= out.PrimaryA.StrengthRank || out.Arbiter.StrengthRank <= out.PrimaryB.StrengthRank {
		return PanelRoleBindings{}, ErrManagedCapabilityRequired
	}
	return out, nil
}

func setFirstPanelRoleBinding(bindings *PanelRoleBindings, item ModelRoleBinding) {
	var target *ModelRoleBinding
	switch item.AgentRole {
	case ModelRolePrimaryA:
		target = &bindings.PrimaryA
	case ModelRolePrimaryB:
		target = &bindings.PrimaryB
	case ModelRoleArbiter:
		target = &bindings.Arbiter
	}
	if target != nil && target.ID == "" {
		*target = item
	}
}

func normalizeRoleBinding(input SaveModelRoleBindingInput) (SaveModelRoleBindingInput, error) {
	stage, subject, archetype, err := normalizeRoleScope(input.EducationStage, input.SubjectCode, input.ArchetypeCode)
	if err != nil {
		return SaveModelRoleBindingInput{}, err
	}
	input.EducationStage, input.SubjectCode, input.ArchetypeCode = stage, subject, archetype
	input.ExpectedRevision = strings.TrimSpace(input.ExpectedRevision)
	if input.ExpectedRevision != ModelRoleBindingAbsent {
		revision, err := uuid.Parse(input.ExpectedRevision)
		if err != nil {
			return SaveModelRoleBindingInput{}, ErrInvalidManagedConfig
		}
		input.ExpectedRevision = revision.String()
	}
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
