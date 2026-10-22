package modelgovernance

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrDocumentSelectionChanged = errors.New("document model selection changed")

type DocumentModelSelection struct {
	Revision      string     `json:"revision"`
	Mode          string     `json:"mode"`
	ModelConfigID string     `json:"model_config_id,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

type DocumentModelSelectionInput struct {
	ExpectedRevision string `json:"expected_revision"`
	Mode             string `json:"mode"`
	ModelConfigID    string `json:"model_config_id,omitempty"`
}

type ModelApplicationStore interface {
	GetDocumentModelSelection(context.Context, string) (DocumentModelSelection, error)
	SaveDocumentModelSelection(context.Context, string, string, DocumentModelSelectionInput) (DocumentModelSelection, error)
	ListGradingModelUses(context.Context, string) ([]GradingModelUse, error)
}

type GradingModelUse struct {
	ModelConfigID  string `json:"model_config_id"`
	EducationStage string `json:"education_stage"`
	SubjectCode    string `json:"subject_code"`
	ArchetypeCode  string `json:"archetype_code"`
	AgentRole      string `json:"agent_role"`
}

func initialDocumentSelection() DocumentModelSelection {
	return DocumentModelSelection{Revision: "absent", Mode: "follow_chat"}
}

func normalizeDocumentSelection(input DocumentModelSelectionInput) (DocumentModelSelectionInput, error) {
	input.Mode, input.ModelConfigID = strings.TrimSpace(input.Mode), strings.TrimSpace(input.ModelConfigID)
	if input.ExpectedRevision != "absent" {
		if _, err := uuid.Parse(input.ExpectedRevision); err != nil {
			return input, ErrInvalidManagedConfig
		}
	}
	if input.Mode == "follow_chat" && input.ModelConfigID == "" {
		return input, nil
	}
	if input.Mode == "model" {
		if _, err := uuid.Parse(input.ModelConfigID); err == nil {
			return input, nil
		}
	}
	return input, ErrInvalidManagedConfig
}

// 独立解析选择不依赖聊天默认项；每次调用重新检查启用状态与当前结构化证据，失效时返回错误。
func ResolveDocumentManagedAPI(ctx context.Context, store ManagedAPIConfigStore, tenantID string) (*ManagedAPIConnection, error) {
	selection := initialDocumentSelection()
	if applications, ok := store.(ModelApplicationStore); ok {
		var err error
		selection, err = applications.GetDocumentModelSelection(ctx, tenantID)
		if err != nil {
			return nil, err
		}
	}
	var connection *ManagedAPIConnection
	var err error
	if selection.Mode == "follow_chat" {
		connection, err = ResolveDefaultManagedAPI(ctx, store, tenantID)
	} else if selection.Mode == "model" {
		var selected ManagedAPIConnection
		selected, err = store.GetManagedAPIConnection(ctx, tenantID, selection.ModelConfigID)
		connection = &selected
	} else {
		return nil, ErrInvalidManagedConfig
	}
	if err != nil || connection == nil {
		return nil, err
	}
	if connection.Config.TenantID != tenantID || connection.Config.Status != "active" || !connection.Config.CredentialConfigured {
		return nil, ErrManagedConfigUnavailable
	}
	if !managedCapabilityVerified(connection.Config) {
		return nil, ErrManagedCapabilityRequired
	}
	return connection, nil
}
