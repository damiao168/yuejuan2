package calibration

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

type memorySession struct {
	tenantID   string
	session    Session
	policy     Policy
	references []goldReference
	attempts   []Attempt
}

type MemoryStore struct {
	mu             sync.RWMutex
	policies       map[string]Policy
	sessions       map[string]memorySession
	qualifications map[string]Qualification
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{policies: map[string]Policy{}, sessions: map[string]memorySession{}, qualifications: map[string]Qualification{}}
}

func (s *MemoryStore) PutPolicy(_ context.Context, tenantID, examID, questionID string, input PutPolicyInput) (Policy, error) {
	if tenantID == "" || examID == "" || questionID == "" {
		return Policy{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := policyKey(tenantID, examID, questionID)
	now := time.Now().UTC()
	current, exists := s.policies[key]
	if exists && current.Revision != input.ExpectedRevision || !exists && input.ExpectedRevision != 0 {
		return Policy{}, ErrConflict
	}
	createdAt, id, revision := now, uuid.NewString(), int64(1)
	if exists {
		createdAt, id, revision = current.CreatedAt, current.ID, current.Revision+1
	}
	policy := Policy{ID: id, ExamID: examID, QuestionID: questionID, ArchetypeCode: input.ArchetypeCode,
		MaxScore: input.MaxScore, MinimumSamples: input.MinimumSamples, MaximumMAE: input.MaximumMAE,
		MinimumExactAgreement: input.MinimumExactAgreement, MinimumWithinOneAgreement: input.MinimumWithinOneAgreement,
		MinimumCriterionAgreement: cloneFloat(input.MinimumCriterionAgreement), MaximumSevereRate: input.MaximumSevereRate,
		SevereErrorThreshold: input.SevereErrorThreshold, QualificationValidityDays: input.QualificationValidityDays,
		Revision: revision, CreatedAt: createdAt, UpdatedAt: now}
	s.policies[key] = policy
	return clonePolicy(policy), nil
}

func (s *MemoryStore) GetPolicy(_ context.Context, tenantID, examID, questionID string) (Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	policy, ok := s.policies[policyKey(tenantID, examID, questionID)]
	if !ok {
		return Policy{}, ErrNotFound
	}
	return clonePolicy(policy), nil
}

func (s *MemoryStore) CreateSession(_ context.Context, tenantID string, session Session, policy Policy, references []goldReference) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.sessions {
		if item.tenantID == tenantID && item.session.ExamID == session.ExamID && item.session.QuestionID == session.QuestionID &&
			item.session.GraderID == session.GraderID && item.session.Status == SessionInProgress {
			return Session{}, ErrConflict
		}
	}
	s.sessions[sessionKey(tenantID, session.ID)] = memorySession{tenantID: tenantID, session: cloneSession(session), policy: clonePolicy(policy), references: cloneReferences(references)}
	return cloneSession(session), nil
}

func (s *MemoryStore) GetSession(_ context.Context, tenantID, sessionID string) (Session, Policy, []goldReference, []Attempt, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.sessions[sessionKey(tenantID, sessionID)]
	if !ok {
		return Session{}, Policy{}, nil, nil, ErrNotFound
	}
	session := cloneSession(item.session)
	session.SubmittedCount = len(item.attempts)
	return session, clonePolicy(item.policy), cloneReferences(item.references), cloneAttempts(item.attempts), nil
}

func (s *MemoryStore) CreateAttempt(_ context.Context, tenantID, sessionID string, attempt Attempt) (Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(tenantID, sessionID)
	item, ok := s.sessions[key]
	if !ok {
		return Attempt{}, ErrNotFound
	}
	if item.session.Status != SessionInProgress {
		return Attempt{}, ErrConflict
	}
	for _, current := range item.attempts {
		if current.GoldPaperID == attempt.GoldPaperID {
			return Attempt{}, ErrConflict
		}
	}
	item.attempts = append(item.attempts, cloneAttempt(attempt))
	item.session.SubmittedCount = len(item.attempts)
	s.sessions[key] = item
	return cloneAttempt(attempt), nil
}

func (s *MemoryStore) CompleteSession(_ context.Context, tenantID string, session Session, qualification Qualification) (Session, Qualification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(tenantID, session.ID)
	item, ok := s.sessions[key]
	if !ok {
		return Session{}, Qualification{}, ErrNotFound
	}
	// 只允许完整且仍在进行中的会话进入终态，避免并发重试提前生成资格或重复完成。
	if item.session.Status != SessionInProgress || len(item.attempts) != len(item.references) {
		return Session{}, Qualification{}, ErrConflict
	}
	item.session.Status, item.session.Metrics, item.session.CompletedAt = session.Status, cloneMetrics(session.Metrics), cloneTime(session.CompletedAt)
	item.session.SubmittedCount = len(item.attempts)
	s.sessions[key] = item
	qualification.Metrics = *cloneMetrics(&qualification.Metrics)
	s.qualifications[qualificationKey(tenantID, qualification.ExamID, qualification.QuestionID, qualification.GraderID)] = qualification
	return cloneSession(item.session), qualification, nil
}

func (s *MemoryStore) GetQualification(_ context.Context, tenantID, examID, questionID, graderID string) (Qualification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	qualification, ok := s.qualifications[qualificationKey(tenantID, examID, questionID, graderID)]
	if !ok {
		return Qualification{}, ErrNotFound
	}
	return cloneQualification(qualification), nil
}

func (s *MemoryStore) InvalidateQualification(_ context.Context, tenantID, qualificationID, reason string) (Qualification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, qualification := range s.qualifications {
		if keyTenant(key) != tenantID || qualification.ID != qualificationID {
			continue
		}
		if reason == "expired" {
			qualification.Status = QualificationExpired
		} else {
			qualification.Status = QualificationRevoked
		}
		qualification.UpdatedAt = time.Now().UTC()
		s.qualifications[key] = qualification
		return cloneQualification(qualification), nil
	}
	return Qualification{}, ErrNotFound
}

func policyKey(tenant, exam, question string) string {
	return tenant + "\x00" + exam + "\x00" + question
}
func sessionKey(tenant, id string) string { return tenant + "\x00" + id }
func qualificationKey(tenant, exam, question, grader string) string {
	return tenant + "\x00" + exam + "\x00" + question + "\x00" + grader
}
func keyTenant(key string) string {
	for index := range key {
		if key[index] == 0 {
			return key[:index]
		}
	}
	return key
}
func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}
func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}
func clonePolicy(value Policy) Policy {
	value.MinimumCriterionAgreement = cloneFloat(value.MinimumCriterionAgreement)
	return value
}
func cloneMetrics(value *Metrics) *Metrics {
	if value == nil {
		return nil
	}
	out := *value
	out.CriterionAgreement = cloneFloat(value.CriterionAgreement)
	return &out
}
func cloneSession(value Session) Session {
	value.Samples = append([]GoldSample(nil), value.Samples...)
	value.Metrics, value.CompletedAt, value.InvalidatedAt = cloneMetrics(value.Metrics), cloneTime(value.CompletedAt), cloneTime(value.InvalidatedAt)
	return value
}
func cloneReferences(values []goldReference) []goldReference {
	out := make([]goldReference, len(values))
	copy(out, values)
	for i := range out {
		out[i].ExpectedCriteria = cloneObject(out[i].ExpectedCriteria)
	}
	return out
}
func cloneAttempt(value Attempt) Attempt {
	value.RubricSelections = cloneObject(value.RubricSelections)
	value.CriterionDifferences = append([]CriterionDifference(nil), value.CriterionDifferences...)
	return value
}
func cloneAttempts(values []Attempt) []Attempt {
	out := make([]Attempt, len(values))
	for i := range values {
		out[i] = cloneAttempt(values[i])
	}
	return out
}
func cloneQualification(value Qualification) Qualification {
	value.Metrics = *cloneMetrics(&value.Metrics)
	return value
}
