package score

import (
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"fmt"
	"strings"
	"sync"
	"time"
)

type SegmentSeed struct {
	ExamID          string
	SubmissionID    string
	StudentID       string
	AnonymousCode   string
	AnswerSegmentID string
	QuestionID      string
	QuestionNo      string
	MaxScore        float64
}

type GradeSeed struct {
	AnswerSegmentID string
	Score           float64
	MaxScore        float64
	Source          string
	Mock            bool
	NeedsReview     bool
	AutoPass        bool
}

type TaskSeed struct {
	ExamID string
	Status string
	// SubmissionID 为空时任务归属未知，名册保守地认为它可能影响本场每一份答卷。
	SubmissionID string
}

type RosterSeed struct {
	ExamID      string
	StudentID   string
	StudentNo   string
	StudentName string
	ClassID     string
	ClassName   string
}

type RosterSubmissionSeed struct {
	ExamID           string
	SubmissionID     string
	StudentID        string
	CandidateNo      string
	ExpectedPages    int
	ActualPages      int
	QualityStatus    string
	SubmissionStatus string
	IdentityStatus   string
}

type MemoryStore struct {
	receipts          commandreceipt.Memory
	mu                sync.RWMutex
	next              int
	segments          map[string]SegmentSeed
	finals            map[string]FinalGrade
	finalBySeg        map[string]string
	humanGrades       map[string]GradeSeed
	ruleGrades        map[string]GradeSeed
	submissions       map[string]SubmissionGrade
	examStatuses      map[string]string
	reviewTasks       []TaskSeed
	arbTasks          []TaskSeed
	ocrTasks          []TaskSeed
	roster            map[string]RosterSeed
	rosterSubmissions []RosterSubmissionSeed
	attendance        map[string]RosterEntry
	// formalReleases 模拟 score_release_current：标记后发布前读取不再按自动准备放宽。
	formalReleases map[string]bool
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		next:           1,
		segments:       map[string]SegmentSeed{},
		finals:         map[string]FinalGrade{},
		finalBySeg:     map[string]string{},
		humanGrades:    map[string]GradeSeed{},
		ruleGrades:     map[string]GradeSeed{},
		submissions:    map[string]SubmissionGrade{},
		examStatuses:   map[string]string{},
		roster:         map[string]RosterSeed{},
		attendance:     map[string]RosterEntry{},
		formalReleases: map[string]bool{},
	}
}

// MarkFormalReleaseForTest 模拟该考试已有正式发布；内存实现没有发布快照，只用于让读取与 PostgreSQL 一致地停止放宽。
func (s *MemoryStore) MarkFormalReleaseForTest(examID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.formalReleases[examID] = true
}

func (s *MemoryStore) AddSegment(seed SegmentSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seed.AnonymousCode == "" {
		seed.AnonymousCode = seed.SubmissionID
	}
	s.segments[seed.AnswerSegmentID] = seed
	if _, exists := s.examStatuses[seed.ExamID]; !exists {
		s.examStatuses[seed.ExamID] = "collecting"
	}
}

func (s *MemoryStore) SetExamStatusForTest(examID string, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.examStatuses[examID] = status
}

func (s *MemoryStore) ExamStatusForTest(examID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.examStatuses[examID]
}

func (s *MemoryStore) AddFinalGrade(seed GradeSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seg := s.segments[seed.AnswerSegmentID]
	now := time.Now().UTC()
	grade := FinalGrade{
		ID:              s.id("final-grade"),
		TenantID:        "",
		ExamID:          seg.ExamID,
		QuestionID:      seg.QuestionID,
		QuestionNo:      seg.QuestionNo,
		AnswerSegmentID: seed.AnswerSegmentID,
		SubmissionID:    seg.SubmissionID,
		AnonymousCode:   seg.AnonymousCode,
		Score:           seed.Score,
		MaxScore:        seed.MaxScore,
		Source:          normalizeSource(seed.Source),
		Status:          "pending_confirmation",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.finals[grade.ID] = grade
	s.finalBySeg[seed.AnswerSegmentID] = grade.ID
}

func (s *MemoryStore) AddHumanGrade(seed GradeSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.humanGrades[seed.AnswerSegmentID] = seed
}

func (s *MemoryStore) AddRuleGrade(seed GradeSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ruleGrades[seed.AnswerSegmentID] = seed
}

func (s *MemoryStore) AddReviewTask(seed TaskSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reviewTasks = append(s.reviewTasks, seed)
}

func (s *MemoryStore) AddArbitrationTask(seed TaskSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.arbTasks = append(s.arbTasks, seed)
}

func (s *MemoryStore) AddOCRTask(seed TaskSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ocrTasks = append(s.ocrTasks, seed)
}

func (s *MemoryStore) AddRosterStudent(seed RosterSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roster[key(seed.ExamID, seed.StudentID)] = seed
	if _, exists := s.examStatuses[seed.ExamID]; !exists {
		s.examStatuses[seed.ExamID] = "collecting"
	}
}

func (s *MemoryStore) AddRosterSubmission(seed RosterSubmissionSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rosterSubmissions = append(s.rosterSubmissions, seed)
}

func cloneSubmissionGrade(in SubmissionGrade) SubmissionGrade {
	in.ConfirmedAt = cloneTime(in.ConfirmedAt)
	in.PublishedAt = cloneTime(in.PublishedAt)
	in.Items = append([]FinalGrade(nil), in.Items...)
	return in
}

func cloneTime(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func normalizeSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "single_review"
	}
	return source
}

func valueOr(value float64, fallback float64) float64 {
	if value == 0 {
		return fallback
	}
	return value
}

func key(left string, right string) string {
	return left + "|" + right
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}
