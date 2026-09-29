package grading

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type MemoryStore struct {
	mu       sync.RWMutex
	next     int
	contexts map[string]Context
	answers  map[string][]SegmentAnswer
	grades   map[string][]Grade
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		next:     1,
		contexts: map[string]Context{},
		answers:  map[string][]SegmentAnswer{},
		grades:   map[string][]Grade{},
	}
}

func (s *MemoryStore) AddContext(tenantID string, segmentID string, question paper.Question) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contexts[key(tenantID, segmentID)] = Context{SegmentID: segmentID, Question: question}
}

func (s *MemoryStore) RecordAnswer(_ context.Context, tenantID string, segmentID string, actorID string, input RecordAnswerInput) (SegmentAnswer, error) {
	input = normalizeAnswerInput(input)
	if err := validateAnswerInput(input); err != nil {
		return SegmentAnswer{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.contexts[key(tenantID, segmentID)]; !ok {
		return SegmentAnswer{}, ErrNotFound
	}
	answer := SegmentAnswer{
		ID:              s.id("segment-answer"),
		TenantID:        tenantID,
		AnswerSegmentID: segmentID,
		AnswerText:      input.AnswerText,
		AnswerPayload:   cloneMap(input.AnswerPayload),
		Source:          input.Source,
		Confidence:      cloneFloat(input.Confidence),
		RecordedBy:      actorID,
		CreatedAt:       time.Now().UTC(),
	}
	s.answers[key(tenantID, segmentID)] = append(s.answers[key(tenantID, segmentID)], answer)
	return answer, nil
}

func (s *MemoryStore) LoadContext(_ context.Context, tenantID string, segmentID string) (Context, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ctx, ok := s.contexts[key(tenantID, segmentID)]
	if !ok {
		return Context{}, ErrNotFound
	}
	if ctx.Question.AnswerKey == nil {
		return Context{}, ErrAnswerKeyMissing
	}
	answers := s.answers[key(tenantID, segmentID)]
	if len(answers) == 0 {
		return Context{}, ErrAnswerMissing
	}
	ctx.AnswerKey = *ctx.Question.AnswerKey
	ctx.Answer = answers[len(answers)-1]
	return ctx, nil
}

func (s *MemoryStore) CreateGrade(_ context.Context, tenantID string, actorID string, grade Grade) (Grade, error) {
	if grade.AnswerSegmentID == "" || grade.QuestionID == "" {
		return Grade{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	grade.ID = s.id("ai-grade")
	grade.TenantID = tenantID
	grade.CreatedBy = actorID
	grade.CreatedAt = time.Now().UTC()
	grade.Mock = false
	grade.RawOutput = cloneMap(grade.RawOutput)
	grade.MatchedPoints = clonePoints(grade.MatchedPoints)
	grade.MissingPoints = clonePoints(grade.MissingPoints)
	grade.Evidence = cloneEvidence(grade.Evidence)
	grade.RiskFlags = cloneStrings(grade.RiskFlags)
	s.grades[key(tenantID, grade.AnswerSegmentID)] = append(s.grades[key(tenantID, grade.AnswerSegmentID)], grade)
	return grade, nil
}

func (s *MemoryStore) ListGrades(_ context.Context, tenantID string, segmentID string) ([]Grade, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.contexts[key(tenantID, segmentID)]; !ok {
		return nil, ErrNotFound
	}
	out := cloneGrades(s.grades[key(tenantID, segmentID)])
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func normalizeAnswerInput(input RecordAnswerInput) RecordAnswerInput {
	input.AnswerText = strings.TrimSpace(input.AnswerText)
	input.Source = strings.TrimSpace(input.Source)
	if input.Source == "" {
		input.Source = "manual_entry"
	}
	if input.AnswerPayload == nil {
		input.AnswerPayload = map[string]any{}
	}
	if input.AnswerText == "" {
		if value, ok := input.AnswerPayload["answer"]; ok {
			input.AnswerText = stringValue(value)
		} else if value, ok := input.AnswerPayload["answers"]; ok {
			input.AnswerText = strings.Join(stringSlice(value), ",")
		} else if value, ok := input.AnswerPayload["value"]; ok {
			input.AnswerText = stringValue(value)
			if unit := strings.TrimSpace(stringValue(input.AnswerPayload["unit"])); unit != "" {
				input.AnswerText += " " + unit
			}
		}
	}
	return input
}

func validateAnswerInput(input RecordAnswerInput) error {
	if input.AnswerText == "" && len(input.AnswerPayload) == 0 {
		return ErrInvalidInput
	}
	switch input.Source {
	case "manual_entry", "ocr_text", "imported_answer":
	default:
		return ErrInvalidInput
	}
	if input.Confidence != nil && (*input.Confidence < 0 || *input.Confidence > 1) {
		return ErrInvalidInput
	}
	return nil
}

func key(tenantID string, segmentID string) string {
	return tenantID + "|" + segmentID
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}

func cloneFloat(in *float64) *float64 {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := map[string]any{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func clonePoints(in []PointResult) []PointResult {
	out := make([]PointResult, len(in))
	copy(out, in)
	return out
}

func cloneEvidence(in []Evidence) []Evidence {
	out := make([]Evidence, len(in))
	copy(out, in)
	return out
}

// 复制容器用于隔离常见顶层修改；嵌套 map、证据坐标等仍共享，不能据此视为完整深拷贝。
func cloneGrades(in []Grade) []Grade {
	out := make([]Grade, len(in))
	for i, grade := range in {
		grade.RawOutput = cloneMap(grade.RawOutput)
		grade.MatchedPoints = clonePoints(grade.MatchedPoints)
		grade.MissingPoints = clonePoints(grade.MissingPoints)
		grade.Evidence = cloneEvidence(grade.Evidence)
		grade.RiskFlags = cloneStrings(grade.RiskFlags)
		out[i] = grade
	}
	return out
}
