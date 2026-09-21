package modelgovernance

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type MemoryStore struct {
	mu             sync.RWMutex
	providers      map[string]Provider
	deployments    map[string]Deployment
	policies       map[string]TenantPolicy
	approvals      map[string]SandboxApproval
	evaluations    map[string]EvaluationRun
	candidates     map[string]EvaluationCandidate
	modelApprovals map[string]ModelApproval
	managedConfigs map[string]ManagedAPIConfig
	managedSecrets map[string]string
	roleBindings   map[string]ModelRoleBinding
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		providers:      map[string]Provider{},
		deployments:    map[string]Deployment{},
		policies:       map[string]TenantPolicy{},
		approvals:      map[string]SandboxApproval{},
		evaluations:    map[string]EvaluationRun{},
		candidates:     map[string]EvaluationCandidate{},
		modelApprovals: map[string]ModelApproval{},
		managedConfigs: map[string]ManagedAPIConfig{},
		managedSecrets: map[string]string{},
		roleBindings:   map[string]ModelRoleBinding{},
	}
}

func (s *MemoryStore) ValidateProductionReadiness(_ context.Context, secrets SecretReferenceResolver) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	providers := make([]Provider, 0, len(s.providers))
	for _, item := range s.providers {
		providers = append(providers, item)
	}
	deployments := make([]Deployment, 0, len(s.deployments))
	for _, item := range s.deployments {
		deployments = append(deployments, item)
	}
	return ValidateProductionInventory(providers, deployments, secrets)
}

func (s *MemoryStore) EnsureLocalBaseline(_ context.Context, tenantID string, baseline LocalBaseline) error {
	if strings.TrimSpace(tenantID) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	providerID := ""
	for id, item := range s.providers {
		if item.TenantID == tenantID && item.Key == baseline.ProviderKey {
			providerID = id
			item.DisplayName = baseline.ProviderName
			item.AdapterType = baseline.AdapterType
			item.Region = baseline.Region
			item.UpdatedAt = now
			s.providers[id] = item
			break
		}
	}
	if providerID == "" {
		providerID = uuid.NewString()
		s.providers[providerID] = Provider{
			ID:          providerID,
			TenantID:    tenantID,
			Key:         baseline.ProviderKey,
			DisplayName: baseline.ProviderName,
			Kind:        ProviderLocal,
			AdapterType: baseline.AdapterType,
			Region:      baseline.Region,
			DataPolicy:  DataPolicy{RetentionMode: "no_store"},
			Status:      "active",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	}
	for id, item := range s.deployments {
		if item.TenantID == tenantID && item.Key == baseline.DeploymentKey {
			item.ProviderID = providerID
			item.ProviderKey = baseline.ProviderKey
			item.ModelName = baseline.ModelName
			item.ModelVersion = baseline.ModelVersion
			item.Region = baseline.Region
			item.CapabilityProfile = baseline.CapabilityProfile
			item.UpdatedAt = now
			s.deployments[id] = item
			return nil
		}
	}
	id := uuid.NewString()
	s.deployments[id] = Deployment{
		ID:                id,
		TenantID:          tenantID,
		ProviderID:        providerID,
		ProviderKey:       baseline.ProviderKey,
		Key:               baseline.DeploymentKey,
		ModelName:         baseline.ModelName,
		ModelVersion:      baseline.ModelVersion,
		Region:            baseline.Region,
		CapabilityProfile: baseline.CapabilityProfile,
		Modalities:        []string{"text"},
		CapabilityPolicy:  map[string]any{},
		PricingPolicy:     map[string]any{"meter": "local_compute"},
		Status:            "shadow_only",
		HealthState:       "unverified",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	return nil
}

func (s *MemoryStore) ListProviders(_ context.Context, tenantID string) ([]Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Provider{}
	for _, item := range s.providers {
		if item.TenantID == tenantID {
			out = append(out, sanitizeProvider(item))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *MemoryStore) CreateProvider(_ context.Context, tenantID string, _ string, input ProviderInput) (Provider, error) {
	provider := ProviderFromInput(input)
	provider.TenantID = tenantID
	provider.DisplayName = strings.TrimSpace(provider.DisplayName)
	if provider.Status == "" {
		provider.Status = "unverified"
	}
	if provider.DisplayName == "" || ValidateProvider(provider) != nil {
		return Provider{}, ErrInvalidProvider
	}
	if provider.Kind == ProviderExternal && provider.Status != "unverified" && provider.Status != "disabled" {
		return Provider{}, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.providers {
		if item.TenantID == tenantID && item.Key == provider.Key {
			return Provider{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	provider.ID = uuid.NewString()
	provider.CreatedAt = now
	provider.UpdatedAt = now
	s.providers[provider.ID] = provider
	return sanitizeProvider(provider), nil
}

func (s *MemoryStore) UpdateProviderStatus(_ context.Context, tenantID string, id string, input ProviderStatusInput) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.providers[id]
	if !ok || item.TenantID != tenantID {
		return Provider{}, ErrNotFound
	}
	item.Status = input.Status
	if ValidateProvider(item) != nil {
		return Provider{}, ErrInvalidProvider
	}
	if item.Kind == ProviderExternal && item.Status != "unverified" && item.Status != "disabled" {
		return Provider{}, ErrConflict
	}
	item.UpdatedAt = time.Now().UTC()
	s.providers[id] = item
	return sanitizeProvider(item), nil
}

func (s *MemoryStore) ListDeployments(_ context.Context, tenantID string) ([]Deployment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Deployment{}
	for _, item := range s.deployments {
		if item.TenantID == tenantID {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *MemoryStore) CreateDeployment(_ context.Context, tenantID string, _ string, input DeploymentInput) (Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	provider, ok := s.providers[input.ProviderID]
	if !ok || provider.TenantID != tenantID {
		return Deployment{}, ErrNotFound
	}
	deployment := DeploymentFromInput(input, provider.Key)
	deployment.TenantID = tenantID
	if deployment.Status == "" {
		deployment.Status = "unverified"
	}
	if deployment.HealthState == "" {
		deployment.HealthState = "unverified"
	}
	if strings.TrimSpace(deployment.ModelName) == "" || ValidateDeployment(deployment, provider) != nil {
		return Deployment{}, ErrInvalidDeployment
	}
	if provider.Kind == ProviderExternal &&
		(deployment.Status != "unverified" || deployment.HealthState != "unverified") {
		return Deployment{}, ErrConflict
	}
	for _, item := range s.deployments {
		if item.TenantID == tenantID && item.Key == deployment.Key {
			return Deployment{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	deployment.ID = uuid.NewString()
	deployment.CreatedAt = now
	deployment.UpdatedAt = now
	s.deployments[deployment.ID] = deployment
	return deployment, nil
}

func (s *MemoryStore) UpdateDeploymentState(_ context.Context, tenantID string, id string, input DeploymentStateInput) (Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.deployments[id]
	if !ok || item.TenantID != tenantID {
		return Deployment{}, ErrNotFound
	}
	provider, ok := s.providers[item.ProviderID]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	item.Status = input.Status
	item.HealthState = input.HealthState
	if ValidateDeployment(item, provider) != nil {
		return Deployment{}, ErrInvalidDeployment
	}
	externalStateAllowed := item.Status == "unverified" && item.HealthState == "unverified" ||
		item.Status == "disabled" &&
			(item.HealthState == "unverified" || item.HealthState == "unavailable" || item.HealthState == "disabled")
	if provider.Kind == ProviderExternal && !externalStateAllowed {
		return Deployment{}, ErrConflict
	}
	item.UpdatedAt = time.Now().UTC()
	s.deployments[id] = item
	return item, nil
}

func (s *MemoryStore) GetPolicy(_ context.Context, tenantID string) (TenantPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if item, ok := s.policies[tenantID]; ok {
		return item, nil
	}
	item := DefaultTenantPolicy()
	item.ID = uuid.NewString()
	item.TenantID = tenantID
	item.PolicyKey = "default"
	item.DisplayName = "Default local-only policy"
	item.Status = "active"
	item.Version = 1
	item.UpdatedAt = time.Now().UTC()
	s.policies[tenantID] = item
	return item, nil
}

func (s *MemoryStore) UpdatePolicy(_ context.Context, tenantID string, _ string, input PolicyUpdateInput) (TenantPolicy, error) {
	policy := PolicyFromUpdate(input)
	if ValidateTenantPolicy(policy) != nil || input.ExpectedVersion < 1 || strings.TrimSpace(input.Reason) == "" {
		return TenantPolicy{}, ErrInvalidPolicy
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.policies[tenantID]
	if !ok {
		current = DefaultTenantPolicy()
		current.ID = uuid.NewString()
		current.TenantID = tenantID
		current.PolicyKey = "default"
		current.Status = "active"
		current.Version = 1
	}
	if current.Version != input.ExpectedVersion {
		return TenantPolicy{}, ErrConflict
	}
	policy.ID = current.ID
	policy.TenantID = tenantID
	policy.PolicyKey = "default"
	policy.DisplayName = strings.TrimSpace(input.DisplayName)
	if policy.DisplayName == "" {
		policy.DisplayName = current.DisplayName
	}
	policy.Status = "active"
	policy.Version = current.Version + 1
	policy.UpdatedAt = time.Now().UTC()
	s.policies[tenantID] = policy
	return policy, nil
}

func (s *MemoryStore) ListSandboxApprovals(_ context.Context, tenantID string) ([]SandboxApproval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []SandboxApproval{}
	for _, item := range s.approvals {
		if item.TenantID == tenantID {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *MemoryStore) CreateSandboxApproval(
	_ context.Context,
	tenantID string,
	_ string,
	input SandboxApprovalInput,
) (SandboxApproval, error) {
	now := time.Now().UTC()
	if ValidateSandboxApprovalInput(input, now) != nil {
		return SandboxApproval{}, ErrInvalidApproval
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	provider, ok := s.providers[input.ProviderID]
	if !ok || provider.TenantID != tenantID ||
		provider.Kind != ProviderExternal ||
		provider.AdapterType != SandboxProtocolDashScopeNative {
		return SandboxApproval{}, ErrNotFound
	}
	deployment, ok := s.deployments[input.DeploymentID]
	if !ok || deployment.TenantID != tenantID ||
		deployment.ProviderID != provider.ID ||
		deployment.Region != input.ApprovedRegion ||
		provider.Region != input.ApprovedRegion {
		return SandboxApproval{}, ErrNotFound
	}
	for _, item := range s.approvals {
		if item.TenantID == tenantID &&
			item.DeploymentID == deployment.ID &&
			item.RevokedAt == nil {
			return SandboxApproval{}, ErrConflict
		}
	}
	item := SandboxApproval{
		ID:                    uuid.NewString(),
		TenantID:              tenantID,
		ProviderID:            provider.ID,
		DeploymentID:          deployment.ID,
		ProviderKey:           provider.Key,
		DeploymentKey:         deployment.Key,
		Protocol:              input.Protocol,
		ApprovalReference:     strings.TrimSpace(input.ApprovalReference),
		ApprovedRegion:        strings.TrimSpace(input.ApprovedRegion),
		SandboxAccount:        input.SandboxAccount,
		ContractReviewed:      input.ContractReviewed,
		RetentionReviewed:     input.RetentionReviewed,
		DataResidencyReviewed: input.DataResidencyReviewed,
		PricingReviewed:       input.PricingReviewed,
		SyntheticDataOnly:     input.SyntheticDataOnly,
		ImageExportReviewed:   input.ImageExportReviewed,
		ExpiresAt:             input.ExpiresAt.UTC(),
		CreatedAt:             now,
	}
	s.approvals[item.ID] = item
	return item, nil
}

func (s *MemoryStore) RevokeSandboxApproval(
	_ context.Context,
	tenantID string,
	_ string,
	id string,
	reason string,
) (SandboxApproval, error) {
	if strings.TrimSpace(reason) == "" {
		return SandboxApproval{}, ErrInvalidApproval
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.approvals[id]
	if !ok || item.TenantID != tenantID {
		return SandboxApproval{}, ErrNotFound
	}
	if item.RevokedAt != nil {
		return SandboxApproval{}, ErrConflict
	}
	now := time.Now().UTC()
	item.RevokedAt = &now
	s.approvals[id] = item
	return item, nil
}

func (s *MemoryStore) ListEvaluationRuns(_ context.Context, tenantID string, filter EvaluationListFilter) ([]EvaluationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []EvaluationRun{}
	for _, item := range s.evaluations {
		if item.TenantID != tenantID {
			continue
		}
		item.Candidates = s.evaluationCandidatesLocked(tenantID, item.ID)
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if filter.CursorID != "" {
		start := 0
		for start < len(out) {
			item := out[start]
			if item.CreatedAt.Before(filter.CursorCreatedAt) ||
				(item.CreatedAt.Equal(filter.CursorCreatedAt) && item.ID < filter.CursorID) {
				break
			}
			start++
		}
		out = out[start:]
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *MemoryStore) CreateEvaluationRun(
	_ context.Context,
	tenantID string,
	_ string,
	input EvaluationRunInput,
) (EvaluationRun, error) {
	if ValidateEvaluationRunInput(input) != nil {
		return EvaluationRun{}, ErrInvalidEvaluation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.evaluations {
		if item.TenantID == tenantID && item.Key == input.Key {
			return EvaluationRun{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	item := EvaluationRun{
		ID:               uuid.NewString(),
		TenantID:         tenantID,
		Key:              strings.TrimSpace(input.Key),
		DisplayName:      strings.TrimSpace(input.DisplayName),
		DatasetReference: strings.TrimSpace(input.DatasetReference),
		DatasetSHA256:    strings.TrimSpace(input.DatasetSHA256),
		AuthorizationRef: strings.TrimSpace(input.AuthorizationRef),
		EvidenceClass:    input.EvidenceClass,
		Subject:          strings.TrimSpace(input.Subject),
		Grade:            strings.TrimSpace(input.Grade),
		QuestionType:     strings.TrimSpace(input.QuestionType),
		Modality:         input.Modality,
		SampleCount:      input.SampleCount,
		RepeatCount:      input.RepeatCount,
		Status:           EvaluationStatusDraft,
		Candidates:       []EvaluationCandidate{},
		CreatedAt:        now,
	}
	s.evaluations[item.ID] = item
	return item, nil
}

func (s *MemoryStore) AddEvaluationCandidate(
	_ context.Context,
	tenantID string,
	_ string,
	runID string,
	input EvaluationCandidateInput,
) (EvaluationCandidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.evaluations[runID]
	if !ok || run.TenantID != tenantID {
		return EvaluationCandidate{}, ErrNotFound
	}
	if run.Status != EvaluationStatusDraft {
		return EvaluationCandidate{}, ErrConflict
	}
	if ValidateEvaluationCandidateInput(input, run) != nil {
		return EvaluationCandidate{}, ErrInvalidEvaluation
	}
	deployment, ok := s.deployments[input.DeploymentID]
	if !ok || deployment.TenantID != tenantID || !contains(deployment.Modalities, run.Modality) {
		return EvaluationCandidate{}, ErrNotFound
	}
	for _, item := range s.candidates {
		if item.TenantID == tenantID && item.RunID == runID && item.DeploymentID == deployment.ID {
			return EvaluationCandidate{}, ErrConflict
		}
	}
	item := PopulateEvaluationMetrics(EvaluationCandidate{
		ID:                     uuid.NewString(),
		TenantID:               tenantID,
		RunID:                  runID,
		DeploymentID:           deployment.ID,
		ProviderKey:            deployment.ProviderKey,
		DeploymentKey:          deployment.Key,
		ModelVersion:           deployment.ModelVersion,
		PromptVersion:          strings.TrimSpace(input.PromptVersion),
		RubricVersion:          strings.TrimSpace(input.RubricVersion),
		EvaluatedSamples:       input.EvaluatedSamples,
		TeacherReviewedSamples: input.TeacherReviewedSamples,
		TeacherAcceptedSamples: input.TeacherAcceptedSamples,
		SeriousErrorSamples:    input.SeriousErrorSamples,
		EvidenceValidSamples:   input.EvidenceValidSamples,
		RepeatComparisons:      input.RepeatComparisons,
		StableRepeatSamples:    input.StableRepeatSamples,
		P95LatencyMS:           input.P95LatencyMS,
		TotalCostMicros:        input.TotalCostMicros,
		CreatedAt:              time.Now().UTC(),
	})
	s.candidates[item.ID] = item
	return item, nil
}

func (s *MemoryStore) CompleteEvaluationRun(
	_ context.Context,
	tenantID string,
	_ string,
	runID string,
	reason string,
) (EvaluationRun, error) {
	if strings.TrimSpace(reason) == "" {
		return EvaluationRun{}, ErrInvalidEvaluation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.evaluations[runID]
	if !ok || run.TenantID != tenantID {
		return EvaluationRun{}, ErrNotFound
	}
	if run.Status != EvaluationStatusDraft {
		return EvaluationRun{}, ErrConflict
	}
	candidates := s.evaluationCandidatesLocked(tenantID, runID)
	if len(candidates) < 2 || !s.hasLocalEvaluationCandidateLocked(tenantID, candidates) {
		return EvaluationRun{}, ErrInvalidEvaluation
	}
	now := time.Now().UTC()
	run.Status = EvaluationStatusCompleted
	run.CompletedAt = &now
	run.Candidates = candidates
	s.evaluations[runID] = run
	return run, nil
}

func (s *MemoryStore) InvalidateEvaluationRun(
	_ context.Context,
	tenantID string,
	_ string,
	runID string,
	reason string,
) (EvaluationRun, error) {
	if strings.TrimSpace(reason) == "" {
		return EvaluationRun{}, ErrInvalidEvaluation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.evaluations[runID]
	if !ok || run.TenantID != tenantID {
		return EvaluationRun{}, ErrNotFound
	}
	if run.Status == EvaluationStatusInvalidated {
		return EvaluationRun{}, ErrConflict
	}
	now := time.Now().UTC()
	run.Status = EvaluationStatusInvalidated
	run.InvalidatedAt = &now
	run.Candidates = s.evaluationCandidatesLocked(tenantID, runID)
	s.evaluations[runID] = run
	return run, nil
}

func (s *MemoryStore) evaluationCandidatesLocked(tenantID string, runID string) []EvaluationCandidate {
	out := []EvaluationCandidate{}
	for _, item := range s.candidates {
		if item.TenantID == tenantID && item.RunID == runID {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeploymentKey == out[j].DeploymentKey {
			return out[i].ID < out[j].ID
		}
		return out[i].DeploymentKey < out[j].DeploymentKey
	})
	return out
}

func (s *MemoryStore) hasLocalEvaluationCandidateLocked(tenantID string, candidates []EvaluationCandidate) bool {
	for _, candidate := range candidates {
		deployment, ok := s.deployments[candidate.DeploymentID]
		if !ok || deployment.TenantID != tenantID {
			continue
		}
		provider, ok := s.providers[deployment.ProviderID]
		if ok && provider.TenantID == tenantID && provider.Kind == ProviderLocal {
			return true
		}
	}
	return false
}

func (s *MemoryStore) ListModelApprovals(_ context.Context, tenantID string) ([]ModelApproval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ModelApproval{}
	for _, item := range s.modelApprovals {
		if item.TenantID == tenantID {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *MemoryStore) CreateModelApproval(
	_ context.Context,
	tenantID string,
	_ string,
	input ModelApprovalInput,
) (ModelApproval, error) {
	now := time.Now().UTC()
	if ValidateModelApprovalInput(input, now) != nil {
		return ModelApproval{}, ErrInvalidPromotion
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.evaluations[input.EvaluationRunID]
	if !ok || run.TenantID != tenantID {
		return ModelApproval{}, ErrNotFound
	}
	if run.Status != EvaluationStatusCompleted ||
		run.EvidenceClass != EvaluationEvidenceAuthorizedFrozenSet {
		return ModelApproval{}, ErrInvalidPromotion
	}
	var candidate EvaluationCandidate
	found := false
	for _, item := range s.candidates {
		if item.TenantID == tenantID &&
			item.RunID == run.ID &&
			item.DeploymentID == input.DeploymentID {
			candidate = item
			found = true
			break
		}
	}
	if !found {
		return ModelApproval{}, ErrNotFound
	}
	for _, item := range s.modelApprovals {
		if item.TenantID == tenantID && item.DecisionReference == input.DecisionReference {
			return ModelApproval{}, ErrConflict
		}
		if item.TenantID == tenantID &&
			item.DeploymentID == candidate.DeploymentID &&
			item.ModelVersion == candidate.ModelVersion &&
			item.PromptVersion == candidate.PromptVersion &&
			item.RubricVersion == candidate.RubricVersion &&
			item.Subject == run.Subject &&
			item.Grade == run.Grade &&
			item.QuestionType == run.QuestionType &&
			item.Modality == run.Modality &&
			item.IsActive(now) {
			return ModelApproval{}, ErrConflict
		}
	}
	item := ModelApproval{
		ID:                    uuid.NewString(),
		TenantID:              tenantID,
		EvaluationRunID:       run.ID,
		EvaluationCandidateID: candidate.ID,
		DeploymentID:          candidate.DeploymentID,
		ProviderKey:           candidate.ProviderKey,
		DeploymentKey:         candidate.DeploymentKey,
		ModelVersion:          candidate.ModelVersion,
		PromptVersion:         candidate.PromptVersion,
		RubricVersion:         candidate.RubricVersion,
		DatasetReference:      run.DatasetReference,
		DatasetSHA256:         run.DatasetSHA256,
		AuthorizationRef:      run.AuthorizationRef,
		Subject:               run.Subject,
		Grade:                 run.Grade,
		QuestionType:          run.QuestionType,
		Modality:              run.Modality,
		ManualReviewRate:      input.ManualReviewRate,
		DecisionReference:     strings.TrimSpace(input.DecisionReference),
		ExpiresAt:             input.ExpiresAt.UTC(),
		Revision:              1,
		CreatedAt:             now,
	}
	s.modelApprovals[item.ID] = item
	return item, nil
}

func (s *MemoryStore) RevokeModelApproval(
	_ context.Context,
	tenantID string,
	_ string,
	id string,
	input ModelApprovalRevokeInput,
) (ModelApproval, error) {
	if strings.TrimSpace(input.Reason) == "" || input.ExpectedRevision < 1 {
		return ModelApproval{}, ErrInvalidPromotion
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.modelApprovals[id]
	if !ok || item.TenantID != tenantID {
		return ModelApproval{}, ErrNotFound
	}
	if item.Revision != input.ExpectedRevision {
		return ModelApproval{}, ErrRevisionConflict
	}
	if item.RevokedAt != nil {
		return ModelApproval{}, ErrConflict
	}
	now := time.Now().UTC()
	item.RevokedAt = &now
	item.Revision++
	s.modelApprovals[id] = item
	return item, nil
}

func sanitizeProvider(provider Provider) Provider {
	provider.CredentialConfigured = provider.CredentialRef != ""
	if parsed, err := url.Parse(provider.CredentialRef); err == nil {
		provider.CredentialScheme = parsed.Scheme
	}
	provider.CredentialRef = ""
	return provider
}
