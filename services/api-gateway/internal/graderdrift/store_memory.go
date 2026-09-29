package graderdrift

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

type storedWindow struct {
	tenantID string
	value    QualityWindow
}

type storedIncident struct {
	tenantID string
	value    Incident
}

type MemoryStore struct {
	mu        sync.RWMutex
	windows   map[string]storedWindow
	incidents map[string]storedIncident
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{windows: map[string]storedWindow{}, incidents: map[string]storedIncident{}}
}

func (s *MemoryStore) UpsertWindow(_ context.Context, tenantID string, input QualityWindow) (QualityWindow, error) {
	if tenantID == "" || input.ExamID == "" || input.QuestionID == "" || input.GraderID == "" || input.WindowSize <= 0 || input.WindowEnd.Before(input.WindowStart) {
		return QualityWindow{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := windowKey(tenantID, input)
	if old, ok := s.windows[key]; ok {
		input.ID = old.value.ID
	}
	if input.ID == "" {
		input.ID = uuid.NewString()
	}
	input = cloneWindow(input)
	s.windows[key] = storedWindow{tenantID: tenantID, value: input}
	return cloneWindow(input), nil
}

// 同一窗口和事件类型只保留一条记录，重复刷新只返回新建事件，不重复告警。
func (s *MemoryStore) CreateIncidentsIfMissing(_ context.Context, tenantID string, candidates []Incident) ([]Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	created := []Incident{}
	for _, candidate := range candidates {
		if tenantID == "" || candidate.SourceWindowID == "" || candidate.Type == "" {
			return nil, ErrInvalidInput
		}
		key := incidentKey(tenantID, candidate.SourceWindowID, candidate.Type)
		if _, exists := s.incidents[key]; exists {
			continue
		}
		if candidate.ID == "" {
			candidate.ID = uuid.NewString()
		}
		if candidate.CreatedAt.IsZero() {
			candidate.CreatedAt = time.Now().UTC()
		}
		candidate = cloneIncident(candidate)
		s.incidents[key] = storedIncident{tenantID: tenantID, value: candidate}
		created = append(created, cloneIncident(candidate))
	}
	return created, nil
}

func (s *MemoryStore) ListWindows(_ context.Context, tenantID string, filter WindowFilter) ([]QualityWindow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := []QualityWindow{}
	for _, entry := range s.windows {
		item := entry.value
		if entry.tenantID != tenantID || filter.ExamID != "" && item.ExamID != filter.ExamID ||
			filter.QuestionID != "" && item.QuestionID != filter.QuestionID || filter.GraderID != "" && item.GraderID != filter.GraderID {
			continue
		}
		result = append(result, cloneWindow(item))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].WindowEnd.Equal(result[j].WindowEnd) {
			return result[i].ID > result[j].ID
		}
		return result[i].WindowEnd.After(result[j].WindowEnd)
	})
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

func (s *MemoryStore) ListIncidents(_ context.Context, tenantID string, filter IncidentFilter) ([]Incident, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := []Incident{}
	for _, entry := range s.incidents {
		item := entry.value
		if entry.tenantID != tenantID || filter.ExamID != "" && item.ExamID != filter.ExamID ||
			filter.QuestionID != "" && item.QuestionID != filter.QuestionID || filter.GraderID != "" && item.GraderID != filter.GraderID ||
			filter.Status != "" && item.Status != filter.Status {
			continue
		}
		result = append(result, cloneIncident(item))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

func (s *MemoryStore) ResolveIncident(_ context.Context, tenantID, incidentID string, now time.Time) (Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, entry := range s.incidents {
		if entry.tenantID != tenantID || entry.value.ID != incidentID {
			continue
		}
		entry.value.Status = IncidentResolved
		resolved := now.UTC()
		entry.value.ResolvedAt = &resolved
		s.incidents[key] = entry
		return cloneIncident(entry.value), nil
	}
	return Incident{}, ErrNotFound
}

func windowKey(tenantID string, value QualityWindow) string {
	return tenantID + "\x00" + value.ExamID + "\x00" + value.QuestionID + "\x00" + value.GraderID + "\x00" +
		strconv.Itoa(value.WindowSize) + "\x00" + value.WindowStart.UTC().Format(time.RFC3339Nano) + "\x00" + value.WindowEnd.UTC().Format(time.RFC3339Nano)
}

func incidentKey(tenantID, windowID string, kind IncidentType) string {
	return tenantID + "\x00" + windowID + "\x00" + string(kind)
}

func cloneWindow(value QualityWindow) QualityWindow {
	if value.RubricAgreement != nil {
		copy := *value.RubricAgreement
		value.RubricAgreement = &copy
	}
	if value.MiddleScoreMAE != nil {
		copy := *value.MiddleScoreMAE
		value.MiddleScoreMAE = &copy
	}
	if value.MiddleScoreExactAgreement != nil {
		copy := *value.MiddleScoreExactAgreement
		value.MiddleScoreExactAgreement = &copy
	}
	if value.EWMABias != nil {
		copy := *value.EWMABias
		value.EWMABias = &copy
	}
	return value
}

func cloneIncident(value Incident) Incident {
	value.MetricSnapshot = cloneMap(value.MetricSnapshot)
	value.AffectedRange = cloneMap(value.AffectedRange)
	if value.ResolvedAt != nil {
		copy := *value.ResolvedAt
		value.ResolvedAt = &copy
	}
	return value
}
