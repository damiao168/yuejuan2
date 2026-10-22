package server

import (
	"context"
	"encoding/hex"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
)

// 多图必须按实际 image 模态检查，OCR 有文字也不能套用 text 审批。
// 检查精确配置的部署；不因另一个已批准部署可用而切换实际供应商。
func referenceImageAdmission(cfg config.Config, models modelgovernance.Store, store subjective.Store) func(context.Context, string, string, string) error {
	preflight := referenceImagePreflight(cfg, models)
	return func(ctx context.Context, tenant, runID, wrapper string) error {
		if store == nil {
			return modelgovernance.ErrModelNotApproved
		}
		run, err := store.GetRun(ctx, tenant, runID)
		if err != nil {
			return err
		}
		value, err := store.LoadContext(ctx, tenant, run.AnswerSegmentID)
		if err != nil {
			return err
		}
		if run.RubricVersion != value.Rubric.Version {
			return modelgovernance.ErrModelNotApproved
		}
		return preflight(ctx, tenant, value, subjective.ModelPolicy{ModelVersion: run.ModelVersion, PromptVersion: run.PromptVersion}, wrapper)
	}
}

// 预检和实际消费共用完整 image 范围；前者只读取配置，不能冻结以后使用模型的授权。
func referenceImagePreflight(cfg config.Config, models modelgovernance.Store) subjective.ReferenceMaterialPreflight {
	return func(ctx context.Context, tenant string, value subjective.Context, requested subjective.ModelPolicy, wrapper string) error {
		denied := modelgovernance.ErrModelNotApproved
		_, wrapperErr := hex.DecodeString(wrapper)
		if models == nil || strings.TrimSpace(tenant) == "" || len(wrapper) != 64 || wrapperErr != nil || wrapper != strings.ToLower(wrapper) ||
			requested.ModelVersion == "" || requested.PromptVersion == "" ||
			requested.ModelVersion != cfg.AIService.ModelVersion || requested.PromptVersion != cfg.AIService.PromptVersion ||
			value.Rubric.Version == "" || value.Subject == "" || value.GradeLevel == "" || value.Question.QuestionType == "" {
			return denied
		}
		providers, err := models.ListProviders(ctx, tenant)
		if err != nil {
			return err
		}
		deployments, err := models.ListDeployments(ctx, tenant)
		if err != nil {
			return err
		}
		policy, err := models.GetPolicy(ctx, tenant)
		if err != nil {
			return err
		}
		var actual *modelgovernance.Deployment
		for _, item := range deployments {
			if item.ProviderKey == cfg.AIService.ProviderKey && item.Key == cfg.AIService.DeploymentKey && item.ModelVersion == requested.ModelVersion && item.Region == cfg.AIService.DeploymentRegion && item.CapabilityProfile == cfg.AIService.CapabilityProfile {
				copy := item
				actual = &copy
				break
			}
		}
		if actual == nil || actual.CapabilityPolicy["math_wrapper_hash"] != wrapper {
			return denied
		}
		providerMatches := false
		for _, provider := range providers {
			if provider.ID == actual.ProviderID && provider.Key == cfg.AIService.ProviderKey && provider.AdapterType == cfg.AIService.AdapterType {
				providerMatches = true
				break
			}
		}
		if !providerMatches {
			return denied
		}
		decision, err := modelgovernance.SelectDeployment(policy, modelgovernance.RouteRequest{Modality: "image"}, providers, []modelgovernance.Deployment{*actual})
		if err != nil || decision.DeploymentKey != actual.Key {
			return denied
		}
		approvals, err := models.ListModelApprovals(ctx, tenant)
		if err != nil {
			return err
		}
		_, err = modelgovernance.RequireActiveModelApproval(approvals, modelgovernance.ModelApprovalScope{DeploymentID: actual.ID, ModelVersion: requested.ModelVersion, PromptVersion: requested.PromptVersion, RubricVersion: value.Rubric.Version, Subject: value.Subject, Grade: value.GradeLevel, QuestionType: value.Question.QuestionType, Modality: "image"}, time.Now().UTC())
		return err
	}
}
