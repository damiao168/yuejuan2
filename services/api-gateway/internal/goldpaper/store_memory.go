package goldpaper

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	mu      sync.RWMutex
	next    int
	sources map[string]Source
	items   map[string]GoldPaper
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{next: 1, sources: map[string]Source{}, items: map[string]GoldPaper{}}
}

// SetSource seeds facts owned by submission/assessment/grading in integrated
// memory servers and tests. Production always derives these facts in SQL.
func (s *MemoryStore) SetSource(tenantID string, source Source) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source.RubricSnapshot = cloneMap(source.RubricSnapshot)
	s.sources[sourceKey(tenantID, source.ExamID, source.QuestionID, source.SubmissionID)] = source
}

func (s *MemoryStore) Nominate(_ context.Context, tenantID, examID, questionID, actorID string, input NominateInput) (GoldPaper, error) {
	versionInput := normalizeInput(CreateVersionInput{ReferenceScore: input.ReferenceScore, Explanation: input.Explanation, TraitScores: input.TraitScores, ErrorTags: input.ErrorTags, SourceGradeIDs: input.SourceGradeIDs})
	if tenantID == "" || examID == "" || questionID == "" || actorID == "" || input.SubmissionID == "" {
		return GoldPaper{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.sources[sourceKey(tenantID, examID, questionID, input.SubmissionID)]
	if !ok {
		return GoldPaper{}, ErrNotFound
	}
	if err := validateInput(versionInput, source.MaxScore); err != nil {
		return GoldPaper{}, err
	}
	if !containsAll(source.AvailableGradeIDs, versionInput.SourceGradeIDs) {
		return GoldPaper{}, ErrSourceGradeMissing
	}
	for _, item := range s.items {
		if item.ExamID == examID && item.QuestionID == questionID && item.SubmissionID == input.SubmissionID {
			return GoldPaper{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	item := GoldPaper{
		ID: s.id("gold"), ExamID: examID, QuestionID: questionID, SubmissionID: input.SubmissionID,
		AnswerImageURL: "/api/v1/answer-segments/" + source.AnswerSegmentID + "/image",
		Status:         StatusPendingApproval, SubjectCode: source.SubjectCode, ArchetypeCode: source.ArchetypeCode,
		RiskTier: source.RiskTier, NominatedBy: actorID, CreatedAt: now, UpdatedAt: now,
	}
	item.Versions = []Version{s.newVersion(source, actorID, 1, versionInput, now)}
	s.items[itemKey(tenantID, item.ID)] = item
	return cloneGold(item), nil
}

func (s *MemoryStore) CreateVersion(_ context.Context, tenantID, goldID, actorID string, input CreateVersionInput) (GoldPaper, error) {
	input = normalizeInput(input)
	if tenantID == "" || goldID == "" || actorID == "" {
		return GoldPaper{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, goldID)
	item, ok := s.items[key]
	if !ok {
		return GoldPaper{}, ErrNotFound
	}
	if item.Status == StatusRetired {
		return GoldPaper{}, ErrConflict
	}
	for _, version := range item.Versions {
		if version.ApprovedAt == nil {
			return GoldPaper{}, ErrConflict
		}
	}
	source, ok := s.sources[sourceKey(tenantID, item.ExamID, item.QuestionID, item.SubmissionID)]
	if !ok {
		return GoldPaper{}, ErrNotFound
	}
	if err := validateInput(input, source.MaxScore); err != nil {
		return GoldPaper{}, err
	}
	if !containsAll(source.AvailableGradeIDs, input.SourceGradeIDs) {
		return GoldPaper{}, ErrSourceGradeMissing
	}
	now := time.Now().UTC()
	item.Versions = append(item.Versions, s.newVersion(source, actorID, len(item.Versions)+1, input, now))
	if item.ActiveVersion == 0 {
		item.Status = StatusPendingApproval
	}
	item.UpdatedAt = now
	s.items[key] = item
	return cloneGold(item), nil
}

// 审批在锁内把版本标为 active；已批准版本不可重复审批，退休 Gold 也不能重新启用。
func (s *MemoryStore) Approve(_ context.Context, tenantID, goldID, actorID string, versionNumber int) (GoldPaper, error) {
	if tenantID == "" || goldID == "" || actorID == "" || versionNumber <= 0 {
		return GoldPaper{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, goldID)
	item, ok := s.items[key]
	if !ok {
		return GoldPaper{}, ErrNotFound
	}
	if item.Status == StatusRetired {
		return GoldPaper{}, ErrConflict
	}
	index := -1
	for i := range item.Versions {
		if item.Versions[i].Version == versionNumber {
			index = i
			break
		}
	}
	if index < 0 {
		return GoldPaper{}, ErrNotFound
	}
	if item.Versions[index].ApprovedAt != nil {
		return GoldPaper{}, ErrAlreadyApproved
	}
	now := time.Now().UTC()
	item.Versions[index].ApprovedBy, item.Versions[index].ApprovedAt = actorID, &now
	item.ActiveVersion, item.Status, item.UpdatedAt = versionNumber, StatusActive, now
	s.items[key] = item
	return cloneGold(item), nil
}

func (s *MemoryStore) Retire(_ context.Context, tenantID, goldID, actorID, reason string) (GoldPaper, error) {
	reason = stringsTrim(reason)
	if tenantID == "" || goldID == "" || actorID == "" || reason == "" {
		return GoldPaper{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, goldID)
	item, ok := s.items[key]
	if !ok {
		return GoldPaper{}, ErrNotFound
	}
	if item.Status == StatusRetired {
		return GoldPaper{}, ErrConflict
	}
	now := time.Now().UTC()
	item.Status, item.RetiredAt, item.RetirementReason, item.UpdatedAt = StatusRetired, &now, reason, now
	s.items[key] = item
	return cloneGold(item), nil
}

func (s *MemoryStore) Get(_ context.Context, tenantID, id string) (GoldPaper, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[itemKey(tenantID, id)]
	if !ok {
		return GoldPaper{}, ErrNotFound
	}
	return cloneGold(item), nil
}

func (s *MemoryStore) List(_ context.Context, tenantID string, filter ListFilter) ([]GoldPaper, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []GoldPaper{}
	for key, item := range s.items {
		if keyTenant(key) != tenantID || filter.ExamID != "" && item.ExamID != filter.ExamID || filter.QuestionID != "" && item.QuestionID != filter.QuestionID || filter.Status != "" && item.Status != filter.Status {
			continue
		}
		out = append(out, cloneGold(item))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) ListActiveApproved(ctx context.Context, tenantID, examID, questionID string) ([]GoldPaper, error) {
	return s.List(ctx, tenantID, ListFilter{ExamID: examID, QuestionID: questionID, Status: StatusActive})
}

func (s *MemoryStore) Coverage(ctx context.Context, tenantID, examID, questionID string) (Coverage, error) {
	items, err := s.ListActiveApproved(ctx, tenantID, examID, questionID)
	if err != nil {
		return Coverage{}, err
	}
	s.mu.RLock()
	var source Source
	for key, candidate := range s.sources {
		if keyTenant(key) == tenantID && candidate.ExamID == examID && candidate.QuestionID == questionID {
			source = candidate
			break
		}
	}
	s.mu.RUnlock()
	if source.QuestionID == "" {
		return Coverage{}, ErrNotFound
	}
	return buildCoverage(examID, questionID, source.SubjectCode, source.ArchetypeCode, source.RiskTier, source.MaxScore, items), nil
}

func (s *MemoryStore) newVersion(source Source, actorID string, version int, input CreateVersionInput, now time.Time) Version {
	return Version{ID: s.id("gold-version"), Version: version, ExamQuestionSnapshotID: source.SnapshotID,
		ReferenceScore: input.ReferenceScore, MaxScore: source.MaxScore, RubricSnapshot: cloneMap(source.RubricSnapshot),
		Explanation: input.Explanation, TraitScores: cloneMap(input.TraitScores), ErrorTags: append([]string(nil), input.ErrorTags...),
		SourceGradeIDs: append([]string(nil), input.SourceGradeIDs...), NominatedBy: actorID, CreatedAt: now}
}

func (s *MemoryStore) id(prefix string) string {
	value := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return value
}
func sourceKey(tenantID, examID, questionID, submissionID string) string {
	return tenantID + "\x00" + examID + "\x00" + questionID + "\x00" + submissionID
}
func itemKey(tenantID, id string) string { return tenantID + "\x00" + id }
func keyTenant(key string) string {
	for i, value := range key {
		if value == 0 {
			return key[:i]
		}
	}
	return key
}
func stringsTrim(value string) string { return strings.TrimSpace(value) }

func containsAll(available, selected []string) bool {
	set := map[string]struct{}{}
	for _, value := range available {
		set[value] = struct{}{}
	}
	for _, value := range selected {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}
