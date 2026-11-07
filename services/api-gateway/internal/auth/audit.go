package auth

import "strings"

func stringsTrim(value string) string {
	return strings.TrimSpace(value)
}

func matchesAuditKeyword(record AuditRecord, actor User, query string) bool {
	if strings.TrimSpace(query) == "" {
		return true
	}
	// 只检索审计列表可读字段；隐藏的before/after值不能通过关键词命中暴露存在性。
	text := strings.Join([]string{record.Action, record.TargetType, record.TargetID, record.ActorID, actor.Username, actor.DisplayName, record.Reason, record.IPAddress, record.UserAgent, record.RequestID}, " ")
	return strings.Contains(strings.ToLower(text), strings.ToLower(strings.TrimSpace(query)))
}

func normalizedAuditLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func auditQueryLimit(filter AuditFilter) int {
	limit := normalizedAuditLimit(filter.Limit)
	if filter.IncludeNext {
		return limit + 1
	}
	return limit
}

// RedactAuditRecord 按敏感字段名递归隐藏审计值；自由文本不会被识别，写入方仍须避免记录秘密。
func RedactAuditRecord(record AuditRecord) AuditRecord {
	record.BeforeValue = redactAuditMap(record.BeforeValue)
	record.AfterValue = redactAuditMap(record.AfterValue)
	return record
}

func RedactAuditRecords(records []AuditRecord) []AuditRecord {
	out := make([]AuditRecord, len(records))
	for i, record := range records {
		out[i] = RedactAuditRecord(record)
	}
	return out
}

func redactAuditMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		if isAuditSensitiveKey(key) {
			out[key] = "[REDACTED]"
			continue
		}
		out[key] = redactAuditValue(item)
	}
	return out
}

func redactAuditValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return redactAuditMap(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, redactAuditValue(item))
		}
		return out
	default:
		return value
	}
}

func isAuditSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, token := range []string{
		"password",
		"passwd",
		"token",
		"secret",
		"credential",
		"authorization",
		"api_key",
		"apikey",
		"private_key",
		"session",
	} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

// 组织导航和分派任务不授予审计权限；学校管理员只读持久化目标归属位于其管理学校的记录。
func auditFilterHasReadableScope(filter AuditFilter) bool {
	switch filter.ScopeMode {
	case "", "tenant", "platform":
		return true
	case "school":
		return len(uniqueSortedIDs(filter.SchoolIDs)) > 0
	default:
		return false
	}
}

// 调用方持有内存仓储锁，目标归属在追加时快照，不能从操作者或自由审计值回推。
func (s *MemoryStore) auditTargetScopeLocked(event AuditEvent) (string, string) {
	if event.TargetType == "user" || event.TargetType == "app_user" {
		for _, user := range s.users {
			if user.TenantID == event.TenantID && user.ID == event.TargetID {
				return managedUserSchoolID(user), ""
			}
		}
		return "", ""
	}
	kind := event.TargetType
	if kind == "class" {
		kind = "school_class"
	}
	boundary, ok := s.boundaries[kind+"|"+event.TargetID]
	if !ok || boundary.TenantID != event.TenantID {
		return "", ""
	}
	return boundary.SchoolID, boundary.ExamID
}
