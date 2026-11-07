package modelgovernance

import (
	"context"
	"github.com/google/uuid"
	"sort"
	"strings"
	"time"
)

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
	target, err := s.evaluationCandidateTargetLocked(tenantID, run, input)
	if err != nil {
		return EvaluationCandidate{}, err
	}
	if s.evaluationCandidateExistsLocked(tenantID, runID, input) {
		return EvaluationCandidate{}, ErrConflict
	}
	item := PopulateEvaluationMetrics(EvaluationCandidate{
		ID:                     uuid.NewString(),
		TenantID:               tenantID,
		RunID:                  runID,
		DeploymentID:           target.DeploymentID,
		ModelConfigID:          target.ModelConfigID,
		ProviderKey:            target.ProviderKey,
		DeploymentKey:          target.DeploymentKey,
		ModelName:              target.ModelName,
		ModelVersion:           target.ModelVersion,
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

// 调用方须持有写锁；身份从当前同租户库存复制，后续评测和晋级使用此快照，不随库存更新。
func (s *MemoryStore) evaluationCandidateTargetLocked(tenantID string, run EvaluationRun, input EvaluationCandidateInput) (EvaluationCandidate, error) {
	var deployment Deployment
	var managed ManagedAPIConfig
	var ok bool
	if input.ModelConfigID != "" {
		managed, ok = s.managedConfigs[input.ModelConfigID]
		if !ok || managed.TenantID != tenantID || managed.Status != "active" ||
			managed.LastCapabilityStatus != "success" ||
			managed.LastCapabilityVersion != "structured-json-v4" || run.Modality != "text" {
			return EvaluationCandidate{}, ErrNotFound
		}
	} else {
		deployment, ok = s.deployments[input.DeploymentID]
		if !ok || deployment.TenantID != tenantID || !contains(deployment.Modalities, run.Modality) {
			return EvaluationCandidate{}, ErrNotFound
		}
	}
	modelName := deployment.ModelName
	modelVersion := deployment.ModelVersion
	providerKey := deployment.ProviderKey
	if input.ModelConfigID != "" {
		modelName = managed.ModelName
		modelVersion = managed.ModelVersion
		providerKey = managed.ProviderKey
	}
	return EvaluationCandidate{
		DeploymentID: deployment.ID, ModelConfigID: managed.ID,
		ProviderKey: providerKey, DeploymentKey: deployment.Key,
		ModelName: modelName, ModelVersion: modelVersion,
	}, nil
}

// 调用方须持有锁；重复检查仍按请求的目标 ID 匹配，不能改成快照中的库存 ID。
func (s *MemoryStore) evaluationCandidateExistsLocked(tenantID string, runID string, input EvaluationCandidateInput) bool {
	for _, item := range s.candidates {
		if item.TenantID == tenantID && item.RunID == runID &&
			evaluationCandidateMatchesTarget(item, input.ModelConfigID, input.DeploymentID) {
			return true
		}
	}
	return false
}

func evaluationCandidateMatchesTarget(item EvaluationCandidate, modelConfigID string, deploymentID string) bool {
	return (modelConfigID != "" && item.ModelConfigID == modelConfigID) ||
		(deploymentID != "" && item.DeploymentID == deploymentID)
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
	managedCount := 0
	for _, candidate := range candidates {
		if candidate.DeploymentID == "" {
			managedCount++
		}
	}
	if len(candidates) < 2 || (managedCount > 0 && managedCount != len(candidates)) ||
		(managedCount == 0 && !s.hasLocalEvaluationCandidateLocked(tenantID, candidates)) {
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
	// 作废证据和撤销依赖批准共用写锁；历史批准仍可查，但不能继续充当运行资格。
	for id, approval := range s.modelApprovals {
		if approval.TenantID == tenantID && approval.EvaluationRunID == runID && approval.RevokedAt == nil {
			approval.RevokedAt = &now
			approval.Revision++
			s.modelApprovals[id] = approval
		}
	}
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
