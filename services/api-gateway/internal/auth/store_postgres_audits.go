package auth

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

func (s *PostgresStore) Audit(ctx context.Context, event AuditEvent) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO audit_log (tenant_id, actor_id, action, target_type, target_id, before_value, after_value, reason, ip_address, user_agent, request_id)
VALUES ($1, NULLIF($2, '')::uuid, $3, $4, NULLIF($5, '')::uuid, $6::jsonb, $7::jsonb, $8, $9, $10, $11)
`, event.TenantID, event.ActorID, event.Action, event.TargetType, event.TargetID, auditJSON(event.BeforeValue), auditJSON(event.AfterValue), event.Reason, event.IPAddress, event.UserAgent, event.RequestID)
	return err
}

func (s *PostgresStore) ListAudits(ctx context.Context, tenantID string, filter AuditFilter) ([]AuditRecord, error) {
	if !auditFilterHasReadableScope(filter) {
		return []AuditRecord{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT
  id::text,
  tenant_id::text,
  COALESCE(actor_id::text, ''),
  action,
  target_type,
  COALESCE(target_id::text, ''),
  COALESCE(before_value::text, ''),
  COALESCE(after_value::text, ''),
  COALESCE(reason, ''),
  COALESCE(ip_address, ''),
  COALESCE(user_agent, ''),
  COALESCE(request_id, ''),
  created_at
FROM audit_log
WHERE tenant_id = $1
  AND ($14 OR EXISTS (SELECT 1 FROM audit_log_scope projection WHERE projection.tenant_id=audit_log.tenant_id AND projection.audit_id=audit_log.id AND projection.school_id::text=ANY(string_to_array($15,','))))
  AND ($2 = '' OR action = $2)
  AND ($3 = '' OR COALESCE(actor_id::text, '') = $3)
  AND ($4 = '' OR target_type = $4)
  AND ($5 = '' OR COALESCE(target_id::text, '') = $5)
  AND ($6 = '' OR (target_type='exam' AND target_id::text=$6) OR EXISTS (SELECT 1 FROM audit_log_scope projection WHERE projection.tenant_id=audit_log.tenant_id AND projection.audit_id=audit_log.id AND projection.exam_id::text=$6))
  AND ($7 = '' OR COALESCE(ip_address, '') = $7)
  AND ($8::timestamptz IS NULL OR created_at >= $8::timestamptz)
  AND ($9::timestamptz IS NULL OR created_at <= $9::timestamptz)
  AND ($11 = '' OR created_at < $10 OR (created_at = $10 AND id::text < $11))
  AND ($13 = '' OR strpos(lower(concat_ws(' ', action, target_type, target_id::text, actor_id::text, reason, ip_address, user_agent, request_id,
    (SELECT concat_ws(' ', u.username, u.display_name) FROM app_user u WHERE u.tenant_id=audit_log.tenant_id AND u.id=audit_log.actor_id))), lower($13)) > 0)
ORDER BY created_at DESC, id::text DESC
LIMIT $12
`, tenantID, stringsTrim(filter.Action), stringsTrim(filter.ActorID), stringsTrim(filter.TargetType), stringsTrim(filter.TargetID), stringsTrim(filter.ExamID), stringsTrim(filter.IPAddress), nullableTime(filter.CreatedFrom), nullableTime(filter.CreatedTo), nullableTime(filter.CursorCreatedAt), stringsTrim(filter.CursorID), auditQueryLimit(filter), stringsTrim(filter.Query), filter.ScopeMode != "school", strings.Join(uniqueSortedIDs(filter.SchoolIDs), ","))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditRecord{}
	for rows.Next() {
		var record AuditRecord
		beforeValue := ""
		afterValue := ""
		if err := rows.Scan(
			&record.ID,
			&record.TenantID,
			&record.ActorID,
			&record.Action,
			&record.TargetType,
			&record.TargetID,
			&beforeValue,
			&afterValue,
			&record.Reason,
			&record.IPAddress,
			&record.UserAgent,
			&record.RequestID,
			&record.CreatedAt,
		); err != nil {
			return nil, err
		}
		record.BeforeValue = parseAuditJSON(beforeValue)
		record.AfterValue = parseAuditJSON(afterValue)
		out = append(out, record)
	}
	return out, rows.Err()
}

func auditJSON(value map[string]any) any {
	if len(value) == 0 {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return string(data)
}

func parseAuditJSON(value string) map[string]any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return nil
	}
	return out
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
