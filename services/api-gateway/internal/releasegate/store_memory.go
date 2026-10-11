package releasegate

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryStore 对应证据和决定只追加的行为，用于确定性服务与 handler 测试；生产写入使用 Postgres。
type MemoryStore struct {
	mu        sync.Mutex
	now       func() time.Time
	sequence  int
	policies  map[string][]Policy
	evidence  map[string]Evidence
	waivers   map[string]Waiver
	decisions map[string]Waiver
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{now: func() time.Time { return time.Now().UTC() }, policies: map[string][]Policy{}, evidence: map[string]Evidence{}, waivers: map[string]Waiver{}, decisions: map[string]Waiver{}}
}

func (s *MemoryStore) SetNow(now func() time.Time) { s.mu.Lock(); defer s.mu.Unlock(); s.now = now }

func (s *MemoryStore) CreatePolicy(_ context.Context, tenantID, examID, actorID string, input CreatePolicyInput) (Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := scopeKey(tenantID, examID)
	for _, current := range s.policies[key] {
		if current.Version == input.Version {
			return Policy{}, ErrInvalidInput
		}
	}
	policy := Policy{ID: s.idLocked("policy"), TenantID: tenantID, ExamID: examID, Version: input.Version, Status: PolicyActive,
		RequireWarningAcknowledgement: input.RequireWarningAcknowledgement, WaivableWarningCodes: append([]string(nil), input.WaivableWarningCodes...), CreatedBy: actorID, CreatedAt: s.now().UTC()}
	s.policies[key] = append(s.policies[key], policy)
	return clonePolicy(policy), nil
}

func (s *MemoryStore) ActivePolicy(_ context.Context, tenantID, examID string) (Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.policies[scopeKey(tenantID, examID)]
	for index := len(items) - 1; index >= 0; index-- {
		if items[index].Status == PolicyActive {
			return clonePolicy(items[index]), nil
		}
	}
	return Policy{}, ErrNotFound
}

func (s *MemoryStore) GetEvidence(_ context.Context, tenantID, evidenceID string) (Evidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	evidence, ok := s.evidence[evidenceID]
	if !ok || evidence.TenantID != tenantID {
		return Evidence{}, ErrNotFound
	}
	return cloneEvidence(evidence), nil
}

func (s *MemoryStore) AppendEvidence(_ context.Context, evidence Evidence) (Evidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if evidence.ID != "" || evidence.Hash == "" || (evidence.Phase != EvidencePreview && evidence.Phase != EvidencePublish) {
		return Evidence{}, ErrInvalidInput
	}
	evidence.ID = s.idLocked("evidence")
	if evidence.CreatedAt.IsZero() {
		evidence.CreatedAt = s.now().UTC()
	}
	s.evidence[evidence.ID] = cloneEvidence(evidence)
	return cloneEvidence(evidence), nil
}

func (s *MemoryStore) RequestWaiver(_ context.Context, waiver Waiver) (Waiver, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if waiver.ID != "" || waiver.Status != WaiverRequested {
		return Waiver{}, ErrInvalidInput
	}
	waiver.ID = s.idLocked("waiver")
	if waiver.RequestedAt.IsZero() {
		waiver.RequestedAt = s.now().UTC()
	}
	s.waivers[waiver.ID] = cloneWaiver(waiver)
	return cloneWaiver(waiver), nil
}

func (s *MemoryStore) GetWaiver(_ context.Context, tenantID, waiverID string) (Waiver, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	waiver, ok := s.waivers[waiverID]
	if !ok || waiver.TenantID != tenantID {
		return Waiver{}, ErrNotFound
	}
	if decision, ok := s.decisions[waiverID]; ok {
		return cloneWaiver(decision), nil
	}
	return cloneWaiver(waiver), nil
}

func (s *MemoryStore) DecideWaiver(_ context.Context, tenantID, waiverID, actorID string, input DecideWaiverInput) (Waiver, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	waiver, ok := s.waivers[waiverID]
	if !ok || waiver.TenantID != tenantID || waiver.Status != WaiverRequested {
		return Waiver{}, ErrNotFound
	}
	if _, decided := s.decisions[waiverID]; decided {
		return Waiver{}, ErrEvidenceImmutable
	}
	now := s.now().UTC()
	waiver.Status, waiver.DecidedBy, waiver.DecidedAt, waiver.DecisionReason = WaiverRejected, actorID, &now, input.Reason
	if input.Approve {
		waiver.Status = WaiverApproved
	}
	s.decisions[waiverID] = cloneWaiver(waiver)
	return cloneWaiver(waiver), nil
}

func (s *MemoryStore) ApprovedWaivers(_ context.Context, tenantID, examID, policyID string) ([]Waiver, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []Waiver{}
	for waiverID, waiver := range s.waivers {
		if waiver.TenantID != tenantID || waiver.ExamID != examID || waiver.PolicyID != policyID {
			continue
		}
		decision, ok := s.decisions[waiverID]
		if ok && decision.Status == WaiverApproved {
			items = append(items, cloneWaiver(decision))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (s *MemoryStore) idLocked(prefix string) string {
	s.sequence++
	return fmt.Sprintf("%s-%d", prefix, s.sequence)
}
func scopeKey(tenantID, examID string) string { return tenantID + ":" + examID }

func cloneEvidence(value Evidence) Evidence {
	value.Evaluation.Policy = clonePolicy(value.Evaluation.Policy)
	value.Evaluation.BaseGate = cloneGate(value.Evaluation.BaseGate)
	value.Evaluation.PendingWarningCodes = append([]string(nil), value.Evaluation.PendingWarningCodes...)
	value.Evaluation.AppliedWaiverIDs = append([]string(nil), value.Evaluation.AppliedWaiverIDs...)
	return value
}
func cloneWaiver(value Waiver) Waiver {
	if value.DecidedAt != nil {
		copy := value.DecidedAt.UTC()
		value.DecidedAt = &copy
	}
	return value
}
