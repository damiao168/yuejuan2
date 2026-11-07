package auth

import (
	"context"
	"fmt"
	"sort"
	"time"
)

func (s *MemoryStore) Audit(_ context.Context, event AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendAudit(event, time.Now().UTC())
	return nil
}

// 调用方持有 s.mu，包括原子记录安全事实的调用方。
func (s *MemoryStore) appendAudit(event AuditEvent, occurredAt time.Time) {
	schoolID, examID := s.auditTargetScopeLocked(event)
	s.auditSeq++
	s.audits = append(s.audits, AuditRecord{
		SchoolID: schoolID, ExamID: examID,
		ID:          fmt.Sprintf("memory-audit-%d", s.auditSeq),
		TenantID:    event.TenantID,
		ActorID:     event.ActorID,
		Action:      event.Action,
		TargetType:  event.TargetType,
		TargetID:    event.TargetID,
		BeforeValue: cloneAuditMap(event.BeforeValue),
		AfterValue:  cloneAuditMap(event.AfterValue),
		Reason:      event.Reason,
		IPAddress:   event.IPAddress,
		UserAgent:   event.UserAgent,
		RequestID:   event.RequestID,
		CreatedAt:   occurredAt,
	})
}

func (s *MemoryStore) Audits() []AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AuditEvent, len(s.audits))
	for i, record := range s.audits {
		out[i] = AuditEvent{
			TenantID:   record.TenantID,
			ActorID:    record.ActorID,
			Action:     record.Action,
			TargetType: record.TargetType,
			TargetID:   record.TargetID,
			Reason:     record.Reason,
			IPAddress:  record.IPAddress,
			UserAgent:  record.UserAgent,
			RequestID:  record.RequestID,
		}
	}
	return out
}

func (s *MemoryStore) ListAudits(_ context.Context, tenantID string, filter AuditFilter) ([]AuditRecord, error) {
	if !auditFilterHasReadableScope(filter) {
		return []AuditRecord{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []AuditRecord{}
	actors := map[string]User{}
	for _, actor := range s.users {
		if actor.TenantID == tenantID {
			actors[actor.ID] = actor.User
		}
	}
	for _, record := range s.audits {
		if (filter.ScopeMode == "school" && !containsID(filter.SchoolIDs, record.SchoolID)) || !matchesAuditIdentity(record, tenantID, filter) || !matchesAuditContext(record, filter) {
			continue
		}
		if !matchesAuditKeyword(record, actors[record.ActorID], filter.Query) {
			continue
		}
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if filter.CursorID != "" {
		out = auditsAfterCursor(out, filter)
	}
	if limit := auditQueryLimit(filter); len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func matchesAuditIdentity(record AuditRecord, tenantID string, filter AuditFilter) bool {
	if record.TenantID != tenantID {
		return false
	}
	if filter.Action != "" && record.Action != filter.Action {
		return false
	}
	if filter.ActorID != "" && record.ActorID != filter.ActorID {
		return false
	}
	if filter.TargetType != "" && record.TargetType != filter.TargetType {
		return false
	}
	return filter.TargetID == "" || record.TargetID == filter.TargetID
}

func matchesAuditContext(record AuditRecord, filter AuditFilter) bool {
	if filter.ExamID != "" && record.ExamID != filter.ExamID && (record.TargetType != "exam" || record.TargetID != filter.ExamID) {
		return false
	}
	if filter.IPAddress != "" && record.IPAddress != filter.IPAddress {
		return false
	}
	if !filter.CreatedFrom.IsZero() && record.CreatedAt.Before(filter.CreatedFrom) {
		return false
	}
	return filter.CreatedTo.IsZero() || !record.CreatedAt.After(filter.CreatedTo)
}

// 排序与游标均按创建时间、ID 倒序；同一时刻的记录必须继续用 ID 划分下一页。
func auditsAfterCursor(items []AuditRecord, filter AuditFilter) []AuditRecord {
	for index, item := range items {
		if item.CreatedAt.Before(filter.CursorCreatedAt) || (item.CreatedAt.Equal(filter.CursorCreatedAt) && item.ID < filter.CursorID) {
			return items[index:]
		}
	}
	return items[len(items):]
}

func cloneAuditMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}
