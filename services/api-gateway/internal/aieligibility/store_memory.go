package aieligibility

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

type MemoryStore struct {
	mu        sync.RWMutex
	policies  map[string][]Policy
	decisions map[string]Decision
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{policies: map[string][]Policy{}, decisions: map[string]Decision{}}
}

func (s *MemoryStore) PutPolicy(_ context.Context, tenantID string, input PutPolicyInput) (Policy, error) {
	key := policyKey(tenantID, input.SubjectCode, input.EducationStage, input.ArchetypeCode, input.RiskTier)
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := s.policies[key]
	latest := 0
	if len(versions) > 0 {
		latest = versions[len(versions)-1].Version
	}
	if input.ExpectedVersion != latest {
		return Policy{}, ErrPolicyConflict
	}
	item := Policy{ID: "memory-policy-" + key + "-v" + itoa(latest+1), TenantID: tenantID,
		SubjectCode: input.SubjectCode, EducationStage: input.EducationStage, ArchetypeCode: input.ArchetypeCode, RiskTier: input.RiskTier,
		MinOCRQuality: input.MinOCRQuality, MinParserQuality: input.MinParserQuality, MinEvalN: input.MinEvalN,
		MaxSevereErrorRate: input.MaxSevereErrorRate, AllowedModes: append([]assessment.ScoringMode(nil), input.AllowedModes...),
		Version: latest + 1, Status: input.Status, CreatedAt: time.Now().UTC()}
	s.policies[key] = append(versions, item)
	return clonePolicy(item), nil
}

func (s *MemoryStore) GetActivePolicy(_ context.Context, tenantID string, subject assessment.SubjectCode, stage assessment.EducationStage, archetype string, risk assessment.RiskTier) (Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	versions := s.policies[policyKey(tenantID, subject, stage, archetype, risk)]
	if len(versions) > 0 && versions[len(versions)-1].Status == PolicyActive {
		return clonePolicy(versions[len(versions)-1]), nil
	}
	return Policy{}, ErrNotFound
}

func (s *MemoryStore) CreateOrGetDecision(_ context.Context, tenantID string, input Decision) (Decision, error) {
	// 一次运行的决定一经记录就不重算；输入或策略改变后需使用新的运行标识。
	key := decisionKey(tenantID, input.RunItemID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.decisions[key]; ok {
		return cloneDecision(existing), nil
	}
	s.decisions[key] = cloneDecision(input)
	return cloneDecision(input), nil
}

func (s *MemoryStore) GetDecision(_ context.Context, tenantID, runItemID string) (Decision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.decisions[decisionKey(tenantID, runItemID)]
	if !ok {
		return Decision{}, ErrNotFound
	}
	return cloneDecision(item), nil
}

func policyKey(tenant string, subject assessment.SubjectCode, stage assessment.EducationStage, archetype string, risk assessment.RiskTier) string {
	return tenant + "\x00" + string(subject) + "\x00" + string(stage) + "\x00" + archetype + "\x00" + string(risk)
}
func decisionKey(tenant, run string) string { return tenant + "\x00" + run }
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	out := make([]byte, 0, 8)
	for value > 0 {
		out = append([]byte{byte('0' + value%10)}, out...)
		value /= 10
	}
	return string(out)
}
func clonePolicy(value Policy) Policy {
	value.AllowedModes = append([]assessment.ScoringMode(nil), value.AllowedModes...)
	return value
}
func cloneDecision(value Decision) Decision {
	raw, _ := json.Marshal(value)
	var result Decision
	_ = json.Unmarshal(raw, &result)
	return result
}
