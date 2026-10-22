package modelgovernance

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

var ErrGradingSelectionChanged = errors.New("grading model selection changed")
var ErrGradingModelRequired = errors.New("grading model configuration is required")

type GradingModelApplicationStore interface {
	GetGradingModelSelection(context.Context, string) (DocumentModelSelection, error)
	SaveGradingModelSelection(context.Context, string, string, DocumentModelSelectionInput) (DocumentModelSelection, error)
}

func initialGradingSelection() DocumentModelSelection {
	return DocumentModelSelection{Revision: "absent", Mode: "unconfigured"}
}

func normalizeGradingSelection(input DocumentModelSelectionInput) (DocumentModelSelectionInput, error) {
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

// CanAssignGradingModel 允许平台管理员指定已启用且凭据可用的学校模型；实际图片能力在识别请求中验证。
func CanAssignGradingModel(config ManagedAPIConfig) bool {
	return config.DeletedAt == nil && config.Status == "active" && config.CredentialConfigured
}

// ResolveGradingManagedAPI 只接受本校显式阅卷选择；停用或凭据不可用时失败，不回退本地或其他用途模型。
func ResolveGradingManagedAPI(ctx context.Context, store ManagedAPIConfigStore, tenantID string) (*ManagedAPIConnection, error) {
	applications, ok := store.(GradingModelApplicationStore)
	if !ok {
		return nil, ErrGradingModelRequired
	}
	selection, err := applications.GetGradingModelSelection(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if selection.Mode != "model" || selection.ModelConfigID == "" {
		return nil, ErrGradingModelRequired
	}
	connection, err := store.GetManagedAPIConnection(ctx, tenantID, selection.ModelConfigID)
	if err != nil {
		return nil, err
	}
	if connection.Config.TenantID != tenantID || !CanAssignGradingModel(connection.Config) || connection.APIKey == "" {
		return nil, ErrGradingModelRequired
	}
	return &connection, nil
}
