package assessment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"sync"
	"time"
)

type MemoryStore struct {
	mu        sync.RWMutex
	next      int
	profiles  map[string][]SubjectProfile
	configs   map[string]QuestionAssessmentConfig
	snapshots map[string]ExamQuestionSnapshot
	evidence  map[string][]ScoringEvidence
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		next:      1,
		profiles:  map[string][]SubjectProfile{},
		configs:   map[string]QuestionAssessmentConfig{},
		snapshots: map[string]ExamQuestionSnapshot{},
		evidence:  map[string][]ScoringEvidence{},
	}
}

func (s *MemoryStore) ListSubjectProfiles(_ context.Context, tenantID string, stage EducationStage, subject SubjectCode) ([]SubjectProfile, error) {
	if tenantID == "" || (stage != "" && !stage.Valid()) || (subject != "" && !subject.Valid()) {
		return nil, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureProfiles(tenantID)
	out := []SubjectProfile{}
	for _, profile := range s.profiles[tenantID] {
		if (stage == "" || profile.EducationStage == stage) && (subject == "" || profile.SubjectCode == subject) {
			out = append(out, cloneSubjectProfile(profile))
		}
	}
	return out, nil
}

func (s *MemoryStore) ListQuestionArchetypes(context.Context) ([]QuestionArchetype, error) {
	return cloneArchetypes(DefaultQuestionArchetypes()), nil
}

func (s *MemoryStore) GetQuestionConfig(_ context.Context, tenantID string, examID string, questionID string) (QuestionAssessmentConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.configs[assessmentKey(tenantID, examID, questionID)]
	if !ok {
		return QuestionAssessmentConfig{}, ErrNotFound
	}
	return cloneQuestionConfig(item), nil
}

func (s *MemoryStore) ConfigureQuestion(_ context.Context, tenantID string, examID string, questionID string, input ConfigureQuestionInput) (QuestionAssessmentConfig, error) {
	if tenantID == "" || examID == "" || questionID == "" || input.SubjectProfileID == "" {
		return QuestionAssessmentConfig{}, ErrInvalidInput
	}
	if err := validateConfigureInput(input); err != nil {
		return QuestionAssessmentConfig{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureProfiles(tenantID)
	profile, ok := memoryProfileByID(s.profiles[tenantID], input.SubjectProfileID)
	if !ok {
		return QuestionAssessmentConfig{}, ErrNotFound
	}
	archetype, ok := memoryArchetypeByCode(input.ArchetypeCode)
	if !ok {
		return QuestionAssessmentConfig{}, ErrNotFound
	}
	if err := validateAllowedEvidence(input.AllowedEvidenceTypes, profile, archetype, input.ScoringPolicy.RequireEvidence); err != nil {
		return QuestionAssessmentConfig{}, err
	}
	key := assessmentKey(tenantID, examID, questionID)
	current, exists := s.configs[key]
	if (!exists && input.ExpectedRevision != 0) || (exists && current.Revision != input.ExpectedRevision) {
		return QuestionAssessmentConfig{}, ErrRevisionConflict
	}
	if _, frozen := s.snapshots[key]; frozen {
		// 冻结后拒绝覆盖配置，保证后续证据仍能解释当时使用的评分规则。
		return QuestionAssessmentConfig{}, ErrExamFrozen
	}
	now := time.Now().UTC()
	revision := int64(1)
	createdAt := now
	id := s.id("assessment-config")
	if exists {
		revision = current.Revision + 1
		createdAt = current.CreatedAt
		id = current.ID
	}
	item := QuestionAssessmentConfig{
		ID: id, TenantID: tenantID, ExamID: examID, QuestionID: questionID,
		SubjectProfileID: profile.ID, SubjectProfileCode: profile.Code,
		SubjectProfileVersion: profile.Version, EducationStage: profile.EducationStage,
		SubjectCode: profile.SubjectCode, ArchetypeCode: input.ArchetypeCode,
		AllowedEvidenceTypes: cloneEvidenceTypes(input.AllowedEvidenceTypes), RiskTier: input.RiskTier,
		ScoringPolicy: cloneScoringPolicy(input.ScoringPolicy), Revision: revision,
		CreatedAt: createdAt, UpdatedAt: now,
	}
	s.configs[key] = item
	return cloneQuestionConfig(item), nil
}

func (s *MemoryStore) FreezeQuestionSnapshot(_ context.Context, tenantID string, examID string, questionID string) (ExamQuestionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := assessmentKey(tenantID, examID, questionID)
	if existing, ok := s.snapshots[key]; ok {
		// 重复冻结返回原快照，不重新读取可能变化的学科配置。
		return cloneQuestionSnapshot(existing), nil
	}
	config, ok := s.configs[key]
	if !ok {
		return ExamQuestionSnapshot{}, ErrNotFound
	}
	profile, ok := memoryProfileByID(s.profiles[tenantID], config.SubjectProfileID)
	if !ok {
		return ExamQuestionSnapshot{}, ErrNotFound
	}
	archetype, ok := memoryArchetypeByCode(config.ArchetypeCode)
	if !ok {
		return ExamQuestionSnapshot{}, ErrNotFound
	}
	profileJSON := map[string]any{
		"id": profile.ID, "code": profile.Code, "education_stage": profile.EducationStage,
		"subject_code": profile.SubjectCode, "version": profile.Version,
		"parser_policy": cloneMap(profile.ParserPolicy), "evidence_policy": cloneMap(profile.EvidencePolicy),
		"scoring_default": profile.ScoringDefault,
	}
	archetypeJSON := map[string]any{
		"code": archetype.Code, "response_schema": cloneMap(archetype.ResponseSchema),
		"evidence_types": cloneEvidenceTypes(archetype.EvidenceTypes), "default_scoring_mode": archetype.DefaultScoringMode,
	}
	hashInput, _ := json.Marshal([]any{profileJSON, archetypeJSON, config.AllowedEvidenceTypes, config.RiskTier, config.ScoringPolicy})
	digest := sha256.Sum256(hashInput)
	item := ExamQuestionSnapshot{
		ID: s.id("assessment-snapshot"), TenantID: tenantID, ExamID: examID, QuestionID: questionID,
		SnapshotVersion: 1, SubjectProfileID: profile.ID, SubjectProfileCode: profile.Code,
		SubjectProfileVersion: profile.Version, EducationStage: profile.EducationStage,
		SubjectCode: profile.SubjectCode, ArchetypeCode: archetype.Code,
		AllowedEvidenceTypes: cloneEvidenceTypes(config.AllowedEvidenceTypes), RiskTier: config.RiskTier,
		ProfileSnapshot: profileJSON, ArchetypeSnapshot: archetypeJSON, RubricSnapshot: map[string]any{},
		ScoringPolicySnapshot: cloneScoringPolicy(config.ScoringPolicy), ContentHash: hex.EncodeToString(digest[:]),
		CreatedAt: time.Now().UTC(),
	}
	s.snapshots[key] = item
	return cloneQuestionSnapshot(item), nil
}

func (s *MemoryStore) GetQuestionSnapshot(_ context.Context, tenantID string, examID string, questionID string) (ExamQuestionSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.snapshots[assessmentKey(tenantID, examID, questionID)]
	if !ok {
		return ExamQuestionSnapshot{}, ErrNotFound
	}
	return cloneQuestionSnapshot(item), nil
}

func (s *MemoryStore) CreateScoringEvidence(_ context.Context, tenantID string, input CreateScoringEvidenceInput) (ScoringEvidence, error) {
	if tenantID == "" || ValidateScoringEvidence(input) != nil {
		return ScoringEvidence{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var snapshot ExamQuestionSnapshot
	ok := false
	for _, candidate := range s.snapshots {
		if candidate.TenantID == tenantID && candidate.ID == input.ExamQuestionSnapshotID && candidate.QuestionID == input.QuestionID {
			snapshot = candidate
			ok = true
			break
		}
	}
	if !ok || !containsEvidence(snapshot.AllowedEvidenceTypes, input.EvidenceType) {
		return ScoringEvidence{}, ErrInvalidInput
	}
	item := ScoringEvidence{
		ID: s.id("scoring-evidence"), TenantID: tenantID, SubmissionID: input.SubmissionID,
		QuestionID: input.QuestionID, ExamQuestionSnapshotID: input.ExamQuestionSnapshotID,
		EvidenceType: input.EvidenceType, SourceArtifactID: input.SourceArtifactID,
		RubricCriterionKey: input.RubricCriterionKey, Payload: cloneMap(input.Payload),
		BoundingBox: cloneBoundingBox(input.BoundingBox), Quality: cloneFloat(input.Quality), CreatedAt: time.Now().UTC(),
	}
	key := evidenceKey(tenantID, input.SubmissionID, input.QuestionID)
	s.evidence[key] = append(s.evidence[key], item)
	return cloneScoringEvidence(item), nil
}

func (s *MemoryStore) ListScoringEvidence(_ context.Context, tenantID string, submissionID string, questionID string) ([]ScoringEvidence, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := s.evidence[evidenceKey(tenantID, submissionID, questionID)]
	out := make([]ScoringEvidence, len(items))
	for index, item := range items {
		out[index] = cloneScoringEvidence(item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) ensureProfiles(tenantID string) {
	if _, ok := s.profiles[tenantID]; !ok {
		s.profiles[tenantID] = DefaultSubjectProfiles(tenantID)
	}
}

func (s *MemoryStore) id(prefix string) string {
	id := prefix + "-" + strconv.Itoa(s.next)
	s.next++
	return id
}

func assessmentKey(tenantID string, examID string, questionID string) string {
	return tenantID + "\x00" + examID + "\x00" + questionID
}

func evidenceKey(tenantID string, submissionID string, questionID string) string {
	return tenantID + "\x00" + submissionID + "\x00" + questionID
}

func memoryProfileByID(profiles []SubjectProfile, id string) (SubjectProfile, bool) {
	for _, profile := range profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return SubjectProfile{}, false
}

func memoryArchetypeByCode(code string) (QuestionArchetype, bool) {
	for _, archetype := range DefaultQuestionArchetypes() {
		if archetype.Code == code {
			return archetype, true
		}
	}
	return QuestionArchetype{}, false
}

func containsEvidence(values []EvidenceType, target EvidenceType) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cloneSubjectProfile(value SubjectProfile) SubjectProfile {
	value.ParserPolicy = cloneMap(value.ParserPolicy)
	value.EvidencePolicy = cloneMap(value.EvidencePolicy)
	value.ScoringDefault = cloneScoringPolicy(value.ScoringDefault)
	return value
}

func cloneQuestionConfig(value QuestionAssessmentConfig) QuestionAssessmentConfig {
	value.AllowedEvidenceTypes = cloneEvidenceTypes(value.AllowedEvidenceTypes)
	value.ScoringPolicy = cloneScoringPolicy(value.ScoringPolicy)
	return value
}

func cloneQuestionSnapshot(value ExamQuestionSnapshot) ExamQuestionSnapshot {
	value.AllowedEvidenceTypes = cloneEvidenceTypes(value.AllowedEvidenceTypes)
	value.ProfileSnapshot = cloneMap(value.ProfileSnapshot)
	value.ArchetypeSnapshot = cloneMap(value.ArchetypeSnapshot)
	value.RubricSnapshot = cloneMap(value.RubricSnapshot)
	value.ScoringPolicySnapshot = cloneScoringPolicy(value.ScoringPolicySnapshot)
	return value
}

func cloneScoringEvidence(value ScoringEvidence) ScoringEvidence {
	value.Payload = cloneMap(value.Payload)
	value.BoundingBox = cloneBoundingBox(value.BoundingBox)
	value.Quality = cloneFloat(value.Quality)
	return value
}

func cloneArchetypes(values []QuestionArchetype) []QuestionArchetype {
	out := make([]QuestionArchetype, len(values))
	for index, value := range values {
		out[index] = value
		out[index].ResponseSchema = cloneMap(value.ResponseSchema)
		out[index].EvidenceTypes = cloneEvidenceTypes(value.EvidenceTypes)
	}
	return out
}

func cloneEvidenceTypes(values []EvidenceType) []EvidenceType {
	return append([]EvidenceType(nil), values...)
}

func cloneScoringPolicy(value ScoringPolicy) ScoringPolicy {
	value.ConfidenceThreshold = cloneFloat(value.ConfidenceThreshold)
	return value
}

func cloneBoundingBox(value *BoundingBox) *BoundingBox {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	raw, _ := json.Marshal(value)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}
