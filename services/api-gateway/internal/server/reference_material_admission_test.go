package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
)

type referenceAdmissionModels struct {
	modelgovernance.Store
	providers   []modelgovernance.Provider
	deployments []modelgovernance.Deployment
	policy      modelgovernance.TenantPolicy
	approvals   []modelgovernance.ModelApproval
	err         error
}

func (m *referenceAdmissionModels) ListProviders(context.Context, string) ([]modelgovernance.Provider, error) {
	return m.providers, m.err
}
func (m *referenceAdmissionModels) ListDeployments(context.Context, string) ([]modelgovernance.Deployment, error) {
	return m.deployments, m.err
}
func (m *referenceAdmissionModels) GetPolicy(context.Context, string) (modelgovernance.TenantPolicy, error) {
	return m.policy, m.err
}
func (m *referenceAdmissionModels) ListModelApprovals(context.Context, string) ([]modelgovernance.ModelApproval, error) {
	return m.approvals, m.err
}

func referenceAdmissionFixture() (config.Config, *referenceAdmissionModels, subjective.Context, subjective.ModelPolicy, string) {
	cfg := config.Config{AIService: config.AIServiceConfig{ProviderKey: "local", DeploymentKey: "local-model", AdapterType: "local_llama_cpp", ModelVersion: "actual-model-v1", PromptVersion: "actual-prompt-v1", DeploymentRegion: "on_premise", CapabilityProfile: "local-pilot-v1"}}
	wrapper := strings.Repeat("a", 64)
	models := &referenceAdmissionModels{
		providers:   []modelgovernance.Provider{{ID: "provider-local", Key: "local", Kind: modelgovernance.ProviderLocal, AdapterType: "local_llama_cpp", Region: "on_premise", Status: "active", DataPolicy: modelgovernance.DataPolicy{RetentionMode: "no_store"}}},
		deployments: []modelgovernance.Deployment{{ID: "deployment-local", ProviderID: "provider-local", Key: "local-model", ProviderKey: "local", ModelVersion: "actual-model-v1", Region: "on_premise", CapabilityProfile: "local-pilot-v1", Modalities: []string{"text", "image"}, Status: "shadow_only", HealthState: "available", CapabilityPolicy: map[string]any{"math_wrapper_hash": wrapper}}},
		policy:      modelgovernance.DefaultTenantPolicy(),
		approvals:   []modelgovernance.ModelApproval{{DeploymentID: "deployment-local", ModelVersion: "actual-model-v1", PromptVersion: "actual-prompt-v1", RubricVersion: "rubric-frozen-v1", Subject: "mathematics", Grade: "senior", QuestionType: "short_answer", Modality: "image", ExpiresAt: time.Now().Add(time.Hour)}},
	}
	value := subjective.Context{SegmentID: "segment-1", Subject: "mathematics", GradeLevel: "senior", Question: paper.Question{QuestionType: "short_answer"}, Rubric: paper.Rubric{Version: "rubric-frozen-v1"}}
	return cfg, models, value, subjective.ModelPolicy{ModelVersion: cfg.AIService.ModelVersion, PromptVersion: cfg.AIService.PromptVersion}, wrapper
}

func TestReferenceImagePreflightRequiresActualDeploymentAndFullImageApprovalScope(t *testing.T) {
	for _, scenario := range []string{"approved", "missing_store", "provider_changed", "deployment_changed", "adapter_changed", "region_changed", "profile_changed", "model_changed", "prompt_changed", "wrapper_changed", "text_only_capability", "text_only_approval", "rubric_changed", "subject_changed", "grade_changed", "question_type_changed", "expired", "revoked", "lookup_failed"} {
		t.Run(scenario, func(t *testing.T) {
			cfg, models, value, requested, wrapper := referenceAdmissionFixture()
			switch scenario {
			case "missing_store":
				models = nil
			case "provider_changed":
				cfg.AIService.ProviderKey = "other"
			case "deployment_changed":
				cfg.AIService.DeploymentKey = "other"
			case "adapter_changed":
				cfg.AIService.AdapterType = "dashscope_native"
			case "region_changed":
				cfg.AIService.DeploymentRegion = "other"
			case "profile_changed":
				cfg.AIService.CapabilityProfile = "other"
			case "model_changed":
				requested.ModelVersion = "other"
			case "prompt_changed":
				requested.PromptVersion = "other"
			case "wrapper_changed":
				wrapper = strings.Repeat("b", 64)
			case "text_only_capability":
				models.deployments[0].Modalities = []string{"text"}
			case "text_only_approval":
				models.approvals[0].Modality = "text"
			case "rubric_changed":
				value.Rubric.Version = "other"
			case "subject_changed":
				value.Subject = "physics"
			case "grade_changed":
				value.GradeLevel = "junior"
			case "question_type_changed":
				value.Question.QuestionType = "fill_blank"
			case "expired":
				models.approvals[0].ExpiresAt = time.Now().Add(-time.Second)
			case "revoked":
				now := time.Now()
				models.approvals[0].RevokedAt = &now
			case "lookup_failed":
				models.err = errors.New("synthetic private database failure")
			}
			var store modelgovernance.Store = models
			if models == nil {
				store = nil
			}
			err := referenceImagePreflight(cfg, store)(context.Background(), "tenant-a", value, requested, wrapper)
			if scenario == "approved" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unapproved material image scope passed preflight")
			}
		})
	}
}

func TestReferenceImagePreflightCannotFallbackToAnotherApprovedDeployment(t *testing.T) {
	cfg, models, value, requested, wrapper := referenceAdmissionFixture()
	other := models.deployments[0]
	other.ID, other.Key = "other-deployment", "another-model"
	models.deployments = append(models.deployments, other)
	models.deployments[0].Status = "disabled"
	models.approvals[0].DeploymentID = other.ID
	if err := referenceImagePreflight(cfg, models)(context.Background(), "tenant-a", value, requested, wrapper); err == nil {
		t.Fatal("preflight silently switched the actual image deployment")
	}
}

type referenceAdmissionRuns struct {
	subjective.Store
	run   subjective.GradingRun
	value subjective.Context
	loads int
}

func (s *referenceAdmissionRuns) GetRun(context.Context, string, string) (subjective.GradingRun, error) {
	return s.run, nil
}
func (s *referenceAdmissionRuns) LoadContext(context.Context, string, string) (subjective.Context, error) {
	s.loads++
	return s.value, nil
}

func TestReferenceImageConsumeRechecksApprovalAfterPreflight(t *testing.T) {
	cfg, models, value, requested, wrapper := referenceAdmissionFixture()
	if err := referenceImagePreflight(cfg, models)(context.Background(), "tenant-a", value, requested, wrapper); err != nil {
		t.Fatal(err)
	}
	runs := &referenceAdmissionRuns{value: value, run: subjective.GradingRun{AnswerSegmentID: value.SegmentID, ModelVersion: requested.ModelVersion, PromptVersion: requested.PromptVersion, RubricVersion: value.Rubric.Version}}
	consume := referenceImageAdmission(cfg, models, runs)
	if err := consume(context.Background(), "tenant-a", "run-1", wrapper); err != nil {
		t.Fatal(err)
	}
	models.approvals[0].ExpiresAt = time.Now().Add(-time.Second)
	if err := consume(context.Background(), "tenant-a", "run-1", wrapper); err == nil {
		t.Fatal("expired approval retained the earlier preflight result")
	}
	models.approvals[0].ExpiresAt = time.Now().Add(time.Hour)
	runs.run.RubricVersion = "other"
	if err := consume(context.Background(), "tenant-a", "run-1", wrapper); err == nil {
		t.Fatal("consume approval used a rubric different from the actual run")
	}
	if runs.loads != 3 {
		t.Fatal("consume did not reload the current frozen context")
	}
}
