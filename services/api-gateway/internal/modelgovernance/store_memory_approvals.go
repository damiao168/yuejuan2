package modelgovernance

import (
	"context"
	"github.com/google/uuid"
	"sort"
	"strings"
	"time"
)

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

// 晋级只接受已完成的授权冻结集评测，并把候选的模型、提示词和量规版本复制进不可变审批快照。
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
	candidate, found := s.modelApprovalCandidateLocked(tenantID, run.ID, input)
	if !found {
		return ModelApproval{}, ErrNotFound
	}
	if s.modelApprovalConflictsLocked(tenantID, run, candidate, input, now) {
		return ModelApproval{}, ErrConflict
	}
	item := ModelApproval{
		ID:                    uuid.NewString(),
		TenantID:              tenantID,
		EvaluationRunID:       run.ID,
		EvaluationCandidateID: candidate.ID,
		DeploymentID:          candidate.DeploymentID,
		ModelConfigID:         candidate.ModelConfigID,
		ProviderKey:           candidate.ProviderKey,
		DeploymentKey:         candidate.DeploymentKey,
		ModelName:             candidate.ModelName,
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

// 调用方须持有锁；保持原 map 遍历的首个匹配候选，不从当前库存补写模型版本。
func (s *MemoryStore) modelApprovalCandidateLocked(tenantID string, runID string, input ModelApprovalInput) (EvaluationCandidate, bool) {
	for _, item := range s.candidates {
		if item.TenantID == tenantID && item.RunID == runID &&
			evaluationCandidateMatchesTarget(item, input.ModelConfigID, input.DeploymentID) {
			return item, true
		}
	}
	return EvaluationCandidate{}, false
}

// 调用方须持有写锁并在同一临界区写入；先按原请求值检查决策编号，再排斥同版本作用域的有效审批。
func (s *MemoryStore) modelApprovalConflictsLocked(tenantID string, run EvaluationRun, candidate EvaluationCandidate, input ModelApprovalInput, now time.Time) bool {
	for _, item := range s.modelApprovals {
		if item.TenantID == tenantID && item.DecisionReference == input.DecisionReference {
			return true
		}
		if item.TenantID == tenantID && modelApprovalMatchesEvaluation(item, run, candidate, now) {
			return true
		}
	}
	return false
}

func modelApprovalMatchesEvaluation(item ModelApproval, run EvaluationRun, candidate EvaluationCandidate, now time.Time) bool {
	return item.ModelConfigID == candidate.ModelConfigID &&
		item.DeploymentID == candidate.DeploymentID &&
		item.ModelVersion == candidate.ModelVersion &&
		item.PromptVersion == candidate.PromptVersion &&
		item.RubricVersion == candidate.RubricVersion &&
		item.Subject == run.Subject &&
		item.Grade == run.Grade &&
		item.QuestionType == run.QuestionType &&
		item.Modality == run.Modality &&
		item.IsActive(now)
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
