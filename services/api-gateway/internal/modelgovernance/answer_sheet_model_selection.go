package modelgovernance

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

var ErrAnswerSheetSelectionChanged = errors.New("answer sheet model selection changed")
var ErrAnswerSheetModelRequired = errors.New("answer sheet model configuration is required")

type AnswerSheetModelApplicationStore interface {
	GetAnswerSheetModelSelection(context.Context, string) (DocumentModelSelection, error)
	SaveAnswerSheetModelSelection(context.Context, string, string, DocumentModelSelectionInput) (DocumentModelSelection, error)
}

func initialAnswerSheetSelection() DocumentModelSelection {
	return DocumentModelSelection{Revision: "absent", Mode: "unconfigured"}
}

func normalizeAnswerSheetSelection(input DocumentModelSelectionInput) (DocumentModelSelectionInput, error) {
	input.Mode, input.ModelConfigID = strings.TrimSpace(input.Mode), strings.TrimSpace(input.ModelConfigID)
	if input.ExpectedRevision != "absent" && uuid.Validate(input.ExpectedRevision) != nil {
		return input, ErrInvalidManagedConfig
	}
	if input.Mode == "unconfigured" && input.ModelConfigID == "" {
		return input, nil
	}
	if input.Mode == "model" && uuid.Validate(input.ModelConfigID) == nil {
		return input, nil
	}
	return input, ErrInvalidManagedConfig
}

// CanAssignAnswerSheetModel 允许平台管理员指定已启用且凭据可用的学校模型；实际图片能力在识别请求中验证。
func CanAssignAnswerSheetModel(config ManagedAPIConfig) bool {
	return config.DeletedAt == nil && config.Status == "active" && config.CredentialConfigured
}

// ResolveAnswerSheetManagedAPI 不回退聊天或评分模型；停用或凭据不可用时明确失败，不改用其他学校模型。
func ResolveAnswerSheetManagedAPI(ctx context.Context, store ManagedAPIConfigStore, tenantID string) (*ManagedAPIConnection, error) {
	applications, ok := store.(AnswerSheetModelApplicationStore)
	if !ok {
		return nil, ErrAnswerSheetModelRequired
	}
	selection, err := applications.GetAnswerSheetModelSelection(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if selection.Mode != "model" || selection.ModelConfigID == "" {
		return nil, ErrAnswerSheetModelRequired
	}
	connection, err := store.GetManagedAPIConnection(ctx, tenantID, selection.ModelConfigID)
	if err != nil {
		return nil, err
	}
	if connection.Config.TenantID != tenantID || !CanAssignAnswerSheetModel(connection.Config) || connection.APIKey == "" {
		return nil, ErrAnswerSheetModelRequired
	}
	return &connection, nil
}
