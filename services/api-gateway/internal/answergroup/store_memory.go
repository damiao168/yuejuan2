package answergroup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	mu         sync.RWMutex
	next       int
	provider   RepresentationProvider
	policy     Policy
	sources    map[string][]SourceAnswer
	groups     map[string]Group
	buildIndex map[string][]string
	candidates map[string][]Candidate
}

func NewMemoryStore(provider RepresentationProvider, policy Policy) *MemoryStore {
	if provider == nil {
		provider = DeterministicTextProvider{}
	}
	if !policy.valid() {
		policy = DefaultPolicy()
	}
	return &MemoryStore{
		next: 1, provider: provider, policy: policy, sources: map[string][]SourceAnswer{},
		groups: map[string]Group{}, buildIndex: map[string][]string{}, candidates: map[string][]Candidate{},
	}
}

func (s *MemoryStore) SetSourceAnswers(tenantID, examID, questionID string, answers []SourceAnswer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyAnswers := append([]SourceAnswer(nil), answers...)
	s.sources[questionKey(tenantID, examID, questionID)] = copyAnswers
}

func (s *MemoryStore) Build(_ context.Context, tenantID, examID, questionID, actorID string, input BuildInput) ([]Group, error) {
	if tenantID == "" || examID == "" || questionID == "" || actorID == "" {
		return nil, ErrInvalidInput
	}
	algorithm := strings.TrimSpace(input.AlgorithmVersion)
	if algorithm == "" {
		algorithm = DefaultAlgorithmVersion
	}
	if algorithm != DefaultAlgorithmVersion {
		return nil, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source := s.sources[questionKey(tenantID, examID, questionID)]
	eligibleAnswers := make([]SourceAnswer, 0, len(source))
	for _, answer := range source {
		if eligible(answer, s.policy) {
			eligibleAnswers = append(eligibleAnswers, answer)
		}
	}
	if len(eligibleAnswers) == 0 {
		return nil, ErrNoEligibleAnswers
	}
	inputHash := buildInputHash(eligibleAnswers, algorithm, s.provider.Version())
	indexKey := questionKey(tenantID, examID, questionID) + "\x00" + inputHash
	if ids := s.buildIndex[indexKey]; len(ids) > 0 {
		return s.groupsByID(ids), nil
	}
	archetype := eligibleAnswers[0].ArchetypeCode
	for _, answer := range eligibleAnswers {
		if answer.ArchetypeCode != archetype || answer.SnapshotID == "" {
			return nil, ErrInvalidInput
		}
	}
	threshold := s.policy.ShortAnswerThreshold
	if archetype == ArchetypeExactText {
		threshold = s.policy.ExactTextThreshold
	}
	now := time.Now().UTC()
	clusters := clusterAnswers(eligibleAnswers, s.provider, threshold)
	ids := make([]string, 0, len(clusters))
	for _, cluster := range clusters {
		members, homogeneity := materializeMembers(cluster, s.policy.HighOutlierThreshold)
		group := Group{
			ID: s.id("answer-group"), TenantID: tenantID, ExamID: examID, QuestionID: questionID,
			ExamQuestionSnapshotID: cluster.Answers[0].Source.SnapshotID,
			AlgorithmVersion:       algorithm, RepresentationVersion: s.provider.Version(),
			MemberCount: len(members), Homogeneity: homogeneity, Status: StatusSampling,
			MinimumSample: minimumSample(len(members), homogeneity), Members: members,
			CreatedAt: now, UpdatedAt: now,
		}
		for _, member := range members {
			if member.Representative {
				group.RepresentativeSubmissionID = member.SubmissionID
			}
			if member.Outlier {
				s.candidates[itemKey(tenantID, group.ID)] = append(s.candidates[itemKey(tenantID, group.ID)], Candidate{
					ID: s.id("answer-candidate"), GroupID: group.ID, SubmissionID: member.SubmissionID,
					SegmentID: member.SegmentID, Kind: CandidateIndividualReview, Status: CandidateManualRequired,
					ScoreCandidate: map[string]any{"reason": "high_outlier"}, RubricSelection: map[string]any{},
					AlgorithmVersion: algorithm, CreatedAt: now,
				})
			}
		}
		refreshReadiness(&group)
		s.groups[itemKey(tenantID, group.ID)] = group
		ids = append(ids, group.ID)
	}
	s.buildIndex[indexKey] = ids
	return s.groupsByID(ids), nil
}

func (s *MemoryStore) List(_ context.Context, tenantID, examID, questionID string) ([]Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []Group{}
	for key, group := range s.groups {
		if keyTenant(key) == tenantID && group.ExamID == examID && group.QuestionID == questionID {
			items = append(items, cloneGroup(group))
		}
	}
	sortGroups(items)
	return items, nil
}

func (s *MemoryStore) Get(_ context.Context, tenantID, groupID string) (Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	group, ok := s.groups[itemKey(tenantID, groupID)]
	if !ok {
		return Group{}, ErrNotFound
	}
	return cloneGroup(group), nil
}

func (s *MemoryStore) ReviewSample(_ context.Context, tenantID, groupID, memberSegmentID, actorID string, input SampleReviewInput) (Group, error) {
	input.Outcome, input.Notes = strings.TrimSpace(input.Outcome), strings.TrimSpace(input.Notes)
	if actorID == "" || memberSegmentID == "" || (input.Outcome != SampleAccepted && input.Outcome != SampleRejected) || len(input.Notes) > 4000 {
		return Group{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, groupID)
	group, ok := s.groups[key]
	if !ok {
		return Group{}, ErrNotFound
	}
	if group.Status == StatusConfirmed || group.Status == StatusRolledBack {
		return Group{}, ErrStateConflict
	}
	index := -1
	for current := range group.Members {
		if group.Members[current].SegmentID == memberSegmentID {
			index = current
			break
		}
	}
	if index < 0 {
		return Group{}, ErrNotFound
	}
	now := time.Now().UTC()
	group.Members[index].SampleStatus, group.Members[index].SampledBy, group.Members[index].SampledAt = input.Outcome, actorID, &now
	group.UpdatedAt = now
	refreshReadiness(&group)
	s.groups[key] = group
	return cloneGroup(group), nil
}

func (s *MemoryStore) PutDecision(_ context.Context, tenantID, groupID, actorID string, input DecisionInput) (Group, error) {
	if actorID == "" || len(input.ScoreCandidate) == 0 || len(input.RubricSelection) == 0 || input.ExpectedRevision < 0 {
		return Group{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, groupID)
	group, ok := s.groups[key]
	if !ok {
		return Group{}, ErrNotFound
	}
	if group.Status == StatusConfirmed || group.Status == StatusRolledBack {
		return Group{}, ErrStateConflict
	}
	now := time.Now().UTC()
	if group.Decision == nil {
		if input.ExpectedRevision != 0 {
			return Group{}, ErrRevisionConflict
		}
		group.Decision = &Decision{ID: s.id("answer-group-decision"), Revision: 1}
	} else {
		if input.ExpectedRevision != group.Decision.Revision {
			return Group{}, ErrRevisionConflict
		}
		group.Decision.Revision++
	}
	group.Decision.ScoreCandidate = cloneMap(input.ScoreCandidate)
	group.Decision.RubricSelection = cloneMap(input.RubricSelection)
	group.Decision.MinimumSample = group.MinimumSample
	group.Decision.SampleSize = group.ReviewedSampleCount
	group.UpdatedAt = now
	refreshReadiness(&group)
	s.groups[key] = group
	return cloneGroup(group), nil
}

func (s *MemoryStore) Confirm(_ context.Context, tenantID, groupID, actorID string, input ConfirmInput) (Group, []Candidate, error) {
	if actorID == "" || input.ExpectedRevision <= 0 {
		return Group{}, nil, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, groupID)
	group, ok := s.groups[key]
	if !ok {
		return Group{}, nil, ErrNotFound
	}
	if group.RepresentationVersion != s.provider.Version() || group.AlgorithmVersion != DefaultAlgorithmVersion {
		return Group{}, nil, ErrStateConflict
	}
	refreshReadiness(&group)
	if group.Decision == nil || group.Decision.Revision != input.ExpectedRevision {
		return Group{}, nil, ErrRevisionConflict
	}
	if !group.CanConfirm {
		return Group{}, nil, ErrSamplingIncomplete
	}
	now := time.Now().UTC()
	rollback := s.id("rollback")
	created := make([]Candidate, 0, group.MemberCount)
	for _, member := range group.Members {
		candidate := Candidate{
			ID: s.id("answer-candidate"), GroupID: group.ID, SubmissionID: member.SubmissionID,
			SegmentID: member.SegmentID, DecisionRevision: group.Decision.Revision,
			Kind: CandidateGroupScore, Status: CandidateActive,
			ScoreCandidate: cloneMap(group.Decision.ScoreCandidate), RubricSelection: cloneMap(group.Decision.RubricSelection),
			AlgorithmVersion: group.AlgorithmVersion, RollbackReference: rollback, CreatedAt: now,
		}
		created = append(created, candidate)
	}
	group.Decision.ConfirmedBy, group.Decision.ConfirmedAt, group.Decision.RollbackReference = actorID, &now, rollback
	group.Decision.SampleSize = group.ReviewedSampleCount
	group.Status, group.CanConfirm, group.UpdatedAt = StatusConfirmed, false, now
	s.groups[key] = group
	s.candidates[key] = append(s.candidates[key], created...)
	return cloneGroup(group), cloneCandidates(created), nil
}

func (s *MemoryStore) Rollback(_ context.Context, tenantID, groupID, actorID string, input RollbackInput) (Group, []Candidate, error) {
	input.RollbackReference, input.Reason = strings.TrimSpace(input.RollbackReference), strings.TrimSpace(input.Reason)
	if actorID == "" || input.RollbackReference == "" || input.Reason == "" || len(input.Reason) > 4000 {
		return Group{}, nil, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, groupID)
	group, ok := s.groups[key]
	if !ok {
		return Group{}, nil, ErrNotFound
	}
	if group.Status != StatusConfirmed || group.Decision == nil || group.Decision.RollbackReference != input.RollbackReference {
		return Group{}, nil, ErrRollbackConflict
	}
	now := time.Now().UTC()
	updated := []Candidate{}
	for index := range s.candidates[key] {
		candidate := &s.candidates[key][index]
		if candidate.RollbackReference == input.RollbackReference && candidate.Status == CandidateActive {
			candidate.Status = CandidateRolledBack
			updated = append(updated, *candidate)
		}
	}
	group.Decision.RolledBackBy, group.Decision.RolledBackAt, group.Decision.RollbackReason = actorID, &now, input.Reason
	group.Status, group.UpdatedAt = StatusRolledBack, now
	s.groups[key] = group
	return cloneGroup(group), cloneCandidates(updated), nil
}

func (s *MemoryStore) Metrics(_ context.Context, tenantID, examID, questionID string) (Metrics, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	metric := Metrics{PostAuditEvidenceStatus: "not_collected"}
	weightedHomogeneity := 0.0
	decided, rolledBack := 0, 0
	for key, group := range s.groups {
		if keyTenant(key) != tenantID || group.ExamID != examID || group.QuestionID != questionID {
			continue
		}
		metric.GroupCount++
		metric.MemberCount += group.MemberCount
		weightedHomogeneity += group.Homogeneity * float64(group.MemberCount)
		if group.Status == StatusConfirmed || group.Status == StatusRolledBack {
			decided++
		}
		if group.Status == StatusRolledBack {
			rolledBack++
		}
		if group.Status == StatusConfirmed && group.Decision != nil {
			metric.HumanActionsSaved += max(0, group.MemberCount-group.Decision.SampleSize)
		}
	}
	if metric.MemberCount > 0 {
		metric.GroupHomogeneity = roundScore(weightedHomogeneity / float64(metric.MemberCount))
	}
	if decided > 0 {
		metric.BatchOverrideRate = roundScore(float64(rolledBack) / float64(decided))
	}
	return metric, nil
}

func (s *MemoryStore) groupsByID(ids []string) []Group {
	items := make([]Group, 0, len(ids))
	for _, id := range ids {
		if item, ok := s.groups[itemKeyFromAnyTenant(s.groups, id)]; ok {
			items = append(items, cloneGroup(item))
			continue
		}
		for _, item := range s.groups {
			if item.ID == id {
				items = append(items, cloneGroup(item))
				break
			}
		}
	}
	sortGroups(items)
	return items
}

func itemKeyFromAnyTenant(groups map[string]Group, id string) string {
	for key, group := range groups {
		if group.ID == id {
			return key
		}
	}
	return ""
}

func buildInputHash(answers []SourceAnswer, algorithm, representation string) string {
	items := append([]SourceAnswer(nil), answers...)
	sort.Slice(items, func(i, j int) bool { return items[i].SegmentID < items[j].SegmentID })
	hash := sha256.New()
	hash.Write([]byte(algorithm + "\x00" + representation))
	for _, answer := range items {
		hash.Write([]byte("\x00" + answer.SegmentID + "\x00" + answer.SnapshotID + "\x00" + normalizeText(answer.AnswerText)))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (s *MemoryStore) id(prefix string) string {
	value := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return value
}

func questionKey(tenantID, examID, questionID string) string {
	return tenantID + "\x00" + examID + "\x00" + questionID
}
func itemKey(tenantID, id string) string { return tenantID + "\x00" + id }
func keyTenant(key string) string {
	if index := strings.IndexByte(key, 0); index >= 0 {
		return key[:index]
	}
	return key
}
