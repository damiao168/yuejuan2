package server

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
)

// newImageGradingModelResolver 只读平台为当前学校选择的自动阅卷模型。
// 停用、取消用途或更换连接后拒绝旧版本，不回退聊天、答题卡或本地模型。
func newImageGradingModelResolver(store modelgovernance.Store) func(context.Context, string) (*subjective.ImageModelConfig, error) {
	managed, ok := store.(modelgovernance.ManagedAPIConfigStore)
	selections, selected := store.(modelgovernance.GradingModelApplicationStore)
	if !ok || !selected {
		return nil
	}
	return func(ctx context.Context, tenantID string) (*subjective.ImageModelConfig, error) {
		selection, err := selections.GetGradingModelSelection(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		if selection.Mode != "model" || selection.ModelConfigID == "" {
			return nil, modelgovernance.ErrGradingModelRequired
		}
		// 模型和版本从同一份选择生成；重复读取选择可能把旧 revision 与后来换选的连接混在一起。
		connection, err := managed.GetManagedAPIConnection(ctx, tenantID, selection.ModelConfigID)
		if err != nil {
			return nil, err
		}
		if connection.Config.TenantID != tenantID || !modelgovernance.CanAssignGradingModel(connection.Config) || connection.APIKey == "" {
			return nil, modelgovernance.ErrGradingModelRequired
		}
		return imageModelConnection(tenantID, selection.Revision, connection), nil
	}
}

// newImageGradingModelByIDResolver 只恢复冻结身份指向的本校连接，不读取后来修改的学校默认项。
// 停用或修改连接仍拒绝旧运行；更换考试或学校选择不影响已经冻结的运行。
func newImageGradingModelByIDResolver(store modelgovernance.Store) func(context.Context, string, string, string) (*subjective.ImageModelConfig, error) {
	managed, ok := store.(modelgovernance.ManagedAPIConfigStore)
	if !ok {
		return nil
	}
	return func(ctx context.Context, tenantID, configID, bindingRevision string) (*subjective.ImageModelConfig, error) {
		connection, err := managed.GetManagedAPIConnection(ctx, tenantID, configID)
		if err != nil {
			return nil, err
		}
		if connection.Config.TenantID != tenantID || connection.Config.ID != configID || !modelgovernance.CanAssignGradingModel(connection.Config) || connection.APIKey == "" {
			return nil, modelgovernance.ErrGradingModelRequired
		}
		return imageModelConnection(tenantID, bindingRevision, connection), nil
	}
}

func imageModelConnection(tenantID, bindingRevision string, connection modelgovernance.ManagedAPIConnection) *subjective.ImageModelConfig {
	config := connection.Config
	revision := grading.GradingModelConfigRevision(tenantID, bindingRevision, config.ID, config.AdapterType, config.BaseURL, config.ModelName, config.ModelVersion, config.UpdatedAt)
	capabilityRevision := grading.GradingModelConfigRevision(tenantID, "", config.ID, config.AdapterType, config.BaseURL, config.ModelName, config.ModelVersion, config.UpdatedAt)
	return &subjective.ImageModelConfig{ConfigID: config.ID, AdapterType: config.AdapterType, ProviderKey: config.ProviderKey, BaseURL: config.BaseURL, APIKey: connection.APIKey, ModelName: config.ModelName, ModelVersion: config.ModelVersion, ConfigRevision: revision, CapabilityRevision: capabilityRevision}
}
